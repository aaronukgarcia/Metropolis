package capexport

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
)

// validTestConfig builds a valid Config carrying all ten §36 lines with a
// uniform test rate, so any test can bind any line. Generated from the Go
// enum (ExportableServices) rather than hand-authored, so it cannot drift from
// the enum.
func validTestConfig() Config {
	cfg := Config{
		Version:                        1,
		SpecRef:                        "§36",
		ProjectionDemandGrowthPerMonth: 0.05,
	}
	for _, line := range ExportableServices {
		cfg.Services = append(cfg.Services, ExportableDef{
			ID:              line,
			Label:           "Label " + string(line),
			Unit:            "unit",
			RateMicropounds: 1_000_000,
			Placeholder:     true,
			SpecRef:         "§36",
		})
	}
	return cfg
}

// newTestAPI constructs a CapExportAPI with all three dependencies wired and
// the treasury seeded with a credit line (so penalty postings never fail on an
// empty treasury). Returns the API and its wired dependencies for the test to
// drive the seams directly.
func newTestAPI(t *testing.T) (*CapExportAPI, *services.ServicesAPI, *finance.FinanceAPI, *projections.ProjectionsAPI) {
	t.Helper()
	a, err := New(validTestConfig(), "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	svc := services.New("test")
	fin := finance.NewFinanceAPI("test")
	proj := projections.NewProjectionsAPI()

	if err := a.SetServices(svc); err != nil {
		t.Fatalf("SetServices: %v", err)
	}
	if err := a.SetFinance(fin); err != nil {
		t.Fatalf("SetFinance: %v", err)
	}
	if err := a.SetProjections(proj); err != nil {
		t.Fatalf("SetProjections: %v", err)
	}
	if err := fin.SetCreditLine(finance.AcctTreasury, finance.Money(1_000_000_000)); err != nil {
		t.Fatalf("SetCreditLine: %v", err)
	}
	return a, svc, fin, proj
}

// registerService registers one engine.services instance with the given
// capacity ceiling, and returns its ServiceID.
func registerService(t *testing.T, svc *services.ServicesAPI, id services.ServiceID, capacity float64) services.ServiceID {
	t.Helper()
	err := svc.RegisterService(services.ServiceSpec{
		ID:             id,
		Kind:           services.ServiceHealthcare,
		CapacityRaw:    "test",
		CoverageRadius: 10,
		X:              1,
		Y:              1,
		StaffingNeed:   5,
		UpgradePath: []services.UpgradeStep{
			{CapacityCeiling: capacity},
		},
	})
	if err != nil {
		t.Fatalf("RegisterService: %v", err)
	}
	return id
}

// setDemand drives the bound service's internal demand.
func setDemand(t *testing.T, svc *services.ServicesAPI, id services.ServiceID, demand float64) {
	t.Helper()
	if err := svc.UpdateDemand(id, demand, 0); err != nil {
		t.Fatalf("UpdateDemand: %v", err)
	}
}

// bindLine binds a line to a service instance and fails the test on error.
func bindLine(t *testing.T, a *CapExportAPI, line ExportableService, id services.ServiceID) {
	t.Helper()
	if err := a.BindServiceLine(line, id); err != nil {
		t.Fatalf("BindServiceLine(%s): %v", line, err)
	}
}
