package compose

import (
	"bytes"
	"context"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-1224 as rule version 1, FEAT-9995385612 lane 4: criteria C9, C10 and
// the live half of C24 at composition level. Five (then six) orders on one
// cell are accepted and charged under version 0; the same city restored
// today replays them exactly and then enforces the refusal.

// orderCostMicro is what one accepted build order costs the treasury, in the
// treasury's own unit (15,000 pounds at the engine's 1000x scale). The test
// derives it from the single-order control and asserts it is this figure.
const orderCostMicro = int64(15_000_000)

// dupControl runs the duplicate session uninterrupted, pinned to version 0,
// with no persistence: the control every restore is compared with.
func dupControl(t *testing.T) (*ruleCity, outcome) {
	t.Helper()
	rc := newRuleCity(t, nil, 0)
	rc.duplicateSession(t, nil)
	return rc, rc.outcome(t)
}

// C9: the duplicate orders restore exactly, from snapshot plus tail and from
// genesis, and the money shows the old rule's charges.
func TestBUG1224_C9_DuplicateOrdersRestoreExactly(t *testing.T) {
	ctrl, want := dupControl(t)
	if n := ctrl.queueLen(); n != 6 {
		t.Fatalf("control queue holds %d orders, want 6 (all duplicates accepted under version 0)", n)
	}

	// The charge: a single-order city at the same tick has more money by
	// exactly five order costs (six orders vs one).
	single := newRuleCity(t, nil, 0)
	single.singleSession(t)
	diff := int64(single.outcome(t).money) - int64(want.money)
	if diff != 5*orderCostMicro {
		t.Fatalf("treasury gap between one order and six duplicates = %d, want %d (five extra order costs of %d)", diff, 5*orderCostMicro, orderCostMicro)
	}

	// Record the same session with persistence.
	store := persist.NewMemStore()
	rec := newRuleCity(t, store, 0)
	rec.duplicateSession(t, store)
	if got := rec.outcome(t); got != want {
		t.Fatalf("recorded run differs from the uninterrupted control:\n got %+v\nwant %+v", got, want)
	}

	cases := []struct {
		name string
		keep []int
		snap bool
	}{
		{"snapshot_plus_tail", []int{0}, true},
		{"genesis", nil, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			rc, rep := wake(t, cloneStore(t, store, c.keep...))
			if got := rc.outcome(t); got != want {
				t.Fatalf("restored state differs from the original run:\n got %+v\nwant %+v", got, want)
			}
			if n := rc.queueLen(); n != 6 {
				t.Errorf("restored queue holds %d orders, want 6", n)
			}
			if rep.UsedSnapshot != c.snap {
				t.Errorf("UsedSnapshot = %v, want %v", rep.UsedSnapshot, c.snap)
			}
			if rc.c.RuleVersion() != rules.Current {
				t.Errorf("RuleVersion after restore = %d, want Current %d", rc.c.RuleVersion(), rules.Current)
			}
			if c.snap && rc.c.LoadedRuleVersion() != 0 {
				t.Errorf("LoadedRuleVersion = %d, want 0 (the city was made under version 0)", rc.c.LoadedRuleVersion())
			}
			if !rep.OlderRules {
				t.Errorf("a version-0 city restored under version %d must report older rules: %+v", rules.Current, rep)
			}
		})
	}
}

// C10: after that restore, new play gets the fix.
func TestBUG1224_C10_AfterRestoreNewPlayGetsTheFix(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	rec := newRuleCity(t, store, 0)
	rec.duplicateSession(t, store)

	for _, keep := range [][]int{{0}, nil} {
		name := "genesis"
		if keep != nil {
			name = "snapshot_plus_tail"
		}
		t.Run(name, func(t *testing.T) {
			live := cloneStore(t, store, keep...)
			rc, _ := wake(t, live)

			framesBefore, err := live.ReadJournal(ctx, ruleTestCity)
			if err != nil {
				t.Fatal(err)
			}
			moneyBefore, queueBefore := rc.c.Treasury(), rc.queueLen()

			// A seventh order on the same cell is refused with MET-G510 and
			// leaves no trace: nothing journaled, nothing charged.
			res := rc.tryBuild(duplicateCell)
			if res.Accepted || res.Error == nil || res.Error.Code != "MET-G510" {
				t.Fatalf("duplicate Build after restore: want MET-G510 refusal, got %+v", res)
			}
			framesAfter, err := live.ReadJournal(ctx, ruleTestCity)
			if err != nil {
				t.Fatal(err)
			}
			if len(framesAfter) != len(framesBefore) {
				t.Errorf("a refusal grew the journal from %d to %d frames", len(framesBefore), len(framesAfter))
			}
			if rc.c.Treasury() != moneyBefore || rc.queueLen() != queueBefore {
				t.Errorf("a refusal changed the city: money %d -> %d, queue %d -> %d", moneyBefore, rc.c.Treasury(), queueBefore, rc.queueLen())
			}

			// A Build on a different free cell is accepted and journaled at
			// version 1.
			other := protocol.CellRef{X: 4, Y: 3}
			rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: other, ZoneType: "dwelling"})
			rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: other, BuildingType: "dwelling"})
			rc.advance(t, 40)
			frames, err := live.ReadJournal(ctx, ruleTestCity)
			if err != nil {
				t.Fatal(err)
			}
			if len(frames) != len(framesBefore)+3 {
				t.Fatalf("journal holds %d frames, want %d", len(frames), len(framesBefore)+3)
			}
			if last := frames[len(frames)-2]; !bytes.HasPrefix(last, []byte(`{"rv":1,`)) {
				t.Errorf("the accepted Build was not journaled at version 1: %s", last)
			}
			want := rc.outcome(t)

			// A second restart (genesis and snapshot plus tail) reproduces the
			// uninterrupted continuation: the journal now spans both versions.
			for _, k := range [][]int{nil, keep} {
				again, _ := wake(t, cloneStore(t, live, k...))
				if got := again.outcome(t); got != want {
					t.Errorf("second restart (keep=%v) differs from the continuation:\n got %+v\nwant %+v", k, got, want)
				}
				if again.queueLen() != rc.queueLen() {
					t.Errorf("second restart queue %d, want %d", again.queueLen(), rc.queueLen())
				}
			}
		})
	}
}

// Live play on a fresh city is refused from the first duplicate, charges once.
func TestBUG1224_LiveCityRefusesDuplicatesAndChargesOnce(t *testing.T) {
	rc := newRuleCity(t, nil, -1)
	if rc.c.RuleVersion() != rules.Current {
		t.Fatalf("fresh city runs version %d, want Current %d", rc.c.RuleVersion(), rules.Current)
	}
	rc.advance(t, 5)
	rc.do(t, protocol.KindBuy, protocol.BuyPayload{Cell: duplicateCell})
	rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: duplicateCell, ZoneType: "dwelling"})
	rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: duplicateCell, BuildingType: "dwelling"})
	for i := 0; i < 4; i++ {
		if res := rc.tryBuild(duplicateCell); res.Accepted || res.Error == nil || res.Error.Code != "MET-G510" {
			t.Fatalf("duplicate %d: want MET-G510 refusal, got %+v", i, res)
		}
	}
	rc.advance(t, 460)
	one := newRuleCity(t, nil, -1)
	one.singleSession(t)
	if rc.outcome(t) != one.outcome(t) {
		t.Errorf("five presses on one cell differ from one press:\n got %+v\nwant %+v", rc.outcome(t), one.outcome(t))
	}
}
