package compose

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-9995385612 lane 4: the version-1 rule fixture (BUG-1224) and the
// estate that restores it. The fixture is the persist directory of a city
// recorded with the engine pinned to version 0: five accepted Build orders
// on one cell before the snapshot, a sixth after it (so the duplicate sits in
// the genesis replay AND in the snapshot's tail).

// fixtureV1Dir is the registry's Fixture path, relative to this package.
const fixtureV1Dir = "testdata/rules/v1"

// duplicateCell is the cell every duplicate order targets.
var duplicateCell = protocol.CellRef{X: 3, Y: 3}

// duplicateSession records the BUG-1224 session through the real command
// path. The engine must already be pinned to the version under test. store
// nil skips the snapshot (the uninterrupted control).
func (rc *ruleCity) duplicateSession(t *testing.T, store persist.Store) {
	t.Helper()
	rc.advance(t, 5)
	rc.do(t, protocol.KindBuy, protocol.BuyPayload{Cell: duplicateCell})
	rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: duplicateCell, ZoneType: "dwelling"})
	for i := 0; i < 5; i++ {
		rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: duplicateCell, BuildingType: "dwelling"})
	}
	rc.advance(t, 60)
	if store != nil {
		rc.snapshot(t, store, 65)
	}
	// The sixth duplicate lands in the tail after the snapshot.
	rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: duplicateCell, BuildingType: "dwelling"})
	rc.advance(t, 400)
}

// singleSession is the same session with ONE order on the cell.
func (rc *ruleCity) singleSession(t *testing.T) {
	t.Helper()
	rc.advance(t, 5)
	rc.do(t, protocol.KindBuy, protocol.BuyPayload{Cell: duplicateCell})
	rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: duplicateCell, ZoneType: "dwelling"})
	rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: duplicateCell, BuildingType: "dwelling"})
	rc.advance(t, 60)
	rc.advance(t, 400)
}

// tryBuild submits one Build and returns the result without failing.
func (rc *ruleCity) tryBuild(cell protocol.CellRef) protocol.CommandResult {
	return rc.e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(fmt.Sprintf("rv-try-%d", ruleCorr.Add(1))),
		Kind:            protocol.KindBuild,
		Payload:         protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"},
	})
}

func (rc *ruleCity) queueLen() int { return len(rc.c.state.buildAPI.Queue()) }

// copyDir copies the regular files under src into dst (a persist directory).
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
	if err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
}

// openFixtureV1 copies the committed fixture to a temp dir (a DiskStore may
// write beside what it reads) and opens it.
func openFixtureV1(t *testing.T) persist.Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "v1")
	copyDir(t, fixtureV1Dir, dir)
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	return st
}

// TestRuleFixtureV1_Generate rewrites testdata/rules/v1 from the real command
// path with the engine pinned to version 0. It runs only when
// METROPOLIS_GEN_RULES_FIXTURE=1 (a developer regenerates deliberately; the
// fixture is otherwise read-only evidence of how version 0 behaved).
func TestRuleFixtureV1_Generate(t *testing.T) {
	if os.Getenv("METROPOLIS_GEN_RULES_FIXTURE") != "1" {
		t.Skip("set METROPOLIS_GEN_RULES_FIXTURE=1 to regenerate testdata/rules/v1")
	}
	if err := os.RemoveAll(fixtureV1Dir); err != nil {
		t.Fatal(err)
	}
	store, err := persist.NewDiskStore(fixtureV1Dir)
	if err != nil {
		t.Fatal(err)
	}
	rc := newRuleCity(t, store, 0)
	rc.duplicateSession(t, store)
	if err := CheckRuleFixture(fixtureV1Dir, 0); err != nil {
		t.Fatalf("generated fixture does not pass its own check: %v", err)
	}
}

// TestRuleFixtureV1_IsRecordedAtVersionZero is C2 for entry 1: the registry's
// fixture exists, holds a journal and a snapshot, and every frame and
// snapshot is at exactly Number-1 = 0.
func TestRuleFixtureV1_IsRecordedAtVersionZero(t *testing.T) {
	e, ok := rules.Entry(rules.RefuseOccupiedCell)
	if !ok || e.Fixture != "internal/engine/compose/"+fixtureV1Dir {
		t.Fatalf("registry fixture path %q does not match %q", e.Fixture, fixtureV1Dir)
	}
	if err := CheckRuleFixture(fixtureV1Dir, rules.RefuseOccupiedCell-1); err != nil {
		t.Fatal(err)
	}
	frames, err := RestoreCommands(context.Background(), openFixtureV1(t), defaultPersistCity)
	if err != nil {
		t.Fatal(err)
	}
	builds := 0
	for _, f := range frames {
		if f.Kind == protocol.KindBuild {
			builds++
		}
	}
	if builds != 6 {
		t.Fatalf("fixture holds %d Build frames, want 6 (five before the snapshot, one in the tail)", builds)
	}
}

// restoreStoreV1 restores a copy of the fixture, optionally dropping its
// snapshot (genesis), and returns the woken city and its report.
func restoreFixtureV1(t *testing.T, genesis bool) (*ruleCity, RestoreReport) {
	t.Helper()
	src := openFixtureV1(t)
	if genesis {
		return wake(t, cloneStore(t, src))
	}
	return wake(t, cloneStore(t, src, 0))
}

// TestRuleFixtureV1_Discriminates is C3: the same journal replayed by the
// same code restores under version 0 and does NOT under version 1 (the first
// duplicate is refused), so the fixture proves the rule.
func TestRuleFixtureV1_Discriminates(t *testing.T) {
	ctx := context.Background()
	src := openFixtureV1(t)
	frames, err := RestoreCommands(ctx, src, defaultPersistCity)
	if err != nil {
		t.Fatal(err)
	}
	// Re-stamp every frame at version 1 into a journal-only store.
	restamped := persist.NewMemStore()
	for _, f := range frames {
		raw, err := encodeJournalFrame(f.Command, rules.RefuseOccupiedCell)
		if err != nil {
			t.Fatal(err)
		}
		if err := restamped.AppendJournal(ctx, defaultPersistCity, raw); err != nil {
			t.Fatal(err)
		}
	}
	orig := persist.NewMemStore()
	for _, f := range frames {
		raw, err := encodeJournalFrame(f.Command, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := orig.AppendJournal(ctx, defaultPersistCity, raw); err != nil {
			t.Fatal(err)
		}
	}
	at0, _ := wake(t, orig)
	if at0.queueLen() != 6 {
		t.Fatalf("pinned to 0 the fixture restores %d orders, want 6", at0.queueLen())
	}
	rc := newRuleCity(t, restamped, -1)
	rc.guard.replaying.Store(true)
	_, err = RestoreLatestSnapshotOrGenesisReport(ctx, rc.e, rc.c, restamped, ruleTestCity)
	if !isCode(err, ErrSnapshotTailReplayRejected) {
		t.Fatalf("pinned to 1 the fixture must be refused with MET-G811 (the duplicate Build), got %v", err)
	}
}

// TestRuleFixtureV1_RestoresToTheVersionZeroControl restores the COMMITTED
// bytes (snapshot plus tail, and genesis) and compares with a control that
// ran the same session uninterrupted under version 0: the proof that bytes
// recorded before the rule replay as accepted and charged.
func TestRuleFixtureV1_RestoresToTheVersionZeroControl(t *testing.T) {
	_, want := dupControl(t)
	for _, genesis := range []bool{false, true} {
		rc, rep := restoreFixtureV1(t, genesis)
		if got := rc.outcome(t); got != want {
			t.Errorf("fixture restore (genesis=%v) differs from the version-0 control:\n got %+v\nwant %+v\n"+
				"testdata/rules/v1 holds a snapshot of simulation state, so it must be regenerated whenever a daily formula changes outcomes.\n"+
				"Regenerate it (from the repo root): METROPOLIS_GEN_RULES_FIXTURE=1 go test -count=1 -run TestRuleFixtureV1_Generate ./internal/engine/compose/", genesis, got, want)
		}
		if rc.queueLen() != 6 || rep.UsedSnapshot == genesis || !rep.OlderRules {
			t.Errorf("fixture restore (genesis=%v): queue %d, report %+v", genesis, rc.queueLen(), rep)
		}
	}
}
