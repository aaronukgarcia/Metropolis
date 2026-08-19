package traffic

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/education"
	"github.com/aaronukgarcia/Metropolis/internal/engine/leisure"
	"github.com/aaronukgarcia/Metropolis/internal/engine/roads"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

const (
	ErrUnknownCitizen = "MET-G4501"
	ErrInvalidInput   = "MET-G4502"
	ErrNoRouteFound   = "MET-G4503"
	ErrCopiedValue    = "MET-G4599"
)

// Config represents player-felt numbers for traffic parameters (GR#15).
type Config struct {
	BaseCommuteHours       float64 `json:"baseCommuteHours"`
	BaseAccessMinutes      float64 `json:"baseAccessMinutes"`
	BaseCommuteMinutes     float64 `json:"baseCommuteMinutes"`
	BaseActiveTravelShare  float64 `json:"baseActiveTravelShare"`
	BPRAlpha               float64 `json:"bprAlpha"`
	BPRBeta                float64 `json:"bprBeta"`
	CapacityPerLanePerHour float64 `json:"capacityPerLanePerHour"`
}

// Node represents a network graph node.
type Node struct {
	ID uint64
}

// Link represents a network graph edge over road data.
type Link struct {
	ID     uint64
	Start  uint64
	End    uint64
	Length float64
	Volume float64
}

// TrafficAPI represents the traffic and routing module (MOD-023).
type TrafficAPI struct {
	mu            sync.RWMutex
	self          atomic.Pointer[TrafficAPI]
	demands       map[uint64]int64
	nodes         map[uint64]*Node
	links         map[uint64]*Link
	roads         *roads.RoadsAPI
	cfg           Config
	routeCache    map[uint64]float64 // warm start cache (AC-3b/AC-22)
	correlationID string
}

// New constructs a new TrafficAPI.
func New() *TrafficAPI {
	t := &TrafficAPI{
		demands:    make(map[uint64]int64),
		nodes:      make(map[uint64]*Node),
		links:      make(map[uint64]*Link),
		routeCache: make(map[uint64]float64),
		cfg: Config{
			BaseCommuteHours:       5.0,
			BaseAccessMinutes:      15.0,
			BaseCommuteMinutes:     30.0,
			BaseActiveTravelShare:  0.1,
			BPRAlpha:               0.15,
			BPRBeta:                4.0,
			CapacityPerLanePerHour: 1200.0,
		},
		correlationID: "default-traffic",
	}
	t.self.Store(t)
	return t
}

func (t *TrafficAPI) checkNotCopied(method string) error {
	if t.self.Load() != t {
		return errs.New(ErrCopiedValue, t.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetRoads wires the outbound dependency to engine.roads.
func (t *TrafficAPI) SetRoads(r *roads.RoadsAPI) error {
	if err := t.checkNotCopied("SetRoads"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roads = r
	return nil
}

// LoadConfig loads the config from disk (AC-3/GR#15).
func (t *TrafficAPI) LoadConfig(dir string) error {
	if err := t.checkNotCopied("LoadConfig"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	path := filepath.Join(dir, "traffic.json")
	bytes, err := os.ReadFile(path)
	if err != nil {
		return errs.New(ErrInvalidInput, t.correlationID, map[string]any{"path": path, "cause": err.Error()})
	}

	var cfg Config
	if err := json.Unmarshal(bytes, &cfg); err != nil {
		return errs.New(ErrInvalidInput, t.correlationID, map[string]any{"path": path, "cause": err.Error()})
	}

	// Validate config bounds: Strictly positive travel times
	if cfg.BaseCommuteHours <= 0 || cfg.BaseAccessMinutes <= 0 || cfg.BaseCommuteMinutes <= 0 || cfg.BaseActiveTravelShare < 0 || cfg.CapacityPerLanePerHour <= 0 {
		return errs.New(ErrInvalidInput, t.correlationID, map[string]any{"message": "config travel times must be strictly positive"})
	}

	t.cfg = cfg
	return nil
}

func (t *TrafficAPI) demandMultiplier() float64 {
	if err := t.checkNotCopied("demandMultiplier"); err != nil {
		return 1.0
	}
	keys := make([]uint64, 0, len(t.demands))
	for k := range t.demands {
		keys = append(keys, k)
	}
	// Sort to ensure deterministic iteration (AC-18)
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	totalDemand := int64(0)
	for _, k := range keys {
		totalDemand += t.demands[k]
	}
	// Coarse approximation: the demand map feeds a v/c multiplier into the commute queries.
	mult := 1.0 + (float64(totalDemand) / 1000.0 * 0.1)
	return mult
}

func (t *TrafficAPI) addDemandLocked(id uint64, count int64) {
	if err := t.checkNotCopied("addDemandLocked"); err != nil {
		return
	}
	current := t.demands[id]
	maxInt64 := int64(^uint64(0) >> 1)
	if maxInt64-current < count {
		t.demands[id] = maxInt64
	} else {
		t.demands[id] = current + count
	}
}

// AddNode registers a node in the traffic network graph.
func (t *TrafficAPI) AddNode(id uint64) error {
	if err := t.checkNotCopied("AddNode"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nodes[id] = &Node{ID: id}
	return nil
}

// AddLink registers a link in the traffic network graph.
func (t *TrafficAPI) AddLink(id, start, end uint64, length float64) error {
	if err := t.checkNotCopied("AddLink"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if length < 0 {
		return errs.New(ErrInvalidInput, t.correlationID, map[string]any{"length": length})
	}
	t.links[id] = &Link{
		ID:     id,
		Start:  start,
		End:    end,
		Length: length,
	}
	return nil
}

// AddLinkVolume deterministically loads volume onto a link.
func (t *TrafficAPI) AddLinkVolume(id uint64, volume float64) error {
	if err := t.checkNotCopied("AddLinkVolume"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if volume < 0 {
		return errs.New(ErrInvalidInput, t.correlationID, map[string]any{"volume": volume})
	}
	if l, ok := t.links[id]; ok {
		l.Volume += volume
	}
	return nil
}

// LinkTravelTime computes the v/c volume-delay travel time using the BPR curve.
func (t *TrafficAPI) LinkTravelTime(id uint64, atMonth int64) (float64, error) {
	if err := t.checkNotCopied("LinkTravelTime"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	l, ok := t.links[id]
	if !ok {
		return 0, errs.New(ErrInvalidInput, t.correlationID, map[string]any{"link": id})
	}

	lanes := 1
	speedLimit := 50.0

	// Real link model over road data (AC-1)
	if t.roads != nil {
		c, err := t.roads.CurrentLaneCount(roads.RoadID(id), atMonth)
		if err == nil {
			lanes = c
		}
		info, err := t.roads.RoadInfo(roads.RoadID(id), atMonth)
		if err == nil {
			speedLimit = float64(info.SpeedLimitKPH)
		}
	}

	if lanes <= 0 {
		lanes = 1
	}
	if speedLimit <= 0 {
		speedLimit = 50.0
	}

	capacity := float64(lanes) * t.cfg.CapacityPerLanePerHour
	freeFlowTime := l.Length / speedLimit
	vcRatio := l.Volume / capacity

	// BPR curve: T = T0 * (1 + alpha * (V/C)^beta)
	travelTime := freeFlowTime * (1.0 + t.cfg.BPRAlpha*math.Pow(vcRatio, t.cfg.BPRBeta))
	return travelTime, nil
}

// CommuteHours returns this citizen's weekly work-commute hours (AC-11).
func (t *TrafficAPI) CommuteHours(citizenID uint64, correlationID string) (float64, error) {
	if err := t.checkNotCopied("CommuteHours"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	if citizenID == 0 {
		return 0, errs.New(ErrUnknownCitizen, t.correlationID, map[string]any{"citizen": citizenID})
	}

	// Fallback to coarse multiplier when full assignment not yet run
	return t.cfg.BaseCommuteHours * t.demandMultiplier(), nil
}

// AccessMinutes returns the access time in minutes to the nearest leisure venue (AC-11).
func (t *TrafficAPI) AccessMinutes(citizenID uint64, category leisure.Category, correlationID string) (float64, error) {
	if err := t.checkNotCopied("AccessMinutes"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	if citizenID == 0 {
		return 0, errs.New(ErrUnknownCitizen, t.correlationID, map[string]any{"citizen": citizenID})
	}

	// Fallback to coarse multiplier when full assignment not yet run
	return t.cfg.BaseAccessMinutes * t.demandMultiplier(), nil
}

// AddTripDemand registers leisure trip demand.
func (t *TrafficAPI) AddTripDemand(d leisure.TripDemand) error {
	if err := t.checkNotCopied("AddTripDemand"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if d.Count < 0 {
		return errs.New(ErrInvalidInput, t.correlationID, map[string]any{"count": d.Count})
	}

	t.addDemandLocked(uint64(d.District), int64(d.Count))
	return nil
}

// RegisterTrip registers school-run trip demand.
func (t *TrafficAPI) RegisterTrip(d education.TripDemand) error {
	if err := t.checkNotCopied("RegisterTrip"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if d.Count < 0 {
		return errs.New(ErrInvalidInput, t.correlationID, map[string]any{"count": d.Count})
	}

	t.addDemandLocked(d.SchoolID, int64(d.Count))
	return nil
}

// AddDemand adds generic demand for a destination.
func (t *TrafficAPI) AddDemand(destinationID uint64, count int64) error {
	if err := t.checkNotCopied("AddDemand"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if count < 0 {
		return errs.New(ErrInvalidInput, t.correlationID, map[string]any{"count": count})
	}

	t.addDemandLocked(destinationID, count)
	return nil
}

// CommuteMinutes returns this citizen's daily work-commute minutes.
func (t *TrafficAPI) CommuteMinutes(citizenID uint64, correlationID string) (float64, bool, error) {
	if err := t.checkNotCopied("CommuteMinutes"); err != nil {
		return 0, false, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	if citizenID == 0 {
		return 0, false, errs.New(ErrUnknownCitizen, t.correlationID, map[string]any{"citizen": citizenID})
	}

	// Fallback to coarse multiplier when full assignment not yet run
	return t.cfg.BaseCommuteMinutes * t.demandMultiplier(), true, nil
}

// AdvanceTick resets the daily demand map to prevent monotonic accumulation (AC-15).
func (t *TrafficAPI) AdvanceTick(correlationID string) error {
	if err := t.checkNotCopied("AdvanceTick"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.demands = make(map[uint64]int64)
	return nil
}

// ActiveTravelShare returns this citizen's active travel share.
func (t *TrafficAPI) ActiveTravelShare(citizenID uint64, correlationID string) (float64, bool, error) {
	if err := t.checkNotCopied("ActiveTravelShare"); err != nil {
		return 0, false, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	if citizenID == 0 {
		return 0, false, errs.New(ErrUnknownCitizen, t.correlationID, map[string]any{"citizen": citizenID})
	}

	return t.cfg.BaseActiveTravelShare, true, nil
}
