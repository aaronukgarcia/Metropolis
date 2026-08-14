package invariant

import (
	"math"
	"testing"
)

// Each stock's test follows the same shape: (1) a BALANCED fixture
// proves the invariant does not cry wolf on legitimate state (weakness
// pattern #1's "must not fire on correct behaviour"); (2) a
// DELIBERATELY BROKEN fixture — an untracked change with no matching
// tracked delta — proves the invariant actually catches it (the
// "check that cannot fail is worthless" trap AC-3/AC-4/AC-6/AC-12 call
// out).

func TestPeopleInvariant_Balanced(t *testing.T) {
	inv := NewPeopleInvariant()
	state := NewSnapshot(1)
	// 100 citizens, +3 births, -1 death, +2 immigration, -1 emigration:
	// tracked delta = 3-1+2-1 = 3, so closing should be 103.
	state.Readings[StockPeople] = StockReading{Registered: true, Opening: 100, Closing: 103, TrackedDelta: 3}

	got := inv.Check(state)
	if !got.Ran {
		t.Fatal("Check.Ran = false for a registered stock, want true")
	}
	if got.Violation.Detected {
		t.Fatalf("Check reported a Violation on a legitimately balanced tick: %+v", got.Violation)
	}
}

// TestPeopleInvariant_DetectsUntrackedDisappearance is AC-3's
// deliberately-broken fixture: a citizen vanishes (closing count drops)
// with NO matching tracked death/migration event.
func TestPeopleInvariant_DetectsUntrackedDisappearance(t *testing.T) {
	inv := NewPeopleInvariant()
	state := NewSnapshot(7)
	// 50 citizens open, 49 close, but TrackedDelta says 0 change — one
	// citizen vanished untracked.
	state.Readings[StockPeople] = StockReading{
		Registered: true, Opening: 50, Closing: 49, TrackedDelta: 0,
		Suspects: []string{"citizen-0042"},
	}

	got := inv.Check(state)
	if !got.Ran {
		t.Fatal("Check.Ran = false, want true (stock was registered)")
	}
	if !got.Violation.Detected {
		t.Fatal("Check did not detect an untracked citizen disappearance — the invariant cannot fail, which makes it worthless")
	}
	if got.Violation.InvariantName != "people" {
		t.Errorf("Violation.InvariantName = %q, want %q", got.Violation.InvariantName, "people")
	}
	if got.Violation.Expected != 0 || got.Violation.Actual != -1 {
		t.Errorf("Violation.Expected/Actual = %d/%d, want 0/-1", got.Violation.Expected, got.Violation.Actual)
	}
	if len(got.Violation.EntityIDs) != 1 || got.Violation.EntityIDs[0] != "citizen-0042" {
		t.Errorf("Violation.EntityIDs = %v, want [citizen-0042]", got.Violation.EntityIDs)
	}
}

func TestMoneyInvariant_Balanced(t *testing.T) {
	inv := NewMoneyInvariant()
	state := NewSnapshot(1)
	state.Readings[StockMoney] = StockReading{Registered: true, Opening: 1_000_000, Closing: 1_050_000, TrackedDelta: 50_000}

	got := inv.Check(state)
	if got.Violation.Detected {
		t.Fatalf("Check reported a Violation on a legitimately balanced tick: %+v", got.Violation)
	}
}

// TestMoneyInvariant_DetectsUntrackedCreation is AC-4's deliberately
// broken fixture: money appears in the total with no matching tracked
// income event.
func TestMoneyInvariant_DetectsUntrackedCreation(t *testing.T) {
	inv := NewMoneyInvariant()
	state := NewSnapshot(3)
	// Total jumps by 500,000 micropounds but tracked income/expenditure
	// records zero net change — money created from nowhere.
	state.Readings[StockMoney] = StockReading{Registered: true, Opening: 10_000_000, Closing: 10_500_000, TrackedDelta: 0}

	got := inv.Check(state)
	if !got.Violation.Detected {
		t.Fatal("Check did not detect untracked money creation — the invariant cannot fail, which makes it worthless")
	}
	if got.Violation.Actual-got.Violation.Expected != 500_000 {
		t.Errorf("unexplained amount = %d, want 500000", got.Violation.Actual-got.Violation.Expected)
	}
}

func TestGoodsInvariant_SyntheticStockScenario(t *testing.T) {
	inv := NewGoodsInvariant()

	t.Run("balanced", func(t *testing.T) {
		state := NewSnapshot(1)
		// 200 units open, +30 produced, -20 consumed, +5 net in-transit:
		// tracked delta = 15, closing should be 215.
		state.Readings[StockGoods] = StockReading{Registered: true, Opening: 200, Closing: 215, TrackedDelta: 15}
		got := inv.Check(state)
		if got.Violation.Detected {
			t.Fatalf("Check reported a Violation on a legitimately balanced synthetic goods tick: %+v", got.Violation)
		}
	})

	// AC-5's deliberately-broken fixture: goods vanish from the tracked
	// stock with no matching production/consumption/transit record.
	t.Run("untracked loss", func(t *testing.T) {
		state := NewSnapshot(1)
		state.Readings[StockGoods] = StockReading{Registered: true, Opening: 200, Closing: 150, TrackedDelta: 0}
		got := inv.Check(state)
		if !got.Violation.Detected {
			t.Fatal("Check did not detect an untracked goods loss — the invariant cannot fail, which makes it worthless")
		}
	})
}

func TestVehicleInvariant_Balanced(t *testing.T) {
	inv := NewVehicleInvariant()
	state := NewSnapshot(1)
	// 10 vehicles open, +2 spawned, -1 despawned: tracked delta = 1.
	state.Readings[StockVehicles] = StockReading{Registered: true, Opening: 10, Closing: 11, TrackedDelta: 1}

	got := inv.Check(state)
	if got.Violation.Detected {
		t.Fatalf("Check reported a Violation on a legitimately balanced tick: %+v", got.Violation)
	}
}

// TestVehicleInvariant_DetectsCountMismatch is AC-6's deliberately
// broken fixture — §19.3's despawn-masking failure mode: a vehicle's
// count changes with no matching spawn/despawn event.
func TestVehicleInvariant_DetectsCountMismatch(t *testing.T) {
	inv := NewVehicleInvariant()
	state := NewSnapshot(9)
	state.Readings[StockVehicles] = StockReading{
		Registered: true, Opening: 40, Closing: 38, TrackedDelta: 0,
		Suspects: []string{"vehicle-1001", "vehicle-1002"},
	}

	got := inv.Check(state)
	if !got.Violation.Detected {
		t.Fatal("Check did not detect a vehicle-count mismatch with no matching spawn/despawn event — despawn-masking would be structurally possible, which is exactly what §19.3 forbids")
	}
	if len(got.Violation.EntityIDs) != 2 {
		t.Errorf("Violation.EntityIDs = %v, want 2 entries", got.Violation.EntityIDs)
	}
}

// TestConservationInvariant_UnregisteredStockSkipped is AC-12's direct
// per-invariant check: an invariant whose stock was never reported for
// the tick does not crash and does not false-flag — it reports Ran:
// false.
func TestConservationInvariant_UnregisteredStockSkipped(t *testing.T) {
	invariants := []Invariant{NewPeopleInvariant(), NewMoneyInvariant(), NewGoodsInvariant(), NewVehicleInvariant()}
	state := NewSnapshot(1) // no readings populated at all

	for _, inv := range invariants {
		got := inv.Check(state)
		if got.Ran {
			t.Errorf("%s: Check.Ran = true for a completely empty Snapshot, want false", inv.Name())
		}
		if got.Violation.Detected {
			t.Errorf("%s: Check reported a Violation for an unregistered stock — false-flagged an assumed zero", inv.Name())
		}
	}
}

// TestStockCheck_OverflowSaturatesNotWraps is SEC-055's twin for the four v1
// stock invariants: a Closing−Opening subtraction that overflows int64 must
// saturate and fail the identity, not wrap into a value that equals
// TrackedDelta and reports "balanced". Closing−Opening = MaxInt64 − (−1)
// overflows to MinInt64 under wrapping arithmetic, so TrackedDelta is set to
// that wrapped value: the old code reports balanced, the fixed code must not.
func TestStockCheck_OverflowSaturatesNotWraps(t *testing.T) {
	inv := NewPeopleInvariant()
	state := NewSnapshot(1)
	state.Readings[StockPeople] = StockReading{
		Registered: true, Opening: -1, Closing: math.MaxInt64, TrackedDelta: math.MinInt64,
	}

	got := inv.Check(state)
	if !got.Ran {
		t.Fatal("Check.Ran = false, want true (stock was registered)")
	}
	if !got.Violation.Detected {
		t.Fatal("Closing−Opening overflowed and was reported balanced — the invariant silently false-negatived (SEC-055)")
	}
}
