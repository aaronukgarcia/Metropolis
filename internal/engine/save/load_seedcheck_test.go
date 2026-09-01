package save

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-479: Load/LoadAt (compose.Composition's own entry points, which
// wrap Manager.Load — see internal/engine/compose/save_wire.go) never
// validated the bundle's WorldSeed against the loading composition's own
// seed, so a differently-seeded load was silently accepted and every
// seed-derived stateless draw (attract hash draws, det.Stream per-draw
// RNG) diverged from the saved trajectory with no error at all. This
// file proves the fix at the save package's own Manager.Load level
// (options.go's LoadOption / WithExpectedWorldSeed / AllowSeedMismatch);
// internal/engine/compose/save_bug479_seedmismatch_test.go proves the
// same thing end-to-end through Composition.Load/LoadAt.

// buildAndSaveWidgetBundle saves a single-widget bundle under seed and
// returns its directory, ready for a Load in another test.
func buildAndSaveWidgetBundle(t *testing.T, seed int64) string {
	t.Helper()
	root := t.TempDir()
	widgets := newWidgetParticipant(widget{ID: 1, Name: "alpha", Score: 3.5})
	mgr := NewManager(root, []Participant{widgets}, "bug479-save")
	ctx := Context{WorldSeed: seed, CreatedAtTick: 7, GameMonth: 1, AppVersion: "test-build"}
	if err := mgr.SaveManual(ctx, "bug479-fixture"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}
	return manualDir(root, "bug479-fixture")
}

// TestLoad_SeedMismatch_RefusedBeforeAnyParticipantApplied is the
// headline BUG-479 case: a bundle saved under one seed, loaded with
// WithExpectedWorldSeed naming a DIFFERENT seed, is refused with
// ErrSaveSeedMismatch — and the sentinel state a test pre-populates onto
// the load-side Participant (standing in for "this composition's own
// already-live module state") is completely untouched, proving the
// refusal happens before the shard-load loop ever invokes a Handler.
func TestLoad_SeedMismatch_RefusedBeforeAnyParticipantApplied(t *testing.T) {
	dir := buildAndSaveWidgetBundle(t, 42)

	sentinel := widget{ID: 999, Name: "sentinel-untouched", Score: -1}
	loadWidgets := newWidgetParticipant(sentinel)
	loadMgr := NewManager(t.TempDir(), []Participant{loadWidgets}, "bug479-load")

	_, _, err := loadMgr.Load(dir, WithExpectedWorldSeed(43))
	if err == nil {
		t.Fatal("Load with mismatched WithExpectedWorldSeed(43) against a seed-42 bundle succeeded, want ErrSaveSeedMismatch")
	}
	if !errors.Is(err, &errs.E{Code: ErrSaveSeedMismatch}) {
		t.Fatalf("Load error = %v, want code %s", err, ErrSaveSeedMismatch)
	}

	if got := loadWidgets.State(); len(got) != 1 || got[0] != sentinel {
		t.Fatalf("participant state changed on a refused load: got %+v, want untouched sentinel %+v", got, []widget{sentinel})
	}
}

// TestLoad_SeedMismatch_ProveCanFail is the prove-can-fail companion:
// removing the seed check (i.e. loading with no LoadOption at all, the
// pre-BUG-479 shape) against the SAME mismatched bundle succeeds, so the
// refusal above is not a coincidence of some unrelated validation
// failure — it is specifically the seed check doing its job.
func TestLoad_SeedMismatch_ProveCanFail(t *testing.T) {
	dir := buildAndSaveWidgetBundle(t, 42)

	loadWidgets := newWidgetParticipant()
	loadMgr := NewManager(t.TempDir(), []Participant{loadWidgets}, "bug479-provecanfail")

	// No LoadOption at all: byte-for-byte the pre-BUG-479 Load call
	// shape. This MUST succeed against the same seed-42 bundle a
	// mismatched WithExpectedWorldSeed(43) call refuses above —
	// otherwise the refusal in the prior test could be caused by
	// something other than the seed check.
	if _, _, err := loadMgr.Load(dir); err != nil {
		t.Fatalf("Load with no seed option (pre-BUG-479 shape) failed: %v — the mismatch test's refusal cannot be attributed to the seed check specifically unless this succeeds", err)
	}
}

// TestLoad_SeedMatch_Succeeds proves WithExpectedWorldSeed does not
// refuse a bundle whose seed genuinely matches — the check is a real
// comparison, not an unconditional refusal.
func TestLoad_SeedMatch_Succeeds(t *testing.T) {
	dir := buildAndSaveWidgetBundle(t, 42)

	loadWidgets := newWidgetParticipant()
	loadMgr := NewManager(t.TempDir(), []Participant{loadWidgets}, "bug479-match")

	if _, _, err := loadMgr.Load(dir, WithExpectedWorldSeed(42)); err != nil {
		t.Fatalf("Load with matching WithExpectedWorldSeed(42) failed: %v", err)
	}
	if got := loadWidgets.State(); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("participant state did not load: got %+v", got)
	}
}

// TestLoad_AllowSeedMismatch_LoadsDespiteMismatch proves the explicit
// reseed opt-in (BUG-479's deliberate-reseed escape hatch, e.g. the
// FEAT-1972079897 rules-change replay case): AllowSeedMismatch alongside
// a mismatched WithExpectedWorldSeed lets the same bundle
// TestLoad_SeedMismatch_RefusedBeforeAnyParticipantApplied refuses load
// successfully instead.
func TestLoad_AllowSeedMismatch_LoadsDespiteMismatch(t *testing.T) {
	dir := buildAndSaveWidgetBundle(t, 42)

	loadWidgets := newWidgetParticipant()
	loadMgr := NewManager(t.TempDir(), []Participant{loadWidgets}, "bug479-allow")

	if _, _, err := loadMgr.Load(dir, WithExpectedWorldSeed(43), AllowSeedMismatch()); err != nil {
		t.Fatalf("Load with WithExpectedWorldSeed(43)+AllowSeedMismatch() against a seed-42 bundle failed: %v, want success (deliberate reseed opt-in)", err)
	}
	if got := loadWidgets.State(); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("participant state did not load under AllowSeedMismatch: got %+v", got)
	}
}

// TestLoad_MissingWorldSeedHeader_TreatedAsMismatch covers the "old
// bundle predating the WorldSeed field" case BUG-479's acceptance
// criteria calls out explicitly: Header.WorldSeed has no `omitempty` on
// its json tag (header.go), so a header.json missing the "worldSeed" key
// entirely (simulating a pre-this-field save format) decodes it as the
// Go zero value, 0. Recommendation adopted here: treat that exactly like
// any other numeric seed mismatch — refused unless it happens to equal
// the expected seed (vanishingly unlikely for a real composition, whose
// seed is essentially never literally 0) or AllowSeedMismatch is passed.
func TestLoad_MissingWorldSeedHeader_TreatedAsMismatch(t *testing.T) {
	dir := buildAndSaveWidgetBundle(t, 42)

	// Rewrite header.json with the "worldSeed" key deleted entirely —
	// not merely set to 0 — to prove the ABSENT-key case specifically,
	// not just a header that happens to carry a literal 0.
	headerPath := dir + string(os.PathSeparator) + "header.json"
	raw, err := os.ReadFile(headerPath)
	if err != nil {
		t.Fatalf("reading fixture header.json: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decoding fixture header.json: %v", err)
	}
	if _, ok := generic["worldSeed"]; !ok {
		t.Fatal("fixture header.json unexpectedly has no worldSeed key before the deliberate deletion below")
	}
	delete(generic, "worldSeed")
	rewritten, err := json.MarshalIndent(generic, "", "  ")
	if err != nil {
		t.Fatalf("re-encoding stripped header.json: %v", err)
	}
	if err := os.WriteFile(headerPath, rewritten, 0o644); err != nil {
		t.Fatalf("writing stripped header.json: %v", err)
	}

	loadWidgets := newWidgetParticipant()
	loadMgr := NewManager(t.TempDir(), []Participant{loadWidgets}, "bug479-missingfield")

	_, _, err = loadMgr.Load(dir, WithExpectedWorldSeed(42))
	if err == nil {
		t.Fatal("Load against a header.json with NO worldSeed key succeeded against a nonzero expected seed, want ErrSaveSeedMismatch (missing key decodes as seed 0)")
	}
	if !errors.Is(err, &errs.E{Code: ErrSaveSeedMismatch}) {
		t.Fatalf("Load error = %v, want code %s", err, ErrSaveSeedMismatch)
	}

	// AllowSeedMismatch still rescues a legacy bundle missing the field
	// entirely, exactly like any other mismatch.
	loadWidgets2 := newWidgetParticipant()
	loadMgr2 := NewManager(t.TempDir(), []Participant{loadWidgets2}, "bug479-missingfield-allow")
	if _, _, err := loadMgr2.Load(dir, WithExpectedWorldSeed(42), AllowSeedMismatch()); err != nil {
		t.Fatalf("Load against a header.json with NO worldSeed key + AllowSeedMismatch() failed: %v, want success", err)
	}
}
