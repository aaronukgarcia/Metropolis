package compose

import (
	"context"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// BUG-480 independent destructive round (Opus r1) — REGISTRY RENDERING.
//
// GR#7 says every error is registry-sourced; BUG-317/BUG-357 are the
// standing proof that "registered" is not the same as "renders". Both of
// those bugs shipped codes whose templates carried tokens the call site
// never supplied, so operators read a literal `{token}` in the log where
// the city/tick should have been. MET-G812 and MET-G813 are new codes on
// exactly that footing: G812's template interpolates {city} {tick} {cause}
// and G813's interpolates {city} {tick}, and neither existing BUG-480 test
// looks at the RENDERED message at all — they only match on Code and Ctx,
// which stay correct even when the template is broken.
//
// This drives both codes through their real call sites and asserts the
// rendered message contains no unresolved token AND actually carries the
// substituted values.
func TestAttackBUG480_NewRegistryCodesRenderWithoutLiteralTokens(t *testing.T) {
	ctx := context.Background()
	const cadence = int64(4)
	mem := persist.NewMemStore()

	// --- MET-G813: MaybeSnapshotEvery refusing while the journaler is dirty.
	dirtyCity := persist.CityKey{TenantID: "t", CityID: "render-g813-480"}
	failing := &nthAppendFailStore{Store: mem, failCall: 2}
	e1, comp1 := buildPersistedComposition(t, failing, dirtyCity)
	advanceViaCommand(t, e1, cadence) // call #1 (ok): tick=4.
	if _, ok, err := comp1.MaybeSnapshotEvery(ctx, failing, dirtyCity, cadence); err != nil || !ok {
		t.Fatalf("seed snapshot: ok=%v err=%v", ok, err)
	}
	// call #2 FAILS and HALTS the Engine (BUG-472's "HALT + SURFACE" ruling)
	// -- its own effect still applies, landing exactly on the next cadence
	// boundary (tick=8); no third command can ever run on e1 afterward.
	advanceViaCommandExpectHalt(t, e1, cadence)
	if _, ok, err := comp1.MaybeSnapshotEvery(ctx, failing, dirtyCity, cadence); err != nil || ok {
		t.Fatalf("dirty boundary: ok=%v err=%v, want the refusal", ok, err)
	}
	assertRendered(t, ErrSnapshotRefusedDirty, dirtyCity.CityID, []string{dirtyCity.CityID, "8"})

	// --- MET-G812: a candidate skipped during the restore walk-back.
	skipCity := persist.CityKey{TenantID: "t", CityID: "render-g812-480"}
	other := persist.NewMemStore()
	eSrc, compSrc := buildPersistedComposition(t, other, persist.CityKey{TenantID: "t", CityID: "src"})
	advanceViaCommand(t, eSrc, 100)
	ahead, err := compSrc.buildSnapshotBytes()
	if err != nil {
		t.Fatalf("buildSnapshotBytes: %v", err)
	}
	e2, _ := buildPersistedComposition(t, mem, skipCity)
	advanceViaCommand(t, e2, cadence)
	if _, err := mem.PutSnapshot(ctx, skipCity, ahead); err != nil {
		t.Fatalf("PutSnapshot: %v", err)
	}
	eR, compR := buildPersistedComposition(t, persist.NewMemStore(), skipCity)
	_ = compR
	if _, _, err := RestoreLatestSnapshotOrGenesis(ctx, eR, compR, mem, skipCity); err != nil {
		t.Fatalf("restore: %v", err)
	}
	assertRendered(t, ErrSnapshotSkipped, skipCity.CityID, []string{skipCity.CityID, "100"})
}

// assertRendered finds the most recent log entry for code naming cityID and
// asserts its rendered Msg (a) contains no unresolved `{token}` placeholder
// and (b) actually contains each expected substituted value.
func assertRendered(t *testing.T, code, cityID string, wantSubstrings []string) {
	t.Helper()
	var msg string
	found := false
	for _, entry := range recentEntries() {
		if entry.code != code {
			continue
		}
		if !strings.Contains(entry.msg, cityID) && !strings.Contains(entry.city, cityID) {
			continue
		}
		msg, found = entry.msg, true
	}
	if !found {
		t.Fatalf("%s: no logged entry found for city %q -- cannot assert rendering", code, cityID)
	}
	if i := strings.IndexByte(msg, '{'); i >= 0 {
		t.Fatalf("%s rendered with a LITERAL unresolved token at offset %d: %q -- the template interpolates a key the call site does not supply (the BUG-317/BUG-357 class)", code, i, msg)
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(msg, want) {
			t.Fatalf("%s rendered message %q does not contain the substituted value %q", code, msg, want)
		}
	}
}

type renderedEntry struct {
	code string
	msg  string
	city string
}

func recentEntries() []renderedEntry {
	out := make([]renderedEntry, 0, 64)
	for _, e := range errs.Recent() {
		city, _ := e.Ctx["city"].(string)
		out = append(out, renderedEntry{code: e.Code, msg: e.Msg, city: city})
	}
	return out
}
