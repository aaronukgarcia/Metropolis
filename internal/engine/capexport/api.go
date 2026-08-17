package capexport

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// CatTradeExport is capexport's trade-ledger category (AC-7): every
// contract-revenue and cancellation-penalty posting goes through FinanceAPI
// tagged with this distinct trade/export category, so a future balance-of-
// trade aggregate (F2/F5, not this item) can sum "money entering via exports"
// without re-deriving it — and exports are never folded into generic opex or
// income (GR#3). finance.Category is an open string type, so this package owns
// its own tag rather than mislabelling a trade flow as opex/income.
const CatTradeExport finance.Category = "trade.export"

// CapExportAPI is code.json's "engine.capexport" inbound contract
// (CapExportAPI, "per-service surplus books; contract curves registered with
// ProjectionsAPI"): §36's service-capacity export — the per-service surplus
// book (capacity − internal demand), contract issuance/cancellation as
// commands with term/rate/penalty, contract-curve registration with
// ProjectionsAPI so F7 can render the crossing, the penalty-vs-service-cut
// choice at crossing time, and ledger posting of contract revenue/penalties
// tagged for trade accounting.
//
// The zero value is not usable; construct via [New] or [Load]. A *CapExportAPI
// is safe for concurrent use: every mutable field is guarded by mu, and
// checkNotCopied rejects a method call on a struct-copied value (SEC-020
// class, mirroring engine.fiscal/engine.services/engine.finance).
//
// # Dependencies (GR#20, contract-first)
//
// Capacity/demand is sourced from engine.services' ServicesAPI through an
// explicit [CapExportAPI.BindServiceLine] binding (never a concrete services
// struct field read directly — AC-1); money is posted through engine.finance's
// FinanceAPI (AC-7); curves are registered with engine.projections'
// ProjectionsAPI (US-3/AC-2). All three are wired via SetServices/SetFinance/
// SetProjections and read under mu; an unwired dependency fails closed with
// ErrDependencyMissing, never a fabricated figure (GR#17).
type CapExportAPI struct {
	mu            sync.RWMutex
	correlationID string

	// catalogue is the data/capexport.json line table, immutable after New.
	catalogue map[ExportableService]ExportableDef
	order     []ExportableService // the catalogue's deterministic order (GR#21)
	// demandGrowth is the placeholder projection growth rate (ASM-309),
	// defaulting from data and overridable via SetDemandGrowth for tests.
	demandGrowth float64

	// Outbound dependencies, wired via SetServices/SetFinance/SetProjections
	// and read under mu (GR#20 — consumed through their registered inbound
	// contracts alone).
	services    *services.ServicesAPI
	finance     *finance.FinanceAPI
	projections *projections.ProjectionsAPI

	// lines binds an exportable line to the engine.services instance whose
	// capacity/demand backs it. Populated via BindServiceLine; a line not
	// bound has no surplus book (ErrNoBackingService).
	lines map[ExportableService]services.ServiceID

	contracts      map[ContractID]Contract
	nextContractID ContractID
	committed      map[ExportableService]float64 // sold quantity per line, maintained on issue/cancel
	cuts           map[ExportableService]ServiceCut
	month          int64

	// self is the SEC-020 copy guard, stored exactly once in New before the
	// value is returned to any caller.
	self atomic.Pointer[CapExportAPI]
}

// New constructs a ready-to-wire CapExportAPI from a validated Config.
// correlationID is attached to every error the returned API constructs
// (GR#1); an empty one mints a fresh ID. Dependencies are wired later via
// SetServices/SetFinance/SetProjections.
func New(cfg Config, correlationID string) (*CapExportAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.Validate(); err != nil {
		return nil, errs.Wrap(ErrCapexportDataInvalid, correlationID, err, map[string]any{
			"cause": err.Error(),
		})
	}

	a := &CapExportAPI{
		correlationID:  correlationID,
		catalogue:      make(map[ExportableService]ExportableDef, len(cfg.Services)),
		order:          make([]ExportableService, 0, len(cfg.Services)),
		demandGrowth:   cfg.ProjectionDemandGrowthPerMonth,
		lines:          make(map[ExportableService]services.ServiceID),
		contracts:      make(map[ContractID]Contract),
		nextContractID: 1,
		committed:      make(map[ExportableService]float64),
		cuts:           make(map[ExportableService]ServiceCut),
	}
	for _, s := range cfg.Services {
		a.catalogue[s.ID] = s
		a.order = append(a.order, s.ID)
	}
	a.self.Store(a) // armed exactly once, before a is returned (SEC-020)
	return a, nil
}

// Load reads and schema-validates data/capexport.json from dir and returns a
// ready-to-wire *CapExportAPI with its catalogue populated (GR#15). Every
// failure is a registry-sourced *errs.E — never a panic or a silent default.
func Load(dir, correlationID string) (*CapExportAPI, error) {
	cfg, err := LoadCapexport(dir, correlationID)
	if err != nil {
		return nil, err
	}
	return New(cfg, correlationID)
}

// LoadDefault resolves data/'s directory via foundation/data's ResolveDataDir
// and then [Load]s it — the convenience entry point for callers (boot wiring,
// tests) that don't already have a resolved data directory in hand.
func LoadDefault(correlationID string) (*CapExportAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// checkNotCopied rejects a method call on a struct-copied *CapExportAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (a *CapExportAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return errs.New(ErrCopiedValue, a.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetServices wires the engine.services dependency (the registered
// engine.capexport → engine.services edge, GR#20). AC-1: every capacity/demand
// figure is sourced through this *ServicesAPI, never a concrete services
// struct field read directly.
func (a *CapExportAPI) SetServices(s *services.ServicesAPI) error {
	if err := a.checkNotCopied("SetServices"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.services = s
	return nil
}

// SetFinance wires the engine.finance dependency (the registered
// engine.capexport → engine.finance edge, GR#20). AC-7: every revenue and
// penalty posting goes through this *FinanceAPI.
func (a *CapExportAPI) SetFinance(f *finance.FinanceAPI) error {
	if err := a.checkNotCopied("SetFinance"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finance = f
	return nil
}

// SetProjections wires the engine.projections dependency (the registered
// engine.capexport → engine.projections edge, GR#20). US-3/AC-2: contract
// curves are registered with this *ProjectionsAPI.
func (a *CapExportAPI) SetProjections(p *projections.ProjectionsAPI) error {
	if err := a.checkNotCopied("SetProjections"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.projections = p
	return nil
}

// requireServices returns the wired services dependency or a registry-sourced
// ErrDependencyMissing naming the operation (GR#17 — fail closed, never
// fabricate a surplus figure on an unwired dependency).
func (a *CapExportAPI) requireServices(op string) (*services.ServicesAPI, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.services == nil {
		return nil, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{"operation": op, "dependency": "engine.services"})
	}
	return a.services, nil
}

// requireServicesLocked is requireServices for callers that already hold the
// write lock — it must not take a.mu.RLock itself (that would deadlock). It
// reads a.services directly, which mu guards.
func (a *CapExportAPI) requireServicesLocked(op string) (*services.ServicesAPI, error) {
	if a.services == nil {
		return nil, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{"operation": op, "dependency": "engine.services"})
	}
	return a.services, nil
}

// requireFinance returns the wired finance dependency or ErrDependencyMissing.
func (a *CapExportAPI) requireFinance(op string) (*finance.FinanceAPI, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.finance == nil {
		return nil, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{"operation": op, "dependency": "engine.finance"})
	}
	return a.finance, nil
}

// requireProjections returns the wired projections dependency or
// ErrDependencyMissing.
func (a *CapExportAPI) requireProjections(op string) (*projections.ProjectionsAPI, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.projections == nil {
		return nil, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{"operation": op, "dependency": "engine.projections"})
	}
	return a.projections, nil
}

// knownLineLocked reports whether line is a catalogue line; the caller holds
// at least a.mu.RLock.
func (a *CapExportAPI) knownLineLocked(line ExportableService) bool {
	_, ok := a.catalogue[line]
	return ok
}

// boundIDLocked returns the engine.services instance bound to line, and
// whether the binding exists; the caller holds at least a.mu.RLock.
func (a *CapExportAPI) boundIDLocked(line ExportableService) (services.ServiceID, bool) {
	id, ok := a.lines[line]
	return id, ok
}

// BindServiceLine binds an exportable line to the engine.services instance
// whose capacity/demand backs it (GR#20 — the composition root supplies the
// binding rather than this package reaching into engine.services internals).
// Re-binding an already-bound line replaces the binding. A call on a
// struct-copied *CapExportAPI returns ErrCopiedValue and binds nothing.
//
// SEC-187/SEC-194: a re-bind whose already-committed (sold) quantity would
// exceed the new service's exportable slack — capacity − internal demand, the
// same figure IssueContract's oversell check uses — is rejected with
// ErrInsufficientSurplus instead of silently accepting a permanently-crossing
// line with no demand change. (SEC-187 closed the raw-capacity half; SEC-194
// closes the demand half — committed must fit capacity − demand, not merely
// capacity.) A line with no committed quantity needs no check (0 ≤ any slack).
func (a *CapExportAPI) BindServiceLine(line ExportableService, id services.ServiceID) error {
	if err := a.checkNotCopied("BindServiceLine"); err != nil {
		return err
	}
	if id == "" {
		return errs.New(ErrInvalidContractInput, a.correlationID, map[string]any{"field": "serviceID", "line": string(line)})
	}

	a.mu.RLock()
	known := a.knownLineLocked(line)
	a.mu.RUnlock()
	if !known {
		return errs.New(ErrUnknownServiceLine, a.correlationID, map[string]any{"line": string(line)})
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	// SEC-194: the re-bind re-check mirrors IssueContract's FULL oversell check
	// — the already-committed (sold) quantity must fit within the new service's
	// exportable slack (capacity − internal demand), not merely within raw
	// capacity. SEC-187 only checked committed > capacity, so a re-bind to a
	// demand-squeezed service (capacity 100, internal demand 95) still yielded a
	// permanently-crossing line. Capacity, demand and committed are all read
	// under the write lock (requireServicesLocked reads a.services without
	// re-locking), so a concurrent issue cannot grow committed past the new
	// service's slack between the snapshot and the bind, and no stale
	// capacity/demand snapshot feeds the check.
	if committed := a.committed[line]; committed > 0 {
		svc, err := a.requireServicesLocked("BindServiceLine")
		if err != nil {
			return err
		}
		capacity, err := svc.Capacity(id)
		if err != nil {
			return err
		}
		demand, err := svc.Demand(id)
		if err != nil {
			return err
		}
		available := capacity - demand
		if committed > available {
			return errs.New(ErrInsufficientSurplus, a.correlationID, map[string]any{
				"line":      string(line),
				"requested": committed,
				"available": available,
			})
		}
	}
	a.lines[line] = id
	return nil
}

// SetMonth advances the simulation month. The month must be non-decreasing
// (GR#21); a backward move is rejected with ErrInvalidContractInput rather
// than silently rewinding the term/penalty arithmetic.
func (a *CapExportAPI) SetMonth(month int64) error {
	if err := a.checkNotCopied("SetMonth"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if month < a.month {
		return errs.New(ErrInvalidContractInput, a.correlationID, map[string]any{"field": "month", "value": month, "current": a.month})
	}
	a.month = month
	return nil
}

// Month returns the current simulation month.
func (a *CapExportAPI) Month() int64 {
	if err := a.checkNotCopied("Month"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.month
}

// SetDemandGrowth overrides the projection demand growth rate (ASM-309's
// placeholder, defaulting from data) — the test seam for AC-2's crossing and
// no-crossing scenarios. The rate must be finite and non-negative (GR#16).
func (a *CapExportAPI) SetDemandGrowth(rate float64) error {
	if err := a.checkNotCopied("SetDemandGrowth"); err != nil {
		return err
	}
	if !num.IsFinite(rate) || rate < 0 {
		return errs.New(ErrInvalidContractInput, a.correlationID, map[string]any{"field": "demandGrowth", "value": rate})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.demandGrowth = rate
	return nil
}

// Catalogue returns the exportable-service catalogue (AC-5) in the
// deterministic data-file order. It is a defensive copy — a caller mutating
// the returned slice cannot corrupt the API's state (weakness pattern #1).
func (a *CapExportAPI) Catalogue() []ExportableDef {
	if err := a.checkNotCopied("Catalogue"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]ExportableDef, 0, len(a.order))
	for _, line := range a.order {
		out = append(out, a.catalogue[line])
	}
	return out
}

// DefaultRate returns a line's data-sourced placeholder rate (GR#15), the
// rate IssueContract uses when a request omits one. An unknown line is
// rejected with ErrUnknownServiceLine.
func (a *CapExportAPI) DefaultRate(line ExportableService) (int64, error) {
	if err := a.checkNotCopied("DefaultRate"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.knownLineLocked(line) {
		return 0, errs.New(ErrUnknownServiceLine, a.correlationID, map[string]any{"line": string(line)})
	}
	return a.catalogue[line].RateMicropounds, nil
}

// Committed returns the sold capacity currently under active contracts for
// line (AC-6's per-service committed-capacity accessor — callable for the
// prison-places line specifically, so engine.prison's §43 overcrowding edge
// can be wired the moment it is registered). An unknown line is rejected; a
// known line with no contracts reads zero.
func (a *CapExportAPI) Committed(line ExportableService) (float64, error) {
	if err := a.checkNotCopied("Committed"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.knownLineLocked(line) {
		return 0, errs.New(ErrUnknownServiceLine, a.correlationID, map[string]any{"line": string(line)})
	}
	return a.committed[line], nil
}

// SurplusBook returns the per-service surplus book for line (US-1/AC-1):
// capacity − internal demand, plus the exportable slack after honouring
// committed contracts. Capacity and Demand are sourced live from the bound
// engine.services instance (GR#20). An unknown line is ErrUnknownServiceLine;
// a known-but-unbound line is ErrNoBackingService.
func (a *CapExportAPI) SurplusBook(line ExportableService) (SurplusBook, error) {
	if err := a.checkNotCopied("SurplusBook"); err != nil {
		return SurplusBook{}, err
	}
	a.mu.RLock()
	if !a.knownLineLocked(line) {
		a.mu.RUnlock()
		return SurplusBook{}, errs.New(ErrUnknownServiceLine, a.correlationID, map[string]any{"line": string(line)})
	}
	id, bound := a.boundIDLocked(line)
	committed := a.committed[line]
	a.mu.RUnlock()
	if !bound {
		return SurplusBook{}, errs.New(ErrNoBackingService, a.correlationID, map[string]any{"line": string(line)})
	}
	svc, err := a.requireServices("SurplusBook")
	if err != nil {
		return SurplusBook{}, err
	}
	capacity, err := svc.Capacity(id)
	if err != nil {
		return SurplusBook{}, err
	}
	demand, err := svc.Demand(id)
	if err != nil {
		return SurplusBook{}, err
	}
	available := capacity - demand - committed
	if available < 0 {
		available = 0
	}
	return SurplusBook{
		Line:      line,
		Capacity:  capacity,
		Demand:    demand,
		Committed: committed,
		Surplus:   capacity - demand,
		Available: available,
	}, nil
}

// CitizenCoverage returns the coverage the citizens of a line currently
// receive: their internal demand as served by capacity (min(demand, capacity)),
// reduced by any recorded service cut (the shortfall [CapExportAPI.CutInternalService]
// chose to impose). It is the "coverage to citizens" figure AC-3's two paths
// change in exactly the specified ways: cutting internal service reduces it by
// the shortfall; paying the cancellation penalty leaves it unchanged (the
// penalty frees committed capacity for the future but does not move the figure
// the cut would have reduced). The reduction is the cut's SHORTFALL, not the
// committed reduction — see ASM-1316 and the AC-3 escalation on MOD-049.
func (a *CapExportAPI) CitizenCoverage(line ExportableService) (float64, error) {
	if err := a.checkNotCopied("CitizenCoverage"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	if !a.knownLineLocked(line) {
		a.mu.RUnlock()
		return 0, errs.New(ErrUnknownServiceLine, a.correlationID, map[string]any{"line": string(line)})
	}
	id, bound := a.boundIDLocked(line)
	cutShortfall := a.cuts[line].Shortfall
	a.mu.RUnlock()
	if !bound {
		return 0, errs.New(ErrNoBackingService, a.correlationID, map[string]any{"line": string(line)})
	}
	svc, err := a.requireServices("CitizenCoverage")
	if err != nil {
		return 0, err
	}
	capacity, err := svc.Capacity(id)
	if err != nil {
		return 0, err
	}
	demand, err := svc.Demand(id)
	if err != nil {
		return 0, err
	}
	served := demand
	if capacity < served {
		served = capacity
	}
	coverage := served - cutShortfall
	if coverage < 0 {
		coverage = 0
	}
	return coverage, nil
}

// Crossing reports whether internal demand has grown past the capacity left
// for citizens (capacity − committed), and the shortfall (AC-2/AC-3). The
// crossing is the binding event the penalty-vs-service-cut choice is offered
// at. Read back from the same live ServicesAPI figures the surplus book uses.
func (a *CapExportAPI) Crossing(line ExportableService) (CrossingState, error) {
	if err := a.checkNotCopied("Crossing"); err != nil {
		return CrossingState{}, err
	}
	a.mu.RLock()
	if !a.knownLineLocked(line) {
		a.mu.RUnlock()
		return CrossingState{}, errs.New(ErrUnknownServiceLine, a.correlationID, map[string]any{"line": string(line)})
	}
	id, bound := a.boundIDLocked(line)
	committed := a.committed[line]
	a.mu.RUnlock()
	if !bound {
		return CrossingState{}, errs.New(ErrNoBackingService, a.correlationID, map[string]any{"line": string(line)})
	}
	svc, err := a.requireServices("Crossing")
	if err != nil {
		return CrossingState{}, err
	}
	capacity, err := svc.Capacity(id)
	if err != nil {
		return CrossingState{}, err
	}
	demand, err := svc.Demand(id)
	if err != nil {
		return CrossingState{}, err
	}
	headroom := capacity - committed
	shortfall := demand - headroom
	return CrossingState{
		Line:      line,
		Crossing:  shortfall > 0,
		Shortfall: shortfall,
		Capacity:  capacity,
		Committed: committed,
		Demand:    demand,
		Headroom:  headroom,
	}, nil
}

// testHookBeforeIssueCommit is a test-only seam (nil in production). When
// non-nil, IssueContract invokes it after releasing the RLock and before
// acquiring the write lock — precisely the window a concurrent BindServiceLine
// can slip into (SEC-198's TOCTOU on the service binding). Tests set it to
// orchestrate a deterministic interleaving; production code never sets it.
var testHookBeforeIssueCommit func()

// IssueContract issues an export contract (AC-4's command): it validates the
// request (positive finite quantity, positive term, non-negative rate), resolves
// the rate (catalogue default when zero), checks the line is bound and the
// requested quantity does not exceed the exportable slack (AC-8's oversell
// rejection), and only then records a durable, queryable Contract and updates
// the line's committed total. On any failure nothing is mutated — never a
// silently-oversold contract that only fails later at the crossing.
func (a *CapExportAPI) IssueContract(req IssueRequest) (Contract, error) {
	if err := a.checkNotCopied("IssueContract"); err != nil {
		return Contract{}, err
	}
	if !num.IsFinite(req.Quantity) || req.Quantity <= 0 {
		return Contract{}, errs.New(ErrInvalidContractInput, a.correlationID, map[string]any{"field": "quantity", "value": req.Quantity})
	}
	if req.TermMonths <= 0 {
		return Contract{}, errs.New(ErrInvalidContractInput, a.correlationID, map[string]any{"field": "termMonths", "value": req.TermMonths})
	}
	if req.RateMicropounds < 0 {
		return Contract{}, errs.New(ErrInvalidContractInput, a.correlationID, map[string]any{"field": "rateMicropounds", "value": req.RateMicropounds})
	}

	// The catalogue is immutable after New, so `known`/`def` may be read once
	// under RLock. The binding (`id`/`bound`) and the current month are MUTABLE
	// and are re-read under the write lock below (SEC-198), not carried from
	// this snapshot.
	a.mu.RLock()
	def, known := a.catalogue[req.Line]
	a.mu.RUnlock()
	if !known {
		return Contract{}, errs.New(ErrUnknownServiceLine, a.correlationID, map[string]any{"line": string(req.Line)})
	}

	rate := req.RateMicropounds
	if rate == 0 {
		rate = def.RateMicropounds
	}

	svc, err := a.requireServices("IssueContract")
	if err != nil {
		return Contract{}, err
	}

	if testHookBeforeIssueCommit != nil {
		testHookBeforeIssueCommit()
	}

	// Commit under the write lock, re-reading the service BINDING, capacity AND
	// demand under the same lock that guards committed, so the oversell check
	// cannot rest on a stale snapshot of any of them (the TOCTOU the attacker
	// flagged): a concurrent demand/capacity mutation — or a concurrent
	// BindServiceLine re-binding the line to a different service — between the
	// read and the decision would otherwise let a stale figure pass an oversold
	// contract. The re-check of the line's committed total under the same lock
	// also keeps a concurrent issue from double-selling the same slack.
	a.mu.Lock()
	defer a.mu.Unlock()

	// SEC-198: the binding is mutable state, so it is re-read under the write
	// lock rather than carried from the RLock snapshot above (which only covers
	// the immutable catalogue). A concurrent BindServiceLine slipping between
	// the RUnlock and this Lock would otherwise leave the oversell check
	// validating against a stale ServiceID while the line is now bound to a
	// different — possibly smaller — service, reintroducing the SEC-187/194
	// permanently-crossing oversold line through a race. The current month is
	// re-read here for the same reason (SetMonth is also a write-lock mutation).
	id, bound := a.lines[req.Line]
	if !bound {
		return Contract{}, errs.New(ErrNoBackingService, a.correlationID, map[string]any{"line": string(req.Line)})
	}
	month := a.month

	capacity, err := svc.Capacity(id)
	if err != nil {
		return Contract{}, err
	}
	demand, err := svc.Demand(id)
	if err != nil {
		return Contract{}, err
	}
	available := capacity - demand - a.committed[req.Line]
	if req.Quantity > available {
		return Contract{}, errs.New(ErrInsufficientSurplus, a.correlationID, map[string]any{
			"line":      string(req.Line),
			"requested": req.Quantity,
			"available": available,
		})
	}
	cid := a.nextContractID
	a.nextContractID++
	contract := Contract{
		ID:              cid,
		Line:            req.Line,
		ServiceID:       id,
		Quantity:        req.Quantity,
		TermMonths:      req.TermMonths,
		RateMicropounds: rate,
		IssuedMonth:     month,
	}
	a.contracts[cid] = contract
	a.committed[req.Line] += req.Quantity
	return contract, nil
}

// Contract returns the durable record for id (AC-4). An unknown or cancelled
// id is reported distinctly (found=false for unknown; Cancelled=true for a
// cancelled record) — never a zero-valued contract silently returned.
func (a *CapExportAPI) Contract(id ContractID) (Contract, bool) {
	if err := a.checkNotCopied("Contract"); err != nil {
		return Contract{}, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	c, ok := a.contracts[id]
	return c, ok
}

// Contracts returns every issued contract in ascending ContractID order
// (deterministic — never map-iteration order, GR#21).
func (a *CapExportAPI) Contracts() []Contract {
	if err := a.checkNotCopied("Contracts"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Contract, 0, len(a.contracts))
	for _, c := range a.contracts {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PayCancellationPenalty is AC-3's first crossing path: it posts the
// contract's cancellation penalty through FinanceAPI (trade-tagged, a debit to
// the city treasury) and cancels the contract, restoring the sold capacity to
// citizens. The penalty is the contract's CancellationPenalty function at the
// current month — nonzero before term-end, zero at/after term-end (AC-4).
//
// SEC-184: the read-check-post-cancel sequence is atomic under the write lock,
// so concurrent cancels of one contract cannot each pass the not-yet-cancelled
// check and each post the full penalty before the loser's re-check. The re-check
// runs BEFORE the ledger post, not after — a caller that loses the race sees
// ErrInvalidContract without a penalty already debited. Posting happens before
// the local cancellation commit, so a ledger failure leaves the contract intact
// (no partial state); an unknown or already-cancelled contract is
// ErrInvalidContract (AC-9).
func (a *CapExportAPI) PayCancellationPenalty(id ContractID) (Cancellation, error) {
	if err := a.checkNotCopied("PayCancellationPenalty"); err != nil {
		return Cancellation{}, err
	}
	fin, err := a.requireFinance("PayCancellationPenalty")
	if err != nil {
		return Cancellation{}, err
	}

	// requireFinance is resolved before the write lock (it takes its own RLock);
	// the rest of the sequence runs under mu so the not-yet-cancelled check, the
	// penalty post, and the Cancelled=true commit are one atomic unit.
	a.mu.Lock()
	defer a.mu.Unlock()

	c, ok := a.contracts[id]
	if !ok || c.Cancelled {
		return Cancellation{}, errs.New(ErrInvalidContract, a.correlationID, map[string]any{"contract": id})
	}

	penalty, err := c.CancellationPenalty(a.month)
	if err != nil {
		return Cancellation{}, err
	}
	var txid finance.TxID
	if penalty > 0 {
		txid, err = fin.Post(finance.Transaction{
			Description: fmt.Sprintf("capacity-export cancellation penalty: %s", c.Line),
			Entries: []finance.Entry{
				{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: penalty, Category: CatTradeExport},
				{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: penalty, Category: CatTradeExport},
			},
		})
		if err != nil {
			// A failed post leaves the contract intact (nothing mutated yet).
			return Cancellation{}, err
		}
	}

	c.Cancelled = true
	a.contracts[id] = c
	a.committed[c.Line] -= c.Quantity
	if a.committed[c.Line] < 0 {
		a.committed[c.Line] = 0
	}
	return Cancellation{Contract: c, Penalty: penalty, TxID: txid}, nil
}

// AccrueRevenue posts the contract's revenue for `months` months through
// FinanceAPI (trade-tagged, a credit to the city treasury) — the inflow half
// of AC-7's "revenue credits the city, penalty debits it" pair. The amount is
// months × rate × quantity. An unknown/cancelled/expired contract is
// ErrInvalidContract.
//
// SEC-185: the accrual is bounded by the contract's remaining life — `months`
// is capped at RemainingMonths(currentMonth), so a caller cannot accrue revenue
// for months beyond the term (money from thin air via a valid-looking call).
// A computed amount whose intermediate saturates int64 is rejected rather than
// posted as a fabricated MaxInt64 value (GR#16).
//
// SEC-193: the accrual is idempotent across calls — the contract records the
// months already accrued (Contract.AccruedMonths), and a call is capped at the
// months still un-accrued. Two identical AccrueRevenue calls cannot double-post
// the same months (the second finds nothing left and is rejected), and the
// not-cancelled check, the cursor read, the post and the cursor commit run
// atomically under the write lock, so a concurrent cancellation cannot slip
// between the check and the post (the TOCTOU observation SEC-193 also flags).
//
// SEC-197: the two accrual bounds are INDEPENDENT and must not be merged. (1)
// months is capped at RemainingMonths(currentMonth) — don't accrue past the
// term. (2) months is capped at TermMonths − AccruedMonths — don't re-accrue
// already-posted months. In the natural per-tick usage (SetMonth then
// AccrueRevenue(id, 1)) the term-remaining and the accrued cursor advance in
// lockstep, so subtracting the cursor from the already-elapsed-adjusted
// remaining (remaining − AccruedMonths) collapses to TermMonths − 2·elapsed and
// wrongly zeroes at half the term. The correct bound is
// min(remaining, TermMonths − AccruedMonths).
func (a *CapExportAPI) AccrueRevenue(id ContractID, months int64) (finance.Money, error) {
	if err := a.checkNotCopied("AccrueRevenue"); err != nil {
		return 0, err
	}
	if months <= 0 {
		return 0, errs.New(ErrInvalidContractInput, a.correlationID, map[string]any{"field": "months", "value": months})
	}
	fin, err := a.requireFinance("AccrueRevenue")
	if err != nil {
		return 0, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	c, ok := a.contracts[id]
	if !ok || c.Cancelled {
		return 0, errs.New(ErrInvalidContract, a.correlationID, map[string]any{"contract": id})
	}
	remaining := c.RemainingMonths(a.month)
	if remaining <= 0 {
		return 0, errs.New(ErrInvalidContract, a.correlationID, map[string]any{"contract": id, "reason": "term ended"})
	}
	// SEC-197: available is the SMALLER of the two independent bounds — the
	// remaining term (don't accrue past expiry) and the un-accrued months
	// (don't re-accrue posted months). Subtracting AccruedMonths from an
	// already-elapsed-adjusted remaining merges them and, in per-tick usage,
	// silently halves the collectable term.
	available := remaining
	if unaccrued := c.TermMonths - c.AccruedMonths; unaccrued < available {
		available = unaccrued
	}
	if available <= 0 {
		return 0, errs.New(ErrInvalidContract, a.correlationID, map[string]any{"contract": id, "reason": "already accrued"})
	}
	if months > available {
		months = available
	}

	perUnit, overflow := num.SafeMul(c.RateMicropounds, months)
	if overflow {
		return 0, errs.New(ErrInvalidContractInput, a.correlationID, map[string]any{"field": "amount", "contract": id, "reason": "rate × months overflow"})
	}
	amountV, err := num.SafeInt64(float64(perUnit) * c.Quantity)
	if err != nil {
		return 0, errs.Wrap(ErrInvalidContractInput, a.correlationID, err, map[string]any{"field": "amount", "contract": id})
	}
	amount := finance.Money(amountV)
	if amount <= 0 {
		return 0, errs.New(ErrInvalidContractInput, a.correlationID, map[string]any{"field": "amount", "value": int64(amount)})
	}
	if _, err := fin.Post(finance.Transaction{
		Description: fmt.Sprintf("capacity-export revenue: %s", c.Line),
		Entries: []finance.Entry{
			{Account: finance.AcctTreasury, Side: finance.SideCredit, Amount: amount, Category: CatTradeExport},
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: amount, Category: CatTradeExport},
		},
	}); err != nil {
		// A failed post leaves the accrued cursor unchanged (nothing mutated
		// yet), mirroring PayCancellationPenalty's post-then-commit ordering.
		return 0, err
	}
	c.AccruedMonths += months
	a.contracts[id] = c
	return amount, nil
}

// CutInternalService is AC-3's second crossing path: it keeps the contract
// intact (citizens take the shortfall) and records the cut as a durable,
// queryable ServiceCut — no ledger posting, no penalty. The line's citizens'
// coverage drops by the shortfall, read back through [CapExportAPI.CitizenCoverage]
// and [CapExportAPI.Cut]. It requires a genuine crossing (ErrNoCrossing
// otherwise — there is nothing to cut).
func (a *CapExportAPI) CutInternalService(line ExportableService) (ServiceCut, error) {
	if err := a.checkNotCopied("CutInternalService"); err != nil {
		return ServiceCut{}, err
	}
	state, err := a.Crossing(line)
	if err != nil {
		return ServiceCut{}, err
	}
	if !state.Crossing {
		return ServiceCut{}, errs.New(ErrNoCrossing, a.correlationID, map[string]any{"line": string(line)})
	}
	cut := ServiceCut{Line: line, Shortfall: state.Shortfall, Month: a.Month()}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cuts[line] = cut
	return cut, nil
}

// Cut returns the last recorded service cut for line (AC-3's durable record),
// and whether one was recorded.
func (a *CapExportAPI) Cut(line ExportableService) (ServiceCut, bool) {
	if err := a.checkNotCopied("Cut"); err != nil {
		return ServiceCut{}, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	c, ok := a.cuts[line]
	return c, ok
}
