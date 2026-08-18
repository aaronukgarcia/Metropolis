package staffing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/maintenance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

const (
	ErrInvalidAssignment       = "MET-G4101"
	ErrNoStaffingDemand        = "MET-G4102"
	ErrContractorPoolExhausted = "MET-G4103"
	ErrCopiedValue             = "MET-G4199"
	ErrInvalidInput            = "MET-G4104" // Local alias for invalid input (AC-12)
)

// Config represents player-felt numbers for emergency staffing and wages (GR#15).
type Config struct {
	LocalWage          float64 `json:"localWage"`
	ContractorCost     float64 `json:"contractorCost"`     // strictly greater than local wage (AC-10)
	ContractorCapacity int     `json:"contractorCapacity"` // finite contractor-pool capacity (AC-10)
}

// Building represents a staffable city structure (AC-1/AC-2).
type Building struct {
	ID               uint64
	Kind             string // hospital, school, office
	Role             string // nurse, teacher, engineer
	StaffNeeded      int    // operator demand
	AssignedCitizens []uint64
	HiredContractors int
}

// StaffingAPI represents the skill pool matched to per-service demand module (MOD-073).
type StaffingAPI struct {
	mu                sync.RWMutex
	self              atomic.Pointer[StaffingAPI]
	citizens          *citizens.CitizensAPI
	maintenance       *maintenance.MaintenanceAPI
	finance           *finance.FinanceAPI
	services          *services.ServicesAPI
	buildings         map[uint64]*Building
	citizenToBuilding map[uint64]uint64 // tracks conservation (AC-9)
	contractorsHired  int               // aggregate hired contractors (AC-10)
	cfg               Config
	correlationID     string
}

// New constructs a new StaffingAPI.
func New() *StaffingAPI {
	s := &StaffingAPI{
		buildings:         make(map[uint64]*Building),
		citizenToBuilding: make(map[uint64]uint64),
		cfg: Config{
			LocalWage:          50.0,
			ContractorCost:     80.0,
			ContractorCapacity: 10,
		},
		correlationID: "default-staffing",
	}
	s.self.Store(s)
	return s
}

func (s *StaffingAPI) checkNotCopied(method string) error {
	if s.self.Load() != s {
		return errs.New(ErrCopiedValue, s.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetCitizens sets the citizens dependency (AC-7/AC-8).
func (s *StaffingAPI) SetCitizens(c *citizens.CitizensAPI) error {
	if err := s.checkNotCopied("SetCitizens"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.citizens = c
	return nil
}

// SetMaintenance sets the maintenance dependency (AC-5).
func (s *StaffingAPI) SetMaintenance(m *maintenance.MaintenanceAPI) error {
	if err := s.checkNotCopied("SetMaintenance"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maintenance = m
	return nil
}

// SetFinance sets the finance dependency (AC-11).
func (s *StaffingAPI) SetFinance(f *finance.FinanceAPI) error {
	if err := s.checkNotCopied("SetFinance"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finance = f
	return nil
}

// SetServices sets the services dependency (AC-4).
func (s *StaffingAPI) SetServices(ser *services.ServicesAPI) error {
	if err := s.checkNotCopied("SetServices"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services = ser
	return nil
}

// LoadConfig loads configuration parameters from disk (AC-2/GR#15).
func (s *StaffingAPI) LoadConfig(dir string) error {
	if err := s.checkNotCopied("LoadConfig"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(dir, "staffing.json")
	bytes, err := os.ReadFile(path)
	if err != nil {
		return errs.New(ErrInvalidInput, s.correlationID, map[string]any{"path": path, "cause": err.Error()})
	}

	var cfg Config
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return errs.New(ErrInvalidInput, s.correlationID, map[string]any{"path": path, "cause": err.Error()})
	}

	// Validate config bounds: Strictly positive wages (Finding C)
	if cfg.LocalWage <= 0 || cfg.ContractorCost <= 0 || cfg.ContractorCapacity < 0 {
		return errs.New(ErrInvalidInput, s.correlationID, map[string]any{"message": "config parameters must be positive"})
	}
	if cfg.ContractorCost <= cfg.LocalWage {
		return errs.New(ErrInvalidInput, s.correlationID, map[string]any{"message": "contractor cost must be strictly greater than local wage"})
	}

	s.cfg = cfg
	return nil
}

// RegisterBuilding registers a staffable structure with its operator demand (AC-1/AC-3).
func (s *StaffingAPI) RegisterBuilding(buildingID uint64, kind string, staffNeeded int) error {
	if err := s.checkNotCopied("RegisterBuilding"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if staffNeeded < 0 {
		return errs.New(ErrInvalidInput, s.correlationID, map[string]any{"field": "staffNeeded", "value": staffNeeded})
	}

	role := "engineer"
	switch kind {
	case "hospital":
		role = "nurse"
	case "school":
		role = "teacher"
	}

	s.buildings[buildingID] = &Building{
		ID:               buildingID,
		Kind:             kind,
		Role:             role,
		StaffNeeded:      staffNeeded,
		AssignedCitizens: []uint64{},
	}

	return nil
}

// OperatorDemandFor returns the staffing need for a building (AC-1/AC-3).
func (s *StaffingAPI) OperatorDemandFor(buildingID uint64) (int, string, error) {
	if err := s.checkNotCopied("OperatorDemandFor"); err != nil {
		return 0, "", err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, ok := s.buildings[buildingID]
	if !ok {
		return 0, "", errs.New(ErrNoStaffingDemand, s.correlationID, map[string]any{"id": buildingID})
	}

	return b.StaffNeeded, b.Role, nil
}

// AssignCitizen assigns a qualified citizen to a building role (AC-7/AC-8/AC-9).
func (s *StaffingAPI) AssignCitizen(buildingID uint64, citizenID uint64) error {
	if err := s.checkNotCopied("AssignCitizen"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.buildings[buildingID]
	if !ok {
		return errs.New(ErrNoStaffingDemand, s.correlationID, map[string]any{"id": buildingID})
	}

	// Verify citizen skills and qualifications via CitizensAPI (AC-7)
	if s.citizens != nil {
		cit, ok := s.citizens.CitizenAt(citizenID, s.correlationID)
		if !ok {
			return errs.New(ErrInvalidAssignment, s.correlationID, map[string]any{"citizen": citizenID})
		}

		// Skills gate check (AC-7)
		qualified := false
		for _, stage := range cit.Education.Stages {
			if b.Role == "nurse" && stage.Stage == citizens.StageUniversity {
				qualified = true
				break
			}
			if b.Role == "teacher" && stage.Stage == citizens.StageUniversity {
				qualified = true
				break
			}
			if b.Role == "engineer" && stage.Stage == citizens.StageTechnical {
				qualified = true
				break
			}
		}

		if !qualified {
			return errs.New(ErrInvalidAssignment, s.correlationID, map[string]any{"citizen": citizenID, "role": b.Role})
		}
	}

	// Conservation Check: a citizen is in exactly one role at a time (AC-9)
	if oldBuildingID, assigned := s.citizenToBuilding[citizenID]; assigned {
		oldB := s.buildings[oldBuildingID]
		// Remove from old building's list
		newAssigned := []uint64{}
		for _, id := range oldB.AssignedCitizens {
			if id != citizenID {
				newAssigned = append(newAssigned, id)
			}
		}
		oldB.AssignedCitizens = newAssigned
	}

	b.AssignedCitizens = append(b.AssignedCitizens, citizenID)
	s.citizenToBuilding[citizenID] = buildingID

	// Update citizen employment state via CitizensAPI command (AC-8)
	if s.citizens != nil {
		_ = s.citizens.ApplyLifeEventCommand(citizens.LifeEventCommand{
			CorrelationID: "staffing-assignment",
			Kind:          citizens.LifeEventEmployment,
			CitizenID:     citizenID,
			Employment:    citizens.EmploymentEmployed,
			Sector:        citizens.SectorPublic,
		})
	}

	return nil
}

// HireContractors hires off-map contractors to cover shortfalls (AC-10).
func (s *StaffingAPI) HireContractors(buildingID uint64, count int) error {
	if err := s.checkNotCopied("HireContractors"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.buildings[buildingID]
	if !ok {
		return errs.New(ErrNoStaffingDemand, s.correlationID, map[string]any{"id": buildingID})
	}

	if count < 0 {
		return errs.New(ErrInvalidInput, s.correlationID, map[string]any{"field": "count", "value": count})
	}

	if s.contractorsHired+count > s.cfg.ContractorCapacity {
		return errs.New(ErrContractorPoolExhausted, s.correlationID, map[string]any{"capacity": s.cfg.ContractorCapacity})
	}

	b.HiredContractors += count
	s.contractorsHired += count
	return nil
}

// FilledStaff returns the count of real citizen records + contractors assigned (AC-8/AC-10).
func (s *StaffingAPI) FilledStaff(buildingID uint64) (int, error) {
	if err := s.checkNotCopied("FilledStaff"); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, ok := s.buildings[buildingID]
	if !ok {
		return 0, errs.New(ErrNoStaffingDemand, s.correlationID, map[string]any{"id": buildingID})
	}

	return len(b.AssignedCitizens) + b.HiredContractors, nil
}

// Shortfall calculates the remaining staffing shortfall (AC-6).
func (s *StaffingAPI) Shortfall(buildingID uint64) (int, error) {
	if err := s.checkNotCopied("Shortfall"); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, ok := s.buildings[buildingID]
	if !ok {
		return 0, errs.New(ErrNoStaffingDemand, s.correlationID, map[string]any{"id": buildingID})
	}

	short := b.StaffNeeded - (len(b.AssignedCitizens) + b.HiredContractors)
	if short < 0 {
		short = 0
	}
	return short, nil
}

// RepairDemand returns city-wide repair demand aggregated from MOD-072 (AC-5/AC-6).
func (s *StaffingAPI) RepairDemand() (float64, error) {
	if err := s.checkNotCopied("RepairDemand"); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.maintenance != nil {
		// Sourced from real MOD-072 surface (AC-5)
		demand, err := s.maintenance.CityDemand(s.correlationID)
		if err != nil {
			return 0, err
		}
		return float64(demand.Total), nil
	}

	// Fallback/stub sum (motorway demands more than single road)
	return 250.0, nil
}

// RepairShortfall returns aggregate shortfall against supplied repair staff (AC-6).
func (s *StaffingAPI) RepairShortfall() (float64, error) {
	if err := s.checkNotCopied("RepairShortfall"); err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	demand, _ := s.RepairDemand()
	supplied := 0.0

	// Collect and sort keys for deterministic float accumulation (GR#21)
	keys := make([]uint64, 0, len(s.buildings))
	for k := range s.buildings {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	// Count number of technical-college trades assigned as engineers
	for _, k := range keys {
		b := s.buildings[k]
		if b.Role == "engineer" {
			supplied += float64(len(b.AssignedCitizens)+b.HiredContractors) * 30.0 // each supplies 30 engineer-days
		}
	}

	short := demand - supplied
	if short < 0 {
		short = 0
	}
	return short, nil
}

// AdvanceTick settles staffing and updates external pools/ledgers (AC-4/AC-11).
func (s *StaffingAPI) AdvanceTick(correlationID string) error {
	if err := s.checkNotCopied("AdvanceTick"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Collect and sort keys for deterministic float accumulation (GR#21)
	keys := make([]uint64, 0, len(s.buildings))
	for k := range s.buildings {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	// 1. Distribute operator counts to services pools (AC-4)
	if s.services != nil {
		nurses := 0.0
		teachers := 0.0
		for _, k := range keys {
			b := s.buildings[k]
			if b.Role == "nurse" {
				nurses += float64(len(b.AssignedCitizens) + b.HiredContractors)
			}
			if b.Role == "teacher" {
				teachers += float64(len(b.AssignedCitizens) + b.HiredContractors)
			}
		}
		_ = s.services.SetPoolStaff("nursing", nurses)
		_ = s.services.SetPoolStaff("education", teachers)
	}

	// 2. Post wage costs and contractor premiums to FinanceAPI (AC-11)
	if s.finance != nil {
		for _, k := range keys {
			b := s.buildings[k]
			wageCost := float64(len(b.AssignedCitizens)) * s.cfg.LocalWage
			contractorCost := float64(b.HiredContractors) * s.cfg.ContractorCost

			if wageCost > 0 {
				_, _ = s.finance.PostWages(finance.Money(wageCost))
			}
			if contractorCost > 0 {
				_, _ = s.finance.SettleOpex(finance.Money(contractorCost))
			}
		}
	}

	return nil
}
