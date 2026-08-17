package roads

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestRoadsAPIHasNoExportedFields (AC-1/GR#20) asserts the RoadsAPI carries
// no exported mutable fields — all graph mutation is through commands, so no
// other package can reach in and write a road/node field directly.
func TestRoadsAPIHasNoExportedFields(t *testing.T) {
	typ := reflect.TypeOf(RoadsAPI{})
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).PkgPath == "" {
			t.Errorf("RoadsAPI.%s is an exported field — all state must be mutated via commands (AC-1)", typ.Field(i).Name)
		}
	}
}

// TestRoadCarriesIdentityFields (AC-2) asserts a fresh road carries class,
// lane count, speed limit, start/end node references and a maintenance
// condition, and that a fresh road's condition differs from a synthetically
// aged one.
func TestRoadCarriesIdentityFields(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)

	if r.Class != ClassTwoLane {
		t.Errorf("class = %s, want two_lane", r.Class.String())
	}
	if r.Lanes <= 0 {
		t.Errorf("lanes = %d, want > 0", r.Lanes)
	}
	if r.SpeedLimitKPH <= 0 {
		t.Errorf("speedLimitKPH = %d, want > 0", r.SpeedLimitKPH)
	}
	if r.Start == 0 || r.End == 0 || r.Start == r.End {
		t.Errorf("start/end nodes = %d/%d, want distinct non-zero refs", r.Start, r.End)
	}
	if r.Condition != 1.0 {
		t.Errorf("fresh condition = %v, want 1.0", r.Condition)
	}

	if err := a.Advance(120); err != nil {
		t.Fatal(err)
	}
	aged, err := a.RoadInfo(r.ID, 120)
	if err != nil {
		t.Fatal(err)
	}
	if aged.Condition >= 1.0 {
		t.Errorf("aged condition = %v, want < 1.0 (degraded over time)", aged.Condition)
	}
	if aged.Condition == r.Condition {
		t.Errorf("aged condition == fresh condition (%v); they must be distinct", aged.Condition)
	}
}

// TestRoadIdentityQueryHasOnlyIdentityFields (AC-8) asserts the identity
// query's return type carries only roads-owned fields — no Volume, VCRatio,
// TopFlows or Alternatives (those are engine.traffic's query surface).
func TestRoadIdentityQueryHasOnlyIdentityFields(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)
	info, err := a.RoadInfo(r.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name == "" || info.Start == 0 || info.End == 0 || info.Lanes == 0 {
		t.Errorf("identity fields incomplete: %+v", info)
	}

	typ := reflect.TypeOf(Road{})
	for _, banned := range []string{"Volume", "VCRatio", "TopFlows", "Alternatives"} {
		if _, ok := typ.FieldByName(banned); ok {
			t.Errorf("Road.%s field present — that data is engine.traffic's query surface (AC-8)", banned)
		}
	}
}

// TestSpeedLimitPlayerSettableWithinBounds (AC-2) asserts the speed limit is
// player-settable within class bounds and rejected outside them.
func TestSpeedLimitPlayerSettableWithinBounds(t *testing.T) {
	a := newTestAPI(t)
	r := addRoad(t, a, 1, 100, 100, 100, 110, ClassTwoLane)

	if err := a.SetSpeedLimit(SetSpeedLimitCommand{CorrelationID: "test", RoadID: r.ID, KPH: 50}); err != nil {
		t.Fatalf("SetSpeedLimit(50) within [30,60]: %v", err)
	}
	info, _ := a.RoadInfo(r.ID, 0)
	if info.SpeedLimitKPH != 50 {
		t.Fatalf("speed limit = %d, want 50", info.SpeedLimitKPH)
	}

	if err := a.SetSpeedLimit(SetSpeedLimitCommand{CorrelationID: "test", RoadID: r.ID, KPH: 200}); !errors.Is(err, &errs.E{Code: ErrSpeedLimitOutOfBounds}) {
		t.Fatalf("SetSpeedLimit(200) = %v, want ErrSpeedLimitOutOfBounds", err)
	}
}

// TestNonexistentRoadRejected (AC-12) asserts commands/query against a
// nonexistent road return the registry error, not a silent no-op.
func TestNonexistentRoadRejected(t *testing.T) {
	a := newTestAPI(t)

	if _, err := a.RoadInfo(999, 0); !errors.Is(err, &errs.E{Code: ErrRoadNotFound}) {
		t.Fatalf("RoadInfo(nonexistent) = %v, want ErrRoadNotFound", err)
	}
	if _, err := a.ApplyUpgrade(ApplyUpgradeCommand{CorrelationID: "test", RoadID: 999, TargetClass: ClassDualCarriageway}); !errors.Is(err, &errs.E{Code: ErrRoadNotFound}) {
		t.Fatalf("ApplyUpgrade(nonexistent) = %v, want ErrRoadNotFound", err)
	}
	if err := a.ScheduleRoadworks(ScheduleRoadworksCommand{CorrelationID: "test", RoadID: 999, Phases: []RoadworksPhase{{StartMonth: 0, DurationMonths: 1, OpenLanes: 1}}}); !errors.Is(err, &errs.E{Code: ErrRoadNotFound}) {
		t.Fatalf("ScheduleRoadworks(nonexistent) = %v, want ErrRoadNotFound", err)
	}
}
