package compose

import (
	"context"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// Round r1 attack tests for BUG-1224 as rule version 1 (FEAT-9995385612
// lane 4), attacker opus-round-r1-bug1224-rule1. They stay as regressions.

// hammerSession runs a multi-cell session across several month closes. When
// refuse is true it fires refused duplicates between every accepted command
// and checks each refusal leaves money, queue and journal untouched.
func hammerSession(t *testing.T, rc *ruleCity, store persist.Store, refuse bool) {
	t.Helper()
	ctx := context.Background()
	cells := []protocol.CellRef{{X: 3, Y: 3}, {X: 4, Y: 3}, {X: 5, Y: 3}, {X: 3, Y: 4}}
	hammer := func(cell protocol.CellRef) {
		if !refuse {
			return
		}
		for i := 0; i < 7; i++ {
			money, q := rc.c.Treasury(), rc.c.state.buildAPI.Queue()
			var before int
			if store != nil {
				fr, err := store.ReadJournal(ctx, ruleTestCity)
				if err != nil {
					t.Fatal(err)
				}
				before = len(fr)
			}
			res := rc.tryBuild(cell)
			if res.Accepted || res.Error == nil || res.Error.Code != "MET-G510" {
				t.Fatalf("duplicate on %v: want MET-G510, got %+v", cell, res)
			}
			if rc.c.Treasury() != money || !reflect.DeepEqual(rc.c.state.buildAPI.Queue(), q) {
				t.Fatalf("refusal on %v changed money or queue", cell)
			}
			if store != nil {
				fr, err := store.ReadJournal(ctx, ruleTestCity)
				if err != nil {
					t.Fatal(err)
				}
				if len(fr) != before {
					t.Fatalf("refusal on %v journaled a frame", cell)
				}
			}
		}
	}
	rc.advance(t, 5)
	rc.do(t, protocol.KindBuy, protocol.BuyPayload{Cell: cells[0]}) // one tile holds every cell
	for i, cell := range cells {
		rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"})
		rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"})
		hammer(cell)
		if i > 0 {
			hammer(cells[i-1])
		}
		rc.advance(t, 31) // crosses a month close each step
		hammer(cell)
	}
	rc.advance(t, 400) // orders complete; refusals now hit standing structures
	for _, cell := range cells {
		hammer(cell)
	}
	rc.advance(t, 40)
}

// Money conservation at version 1: a hammered city is bit-identical to one
// that never pressed the duplicates (digest, money, population, tick, the
// full queue including order ids), and its journal restores to it.
func TestRoundR1Rule1_RefusalsAreInvisibleAcrossMonthCloses(t *testing.T) {
	store := persist.NewMemStore()
	hot := newRuleCity(t, store, -1)
	hammerSession(t, hot, store, true)
	cold := newRuleCity(t, nil, -1)
	hammerSession(t, cold, nil, false)
	if hot.outcome(t) != cold.outcome(t) {
		t.Fatalf("hammered city differs from the clean one:\n got %+v\nwant %+v", hot.outcome(t), cold.outcome(t))
	}
	if !reflect.DeepEqual(hot.c.state.buildAPI.Queue(), cold.c.state.buildAPI.Queue()) {
		t.Fatalf("queues differ (ids or contents)")
	}
	for _, keep := range [][]int{nil} {
		rc, _ := wake(t, cloneStore(t, store, keep...))
		if rc.outcome(t) != hot.outcome(t) {
			t.Fatalf("restore of the hammered journal differs:\n got %+v\nwant %+v", rc.outcome(t), hot.outcome(t))
		}
	}
}

// Out-of-range and negative cells keep their own refusals (no G510, no
// charge) at version 1.
func TestRoundR1Rule1_BadCellsAreNotCellInUse(t *testing.T) {
	rc := newRuleCity(t, nil, -1)
	rc.advance(t, 5)
	money := rc.c.Treasury()
	for _, cell := range []protocol.CellRef{{X: -1, Y: 3}, {X: 3, Y: -1}, {X: 1 << 20, Y: 3}, {X: 3, Y: 1 << 20}, {X: 6, Y: 6}} {
		res := rc.tryBuild(cell)
		if res.Accepted {
			t.Fatalf("build on %v accepted", cell)
		}
		if res.Error != nil && res.Error.Code == "MET-G510" {
			t.Fatalf("build on %v refused as cell-in-use: %+v", cell, res.Error)
		}
	}
	if rc.c.Treasury() != money || rc.queueLen() != 0 {
		t.Fatalf("bad-cell refusals moved money or queue")
	}
}

// The switch point is durable: a version-0 city woken twice with no new
// command in between restores identically, runs Current both times, and a
// snapshot taken right after the first wake (before any new command) is
// stamped Current yet still restores the version-0 duplicates exactly.
func TestRoundR1Rule1_SwitchIsDurableAcrossRestarts(t *testing.T) {
	_, want := dupControl(t)
	store := persist.NewMemStore()
	rec := newRuleCity(t, store, 0)
	rec.duplicateSession(t, store)

	first, _ := wake(t, cloneStore(t, store, 0))
	if first.c.RuleVersion() != rules.Current || first.outcome(t) != want {
		t.Fatalf("first wake: version %d outcome %+v", first.c.RuleVersion(), first.outcome(t))
	}
	second, rep := wake(t, cloneStore(t, store, 0))
	if second.c.RuleVersion() != rules.Current || second.outcome(t) != want || !rep.OlderRules {
		t.Fatalf("second wake: version %d outcome %+v rep %+v", second.c.RuleVersion(), second.outcome(t), rep)
	}

	// Snapshot right after the switch, no new command.
	live := cloneStore(t, store, 0)
	rc, _ := wake(t, live)
	rc.snapshot(t, live, 1)
	ids, err := live.ListSnapshots(context.Background(), ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	data, err := live.GetSnapshot(context.Background(), ruleTestCity, ids[len(ids)-1])
	if err != nil {
		t.Fatal(err)
	}
	v, err := snapshotRuleVersion(data)
	if err != nil {
		t.Fatal(err)
	}
	if v != int(rules.Current) {
		t.Fatalf("snapshot after the switch stamped %d, want %d", v, rules.Current)
	}
	again, _ := wake(t, cloneStore(t, live, len(ids)-1))
	if again.outcome(t) != want || again.queueLen() != 6 {
		t.Fatalf("restore from the post-switch snapshot: %+v queue %d", again.outcome(t), again.queueLen())
	}
	if res := again.tryBuild(duplicateCell); res.Accepted || res.Error == nil || res.Error.Code != "MET-G510" {
		t.Fatalf("after the post-switch snapshot a duplicate must be refused: %+v", res)
	}
}

// A journal mixing version-0 accepted duplicates with version-1 frames
// (C11 with the real rule): genesis and every snapshot restore to the
// continuation, and the frames really carry both versions.
func TestRoundR1Rule1_MixedJournalRestoresEveryWay(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	rec := newRuleCity(t, store, 0)
	rec.duplicateSession(t, store) // snapshot 0 at v0, v0 frames
	rec.snapshot(t, store, 1)      // snapshot 1 at v0 (pinned), the boundary

	live := cloneStore(t, store, 0, 1)
	rc, _ := wake(t, live)
	if res := rc.tryBuild(duplicateCell); res.Accepted {
		t.Fatalf("duplicate accepted after the switch")
	}
	for _, cell := range []protocol.CellRef{{X: 4, Y: 3}, {X: 5, Y: 3}} {
		rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"})
		rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"})
		if res := rc.tryBuild(cell); res.Accepted {
			t.Fatalf("duplicate on %v accepted at v1", cell)
		}
	}
	rc.advance(t, 50)
	rc.snapshot(t, live, 1) // snapshot 2 at v1, after the boundary
	rc.advance(t, 30)
	want := rc.outcome(t)
	wantQ := rc.c.state.buildAPI.Queue()

	frames, err := RestoreCommands(ctx, live, ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[rules.Version]int{}
	for _, f := range frames {
		seen[f.Version]++
	}
	if seen[0] == 0 || seen[1] == 0 {
		t.Fatalf("journal does not mix versions: %v", seen)
	}
	for _, keep := range [][]int{nil, {0}, {1}, {2}, {0, 1, 2}} {
		got, _ := wake(t, cloneStore(t, live, keep...))
		if got.outcome(t) != want {
			t.Errorf("keep=%v: restore differs:\n got %+v\nwant %+v", keep, got.outcome(t), want)
		}
		if !reflect.DeepEqual(got.c.state.buildAPI.Queue(), wantQ) {
			t.Errorf("keep=%v: queue differs", keep)
		}
		if got.c.RuleVersion() != rules.Current {
			t.Errorf("keep=%v: live version %d", keep, got.c.RuleVersion())
		}
	}
}
