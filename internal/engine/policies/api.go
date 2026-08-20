package policies

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// projectionSeam is the subset of *projections.ProjectionsAPI this package
// consumes, declared as a dependency-inversion seam so a test can capture
// the exact coefficient-delta payload PreviewImpact and Enact feed into the
// shared curve machinery (AC-4) without reimplementing projections. The
// concrete *projections.ProjectionsAPI satisfies it in production.
type projectionSeam interface {
	EnqueueDecision(d projections.Decision) error
	CancelDecision(id string) error
	Curve(key string, fromMonth, toMonth int64) ([]projections.Point, error)
	HorizonMonths() (int64, error)
	SetCurrentMonth(monthIndex int64) error
}

// financeSeam is the subset of *finance.FinanceAPI this package consumes
// (the ledger Post that AC-19's cost/opex debits flow through). The
// concrete *finance.FinanceAPI satisfies it in production.
type financeSeam interface {
	Post(tx finance.Transaction) (finance.TxID, error)
}

// taxSeam is the subset of *tax.TaxAPI this package consumes for
// data-declared tax coefficient moves. The concrete *tax.TaxAPI satisfies
// it in production.
type taxSeam interface {
	SetDistrictMultiplier(district tax.DistrictID, instrumentID string, multiplier float64) error
	GetDistrictMultiplier(district tax.DistrictID, instrumentID string) (float64, error)
}

// PoliciesAPI is code.json's "engine.policies" inbound contract
// (PoliciesAPI, "instruments = data-defined coefficient moves; scope
// resolution service for all modules"). It owns the policy library loaded
// from data/policies.json, the named-district scope system, enactment and
// repeal, the same-model enactment preview (AC-4), the PreviewDrift
// reckoning (AC-7), and the interaction/conflict surfaces (AC-10/AC-11).
//
// The zero value is not usable; construct via [Load] or [LoadDefault]
// (a ready-to-wire empty instance is available via [NewPoliciesAPI]).
// A *PoliciesAPI is safe for concurrent use (AC-16): every mutable field is
// guarded by mu, policy definitions are immutable after Load, and
// checkNotCopied rejects a method call on a struct-copied value
// (SEC-020-class).
type PoliciesAPI struct {
	mu            sync.RWMutex
	correlationID string

	library   map[PolicyID]*policyDef // immutable after Load
	districts map[DistrictID]*district
	roads     map[RoadID]roadDef

	active   map[EnactmentID]*enactment // active enactments
	previews map[EnactmentID]storedPreview
	events   []PreviewDriftEvent
	warnings []ConflictWarning

	nextEnactmentID uint64
	nextDistrictID  uint64
	currentMonth    int64

	// lastPostedMonth is the most recent simulation month for which
	// AdvanceMonth fully completed (checkpoint, recurring opex posting, and
	// clock advance). A second AdvanceMonth for the same month is a no-op —
	// never a double debit, never a re-run checkpoint. -1 means "no month
	// has been fully processed yet".
	lastPostedMonth int64

	meta policiesMeta

	finance     financeSeam
	projections projectionSeam
	tax         taxSeam

	self atomic.Pointer[PoliciesAPI]
}

// district is one named district: its name (AC-12) and its cell set
// (ASM-285 — a set of world cell references, not vector polygons).
type district struct {
	name  string
	cells []CellRef
}

// roadDef is one named road's edge set, a scope reference only (the road
// geometry itself lives in engine.roads, out of scope).
type roadDef struct {
	edges []EdgeRef
}

// enactment is one active enacted-policy instance.
type enactment struct {
	id       EnactmentID
	policyID PolicyID
	scope    Scope
}

// storedPreview is the AC-7 persisted preview snapshot: the coefficient-
// delta payload and the Computed-tagged portion of its curve, keyed by
// enactment ID.
type storedPreview struct {
	deltas  []CoefficientDelta
	points  map[string][]projections.Point // coefficient key -> Computed points
	horizon int64
}

// NewPoliciesAPI constructs an empty, ready-to-wire *PoliciesAPI with no
// library entries. It exists for tests and for a composition root that
// wires a library built some other way; production boot should use
// [Load] so the library is data-sourced (GR#15).
func NewPoliciesAPI(correlationID string) *PoliciesAPI {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	a := &PoliciesAPI{
		correlationID:   correlationID,
		library:         make(map[PolicyID]*policyDef),
		districts:       make(map[DistrictID]*district),
		roads:           make(map[RoadID]roadDef),
		active:          make(map[EnactmentID]*enactment),
		previews:        make(map[EnactmentID]storedPreview),
		nextEnactmentID: 1,
		nextDistrictID:  1,
		lastPostedMonth: -1,
	}
	a.self.Store(a)
	return a
}

// Load reads and schema-validates data/policies.json from dir (via
// foundation/data's generic loader) and builds a ready-to-query
// *PoliciesAPI. Every load failure is a registry-sourced *errs.E, never a
// silent default or panic.
func Load(dir, correlationID string) (*PoliciesAPI, error) {
	f, err := loadPoliciesFile(dir, correlationID)
	if err != nil {
		return nil, err
	}
	lib, meta, err := f.buildLibrary(correlationID)
	if err != nil {
		return nil, err
	}
	a := NewPoliciesAPI(correlationID)
	a.library = lib
	a.meta = meta
	return a, nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it.
func LoadDefault(correlationID string) (*PoliciesAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// checkNotCopied rejects a method call on a struct-copied *PoliciesAPI
// (SEC-020 family). Lock-free: a single atomic.Pointer.Load.
func (a *PoliciesAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return errs.New(ErrCopiedValue, a.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetProjections wires the engine.projections dependency used by previews
// and enactment (the registered engine.policies → engine.projections edge,
// GR#20). A nil projection leaves those operations failing with
// ErrProjectionsNotWired rather than silently no-op'ing (GR#17).
func (a *PoliciesAPI) SetProjections(p *projections.ProjectionsAPI) error {
	if err := a.checkNotCopied("SetProjections"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if p == nil {
		a.projections = nil
	} else {
		a.projections = p
	}
	return nil
}

// SetFinance wires the engine.finance dependency used by enactment-cost and
// enforcement-opex debits (the registered engine.policies → engine.finance
// edge, GR#20). A nil finance leaves those postings failing with
// ErrFinanceNotWired rather than silently skipping the money (GR#17).
func (a *PoliciesAPI) SetFinance(f *finance.FinanceAPI) error {
	if err := a.checkNotCopied("SetFinance"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if f == nil {
		a.finance = nil
	} else {
		a.finance = f
	}
	return nil
}

// SetTax wires the engine.tax dependency used by data-declared tax
// coefficient moves (the registered engine.policies → engine.tax edge,
// GR#20). A nil tax leaves a tax-carrying enactment failing with
// ErrTaxNotWired rather than silently dropping the move (GR#17).
func (a *PoliciesAPI) SetTax(t *tax.TaxAPI) error {
	if err := a.checkNotCopied("SetTax"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if t == nil {
		a.tax = nil
	} else {
		a.tax = t
	}
	return nil
}

// lookupLocked resolves a library entry, returning ErrUnknownPolicy for any
// key not loaded from data — never a zero-value policy.
func (a *PoliciesAPI) lookupLocked(id PolicyID) (*policyDef, error) {
	if err := a.checkNotCopied("lookupLocked"); err != nil {
		return nil, err
	}
	def, ok := a.library[id]
	if !ok {
		return nil, errs.New(ErrUnknownPolicy, a.correlationID, map[string]any{"policy": string(id)})
	}
	return def, nil
}

// sortedPolicyIDsLocked returns the library keys in ascending order
// (GR#21 — never map-iteration order on a path whose result matters).
func (a *PoliciesAPI) sortedPolicyIDsLocked() []PolicyID {
	if err := a.checkNotCopied("sortedPolicyIDsLocked"); err != nil {
		return nil
	}
	ids := make([]PolicyID, 0, len(a.library))
	for id := range a.library {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// sortedDistrictIDsLocked returns the district IDs in ascending order
// (GR#21).
func (a *PoliciesAPI) sortedDistrictIDsLocked() []DistrictID {
	if err := a.checkNotCopied("sortedDistrictIDsLocked"); err != nil {
		return nil
	}
	ids := make([]DistrictID, 0, len(a.districts))
	for id := range a.districts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// sortedActiveEnactmentsLocked returns the active enactments ordered by
// (PolicyID, then district, then road) — AC-14's documented stable key.
func (a *PoliciesAPI) sortedActiveEnactmentsLocked() []*enactment {
	if err := a.checkNotCopied("sortedActiveEnactmentsLocked"); err != nil {
		return nil
	}
	acts := make([]*enactment, 0, len(a.active))
	for _, e := range a.active {
		acts = append(acts, e)
	}
	sort.Slice(acts, func(i, j int) bool {
		if acts[i].policyID != acts[j].policyID {
			return acts[i].policyID < acts[j].policyID
		}
		if acts[i].scope.District != acts[j].scope.District {
			return acts[i].scope.District < acts[j].scope.District
		}
		return acts[i].scope.Road < acts[j].scope.Road
	})
	return acts
}

// encodeEnactmentID renders the deterministic enactment ID for the given
// counter value ("enactment-<n>"). It is a pure function: the caller owns
// the counter and advances it only after a successful commit, so a failed
// enactment never burns an ID (GR#21).
func encodeEnactmentID(n uint64) EnactmentID {
	return EnactmentID(fmt.Sprintf("enactment-%d", n))
}
