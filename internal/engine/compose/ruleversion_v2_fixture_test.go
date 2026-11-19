package compose

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-9995385628 (roads lane 1a, rule version 2): the version-2 rule
// fixture. A city recorded at version 1 (the version before LayRoad) cannot
// contain a road, so the fixture session is an ordinary version-1 session: a
// cell bought and a home built on it, a snapshot, then a second cell bought
// in the tail. It is restored today (version 2) and must reach the same state
// as a version-1 control run, and then a road order must be accepted.

// fixtureV2Dir is the registry's Fixture path, relative to this package.
const fixtureV2Dir = "testdata/rules/v2"

// roadHomeCell holds the home; roadCell is bought in the tail and is where a
// road is later ordered.
var (
	roadHomeCell = protocol.CellRef{X: 3, Y: 3}
	roadCell     = protocol.CellRef{X: 5, Y: 3}
)

// roadFixtureSession records the version-1 session through the real command
// path. The engine must already be pinned to 1. store nil skips the snapshot
// (the uninterrupted control).
func (rc *ruleCity) roadFixtureSession(t *testing.T, store persist.Store) {
	t.Helper()
	rc.advance(t, 5)
	rc.do(t, protocol.KindBuy, protocol.BuyPayload{Cell: roadHomeCell})
	rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: roadHomeCell, ZoneType: "dwelling"})
	rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: roadHomeCell, BuildingType: "dwelling"})
	rc.advance(t, 60)
	if store != nil {
		rc.snapshot(t, store, 65)
	}
	// The tail after the snapshot: a second cell, then time passes.
	rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: roadCell, ZoneType: "dwelling"})
	rc.advance(t, 400)
}

// tryRoad submits one Build "road" and returns the result without failing.
func (rc *ruleCity) tryRoad(cell protocol.CellRef) protocol.CommandResult {
	return rc.e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("rv-road-" + strconv.FormatInt(ruleCorr.Add(1), 10)),
		Kind:            protocol.KindBuild,
		Payload:         protocol.BuildPayload{Cell: cell, BuildingType: "road"},
	})
}

// roadControl runs the fixture session uninterrupted, pinned to version 1.
func roadControl(t *testing.T) (*ruleCity, outcome) {
	t.Helper()
	rc := newRuleCity(t, nil, rules.LayRoad-1)
	rc.roadFixtureSession(t, nil)
	return rc, rc.outcome(t)
}

// openFixtureV2 copies the committed fixture to a temp dir and opens it.
func openFixtureV2(t *testing.T) persist.Store {
	t.Helper()
	dir := materializeRuleFixture(t, fixtureV2Dir)
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	return st
}

// TestRuleFixtureV2_Generate rewrites testdata/rules/v2 from the real command
// path with the engine pinned to version 1. It runs only when
// METROPOLIS_GEN_RULES_FIXTURE=1 (the same variable as the v1 generator).
func TestRuleFixtureV2_Generate(t *testing.T) {
	if os.Getenv("METROPOLIS_GEN_RULES_FIXTURE") != "1" {
		t.Skip("set METROPOLIS_GEN_RULES_FIXTURE=1 to regenerate testdata/rules/v2")
	}
	real := t.TempDir()
	store, err := persist.NewDiskStore(real)
	if err != nil {
		t.Fatal(err)
	}
	rc := newRuleCity(t, store, rules.LayRoad-1)
	rc.roadFixtureSession(t, store)
	if err := CheckRuleFixture(real, rules.LayRoad-1); err != nil {
		t.Fatalf("generated fixture does not pass its own check: %v", err)
	}
	writeShortRuleFixture(t, real, fixtureV2Dir)
}

// TestRuleFixtureV2_IsRecordedAtVersionOne: the registry's fixture exists,
// holds a journal and a snapshot, and every frame and snapshot is at exactly
// Number-1 = 1, and the journal holds no road.
func TestRuleFixtureV2_IsRecordedAtVersionOne(t *testing.T) {
	e, ok := rules.Entry(rules.LayRoad)
	if !ok || e.Fixture != "internal/engine/compose/"+fixtureV2Dir {
		t.Fatalf("registry fixture path %q does not match %q", e.Fixture, fixtureV2Dir)
	}
	if err := CheckRuleFixture(materializeRuleFixture(t, fixtureV2Dir), rules.LayRoad-1); err != nil {
		t.Fatal(err)
	}
	frames, err := RestoreCommands(context.Background(), openFixtureV2(t), defaultPersistCity)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		if bp, ok := f.Payload.(protocol.BuildPayload); ok && bp.BuildingType == "road" {
			t.Fatalf("a version-1 fixture must not hold a road order")
		}
	}
}

// TestRuleFixtureV2_RestoresToTheVersionOneControl restores the COMMITTED
// bytes (snapshot plus tail, and genesis), compares with the uninterrupted
// version-1 control, and then continues at version 2: a road order is
// accepted and queued.
func TestRuleFixtureV2_RestoresToTheVersionOneControl(t *testing.T) {
	_, want := roadControl(t)
	for _, genesis := range []bool{false, true} {
		src := openFixtureV2(t)
		var rc *ruleCity
		var rep RestoreReport
		if genesis {
			rc, rep = wake(t, cloneStore(t, src))
		} else {
			rc, rep = wake(t, cloneStore(t, src, 0))
		}
		if got := rc.outcome(t); got != want {
			t.Errorf("fixture restore (genesis=%v) differs from the version-1 control:\n got %+v\nwant %+v\n"+
				"testdata/rules/v2 holds a snapshot of simulation state, so it must be regenerated whenever a daily formula changes outcomes.\n"+
				"Regenerate it (from the repo root): METROPOLIS_GEN_RULES_FIXTURE=1 go test -count=1 -run TestRuleFixtureV2_Generate ./internal/engine/compose/", genesis, got, want)
		}
		if rep.UsedSnapshot == genesis || !rep.OlderRules {
			t.Errorf("fixture restore (genesis=%v): report %+v", genesis, rep)
		}
		if rc.c.RuleVersion() != rules.Current {
			t.Errorf("restored city runs rule %d, want %d", rc.c.RuleVersion(), rules.Current)
		}
		before := rc.queueLen()
		if res := rc.tryRoad(roadCell); !res.Accepted {
			t.Fatalf("after restoring a version-1 city a road order must be accepted at version 2: %+v", res.Error)
		}
		if rc.queueLen() != before+1 {
			t.Errorf("queue %d after the road order, want %d", rc.queueLen(), before+1)
		}
	}
}

// roadFrameJournal returns a journal-only store holding the fixture's frames
// plus one hand-built Build "road" frame stamped at stamp.
func roadFrameJournal(t *testing.T, stamp rules.Version) persist.Store {
	t.Helper()
	ctx := context.Background()
	dst := cloneStore(t, openFixtureV2(t)) // journal only, no snapshot
	raw, err := encodeJournalFrame(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   "rv-road-frame",
		Kind:            protocol.KindBuild,
		Payload:         protocol.BuildPayload{Cell: roadCell, BuildingType: "road"},
	}, stamp)
	if err != nil {
		t.Fatal(err)
	}
	if err := dst.AppendJournal(ctx, defaultPersistCity, raw); err != nil {
		t.Fatal(err)
	}
	return dst
}

// TestRuleFixtureV2_Discriminates is criterion 13: a hand-built frame stamped
// below 2 that holds Build "road" replays as REFUSED (MET-G811 wrapping the
// MET-G501 refusal) and the same frame stamped 2 replays as accepted; live, a
// city pinned to 1 refuses the order with MET-G501 and charges nothing.
func TestRuleFixtureV2_Discriminates(t *testing.T) {
	ctx := context.Background()

	below := roadFrameJournal(t, rules.LayRoad-1)
	rc := newRuleCity(t, below, -1)
	rc.guard.replaying.Store(true)
	_, err := RestoreLatestSnapshotOrGenesisReport(ctx, rc.e, rc.c, below, ruleTestCity)
	if !isCode(err, ErrSnapshotTailReplayRejected) {
		t.Fatalf("a road frame stamped 1 must be refused on replay (MET-G811), got %v", err)
	}

	at, _ := wake(t, roadFrameJournal(t, rules.LayRoad))
	base, _ := wake(t, cloneStore(t, openFixtureV2(t)))
	if at.queueLen() != base.queueLen()+1 {
		t.Fatalf("a road frame stamped 2 must replay as accepted: queue %d, want %d", at.queueLen(), base.queueLen()+1)
	}

	live := newRuleCity(t, nil, rules.LayRoad-1)
	live.roadFixtureSession(t, nil)
	money := live.c.Treasury()
	res := live.tryRoad(roadCell)
	if res.Accepted || res.Error == nil || res.Error.Code != "MET-G501" {
		t.Fatalf("at version 1 a road order must be refused with MET-G501, got %+v", res)
	}
	if live.c.Treasury() != money {
		t.Fatalf("a refused road order changed the treasury: %d to %d", money, live.c.Treasury())
	}
}

// TestRuleFixtureV2_NewerSaveRefusedByOlderBuild is criterion 13(c): a journal
// frame or a save stamped 2, opened by a build whose live version is 1, is
// refused as saved under newer rules. classifyVersion is called directly with
// the current version standing at 1 (SetCurrentForTest cannot go below
// Current, so the older build is stood in for by the pure classify function
// over a version table of 1).
func TestRuleFixtureV2_NewerSaveRefusedByOlderBuild(t *testing.T) {
	if got := classifyAgainst(1, rules.LayRoad); got != rules.SupportTooNew {
		t.Fatalf("a build at version 1 must treat a version-2 stamp as too new, got %v", got)
	}
	if got := classifyAgainst(1, 1); got != rules.SupportOK {
		t.Fatalf("a build at version 1 must accept a version-1 stamp, got %v", got)
	}
	if got := classifyAgainst(rules.Current, rules.LayRoad); got != rules.SupportOK {
		t.Fatalf("this build must accept a version-2 stamp, got %v", got)
	}
	// And the real code path: this build refuses a stamp above itself.
	if err := classifyVersion("rv-newer", rules.Effective()+1, true); !isCode(err, rules.ErrSavedUnderNewerRules) {
		t.Fatalf("a stamp above the live version must be refused with %s, got %v", rules.ErrSavedUnderNewerRules, err)
	}
}

// classifyAgainst is rules.Classify with the live version replaced by cur,
// mirroring its branches (too new above cur).
func classifyAgainst(cur, v rules.Version) rules.Support {
	switch {
	case v < 0:
		return rules.SupportInvalid
	case v > cur:
		return rules.SupportTooNew
	case v < rules.OldestSupported:
		return rules.SupportTooOld
	}
	return rules.SupportOK
}
