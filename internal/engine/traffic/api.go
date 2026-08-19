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
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

const (
	ErrUnknownCitizen      = "MET-G4501"
	ErrInvalidInput        = "MET-G4502"
	ErrNonFiniteTravelTime = "MET-G4503"
	ErrCopiedValue         = "MET-G4599"
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

// validateConfig reports whether cfg's fields are all finite and within
// their documented sane ranges (GR#15 balance-data trust boundary). On
// rejection it names the first field/reason that failed so the caller's
// registry error can cite it. Split out from LoadConfig so both the
// disk-loading path and unit tests can exercise the same rule directly
// with hand-built structs (e.g. math.NaN() injection), without needing a
// JSON encoding of a value JSON itself cannot represent.
func validateConfig(cfg Config) (reason string, ok bool) {
	switch {
	case !num.IsFinite(cfg.BaseCommuteHours) || cfg.BaseCommuteHours <= 0:
		return "baseCommuteHours must be finite and positive", false
	case !num.IsFinite(cfg.BaseAccessMinutes) || cfg.BaseAccessMinutes <= 0:
		return "baseAccessMinutes must be finite and positive", false
	case !num.IsFinite(cfg.BaseCommuteMinutes) || cfg.BaseCommuteMinutes <= 0:
		return "baseCommuteMinutes must be finite and positive", false
	case !num.IsFinite(cfg.BaseActiveTravelShare) || cfg.BaseActiveTravelShare < 0:
		return "baseActiveTravelShare must be finite and non-negative", false
	case !num.IsFinite(cfg.BPRAlpha) || cfg.BPRAlpha <= 0:
		// BPR's volume-delay curve is T = T0 * (1 + alpha * (V/C)^beta); an
		// alpha <= 0 collapses the congestion term to a no-op (or, if
		// negative, an inverted one) rather than a merely-small effect, so
		// zero is rejected alongside negative/NaN as "nonsensical" per the
		// MOD-023 r2 destructive verdict.
		return "bprAlpha must be finite and positive", false
	case !num.IsFinite(cfg.BPRBeta) || cfg.BPRBeta <= 0:
		// Same reasoning as bprAlpha: beta <= 0 makes (V/C)^beta a constant
		// 1 regardless of volume, silently disabling the curve's shape.
		return "bprBeta must be finite and positive", false
	case !num.IsFinite(cfg.CapacityPerLanePerHour) || cfg.CapacityPerLanePerHour <= 0:
		return "capacityPerLanePerHour must be finite and positive", false
	}
	return "", true
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

// TrafficAPI represents the traffic and routing module (MOD-023), currently
// shipping the Baseline One coarse-approximation layer plus the Stage 1
// network primitives. See doc.go for exactly what does and does not ship.
type TrafficAPI struct {
	mu            sync.RWMutex
	self          atomic.Pointer[TrafficAPI]
	demands       map[uint64]int64
	nodes         map[uint64]*Node
	links         map[uint64]*Link
	roads         *roads.RoadsAPI
	cfg           Config
	correlationID string
}

// New constructs a new TrafficAPI.
func New() *TrafficAPI {
	t := &TrafficAPI{
		demands: make(map[uint64]int64),
		nodes:   make(map[uint64]*Node),
		links:   make(map[uint64]*Link),
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

	if reason, ok := validateConfig(cfg); !ok {
		return errs.New(ErrInvalidInput, t.correlationID, map[string]any{"path": path, "reason": reason})
	}

	t.cfg = cfg
	return nil
}

// demandMultiplier computes the coarse v/c-style multiplier fed into the
// commute/access queries below from the accumulated demand map, sorted into
// a deterministic key order before summation (AC-18).
func (t *TrafficAPI) demandMultiplier() float64 {
	if err := t.checkNotCopied("demandMultiplier"); err != nil {
		return 1.0
	}
	keys := make([]uint64, 0, len(t.demands))
	for k := range t.demands {
		keys = append(keys, k)
	}
	// Sort to ensure deterministic iteration (AC-18).
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	totalDemand := int64(0)
	for _, k := range keys {
		totalDemand += t.demands[k]
	}
	// Coarse approximation: the demand map feeds a v/c-style multiplier into
	// the commute queries. This is NOT a link-level v/c ratio -- see doc.go.
	mult := 1.0 + (float64(totalDemand) / 1000.0 * 0.1)
	return mult
}

func (t *TrafficAPI) addDemandLocked(id uint64, count int64) {
	if err := t.checkNotCopied("addDemandLocked"); err != nil {
		return
	}
	// Saturating add (GR#16): a citizen-count sum must never wrap negative.
	t.demands[id] = num.SatAdd(t.demands[id], count)
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
	if !num.IsFinite(length) || length < 0 {
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
	if !num.IsFinite(volume) || volume < 0 {
		return errs.New(ErrInvalidInput, t.correlationID, map[string]any{"volume": volume})
	}
	if l, ok := t.links[id]; ok {
		l.Volume += volume
	}
	return nil
}

// LinkTravelTime computes the v/c volume-delay travel time for a Stage 1
// network link using the BPR curve: T = T0 * (1 + alpha * (V/C)^beta).
//
// Every input that can drive the result non-finite is guarded before it
// reaches math.Pow, and the computed result is checked again afterwards
// (defence in depth against any guard this function doesn't yet know it
// needs): a non-positive/non-finite capacity, a negative/non-finite
// volume, or a non-finite travelTime (e.g. from an extreme V/C ratio
// overflowing the pow term) is rejected with ErrNonFiniteTravelTime rather
// than returned as +Inf/NaN with a nil error (MOD-023 r2 destructive
// verdict: this previously happened for capacity=0, huge volume, and
// negative volume with a non-integer beta).
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

	// Real link model over road data (AC-1).
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
	if !num.IsFinite(capacity) || capacity <= 0 {
		return 0, errs.New(ErrNonFiniteTravelTime, t.correlationID, map[string]any{"link": id, "capacity": capacity})
	}

	volume := l.Volume
	if !num.IsFinite(volume) || volume < 0 {
		return 0, errs.New(ErrNonFiniteTravelTime, t.correlationID, map[string]any{"link": id, "volume": volume})
	}

	freeFlowTime := l.Length / speedLimit
	vcRatio := volume / capacity

	travelTime := freeFlowTime * (1.0 + t.cfg.BPRAlpha*math.Pow(vcRatio, t.cfg.BPRBeta))
	if !num.IsFinite(travelTime) {
		return 0, errs.New(ErrNonFiniteTravelTime, t.correlationID, map[string]any{"link": id, "vcRatio": vcRatio, "travelTime": travelTime})
	}
	return travelTime, nil
}

// CommuteHours returns this citizen's weekly work-commute hours (AC-11,
// coarse fallback -- see doc.go).
func (t *TrafficAPI) CommuteHours(citizenID uint64, correlationID string) (float64, error) {
	if err := t.checkNotCopied("CommuteHours"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	if citizenID == 0 {
		return 0, errs.New(ErrUnknownCitizen, t.correlationID, map[string]any{"citizen": citizenID})
	}

	return t.cfg.BaseCommuteHours * t.demandMultiplier(), nil
}

// AccessMinutes returns the access time in minutes to the nearest leisure
// venue (AC-11, coarse fallback -- see doc.go).
func (t *TrafficAPI) AccessMinutes(citizenID uint64, category leisure.Category, correlationID string) (float64, error) {
	if err := t.checkNotCopied("AccessMinutes"); err != nil {
		return 0, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	if citizenID == 0 {
		return 0, errs.New(ErrUnknownCitizen, t.correlationID, map[string]any{"citizen": citizenID})
	}

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

// CommuteMinutes returns this citizen's daily work-commute minutes (AC-11,
// coarse fallback -- see doc.go).
func (t *TrafficAPI) CommuteMinutes(citizenID uint64, correlationID string) (float64, bool, error) {
	if err := t.checkNotCopied("CommuteMinutes"); err != nil {
		return 0, false, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	if citizenID == 0 {
		return 0, false, errs.New(ErrUnknownCitizen, t.correlationID, map[string]any{"citizen": citizenID})
	}

	return t.cfg.BaseCommuteMinutes * t.demandMultiplier(), true, nil
}

// AdvanceTick applies the module's day-boundary contract: it wipes the
// demand accumulated during the PRIOR day. Demand added via AddDemand /
// AddTripDemand / RegisterTrip AFTER this call returns belongs to the new
// day and survives, unaffected, until the NEXT call to AdvanceTick (see
// doc.go's "Day-boundary contract" section for the full statement and the
// composition-root calling obligation, FEAT-206).
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
