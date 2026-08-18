package dispatch

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// Incident represents an emergency or care event in the dispatch system (AC-2).
type Incident struct {
	ID       uint64
	Category string // Fire, Medical, Crime, Accident
	Severity int    // 1 (low) to 3 (highest)
	Status   string // Queued, Dispatched, Resolved
}

// DispatchAPI represents the unified emergency & care dispatch module (MOD-040).
type DispatchAPI struct {
	mu            sync.RWMutex
	self          atomic.Pointer[DispatchAPI]
	incidents     map[uint64]*Incident
	queue         []uint64 // FIFO priority-tiered queue IDs
	nextID        uint64
	autonomyActive bool // late-era autonomy response improvement toggle (AC-7)
}

// New constructs a new DispatchAPI.
func New() *DispatchAPI {
	d := &DispatchAPI{
		incidents: make(map[uint64]*Incident),
		queue:     []uint64{},
		nextID:    1,
	}
	d.self.Store(d)
	return d
}

func (d *DispatchAPI) checkNotCopied(method string) error {
	if d.self.Load() != d {
		return fmt.Errorf("MET-E_DISPATCH_99: copy guard error: method %s called on copied value", method)
	}
	return nil
}

// ReportIncident files a new incident category and severity into the dispatch queue (AC-1/AC-2).
func (d *DispatchAPI) ReportIncident(category string, severity int) error {
	if err := d.checkNotCopied("ReportIncident"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	// Validate incident inputs (AC-11)
	if category != "fire" && category != "medical" && category != "crime" && category != "accident" {
		return fmt.Errorf("MET-E_DISPATCH_02: invalid incident category: %s (AC-11)", category)
	}
	if severity < 1 || severity > 3 {
		return fmt.Errorf("MET-E_DISPATCH_03: invalid incident severity: %d, must be [1, 3] (AC-11)", severity)
	}

	id := d.nextID
	d.nextID++

	inc := &Incident{
		ID:       id,
		Category: category,
		Severity: severity,
		Status:   "Queued",
	}

	d.incidents[id] = inc

	// Insert into priority-tiered queue: Severity 3 items go to the front (AC-2)
	insertIdx := len(d.queue)
	for i, qID := range d.queue {
		other := d.incidents[qID]
		if inc.Severity > other.Severity {
			insertIdx = i
			break
		}
	}

	// Insert at insertIdx
	d.queue = append(d.queue, 0)
	copy(d.queue[insertIdx+1:], d.queue[insertIdx:])
	d.queue[insertIdx] = id

	return nil
}

// QueueSize returns the current number of queued incidents (AC-1).
func (d *DispatchAPI) QueueSize() (int, error) {
	if err := d.checkNotCopied("QueueSize"); err != nil {
		return 0, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.queue), nil
}

// DispatchNext pulls and dispatches the highest priority incident in the queue (AC-2/AC-3).
func (d *DispatchAPI) DispatchNext() (*Incident, error) {
	if err := d.checkNotCopied("DispatchNext"); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.queue) == 0 {
		return nil, fmt.Errorf("MET-E_DISPATCH_01: dispatch queue is empty (AC-10)")
	}

	id := d.queue[0]
	d.queue = d.queue[1:]

	inc := d.incidents[id]
	inc.Status = "Dispatched"

	return inc, nil
}

// ResolveIncident marks a dispatched incident as resolved (AC-3).
func (d *DispatchAPI) ResolveIncident(id uint64) error {
	if err := d.checkNotCopied("ResolveIncident"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	inc, ok := d.incidents[id]
	if !ok {
		return fmt.Errorf("MET-E_DISPATCH_04: unknown incident ID: %d (AC-10)", id)
	}

	if inc.Status != "Dispatched" {
		return fmt.Errorf("MET-E_DISPATCH_05: cannot resolve incident in state %s, must be Dispatched (AC-11)", inc.Status)
	}

	inc.Status = "Resolved"
	return nil
}

// SetAutonomyEra toggles the late-era autonomy response delay shrinkage (AC-7).
func (d *DispatchAPI) SetAutonomyEra(active bool) error {
	if err := d.checkNotCopied("SetAutonomyEra"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.autonomyActive = active
	return nil
}

// ResponseTimeMinutes calculates response time minutes based on category, severity, and late-era autonomy (AC-5/AC-6/AC-7).
func (d *DispatchAPI) ResponseTimeMinutes(category string, severity int) (float64, error) {
	if err := d.checkNotCopied("ResponseTimeMinutes"); err != nil {
		return 0, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	// Base response times per category
	base := 15.0
	switch category {
	case "fire":
		base = 8.0
	case "medical":
		base = 6.0
	case "crime":
		base = 10.0
	case "accident":
		base = 12.0
	default:
		return 0, fmt.Errorf("MET-E_DISPATCH_02: invalid incident category: %s (AC-11)", category)
	}

	// High severity speeds up response time (AC-2)
	severityBonus := float64(severity-1) * 2.0
	timeVal := base - severityBonus

	if d.autonomyActive {
		// Late-era autonomous vehicles reduce response delay by 30% (AC-7)
		timeVal = timeVal * 0.7
	}

	if timeVal < 1.0 {
		timeVal = 1.0
	}
	return timeVal, nil
}
