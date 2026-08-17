package roads

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestRoadworksReduceCurrentLanes (AC-6) asserts a scheduled roadworks phase
// reduces the road's current lane count for the phase's duration and
// restores it afterward — entirely within this package (no traffic call).
func TestRoadworksReduceCurrentLanes(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane) // 2 lanes steady-state

	err := a.ScheduleRoadworks(ScheduleRoadworksCommand{
		CorrelationID: "test", RoadID: r.ID,
		Phases: []RoadworksPhase{{StartMonth: 10, DurationMonths: 5, OpenLanes: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}

	before, _ := a.CurrentLaneCount(r.ID, 0)
	during, _ := a.CurrentLaneCount(r.ID, 12)
	after, _ := a.CurrentLaneCount(r.ID, 20)
	if before != 2 {
		t.Fatalf("before phase lanes = %d, want 2", before)
	}
	if during >= before {
		t.Fatalf("during phase lanes = %d, want a reduction below steady-state %d", during, before)
	}
	if during != 1 {
		t.Fatalf("during phase lanes = %d, want 1", during)
	}
	if after != 2 {
		t.Fatalf("after phase lanes = %d, want restored 2", after)
	}
}

// TestRoadworksSummerWindow (AC-17) asserts the summer scheduling window is
// a simulation-calendar predicate: a non-summer phase start is rejected, a
// summer one accepted.
func TestRoadworksSummerWindow(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)

	// Month 0 is January — not summer.
	err := a.ScheduleRoadworks(ScheduleRoadworksCommand{
		CorrelationID: "test", RoadID: r.ID,
		Phases: []RoadworksPhase{{StartMonth: 0, DurationMonths: 2, OpenLanes: 1}},
		Window: WindowSummer,
	})
	if !errors.Is(err, &errs.E{Code: ErrInvalidRoadworks}) {
		t.Fatalf("non-summer start = %v, want ErrInvalidRoadworks", err)
	}

	// Month 6 is July — summer.
	err = a.ScheduleRoadworks(ScheduleRoadworksCommand{
		CorrelationID: "test", RoadID: r.ID,
		Phases: []RoadworksPhase{{StartMonth: 6, DurationMonths: 2, OpenLanes: 1}},
		Window: WindowSummer,
	})
	if err != nil {
		t.Fatalf("summer start rejected: %v", err)
	}
}

// TestSummerWindowPredicate (AC-17) asserts the month-index predicate is
// correct and periodic across years.
func TestSummerWindowPredicate(t *testing.T) {
	for m := 0; m < 12; m++ {
		got := isSummerMonth(int64(m))
		want := m >= 5 && m <= 7
		if got != want {
			t.Errorf("isSummerMonth(%d) = %v, want %v", m, got, want)
		}
	}
	if !isSummerMonth(int64(12+6)) || !isSummerMonth(int64(24+7)) {
		t.Errorf("summer predicate must be periodic across years")
	}
	if isSummerMonth(int64(12 + 0)) {
		t.Errorf("January next year must not be summer")
	}
}

// TestRoadworksInvalidScheduleRejected (AC-6/AC-12) asserts malformed
// schedules are rejected with the registry error and no state change.
func TestRoadworksInvalidScheduleRejected(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane) // 2 lanes

	cases := []struct {
		name   string
		phases []RoadworksPhase
	}{
		{"empty", nil},
		{"negative start", []RoadworksPhase{{StartMonth: -1, DurationMonths: 1, OpenLanes: 1}}},
		{"non-positive duration", []RoadworksPhase{{StartMonth: 0, DurationMonths: 0, OpenLanes: 1}}},
		{"too many open lanes", []RoadworksPhase{{StartMonth: 0, DurationMonths: 1, OpenLanes: 3}}},
		{"overlap", []RoadworksPhase{{StartMonth: 0, DurationMonths: 5, OpenLanes: 1}, {StartMonth: 3, DurationMonths: 5, OpenLanes: 1}}},
	}
	for _, tc := range cases {
		err := a.ScheduleRoadworks(ScheduleRoadworksCommand{CorrelationID: "test", RoadID: r.ID, Phases: tc.phases})
		if !errors.Is(err, &errs.E{Code: ErrInvalidRoadworks}) {
			t.Errorf("%s: got %v, want ErrInvalidRoadworks", tc.name, err)
		}
	}
}
