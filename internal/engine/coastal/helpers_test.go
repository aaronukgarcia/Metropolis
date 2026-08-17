package coastal

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// testConfig returns a Config with small, known magnitudes so the mechanism
// tests assert structural properties (trade-off directions, overflow
// presence) rather than the data file's placeholder magnitudes. Every field
// that a test varies is overridden by that test; the defaults here are
// chosen so a single arrival has size 1 (maxBoatSize 1), a duration of
// exactly 1 month (min=max=1), and grants by default.
func testConfig() Config {
	cfg := Config{
		BaseArrivalRate:      1.0,
		MaxBoatSize:          1,
		MaxArrivalsPerMonth:  50,
		WorldConditionsScale: 0,
		Rescue: RescueConfig{
			CoastguardServiceID: "coastguard",
			LifeboatServiceID:   "lifeboat",
		},
		Reception: ReceptionConfig{
			CaseworkerThroughputPerMonth: 10,
			HotelCostPerCase:             1000,
			SatisfactionFrictionPerCase:  0.1,
		},
		Pipeline: PipelineConfig{
			MinMonths:            1,
			MaxMonths:            1,
			GrantRate:            0.0, // granted tests override to 1.0; the neutral default needs no citizens dep
			DepartureCostPerCase: 500,
			MaxReductionMonths:   0,
		},
		Policy: PolicyConfig{
			ProcessingFundingDefault:                 0.5,
			ProcessingFundingThroughputGainPerUnit:   1.0,
			ProcessingFundingOpexPerUnitPerMonth:     1000,
			HousingApproachDefault:                   0.0,
			HousingApproachCostPerUnitPerMonth:       -100,
			HousingApproachFrictionIncreasePerUnit:   0.5,
			HousingApproachIntegrationPenaltyPerUnit: 0.3,
			IntegrationInvestmentDefault:             0.0,
			IntegrationInvestmentGainPerUnit:         0.6,
			IntegrationInvestmentOpexPerUnitPerMonth: 1000,
		},
		WorldProfile: WorldProfileConfig{AttainmentMean: 30, AttainmentSpread: 0},
	}
	for i := range cfg.EraMultipliers {
		cfg.EraMultipliers[i] = 1.0
	}
	for i := range cfg.SeasonMultipliers {
		cfg.SeasonMultipliers[i] = 1.0
	}
	return cfg
}

// fakeShore is a deterministic in-memory ShoreSource for tests.
type fakeShore struct {
	cells []CellCoord
	set   map[CellCoord]bool
}

func (f fakeShore) ShoreCells() []CellCoord { return f.cells }

func (f fakeShore) IsShore(c CellCoord) bool { return f.set[c] }

// newFakeShore builds a fake shore source where every listed cell is shore.
func newFakeShore(cells ...CellCoord) fakeShore {
	set := make(map[CellCoord]bool, len(cells))
	for _, c := range cells {
		set[c] = true
	}
	return fakeShore{cells: cells, set: set}
}

// oneCell is a single shore cell for the minimal fixture.
var oneCell = CellCoord{X: 1, Y: 1}

// mustAPI builds a CoastalAPI from cfg with a fixed seed and wires the given
// shore source, failing the test on any error.
func mustAPI(t *testing.T, cfg Config, shore ShoreSource) *CoastalAPI {
	t.Helper()
	api, err := New(42, cfg, "corr-test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := api.SetShore(shore); err != nil {
		t.Fatalf("SetShore: %v", err)
	}
	return api
}

// registerRescueServices registers a "coastguard" and a "lifeboat" service
// with the given capacity ceilings into a real services API (AC-4's
// "reduce coastguard capacity via engine.services").
func registerRescueServices(t *testing.T, svc *services.ServicesAPI, coastguardID, lifeboatID string, coastguardCap, lifeboatCap float64) {
	t.Helper()
	if err := svc.RegisterKind(services.ServiceKind("coastguard"), services.KindDef{Name: "Coastguard"}); err != nil {
		t.Fatalf("RegisterKind coastguard: %v", err)
	}
	if err := svc.RegisterKind(services.ServiceKind("lifeboat"), services.KindDef{Name: "Lifeboat"}); err != nil {
		t.Fatalf("RegisterKind lifeboat: %v", err)
	}
	if err := svc.RegisterService(services.ServiceSpec{
		ID:          services.ServiceID(coastguardID),
		Kind:        services.ServiceKind("coastguard"),
		UpgradePath: []services.UpgradeStep{{CapacityCeiling: coastguardCap}},
	}); err != nil {
		t.Fatalf("RegisterService coastguard: %v", err)
	}
	if err := svc.RegisterService(services.ServiceSpec{
		ID:          services.ServiceID(lifeboatID),
		Kind:        services.ServiceKind("lifeboat"),
		UpgradePath: []services.UpgradeStep{{CapacityCeiling: lifeboatCap}},
	}); err != nil {
		t.Fatalf("RegisterService lifeboat: %v", err)
	}
}

// assertRegistryCode asserts err is a registry-sourced *errs.E with the given
// code (GR#7 assertion — the code matches, not merely "an error happened").
func assertRegistryCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s, got nil", code)
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("expected *errs.E, got %T (%v)", err, err)
	}
	if e.Code != code {
		t.Fatalf("expected error code %s, got %s (%s)", code, e.Code, e.Display())
	}
}
