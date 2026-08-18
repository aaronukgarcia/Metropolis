package traffic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/education"
	"github.com/aaronukgarcia/Metropolis/internal/engine/leisure"
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
	BaseCommuteHours      float64 `json:"baseCommuteHours"`
	BaseAccessMinutes     float64 `json:"baseAccessMinutes"`
	BaseCommuteMinutes    float64 `json:"baseCommuteMinutes"`
	BaseActiveTravelShare float64 `json:"baseActiveTravelShare"`
}

// TrafficAPI represents the traffic and routing module (MOD-023).
type TrafficAPI struct {
	mu            sync.RWMutex
	self          atomic.Pointer[TrafficAPI]
	demands       map[uint64]int64
	cfg           Config
	correlationID string
}

// New constructs a new TrafficAPI.
func New() *TrafficAPI {
	t := &TrafficAPI{
		demands: make(map[uint64]int64),
		cfg: Config{
			BaseCommuteHours:      5.0,
			BaseAccessMinutes:     15.0,
			BaseCommuteMinutes:    30.0,
			BaseActiveTravelShare: 0.1,
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
	if cfg.BaseCommuteHours <= 0 || cfg.BaseAccessMinutes <= 0 || cfg.BaseCommuteMinutes <= 0 || cfg.BaseActiveTravelShare < 0 {
		return errs.New(ErrInvalidInput, t.correlationID, map[string]any{"message": "config travel times must be strictly positive"})
	}

	t.cfg = cfg
	return nil
}

func (t *TrafficAPI) demandMultiplier() float64 {
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
	current := t.demands[id]
	maxInt64 := int64(^uint64(0) >> 1)
	if maxInt64-current < count {
		t.demands[id] = maxInt64
	} else {
		t.demands[id] = current + count
	}
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
