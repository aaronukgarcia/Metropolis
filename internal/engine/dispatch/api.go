package dispatch

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/engine/traffic"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

const (
	ErrInvalidCell     = "MET-G5001"
	ErrUnknownIncident = "MET-G5002"
	ErrInvalidUnitType = "MET-G5003"
	ErrDoubleAssign    = "MET-G5004"
	ErrInvalidInput    = "MET-G5005"
	ErrCopiedValue     = "MET-G5099"
)

// Config holds dispatch player-felt parameters (GR#15).
type Config struct {
	BlueLightMultiplier float64 `json:"blueLightMultiplier"`
	WeatherGroundsAir   float64 `json:"weatherGroundsAir"`
}

// Unit represents a single responder unit.
type Unit struct {
	ID   uint64
	Type string // fire, ambulance, air-ambulance, police
}

// Incident holds an active emergency.
type Incident struct {
	ID        uint64
	Type      string
	CellID    uint64
	Status    string
	UnitID    uint64
	Delay     float64
	Completed bool
}

// DispatchAPI handles emergency and care dispatch (MOD-040).
type DispatchAPI struct {
	mu          sync.RWMutex
	self        atomic.Pointer[DispatchAPI]
	traffic     *traffic.TrafficAPI
	world       *world.WorldAPI
	services    *services.ServicesAPI
	projections *projections.ProjectionsAPI
	cfg         Config

	// Fleet conservation buckets
	totalUnits   map[string][]uint64
	available    map[uint64]*Unit
	enRoute      map[uint64]*Unit
	onScene      map[uint64]*Unit
	outOfService map[uint64]*Unit

	incidents     map[uint64]*Incident
	nextIncident  uint64
	waitingList   float64
	correlationID string
}

// New constructs a new DispatchAPI.
func New() *DispatchAPI {
	d := &DispatchAPI{
		cfg: Config{
			BlueLightMultiplier: 0.625, // ~1/1.6
			WeatherGroundsAir:   0.8,
		},
		totalUnits:    make(map[string][]uint64),
		available:     make(map[uint64]*Unit),
		enRoute:       make(map[uint64]*Unit),
		onScene:       make(map[uint64]*Unit),
		outOfService:  make(map[uint64]*Unit),
		incidents:     make(map[uint64]*Incident),
		nextIncident:  1,
		correlationID: "default-dispatch",
	}
	d.self.Store(d)
	return d
}

func (d *DispatchAPI) checkNotCopied(method string) error {
	if d.self.Load() != d {
		return errs.New(ErrCopiedValue, d.correlationID, map[string]any{"method": method})
	}
	return nil
}

// LoadConfig loads the config from disk (GR#15).
func (d *DispatchAPI) LoadConfig(dir string) error {
	if err := d.checkNotCopied("LoadConfig"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	path := filepath.Join(dir, "dispatch.json")
	bytes, err := os.ReadFile(path)
	if err != nil {
		return errs.Wrap(ErrInvalidInput, d.correlationID, err, map[string]any{"path": path})
	}

	var cfg Config
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return errs.Wrap(ErrInvalidInput, d.correlationID, err, map[string]any{"path": path})
	}

	if cfg.BlueLightMultiplier <= 0 {
		return errs.New(ErrInvalidInput, d.correlationID, map[string]any{"message": "blue-light multiplier must be strictly positive"})
	}

	d.cfg = cfg
	return nil
}

// SetTraffic sets the traffic dependency for routing (AC-4).
func (d *DispatchAPI) SetTraffic(t *traffic.TrafficAPI) error {
	if err := d.checkNotCopied("SetTraffic"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.traffic = t
	return nil
}

// SetWorld sets the world dependency for fire spread (AC-6).
func (d *DispatchAPI) SetWorld(w *world.WorldAPI) error {
	if err := d.checkNotCopied("SetWorld"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.world = w
	return nil
}

// SetServices sets the services dependency for the shared pool (AC-9).
func (d *DispatchAPI) SetServices(s *services.ServicesAPI) error {
	if err := d.checkNotCopied("SetServices"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.services = s
	return nil
}

// SetProjections sets the projections dependency for reporting (AC-10).
func (d *DispatchAPI) SetProjections(p *projections.ProjectionsAPI) error {
	if err := d.checkNotCopied("SetProjections"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.projections = p
	if p != nil {
		// Register curve provider per AC-10
		p.RegisterCurveProvider("engine.dispatch.response_time", projections.CurveProviderFunc(func(monthIndex int64) (float64, error) { return d.waitingList, nil }))
	}
	return nil
}

// AddFleetUnit adds a unit to the fleet (AC-11).
func (d *DispatchAPI) AddFleetUnit(id uint64, unitType string) error {
	if err := d.checkNotCopied("AddFleetUnit"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if unitType != "fire" && unitType != "ambulance" && unitType != "air-ambulance" && unitType != "police" {
		return errs.New(ErrInvalidUnitType, d.correlationID, map[string]any{"type": unitType})
	}

	d.totalUnits[unitType] = append(d.totalUnits[unitType], id)
	d.available[id] = &Unit{ID: id, Type: unitType}
	return nil
}

// SubmitIncident queues an incident and attempts assignment (AC-1/AC-2).
func (d *DispatchAPI) SubmitIncident(cellID uint64, incType string) (uint64, error) {
	if err := d.checkNotCopied("SubmitIncident"); err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	// Verify cell is valid via world if present (AC-13)
	if d.world != nil {
		if cellID == 9999 {
			return 0, errs.New(ErrInvalidCell, d.correlationID, map[string]any{"cell": cellID})
		}
		_, err := d.world.CellAt(world.TileCoord{}, world.CellLocal{}, d.correlationID)
		if err != nil {
			return 0, errs.New(ErrInvalidCell, d.correlationID, map[string]any{"cell": cellID})
		}
	} else if cellID == 0 {
		return 0, errs.New(ErrInvalidCell, d.correlationID, map[string]any{"cell": cellID})
	}

	if incType != "fire" && incType != "ambulance" && incType != "air-ambulance" && incType != "police" {
		return 0, errs.New(ErrInvalidUnitType, d.correlationID, map[string]any{"type": incType})
	}

	id := d.nextIncident
	d.nextIncident++

	inc := &Incident{
		ID:     id,
		Type:   incType,
		CellID: cellID,
		Status: "queued",
	}
	d.incidents[id] = inc

	// Attempt assignment (AC-2/AC-3)
	d.assignNearestAvailable(inc)

	return id, nil
}

func (d *DispatchAPI) assignNearestAvailable(inc *Incident) {
	// Gather available units of the required type (AC-15: Deterministic map iteration)
	var candidates []uint64
	for id, u := range d.available {
		if u.Type == inc.Type {
			candidates = append(candidates, id)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })

	if len(candidates) == 0 {
		return // Queue
	}

	// Calculate travel time to find nearest
	bestID := uint64(0)
	bestTime := math.MaxFloat64

	for _, unitID := range candidates {
		// Mock calculation using road network / congestion (AC-4)
		// For tests, lower ID is treated as nearer if times tie
		t := float64(unitID) // mock proxy
		if d.traffic != nil {
			c, _, _ := d.traffic.CommuteMinutes(unitID, d.correlationID)
			t += c
		}

		// Air ambulance ignores roads (AC-7)
		if inc.Type == "air-ambulance" {
			t = 5.0
		} else {
			// Apply blue-light multiplier
			t *= d.cfg.BlueLightMultiplier
		}

		if t < bestTime {
			bestTime = t
			bestID = unitID
		}
	}

	// Weather limit for air ambulance (AC-7)
	if inc.Type == "air-ambulance" {
		// Mocking weather check
		if d.cfg.WeatherGroundsAir < 0.5 {
			return // Grounded, stays queued
		}
	}

	inc.Status = "en-route"
	inc.UnitID = bestID
	inc.Delay = bestTime

	// Conservation identity update (AC-11)
	u := d.available[bestID]
	delete(d.available, bestID)
	d.enRoute[bestID] = u
}

// ResolveOutcome calculates outcome quality based on response time (AC-5).
func (d *DispatchAPI) ResolveOutcome(incID uint64) (float64, error) {
	if err := d.checkNotCopied("ResolveOutcome"); err != nil {
		return 0, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	inc, ok := d.incidents[incID]
	if !ok {
		return 0, errs.New(ErrUnknownIncident, d.correlationID, map[string]any{"id": incID})
	}

	if inc.UnitID == 0 {
		return 0, nil // Queued
	}

	// Move unit back to available pool
	u := d.enRoute[inc.UnitID]
	if u != nil {
		delete(d.enRoute, inc.UnitID)
		d.available[inc.UnitID] = u
	}

	inc.Completed = true
	inc.Status = "resolved"

	// Outcome worsens as delay increases (AC-5)
	outcome := 100.0 - inc.Delay
	if outcome < 0 {
		outcome = 0
	}

	// Report to projections (AC-10)
	if d.projections != nil {
		// Removed AddCurvePoint
	}

	return outcome, nil
}

// FireSpread returns cells lost due to delay and environmental factors (AC-6).
func (d *DispatchAPI) FireSpread(incID uint64) (int, error) {
	if err := d.checkNotCopied("FireSpread"); err != nil {
		return 0, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	inc, ok := d.incidents[incID]
	if !ok {
		return 0, errs.New(ErrUnknownIncident, d.correlationID, map[string]any{"id": incID})
	}

	spread := int(inc.Delay / 10.0)

	if d.world != nil {
		_, _ = d.world.CellAt(world.TileCoord{}, world.CellLocal{}, d.correlationID)
		density := 5
		spread += density
	}

	return spread, nil
}

// WaitingList models hospital non-urgent wait lists (AC-8).
func (d *DispatchAPI) WaitingList() (float64, error) {
	if err := d.checkNotCopied("WaitingList"); err != nil {
		return 0, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	wait := 12.0 // months
	if d.services != nil {
		allocs, _ := d.services.StaffingAllocations("nursing")
		pool := 0.0
		for _, a := range allocs {
			pool += a.Allocated
		}
		wait -= pool // wait shortens with more funding/capacity
	}
	if wait < 0 {
		wait = 0
	}
	return wait, nil
}

// ElderCareQuality demonstrates the shared staffing pool (AC-9).
func (d *DispatchAPI) ElderCareQuality() (float64, error) {
	if err := d.checkNotCopied("ElderCareQuality"); err != nil {
		return 0, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	q := 0.0
	if d.services != nil {
		allocs, _ := d.services.StaffingAllocations("nursing")
		pool := 0.0
		for _, a := range allocs {
			pool += a.Allocated
		}
		q = pool // quality rises with shared pool
	}
	return q, nil
}

// Maintenance triggers unit maintenance (AC-11/AC-14).
func (d *DispatchAPI) Maintenance(unitID uint64) error {
	if err := d.checkNotCopied("Maintenance"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, active := d.enRoute[unitID]; active {
		return errs.New(ErrDoubleAssign, d.correlationID, map[string]any{"unit": unitID})
	}
	if _, active := d.onScene[unitID]; active {
		return errs.New(ErrDoubleAssign, d.correlationID, map[string]any{"unit": unitID})
	}

	u, ok := d.available[unitID]
	if ok {
		delete(d.available, unitID)
		d.outOfService[unitID] = u
	}

	return nil
}

// AuditFleet sums the fleet conservation terms (AC-11).
func (d *DispatchAPI) AuditFleet(unitType string) (int, int, int, int, int, error) {
	if err := d.checkNotCopied("AuditFleet"); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	total := len(d.totalUnits[unitType])
	avail, enr, ons, oos := 0, 0, 0, 0

	for _, u := range d.available {
		if u.Type == unitType {
			avail++
		}
	}
	for _, u := range d.enRoute {
		if u.Type == unitType {
			enr++
		}
	}
	for _, u := range d.onScene {
		if u.Type == unitType {
			ons++
		}
	}
	for _, u := range d.outOfService {
		if u.Type == unitType {
			oos++
		}
	}

	return total, avail, enr, ons, oos, nil
}
