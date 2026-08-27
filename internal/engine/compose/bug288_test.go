package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/invariant"
)

// compareStockReadings compares two StockReadings field by field.
func compareStockReadings(t *testing.T, name string, r1, r2 invariant.StockReading) bool {
	match := true
	if r1.Registered != r2.Registered {
		t.Errorf("%s: Registered mismatch: %v vs %v", name, r1.Registered, r2.Registered)
		match = false
	}
	if r1.Opening != r2.Opening {
		t.Errorf("%s: Opening mismatch: %d vs %d", name, r1.Opening, r2.Opening)
		match = false
	}
	if r1.Closing != r2.Closing {
		t.Errorf("%s: Closing mismatch: %d vs %d", name, r1.Closing, r2.Closing)
		match = false
	}
	if r1.TrackedDelta != r2.TrackedDelta {
		t.Errorf("%s: TrackedDelta mismatch: %d vs %d", name, r1.TrackedDelta, r2.TrackedDelta)
		match = false
	}
	return match
}

// TestBUG288_SnapshotProviderPurity tests that snapshot is PURE.
func TestBUG288_SnapshotProviderPurity(t *testing.T) {
	e := core.NewEngine(core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire failed: %v", err)
	}
	st := comp.state

	clock, _ := e.Clock()
	tick := clock.Tick()

	snap1 := st.snapshot(tick)
	snap2 := st.snapshot(tick)

	if snap1.Tick != snap2.Tick {
		t.Errorf("Tick mismatch: %d vs %d", snap1.Tick, snap2.Tick)
	}

	r1_people, ok1 := snap1.Reading(invariant.StockPeople)
	r2_people, ok2 := snap2.Reading(invariant.StockPeople)
	if ok1 != ok2 {
		t.Fatalf("People reading presence mismatch: %v vs %v", ok1, ok2)
	}
	compareStockReadings(t, "People", r1_people, r2_people)

	r1_money, ok1 := snap1.Reading(invariant.StockMoney)
	r2_money, ok2 := snap2.Reading(invariant.StockMoney)
	if ok1 != ok2 {
		t.Fatalf("Money reading presence mismatch: %v vs %v", ok1, ok2)
	}
	compareStockReadings(t, "Money", r1_money, r2_money)
}

// TestBUG288_SnapshotProviderIsolation tests ledger chaining across ticks.
func TestBUG288_SnapshotProviderIsolation(t *testing.T) {
	e := core.NewEngine(core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire failed: %v", err)
	}
	st := comp.state

	clock, _ := e.Clock()
	tick := clock.Tick()

	snap1 := st.snapshot(tick)
	people1, _ := snap1.Reading(invariant.StockPeople)

	// Second tick
	tick2 := tick + 1
	snap2 := st.snapshot(tick2)
	people2, _ := snap2.Reading(invariant.StockPeople)

	// Tick 2's opening must equal Tick 1's closing (ledger chaining)
	if people2.Opening != people1.Closing {
		t.Errorf("LEDGER CHAIN BROKEN: Tick2 opening (%d) != Tick1 closing (%d)",
			people2.Opening, people1.Closing)
	}

	// Tick 2's delta should be 0 at start (no mutations yet)
	if people2.TrackedDelta != 0 {
		t.Errorf("Tick 2 tracked delta should be 0 at start: %d",
			people2.TrackedDelta)
	}
}

// TestBUG288_CloseLedgerOnce tests that ledger closing happens exactly once per tick.
func TestBUG288_CloseLedgerOnce(t *testing.T) {
	e := core.NewEngine(core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire failed: %v", err)
	}
	st := comp.state

	clock, _ := e.Clock()
	tick := clock.Tick()

	if st.lastClosedTick != 0 {
		t.Errorf("Before first snapshot, lastClosedTick should be 0: %d", st.lastClosedTick)
	}

	for i := 0; i < 3; i++ {
		st.snapshot(tick)
		if st.lastClosedTick != tick {
			t.Errorf("After call %d, lastClosedTick should be %d: %d", i, tick, st.lastClosedTick)
		}
	}

	tick2 := tick + 1
	st.snapshot(tick2)
	if st.lastClosedTick != tick2 {
		t.Errorf("After first snapshot on tick2, lastClosedTick should be %d: %d", tick2, st.lastClosedTick)
	}
}

// TestBUG288_MultiTickLedgerChaining tests that opening values chain correctly across ticks.
func TestBUG288_MultiTickLedgerChaining(t *testing.T) {
	e := core.NewEngine(core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire failed: %v", err)
	}
	st := comp.state

	clock, _ := e.Clock()
	tick := clock.Tick()

	// Tick 1
	snap1 := st.snapshot(tick)
	people1, _ := snap1.Reading(invariant.StockPeople)
	money1, _ := snap1.Reading(invariant.StockMoney)

	// Tick 2 (no mutations, just advance)
	tick2 := tick + 1
	snap2 := st.snapshot(tick2)
	people2, _ := snap2.Reading(invariant.StockPeople)
	money2, _ := snap2.Reading(invariant.StockMoney)

	// Critical: Tick 2's opening MUST equal Tick 1's closing (ledger chaining)
	if people2.Opening != people1.Closing {
		t.Errorf("LEDGER CHAIN BROKEN (people): Tick2 opening (%d) != Tick1 closing (%d)",
			people2.Opening, people1.Closing)
	}
	if money2.Opening != money1.Closing {
		t.Errorf("LEDGER CHAIN BROKEN (money): Tick2 opening (%d) != Tick1 closing (%d)",
			money2.Opening, money1.Closing)
	}

	// Tick 3
	tick3 := tick2 + 1
	snap3 := st.snapshot(tick3)
	people3, _ := snap3.Reading(invariant.StockPeople)
	money3, _ := snap3.Reading(invariant.StockMoney)

	// Tick 3's opening should equal Tick 2's closing
	if people3.Opening != people2.Closing {
		t.Errorf("LEDGER CHAIN BROKEN (people): Tick3 opening (%d) != Tick2 closing (%d)",
			people3.Opening, people2.Closing)
	}
	if money3.Opening != money2.Closing {
		t.Errorf("LEDGER CHAIN BROKEN (money): Tick3 opening (%d) != Tick2 closing (%d)",
			money3.Opening, money2.Closing)
	}
}
