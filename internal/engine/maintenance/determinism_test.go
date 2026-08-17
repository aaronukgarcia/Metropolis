package maintenance

import (
	"bytes"
	"encoding/binary"
	"math"
	"sort"
	"testing"
)

// fingerprint encodes the module's full observable state — every per-instance
// MaintenanceView (in sorted structure-id order), the per-class backlog (in
// sorted class order), the total backlog, and the city-wide demand — as a
// fixed-width binary string, so two runs are compared byte-for-byte.
func fingerprint(t *testing.T, a *MaintenanceAPI) []byte {
	t.Helper()
	var buf bytes.Buffer
	putU := func(u uint64) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], u)
		buf.Write(b[:])
	}
	putI := func(i int64) { putU(uint64(i)) }
	putF := func(f float64) { putU(math.Float64bits(f)) }

	a.mu.RLock()
	ids := a.sortedIDsLocked()
	for _, id := range ids {
		v := a.viewLocked(a.instances[id])
		putI(int64(v.StructureID))
		putI(int64(len(v.Class)))
		buf.WriteString(string(v.Class))
		putI(v.AgeMonths)
		putF(v.AgeYears)
		putF(v.Efficiency)
		putI(v.BaseEngineerDaysPerYear)
		putI(v.RepairDemandPerYear)
		putI(v.LifetimeYears)
		if v.NeedsRefit {
			putU(1)
		} else {
			putU(0)
		}
	}
	classes := make([]string, 0, len(a.backlog))
	for c := range a.backlog {
		classes = append(classes, string(c))
	}
	sort.Strings(classes)
	for _, c := range classes {
		putI(int64(len(c)))
		buf.WriteString(c)
		putI(a.backlog[Class(c)])
	}
	putI(a.backlogTotal)
	demand := CityDemand{}
	var sum int64
	for _, id := range ids {
		sum += a.viewLocked(a.instances[id]).RepairDemandPerYear
	}
	demand.RepairDemandPerYear = sum
	demand.Backlog = a.backlogTotal
	putI(demand.RepairDemandPerYear)
	putI(demand.Backlog)
	a.mu.RUnlock()

	return buf.Bytes()
}

// buildDeterministic runs an identical command sequence over the given
// registration order: register three structures, age the world, enqueue two
// jobs, apply a partial budget.
func buildDeterministic(t *testing.T, order []StructureID) *MaintenanceAPI {
	t.Helper()
	a := newTestAPI(t)
	for _, id := range order {
		class := Class("dwelling")
		if id%2 == 0 {
			class = "heavy_industry"
		}
		if err := a.Register(id, class, RegisterOptions{}, "test"); err != nil {
			t.Fatalf("register %d: %v", id, err)
		}
	}
	if err := a.AdvanceMonth(36, "test"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if _, err := a.EnqueueJob("dwelling", 10, "test"); err != nil {
		t.Fatalf("enqueue dwelling: %v", err)
	}
	if _, err := a.EnqueueJob("heavy_industry", 40, "test"); err != nil {
		t.Fatalf("enqueue heavy: %v", err)
	}
	if err := a.SetDailyBudget(30, "test"); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	if _, err := a.RunCrewDay("test"); err != nil {
		t.Fatalf("run crew day: %v", err)
	}
	return a
}

// TestDeterminism proves AC-13/GR#21: the maintenance tick (aging, efficiency
// derivation, backlog growth, crew application) is a deterministic function of
// prior state, data, and injected inputs. Two runs with the same commands but
// DIFFERENT registration orders produce byte-identical state, because
// instances are iterated in sorted structure-id order and jobs in FIFO order
// — never map-iteration order.
func TestDeterminism(t *testing.T) {
	a1 := buildDeterministic(t, []StructureID{1, 2, 3})
	a2 := buildDeterministic(t, []StructureID{3, 1, 2})

	f1 := fingerprint(t, a1)
	f2 := fingerprint(t, a2)

	if !bytes.Equal(f1, f2) {
		t.Fatalf("determinism failure: identical commands with different registration order produced different state (%d vs %d bytes)",
			len(f1), len(f2))
	}
}
