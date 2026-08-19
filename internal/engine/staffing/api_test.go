package staffing

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func TestStaffing_AC2_DataSourcedWages(t *testing.T) {
	api := New()
	tempDir, err := os.MkdirTemp("", "staffing-config-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	configData := `{"localWage": 65.0, "contractorCost": 95.0, "contractorCapacity": 12}`
	_ = os.WriteFile(filepath.Join(tempDir, "staffing.json"), []byte(configData), 0644)

	err = api.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("unexpected error loading config: %v", err)
	}

	if api.cfg.LocalWage != 65.0 || api.cfg.ContractorCost != 95.0 {
		t.Errorf("expected data-sourced wage 65.0/95.0, got %f/%f", api.cfg.LocalWage, api.cfg.ContractorCost)
	}
}

func TestStaffing_AC3_DirectionalOperatorDemand(t *testing.T) {
	api := New()

	// Register a hospital with 10 nurses need, and a clinic with 2 nurses need
	_ = api.RegisterBuilding(101, "hospital", 10)
	_ = api.RegisterBuilding(102, "hospital", 2)
	_ = api.RegisterBuilding(201, "school", 5)

	dem101, role101, _ := api.OperatorDemandFor(101)
	dem102, _, _ := api.OperatorDemandFor(102)
	_, role201, _ := api.OperatorDemandFor(201)

	// Hospital demands more than clinic
	if dem101 <= dem102 {
		t.Errorf("expected hospital demand (%d) > clinic (%d)", dem101, dem102)
	}

	// Distinct roles for school vs hospital
	if role101 != "nurse" || role201 != "teacher" {
		t.Errorf("expected distinct roles nurse/teacher, got %s/%s", role101, role201)
	}
}

func TestStaffing_AC4_SharedPoolIntegration(t *testing.T) {
	api := New()

	// Load real ServicesAPI
	servicesAPI, err := services.LoadDefault("test-staffing")
	if err != nil {
		t.Fatalf("failed to load services: %v", err)
	}
	_ = api.SetServices(servicesAPI)

	_ = api.RegisterBuilding(101, "hospital", 5)
	_ = api.RegisterBuilding(102, "hospital", 5)

	// Register the two hospitals as service instances inside servicesAPI (AC-4)
	_ = servicesAPI.RegisterService(services.ServiceSpec{
		ID:           "101",
		Kind:         "healthcare",
		StaffingNeed: 5.0,
	})
	_ = servicesAPI.RegisterService(services.ServiceSpec{
		ID:           "102",
		Kind:         "healthcare",
		StaffingNeed: 5.0,
	})

	// Set pool staff directly via servicesAPI
	_ = servicesAPI.SetPoolStaff("nursing", 6.0)

	// Assigned and resolved in Services staffing pool
	alloc, err := servicesAPI.AllocateStaffing("nursing")
	if err != nil {
		t.Fatalf("unexpected allocation error: %v", err)
	}

	// Ratio = 6.0 / (5.0 + 5.0) = 0.6. Both members get 3.0
	if len(alloc) < 2 || alloc[0].Allocated != 3.0 || alloc[1].Allocated != 3.0 {
		t.Errorf("shared pool allocation failed: %+v", alloc)
	}
}

func TestStaffing_AC5_AC6_RepairDemand(t *testing.T) {
	api := New()

	// Initial aggregated repair demand fallback should be 250.0
	dem, err := api.RepairDemand()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dem != 250.0 {
		t.Errorf("expected aggregated repair demand 250.0, got %f", dem)
	}

	// Initial shortfall should equal demand when no staff is assigned
	short, err := api.RepairShortfall()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if short != 250.0 {
		t.Errorf("expected initial repair shortfall 250.0, got %f", short)
	}
}

func TestStaffing_AC7_AC8_SkillsAndLabourPool(t *testing.T) {
	api := New()

	// Load real CitizensAPI
	citAPI, err := citizens.NewCitizensAPI(12345, "test-staffing")
	if err != nil {
		t.Fatalf("failed to create citizens api: %v", err)
	}
	_ = api.SetCitizens(citAPI)

	// Seed real citizens:
	// A: uneducated (StageNone)
	// B: university graduate (StageUniversity)
	_ = citAPI.SeedColdRecords([]citizens.ColdRecord{
		{ID: 1, BirthMonth: 120, Sex: citizens.SexMale, Stage: citizens.StageNone},
		{ID: 2, BirthMonth: 120, Sex: citizens.SexFemale, Stage: citizens.StageUniversity},
	}, "test-seed")

	_ = api.RegisterBuilding(101, "hospital", 5)

	// (a) Assigning uneducated citizen ID 1 fails skill gate
	err = api.AssignCitizen(101, 1)
	if err == nil {
		t.Error("expected error assigning uneducated citizen to nurse role")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrInvalidAssignment {
		t.Errorf("expected skill-gate error MET-G4101, got: %v", err)
	}

	// (b) Assigning qualified citizen ID 2 succeeds
	err = api.AssignCitizen(101, 2)
	if err != nil {
		t.Fatalf("unexpected qualified assignment error: %v", err)
	}

	filled, _ := api.FilledStaff(101)
	if filled != 1 {
		t.Errorf("expected filled staff 1, got %d", filled)
	}

	// Read back employment state from CitizensAPI
	c, _ := citAPI.CitizenAt(2, "test-verify")
	if c.Employment.State != citizens.EmploymentEmployed || c.Employment.Sector != citizens.SectorPublic {
		t.Errorf("expected citizen employed in public sector, got %+v", c.Employment)
	}
}

func TestStaffing_AC9_ConservationOneRole(t *testing.T) {
	api := New()
	_ = api.RegisterBuilding(101, "hospital", 5)
	_ = api.RegisterBuilding(102, "hospital", 5)

	// Assign C to building 101
	_ = api.AssignCitizen(101, 555)
	f1, _ := api.FilledStaff(101)
	if f1 != 1 {
		t.Errorf("expected filled staff to be 1, got %d", f1)
	}

	// Assign C to building 102 (should vacate from 101)
	_ = api.AssignCitizen(102, 555)
	f1_new, _ := api.FilledStaff(101)
	f2_new, _ := api.FilledStaff(102)

	if f1_new != 0 {
		t.Error("citizen should have been vacated from building 101")
	}
	if f2_new != 1 {
		t.Errorf("expected filled staff on building 102 to be 1, got %d", f2_new)
	}
}

func TestStaffing_AC10_OffMapContractors(t *testing.T) {
	api := New()
	_ = api.RegisterBuilding(101, "hospital", 15)

	// Local Wage is 50.0, Contractor Cost is 80.0
	if api.cfg.ContractorCost <= api.cfg.LocalWage {
		t.Errorf("contractor cost (%f) must exceed local wage (%f)", api.cfg.ContractorCost, api.cfg.LocalWage)
	}

	// Hire up to capacity 10
	err := api.HireContractors(101, 8)
	if err != nil {
		t.Fatalf("unexpected hire error: %v", err)
	}

	// Exceeding capacity should fail
	err = api.HireContractors(101, 5)
	if err == nil {
		t.Error("expected error for exceeding contractor capacity")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrContractorPoolExhausted {
		t.Errorf("expected contractor pool exhausted code, got: %v", err)
	}
}

func TestStaffing_AC10_ContractorsIntOverflow(t *testing.T) {
	api := New()
	_ = api.RegisterBuilding(101, "hospital", 15)

	// Step 1: Hire a valid amount
	err := api.HireContractors(101, 1)
	if err != nil {
		t.Fatalf("unexpected hire error: %v", err)
	}

	// Step 2: Attempt to trigger integer overflow with a massive count.
	// Int overflow would make contractorsHired + count negative, bypassing a simple
	// contractorsHired + count > capacity check.
	// Since max int is architecture dependent (int32 or int64), we can just use a large positive integer
	// that when added to 1 overflows positive bounds if possible, or just tests the checked add logic.
	// Actually, passing `int(^uint(0) >> 1)` which is MaxInt.
	maxInt := int(^uint(0) >> 1)

	err = api.HireContractors(101, maxInt)
	if err == nil {
		t.Error("expected error for exceeding contractor capacity via overflow")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrContractorPoolExhausted {
		t.Errorf("expected contractor pool exhausted code, got: %v", err)
	}
}

func TestStaffing_AC11_WagesFiscalPosting(t *testing.T) {
	api := New()

	// Load real FinanceAPI
	financeAPI := finance.NewFinanceAPI("test-finance")
	_ = api.SetFinance(financeAPI)

	// Give the treasury a credit line to prevent overdraft (AC-13)
	_ = financeAPI.SetCreditLine(finance.AcctTreasury, finance.Money(1000000))

	_ = api.RegisterBuilding(101, "hospital", 5)
	_ = api.AssignCitizen(101, 100) // assign local worker
	_ = api.HireContractors(101, 2) // hire 2 contractors

	err := api.AdvanceTick("test-finance-tick")
	if err != nil {
		t.Fatalf("unexpected advance tick error: %v", err)
	}

	// Verification of posted expenses
	// Local wage = 1 * 50.0 = 50
	// Contractor cost = 2 * 80.0 = 160
	wages := financeAPI.WagesPosted()
	opex := financeAPI.OpexTotal()

	if wages != 50 {
		t.Errorf("expected posted wages 50, got %d", wages)
	}
	if opex != 160 {
		t.Errorf("expected posted opex 160, got %d", opex)
	}
}

func TestStaffing_AC12_ErrorRegistrationSafety(t *testing.T) {
	api := New()

	// Register unknown building query
	_, _, err := api.OperatorDemandFor(999)
	if err == nil {
		t.Error("expected error for unregistered building ID")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrNoStaffingDemand {
		t.Errorf("expected error matching empty queue code, got: %v", err)
	}
}

func TestStaffing_AC13_Determinism(t *testing.T) {
	api1 := New()
	api2 := New()

	_ = api1.RegisterBuilding(101, "hospital", 5)
	_ = api2.RegisterBuilding(101, "hospital", 5)

	dem1, _, _ := api1.OperatorDemandFor(101)
	dem2, _, _ := api2.OperatorDemandFor(101)

	if dem1 != dem2 {
		t.Errorf("expected deterministic demand to be equal, got %d and %d", dem1, dem2)
	}
}

func TestStaffing_AC15_Concurrency(t *testing.T) {
	api := New()
	_ = api.RegisterBuilding(101, "hospital", 100)

	var wg sync.WaitGroup
	workers := 10
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func(citID uint64) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = api.AssignCitizen(101, citID+uint64(j))
				_, _ = api.FilledStaff(101)
			}
		}(uint64(i * 100))
	}

	wg.Wait()
}
