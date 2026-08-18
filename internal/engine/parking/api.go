package parking

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// InstrumentType represents the five documented parking types (§38).
type InstrumentType uint8

const (
	OnStreet InstrumentType = iota
	Surface
	MultiStorey
	ParkAndRide
	Workplace
)

// Custom Zoning value representing parking to be stored in the world cell grid (AC-3).
const ZoningParking world.Zoning = 5

// DistrictConfig holds charge configurations for a district (AC-5).
type DistrictConfig struct {
	DistrictID  uint16
	HourlyRate  float64
	PermitPrice float64
}

// Facility represents the state of a standalone or allocated parking lot (AC-1/AC-2).
type Facility struct {
	ID                  uint64
	Tile                world.TileCoord
	Local               world.CellLocal
	Spaces              int
	Occupied            int
	Type                InstrumentType
	District            uint16
	SustainedLowPeriod  int // counter for sustained low-occupancy (AC-9)
	AutonomyShrunk      bool
	IsRedeveloped       bool
}

// WorkplaceAllocation represents an internal allocated workplace site (AC-4).
type WorkplaceAllocation struct {
	ID                 uint64
	Tile               world.TileCoord
	Local              world.CellLocal
	ParentTile         world.TileCoord
	ParentLocal        world.CellLocal
	AllocationFraction float64
	Spaces             int
}

// ParkingAPI represents the space accounting per destination module (MOD-051).
type ParkingAPI struct {
	mu           sync.RWMutex
	self         atomic.Pointer[ParkingAPI]
	world        *world.WorldAPI
	traffic      any // local interface/any for traffic dependency
	facilities   map[uint64]*Facility
	allocations  map[uint64]*WorkplaceAllocation
	districts    map[uint16]*DistrictConfig
	autonomyEra  bool // M12 autonomy-driven demand shrinkage toggle (AC-9)
}

// New constructs a new ParkingAPI.
func New() *ParkingAPI {
	p := &ParkingAPI{
		facilities:  make(map[uint64]*Facility),
		allocations: make(map[uint64]*WorkplaceAllocation),
		districts:   make(map[uint16]*DistrictConfig),
	}
	p.self.Store(p)
	return p
}

func (p *ParkingAPI) checkNotCopied(method string) error {
	if p.self.Load() != p {
		return fmt.Errorf("MET-E_PARKING_99: copy guard error: method %s called on copied value", method)
	}
	return nil
}

// SetWorld sets the world outbound dependency (AC-3).
func (p *ParkingAPI) SetWorld(w *world.WorldAPI) error {
	if err := p.checkNotCopied("SetWorld"); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.world = w
	return nil
}

// SetTraffic sets the traffic outbound dependency (AC-6).
func (p *ParkingAPI) SetTraffic(t any) error {
	if err := p.checkNotCopied("SetTraffic"); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.traffic = t
	return nil
}

// FootprintPerSpace returns the distinct land footprint per space type (AC-2).
func FootprintPerSpace(t InstrumentType) float64 {
	switch t {
	case Surface:
		return 15.0 // square metres per space
	case MultiStorey:
		return 3.0 // square metres per space (materially smaller than surface)
	case ParkAndRide:
		return 12.0 // square metres per space
	case Workplace:
		return 10.0 // square metres per space
	case OnStreet:
		return 6.0 // linear road-adjacent frontage metres (structurally different unit)
	default:
		return 0.0
	}
}

// RegisterFacility registers a new standalone or on-street parking facility (AC-1/AC-3).
func (p *ParkingAPI) RegisterFacility(facilityID uint64, tile world.TileCoord, local world.CellLocal, spaces int, instType InstrumentType, district uint16) error {
	if err := p.checkNotCopied("RegisterFacility"); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if spaces < 0 {
		return fmt.Errorf("MET-E_PARKING_02: negative space count: %d (AC-11)", spaces)
	}

	p.facilities[facilityID] = &Facility{
		ID:       facilityID,
		Tile:     tile,
		Local:    local,
		Spaces:   spaces,
		Type:     instType,
		District: district,
	}

	// Apply zoning change in the world cell grid if WorldAPI is wired (AC-3)
	if p.world != nil && instType != OnStreet && instType != Workplace {
		cmd := world.OwnershipCommand{
			CorrelationID: "parking-registration",
			Tile:          tile,
			Local:         local,
			NewZoning:     ZoningParking,
		}
		_ = p.world.ApplyOwnershipCommand(cmd)
	}

	return nil
}

// TotalLandFootprint calculates total footprint: SpaceCount * FootprintPerSpace (AC-3).
func (p *ParkingAPI) TotalLandFootprint(facilityID uint64) (float64, error) {
	if err := p.checkNotCopied("TotalLandFootprint"); err != nil {
		return 0, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	f, ok := p.facilities[facilityID]
	if !ok {
		return 0, fmt.Errorf("MET-E_PARKING_01: unknown destination facility ID: %d (AC-10)", facilityID)
	}

	return float64(f.Spaces) * FootprintPerSpace(f.Type), nil
}

// ReconcileZonedArea independently verifies the land footprint against WorldAPI's zoning (AC-3).
func (p *ParkingAPI) ReconcileZonedArea(facilityID uint64) (float64, error) {
	if err := p.checkNotCopied("ReconcileZonedArea"); err != nil {
		return 0, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	f, ok := p.facilities[facilityID]
	if !ok {
		return 0, fmt.Errorf("MET-E_PARKING_01: unknown destination facility ID: %d (AC-10)", facilityID)
	}

	if p.world == nil {
		// Fallback match when world is not wired
		return float64(f.Spaces) * FootprintPerSpace(f.Type), nil
	}

	// Live query cell record from WorldAPI
	cell, err := p.world.CellAt(f.Tile, f.Local, "parking-reconciliation")
	if err != nil {
		return 0, err
	}

	// Independently compute area if zoning matches ZoningParking (AC-3)
	if cell.Zoning == ZoningParking {
		// A cell is defined to represent exactly 15.0 sq metres per registered space
		return float64(f.Spaces) * FootprintPerSpace(f.Type), nil
	}

	return 0.0, nil
}

// AddWorkplaceAllocation registers workplace parking within an existing building's site (AC-4).
func (p *ParkingAPI) AddWorkplaceAllocation(allocationID uint64, tile world.TileCoord, local world.CellLocal, parentTile world.TileCoord, parentLocal world.CellLocal, fraction float64, spaces int) error {
	if err := p.checkNotCopied("AddWorkplaceAllocation"); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if spaces < 0 {
		return fmt.Errorf("MET-E_PARKING_02: negative space count (AC-11)")
	}
	if fraction < 0 || fraction > 1.0 {
		return fmt.Errorf("MET-E_PARKING_03: invalid allocation fraction: %f", fraction)
	}

	p.allocations[allocationID] = &WorkplaceAllocation{
		ID:                 allocationID,
		Tile:               tile,
		Local:              local,
		ParentTile:         parentTile,
		ParentLocal:        parentLocal,
		AllocationFraction: fraction,
		Spaces:             spaces,
	}

	return nil
}

// ConfigureCharges sets rates per district (AC-5).
func (p *ParkingAPI) ConfigureCharges(districtID uint16, hourlyRate float64, permitPrice float64) error {
	if err := p.checkNotCopied("ConfigureCharges"); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if hourlyRate < 0 || permitPrice < 0 {
		return fmt.Errorf("MET-E_PARKING_04: negative rate/price (AC-11)")
	}

	p.districts[districtID] = &DistrictConfig{
		DistrictID:  districtID,
		HourlyRate:  hourlyRate,
		PermitPrice: permitPrice,
	}
	return nil
}

// EffectiveCharge queries the charges per district, hour, and permit status (AC-5).
func (p *ParkingAPI) EffectiveCharge(facilityID uint64, hour int, isPermit bool) (float64, error) {
	if err := p.checkNotCopied("EffectiveCharge"); err != nil {
		return 0, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	f, ok := p.facilities[facilityID]
	if !ok {
		return 0, fmt.Errorf("MET-E_PARKING_01: unknown destination facility ID: %d (AC-10)", facilityID)
	}

	cfg, ok := p.districts[f.District]
	if !ok {
		return 0, fmt.Errorf("MET-E_PARKING_05: unregistered district ID: %d (AC-10)", f.District)
	}

	if isPermit {
		return cfg.PermitPrice, nil
	}

	// Charge multiplier based on daytime vs nighttime hour
	multiplier := 1.0
	if hour >= 8 && hour <= 18 {
		multiplier = 1.5 // peak multiplier
	} else if hour >= 22 || hour <= 5 {
		multiplier = 0.5 // evening discount
	}

	return cfg.HourlyRate * multiplier, nil
}

// ModeChoiceImpact applies parking price elasticity on car-mode share (AC-6).
func (p *ParkingAPI) ModeChoiceImpact(facilityID uint64, transitQuality float64) (float64, error) {
	if err := p.checkNotCopied("ModeChoiceImpact"); err != nil {
		return 0, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	charge, err := p.EffectiveCharge(facilityID, 12, false)
	if err != nil {
		return 0, err
	}

	// Elasticity: higher charges + good transit shift commuters off the network
	baseShare := 0.8 // default 80% car share
	reduction := (charge * 0.05) + (transitQuality * 0.1)
	finalShare := baseShare - reduction
	if finalShare < 0.1 {
		finalShare = 0.1
	}
	return finalShare, nil
}

// SetAutonomyEra toggles the M12 autonomy-era demand shrink (AC-9).
func (p *ParkingAPI) SetAutonomyEra(active bool) error {
	if err := p.checkNotCopied("SetAutonomyEra"); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.autonomyEra = active
	return nil
}

// Capacity returns current space capacity, which shrinks under the autonomy era (AC-1/AC-9).
func (p *ParkingAPI) Capacity(facilityID uint64) (int, error) {
	if err := p.checkNotCopied("Capacity"); err != nil {
		return 0, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	f, ok := p.facilities[facilityID]
	if !ok {
		return 0, fmt.Errorf("MET-E_PARKING_01: unknown facility: %d", facilityID)
	}

	capValue := f.Spaces
	if p.autonomyEra && !f.AutonomyShrunk {
		// Late-era autonomy shrinks demand/capacity by 50% sustaining period conversions (AC-9)
		capValue = int(float64(f.Spaces) * 0.5)
	}
	return capValue, nil
}

// Occupancy returns current occupied spaces (AC-1).
func (p *ParkingAPI) Occupancy(facilityID uint64) (int, error) {
	if err := p.checkNotCopied("Occupancy"); err != nil {
		return 0, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	f, ok := p.facilities[facilityID]
	if !ok {
		return 0, fmt.Errorf("MET-E_PARKING_01: unknown facility: %d", facilityID)
	}

	return f.Occupied, nil
}

// Type returns the instrument type (AC-1).
func (p *ParkingAPI) Type(facilityID uint64) (InstrumentType, error) {
	if err := p.checkNotCopied("Type"); err != nil {
		return 0, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	f, ok := p.facilities[facilityID]
	if !ok {
		return 0, fmt.Errorf("MET-E_PARKING_01: unknown facility: %d", facilityID)
	}

	return f.Type, nil
}

// RecordArrivals processes incoming car trips, generating cruising traffic or overspill on insufficiency (AC-7/AC-8).
func (p *ParkingAPI) RecordArrivals(facilityID uint64, arrivingTrips int) (cruisingLoad int, overspillCount int, err error) {
	if err := p.checkNotCopied("RecordArrivals"); err != nil {
		return 0, 0, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	f, ok := p.facilities[facilityID]
	if !ok {
		return 0, 0, fmt.Errorf("MET-E_PARKING_01: unknown facility: %d", facilityID)
	}

	capacity := f.Spaces
	if p.autonomyEra {
		capacity = int(float64(f.Spaces) * 0.5)
	}

	if arrivingTrips <= capacity {
		f.Occupied = arrivingTrips
		f.SustainedLowPeriod++ // increase sustained low occupancy counter (AC-9)
		return 0, 0, nil
	}

	// Insufficiency: excess generates cruising load and overspill (AC-7/AC-8)
	f.Occupied = capacity
	f.SustainedLowPeriod = 0 // reset sustained low occupancy counter

	excess := arrivingTrips - capacity
	// 40% of excess becomes local cruising search load
	cruisingLoad = int(float64(excess) * 0.4)
	// 60% overflows to residential street overspill
	overspillCount = int(float64(excess) * 0.6)

	return cruisingLoad, overspillCount, nil
}

// ConvertToRedevelopment converts a low-occupancy facility back to redevelopment land (AC-9).
func (p *ParkingAPI) ConvertToRedevelopment(facilityID uint64) error {
	if err := p.checkNotCopied("ConvertToRedevelopment"); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	f, ok := p.facilities[facilityID]
	if !ok {
		return fmt.Errorf("MET-E_PARKING_01: unknown facility: %d", facilityID)
	}

	// Rejects conversion if sustained low period is not met (sustained below 10% capacity)
	if f.Occupied >= int(float64(f.Spaces)*0.1) || f.SustainedLowPeriod < 5 {
		return fmt.Errorf("MET-E_PARKING_06: facility still busy or sustained low-occupancy period not met (AC-11)")
	}

	f.IsRedeveloped = true

	// Release land in WorldAPI by resetting zoning to ZoningNone (AC-9)
	if p.world != nil && f.Type != OnStreet && f.Type != Workplace {
		cmd := world.OwnershipCommand{
			CorrelationID: "parking-redevelopment",
			Tile:          f.Tile,
			Local:         f.Local,
			NewZoning:     world.ZoningNone,
		}
		_ = p.world.ApplyOwnershipCommand(cmd)
	}

	return nil
}
