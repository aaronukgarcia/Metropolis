package social

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
)

// TestInterventionMarkerWrittenAtDecision (AC-6): an underfunded child-
// protection intervention writes a documented marker to the affected
// citizen's record through engine.citizens' command path AT the decision
// month — the marker is queryable through CitizensAPI immediately, with no
// elapsed time, without re-deriving it.
func TestInterventionMarkerWrittenAtDecision(t *testing.T) {
	c, err := citizens.NewCitizensAPI(1, "test")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	seedCitizen(t, c, 42, 0)

	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.SetCitizens(c); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}

	// Underfunded family support: quality below the harm threshold.
	if _, err := a.RecordChildProtectionIntervention(42, 5, 0.1); err != nil {
		t.Fatalf("RecordChildProtectionIntervention underfunded: %v", err)
	}
	got, ok := c.CitizenAt(42, "test")
	if !ok {
		t.Fatal("citizen 42 must exist after the intervention")
	}
	if got.HealthBand != citizens.HealthCritical {
		t.Fatalf("underfunded intervention must write a HealthCritical marker, got %v", got.HealthBand)
	}
}

// TestCohortAuditMarkerReflectsFunding (AC-6, the adequate-funding half): an
// adequately-funded intervention writes the stabilised marker instead, so the
// marker genuinely records the funding outcome rather than a constant.
func TestCohortAuditMarkerReflectsFunding(t *testing.T) {
	c, err := citizens.NewCitizensAPI(1, "test")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	seedCitizen(t, c, 7, 0)

	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.SetCitizens(c); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}
	if _, err := a.RecordChildProtectionIntervention(7, 5, 0.9); err != nil {
		t.Fatalf("RecordChildProtectionIntervention funded: %v", err)
	}
	got, ok := c.CitizenAt(7, "test")
	if !ok {
		t.Fatal("citizen 7 must exist")
	}
	if got.HealthBand != citizens.HealthFair {
		t.Fatalf("funded intervention must write a HealthFair marker, got %v", got.HealthBand)
	}
}

// TestHomelessnessRoughSleepingAttributedToTownCentre (AC-7): with prevention
// off, housing-first off, and hostel capacity exhausted, homelessness cases
// fail across all three paths and the rough-sleeping count rises, attributed
// to the documented town-centre location.
func TestHomelessnessRoughSleepingAttributedToTownCentre(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := services.New("test")
	if err := a.SetServices(svc); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if err := a.RegisterServices(); err != nil {
		t.Fatalf("RegisterServices: %v", err)
	}
	if err := a.SetPrevention(false); err != nil {
		t.Fatalf("SetPrevention: %v", err)
	}
	if err := a.SetHousingFirst(false); err != nil {
		t.Fatalf("SetHousingFirst: %v", err)
	}

	// Deprivation 1.0 opens 3 homelessness cases (rate 3), hostel capacity 2.
	_ = a.AdvanceMonth(1, DriverInputs{Deprivation: 1.0})
	if err := a.RouteHomelessness(1); err != nil {
		t.Fatalf("RouteHomelessness: %v", err)
	}

	if got := a.HostelPlaced(); got != 2 {
		t.Fatalf("hostel capacity 2 should place exactly 2, got %d", got)
	}
	if got := a.RoughSleeping(); got != 1 {
		t.Fatalf("one case must fall through to rough sleeping, got %d", got)
	}
	if got := a.RoughSleepingLocation(); got != "town-centre" {
		t.Fatalf("rough sleeping must be attributed to the town centre, got %q", got)
	}
}

// TestCarersReleasedRisesWithFunding (AC-8): at fixed caseload, increasing
// disability & carers funding increases the released-carers figure — a real
// labour-supply effect, not a satisfaction-only number.
func TestCarersReleasedRisesWithFunding(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := services.New("test")
	if err := a.SetServices(svc); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if err := a.RegisterServices(); err != nil {
		t.Fatalf("RegisterServices: %v", err)
	}

	if err := a.SetFunding(FundingCommand{Category: CategoryDisabilityCarers, Level: 0.2, Month: 0}); err != nil {
		t.Fatalf("SetFunding low: %v", err)
	}
	low := a.CarersReleased()
	if err := a.SetFunding(FundingCommand{Category: CategoryDisabilityCarers, Level: 0.8, Month: 1}); err != nil {
		t.Fatalf("SetFunding high: %v", err)
	}
	high := a.CarersReleased()
	if high <= low {
		t.Fatalf("increasing funding must increase released carers: low=%d high=%d", low, high)
	}
}

// TestFosterCapacityExhaustionQueues (AC-9): exhausting foster-placement
// capacity makes the next placement attempt return a documented queued state,
// never a silently-succeeded placement.
func TestFosterCapacityExhaustionQueues(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := services.New("test")
	if err := a.SetServices(svc); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if err := a.RegisterServices(); err != nil {
		t.Fatalf("RegisterServices: %v", err)
	}

	// Open 3 fostering cases (crowding 5 + financial 5 → 10, but we only
	// exercise the first three).
	_ = a.AdvanceMonth(1, DriverInputs{CrowdingStress: 2, FinancialStress: 2})
	ids := a.OpenCaseIDs(CategoryFostering)
	if len(ids) < 3 {
		t.Fatalf("expected at least 3 fostering cases, got %d", len(ids))
	}

	if r, err := a.AttemptFosteringPlacement(ids[0], 1); err != nil || r != PlacementPlaced {
		t.Fatalf("first placement must succeed: r=%v err=%v", r, err)
	}
	if r, err := a.AttemptFosteringPlacement(ids[1], 1); err != nil || r != PlacementPlaced {
		t.Fatalf("second placement must succeed (capacity 2): r=%v err=%v", r, err)
	}
	if r, err := a.AttemptFosteringPlacement(ids[2], 1); err != nil || r != PlacementQueued {
		t.Fatalf("third placement must queue at capacity, got r=%v err=%v", r, err)
	}
}
