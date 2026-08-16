package firms

import (
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/freight"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FirmID identifies one firm in a FirmsAPI's registry.
type FirmID uint64

// Stage is the four-stage firm lifecycle (AC-6): Startup (1-5 staff),
// Small (6-25), Medium (26-250), Enterprise (250+, multi-site, may
// export/list). The ORDER and the four names are §45's fixed vocabulary;
// the staff-count BANDS are data (data/firms.json), never Go literals
// (GR#15).
type Stage uint8

const (
	StageStartup    Stage = 0
	StageSmall      Stage = 1
	StageMedium     Stage = 2
	StageEnterprise Stage = 3
)

// String renders the stage's canonical §45 name.
func (s Stage) String() string {
	switch s {
	case StageStartup:
		return "Startup"
	case StageSmall:
		return "Small"
	case StageMedium:
		return "Medium"
	case StageEnterprise:
		return "Enterprise"
	}
	return "Unknown"
}

// stageName returns the stage's data-file slug.
func stageName(s Stage) string {
	switch s {
	case StageStartup:
		return "startup"
	case StageSmall:
		return "small"
	case StageMedium:
		return "medium"
	case StageEnterprise:
		return "enterprise"
	}
	return "unknown"
}

// nextStage returns the stage after s, and whether one exists.
func nextStage(s Stage) (Stage, bool) {
	if s < StageEnterprise {
		return s + 1, true
	}
	return s, false
}

// Premises is a firm's premises record (AC-7): whether a premises of the
// right zone class/size has been secured, and which build zone class it
// lives in. engine.build owns the zoning mechanics; this module records
// the firm's premises state and gates growth on it.
type Premises struct {
	Secured   bool
	ZoneClass string
}

// Financial is a firm's financial state (AC-1 "financial state"): the
// outstanding credit (micro-pounds, money — int64, never a float), the
// monthly cash flow (revenue − input cost − wage cost, micro-pounds), and
// the output scale (per-mille, 1000 = fully supplied — reduced by an
// input shortfall per AC-8).
type Financial struct {
	CreditOutstanding int64
	MonthlyCashFlow   int64
	OutputScale       int64
}

// Firm is one registered firm (AC-1/AC-4). Staff is a slice of REAL
// CitizenIDs (uint64, the same ID type CitizensAPI uses), never a bare
// StaffCount int.
type Firm struct {
	ID               FirmID
	Name             string
	FounderCitizenID uint64
	Stage            Stage
	Staff            []uint64
	Sector           citizens.Sector
	InputCommodity   market.CommodityType
	InputRequired    int64
	Premises         Premises
	Stalled          bool
	Financial        Financial
}

// firmState is the mutable per-firm runtime record (the Firm value). The
// founder exit-history that feeds the angel-boost ledger (AC-12) is keyed by
// CitizenID in founderHistory, not on the firm.
type firmState struct {
	firm Firm
}

// Startup is the result of one successful founding evaluation (AC-2): the
// citizen who founded (their own ID, of the same type CitizensAPI uses)
// and the firm that was created.
type Startup struct {
	FounderCitizenID uint64
	FirmID           FirmID
}

// FoundingContext is the shared, non-per-citizen context feeding the
// founding composite (AC-2): premises availability (engine.build), the
// local demand signal (engine.market/aggregate), and the founder's
// exit-history angel boost (AC-12). It is held constant by the AC-3
// isolation test so only the citizen's own values vary.
type FoundingContext struct {
	PremisesAvailable bool
	DemandSignal      int64
	ExitedFounder     bool
}

// Insolvency is the distinct outcome type for a firm failure (AC-9): the
// firm's staff are UNEMPLOYED (their employmentState set to unemployed via
// CitizensAPI, AC-5).
type Insolvency struct {
	FirmID     FirmID
	Unemployed []uint64
}

// Acquisition is the distinct outcome type for a firm absorbed into
// another (AC-9): the target's staff TRANSFER to the acquirer (no
// employment-state change), never unemploy.
type Acquisition struct {
	AcquirerID  FirmID
	TargetID    FirmID
	Transferred []uint64
}

// LifecycleKind enumerates the firm lifecycle events (AC-1/US-6).
type LifecycleKind uint8

const (
	EventFounded  LifecycleKind = 0
	EventGrown    LifecycleKind = 1
	EventFailed   LifecycleKind = 2
	EventAcquired LifecycleKind = 3
)

// LifecycleEvent is one lifecycle event emitted to subscribers (AC-1).
type LifecycleEvent struct {
	Kind   LifecycleKind
	FirmID FirmID
	Month  int64
}

// foundedEvent is one real founding event (firmID + month), the input to
// the entrepreneur-culture index (AC-10) — computed from the founding
// events themselves, never a separately-maintained shadow counter.
type foundedEvent struct {
	FirmID FirmID
	Month  int64
}

// founderRecord is the per-citizen founder-history ledger entry (AC-12).
type founderRecord struct {
	exited bool
}

// FirmsAPI is code.json's "engine.firms" inbound contract (FirmsAPI,
// GUID f6c47094-fae7-4d93-8267-653b15cc1a2a): the firm lifecycle —
// founding from real ambitious citizens, Startup→Small→Medium→Enterprise
// progression, staff as real CitizenIDs, failure/insolvency/acquisition,
// the entrepreneur-culture index, the superlinear professional-services
// demand relationship, and the deposit-bounded, rate-cycle-priced credit
// layer. It consumes engine.citizens, engine.finance, engine.market and
// engine.build through their registered interfaces only (GR#20).
//
// The zero value is not usable; construct via [Load] or [LoadDefault],
// then wire the stateful dependencies with [SetCitizens]/[SetFinance] and
// (optionally) [SetMarket]/[SetBuild]. A *FirmsAPI is safe for concurrent
// use (AC-19): every mutable field is guarded by mu, and checkNotCopied
// rejects a method call on a struct-copied value (SEC-020-class).
type FirmsAPI struct {
	mu            sync.RWMutex
	correlationID string
	seed          uint64

	citizens *citizens.CitizensAPI
	finance  *finance.FinanceAPI
	market   *market.MarketAPI
	build    *build.BuildAPI

	cfg config

	firms map[FirmID]*firmState

	// totalCreditOutstanding is the running sum of every live firm's
	// CreditOutstanding (SEC-100) — the aggregate ApproveCredit bounds
	// against the deposit-backed lending capacity so cumulative borrowing
	// can never exceed what the deposit base supports.
	totalCreditOutstanding int64

	founderHistory map[uint64]*founderRecord

	subscribers map[uint64]chan LifecycleEvent
	nextSubID   uint64
	events      []LifecycleEvent

	foundedEvents []foundedEvent
	foundedCount  int64
	failedCount   int64

	month int64

	self atomic.Pointer[FirmsAPI]
}

// Load reads and validates data/firms.json from dir and returns a ready
// *FirmsAPI with an empty firm registry. correlationID is attached to
// every error this call (and the returned API's methods) construct
// (GR#1). Every failure is a registry-sourced *errs.E — never a silent
// default substitution, never a panic. The citizens/finance/market/build
// dependencies are wired later via the Set* setters.
func Load(dir string, seed uint64, correlationID string) (*FirmsAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	cfg, err := LoadConfig(filepath.Join(dir, fileFirms), correlationID)
	if err != nil {
		return nil, err
	}
	api := &FirmsAPI{
		correlationID:  correlationID,
		seed:           seed,
		cfg:            cfg,
		firms:          make(map[FirmID]*firmState),
		founderHistory: make(map[uint64]*founderRecord),
		subscribers:    make(map[uint64]chan LifecycleEvent),
		nextSubID:      1,
	}
	api.self.Store(api)
	return api, nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers (boot wiring, tests) that don't already have a resolved data
// directory in hand.
func LoadDefault(seed uint64, correlationID string) (*FirmsAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, seed, correlationID)
}

// SetCitizens wires the engine.citizens dependency (AC-2/AC-4/AC-5).
func (f *FirmsAPI) SetCitizens(c *citizens.CitizensAPI) error {
	if err := f.checkNotCopied("SetCitizens"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.citizens = c
	return nil
}

// SetFinance wires the engine.finance dependency (AC-13's deposit pool).
func (f *FirmsAPI) SetFinance(fin *finance.FinanceAPI) error {
	if err := f.checkNotCopied("SetFinance"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finance = fin
	return nil
}

// SetMarket wires the engine.market dependency (AC-8's input pricing/
// availability).
func (f *FirmsAPI) SetMarket(m *market.MarketAPI) error {
	if err := f.checkNotCopied("SetMarket"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.market = m
	return nil
}

// SetBuild wires the engine.build dependency (AC-7's premises query).
func (f *FirmsAPI) SetBuild(b *build.BuildAPI) error {
	if err := f.checkNotCopied("SetBuild"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.build = b
	return nil
}

// checkNotCopied rejects a method call on a struct-copied *FirmsAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — so it is
// safe to run before mu is ever touched.
func (f *FirmsAPI) checkNotCopied(method string) error {
	if f.self.Load() != f {
		return errs.New(ErrCopiedValue, f.correlationID, map[string]any{"method": method})
	}
	return nil
}

// Month returns the current simulation month (the last founding-evaluation
// or month-resolution month).
func (f *FirmsAPI) Month() int64 {
	if err := f.checkNotCopied("Month"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.month
}

// Firm returns the firm's current lifecycle record, or ErrUnknownFirm
// (AC-1 query surface). The returned Staff is a defensive copy.
func (f *FirmsAPI) Firm(id FirmID) (Firm, error) {
	if err := f.checkNotCopied("Firm"); err != nil {
		return Firm{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	fs, ok := f.firms[id]
	if !ok {
		return Firm{}, errs.New(ErrUnknownFirm, f.correlationID, map[string]any{"firm": uint64(id)})
	}
	return cloneFirm(fs.firm), nil
}

// Firms returns every registered firm in ascending FirmID order (GR#21).
func (f *FirmsAPI) Firms() []Firm {
	if err := f.checkNotCopied("Firms"); err != nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	ids := make([]FirmID, 0, len(f.firms))
	for id := range f.firms {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]Firm, 0, len(ids))
	for _, id := range ids {
		out = append(out, cloneFirm(f.firms[id].firm))
	}
	return out
}

// Stage returns the firm's current lifecycle stage, or ErrUnknownFirm.
func (f *FirmsAPI) Stage(id FirmID) (Stage, error) {
	if err := f.checkNotCopied("Stage"); err != nil {
		return 0, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	fs, ok := f.firms[id]
	if !ok {
		return 0, errs.New(ErrUnknownFirm, f.correlationID, map[string]any{"firm": uint64(id)})
	}
	return fs.firm.Stage, nil
}

// StaffRoster returns the firm's staff roster (real CitizenIDs), or
// ErrUnknownFirm (AC-1/AC-4 query surface).
func (f *FirmsAPI) StaffRoster(id FirmID) ([]uint64, error) {
	firm, err := f.Firm(id)
	if err != nil {
		return nil, err
	}
	return firm.Staff, nil
}

// FounderExited reports whether a citizen has a logged successful exit
// (acquired, or grown to Enterprise and later exited) in the founder
// history ledger (AC-12). False for an unknown citizen — an unknown
// citizen has no exit history.
func (f *FirmsAPI) FounderExited(citizenID uint64) bool {
	if err := f.checkNotCopied("FounderExited"); err != nil {
		return false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	rec, ok := f.founderHistory[citizenID]
	return ok && rec.exited
}

// FoundedCount returns the total number of firms founded (the real founding
// events), for AC-9's churn assertion and the culture index.
func (f *FirmsAPI) FoundedCount() int64 {
	if err := f.checkNotCopied("FoundedCount"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.foundedCount
}

// FailedCount returns the total number of firms failed (insolvency, not
// acquisition), for AC-9's churn assertion.
func (f *FirmsAPI) FailedCount() int64 {
	if err := f.checkNotCopied("FailedCount"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.failedCount
}

// TotalCreditOutstanding returns the running sum of every live firm's
// outstanding credit (SEC-100) — the aggregate ApproveCredit bounds against
// the deposit-backed lending capacity.
func (f *FirmsAPI) TotalCreditOutstanding() int64 {
	if err := f.checkNotCopied("TotalCreditOutstanding"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.totalCreditOutstanding
}

// Subscribe registers a new lifecycle-event subscriber and returns its
// subscription ID and a receive-only channel (US-6/AC-1). Events are
// delivered best-effort (a slow subscriber's buffered channel fills and
// the event is dropped for that subscriber, never blocking the firm
// lifecycle).
func (f *FirmsAPI) Subscribe() (uint64, <-chan LifecycleEvent, error) {
	if err := f.checkNotCopied("Subscribe"); err != nil {
		return 0, nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextSubID
	f.nextSubID++
	ch := make(chan LifecycleEvent, 64)
	f.subscribers[id] = ch
	return id, ch, nil
}

// Unsubscribe removes a subscription by ID.
func (f *FirmsAPI) Unsubscribe(id uint64) error {
	if err := f.checkNotCopied("Unsubscribe"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch, ok := f.subscribers[id]; ok {
		delete(f.subscribers, id)
		close(ch)
	}
	return nil
}

// Events returns the lifecycle events emitted so far, in emission order.
func (f *FirmsAPI) Events() []LifecycleEvent {
	if err := f.checkNotCopied("Events"); err != nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]LifecycleEvent, len(f.events))
	copy(out, f.events)
	return out
}

// emitLocked appends a lifecycle event to the log and fans it out to
// subscribers (best-effort). The caller holds f.mu.
func (f *FirmsAPI) emitLocked(e LifecycleEvent) {
	f.events = append(f.events, e)
	for id, ch := range f.subscribers {
		select {
		case ch <- e:
		default: // slow subscriber: drop for this one, never block the lifecycle
			_ = id
		}
	}
}

// cloneFirm returns a defensive copy of a firm (its Staff slice is copied
// so a caller can never mutate the registry through a returned value).
func cloneFirm(f Firm) Firm {
	f.Staff = append([]uint64(nil), f.Staff...)
	return f
}

// inputCommodityFor maps a firm's sector onto the §33 chain-input
// commodity this module prices/availability-bounds it through (AC-8).
// This is a documented placeholder mapping (ASM-logged) — the full
// per-sector input bill is engine.freight's job (GR#3).
func inputCommodityFor(s citizens.Sector) market.CommodityType {
	switch s {
	case citizens.SectorPrimary:
		return market.FoodStaples
	case citizens.SectorSecondary:
		return market.ConstructionMaterials
	default:
		return market.ConsumerGoods
	}
}

// isServicesSector reports whether a sector is a services-sector (the
// professional/financial-services demand AC-11 excludes services firms
// from the served-firm count).
func isServicesSector(s citizens.Sector) bool {
	return s == citizens.SectorTertiary || s == citizens.SectorPublic
}

// RegisterFirm implements freight.FirmRegistrar (the dependency-inversion
// seam freight defines for engine.firms — freight's AC-4 "stages register
// as firms"). It registers a chain stage as a Startup firm and returns the
// freight.Firm snapshot (ID, staff, premises). The registered firm carries
// the stage's jobs as its staff count. See doc.go and the AC-4 block in
// internal/engine/freight/doc.go.
func (f *FirmsAPI) RegisterFirm(name string, staff int64, premises string) (freight.Firm, error) {
	if err := f.checkNotCopied("RegisterFirm"); err != nil {
		return freight.Firm{}, err
	}
	if staff < 0 {
		return freight.Firm{}, errs.New(ErrInvalidStaffCount, f.correlationID, map[string]any{"value": staff})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// A chain-stage firm has no single founder citizen, so its ID is
	// derived deterministically from its stage name (SEC-102: no
	// lock-acquisition-ordered counter).
	id := f.firmIDForLocked(0, f.month, "stagefirm:"+name)
	// A chain-stage firm has no single founder citizen — the stage is
	// registered AS a firm by freight, not founded by an ambitious citizen
	// (FounderCitizenID 0 = no founder). Its staff count is the stage's
	// jobs; the roster itself is filled by the labour-pool hires at growth
	// time, so the founding record carries the count as a placeholder
	// headroom (documented, not a bare StaffCount int on Firm).
	fs := &firmState{firm: Firm{
		ID:               id,
		Name:             name,
		FounderCitizenID: 0,
		Stage:            StageStartup,
		Sector:           citizens.SectorSecondary,
		InputCommodity:   inputCommodityFor(citizens.SectorSecondary),
		InputRequired:    staff,
		Premises:         Premises{ZoneClass: premises},
		Financial:        Financial{OutputScale: 1000},
	}}
	f.firms[id] = fs
	f.foundedCount++
	f.foundedEvents = append(f.foundedEvents, foundedEvent{FirmID: id, Month: f.month})
	f.emitLocked(LifecycleEvent{Kind: EventFounded, FirmID: id, Month: f.month})
	return freight.Firm{ID: uint64(id), Staff: staff, Premises: premises}, nil
}

// clampPerMille clamps v into [0, 1000] (GR#16).
func clampPerMille(v int64) int64 {
	if v < 0 {
		return 0
	}
	if v > 1000 {
		return 1000
	}
	return v
}

// firmIDForLocked derives a deterministic FirmID from the founding event's
// own identity — (founder, month) for a citizen-founded firm, or the stage
// name (via the purpose tag) for a freight-registered stage firm. It is a
// pure function of (seed, founder, month, purpose), never of
// lock-acquisition order, so sharded concurrent founding maps every founder
// to the same firm ID regardless of goroutine scheduling (SEC-102, AC-17,
// GR#21). A collision (2^-64) re-probes deterministically via the
// position-independent stream At(n).
func (f *FirmsAPI) firmIDForLocked(founder uint64, month int64, purpose string) FirmID {
	stream := det.NewStream(f.seed, founder, month, purpose)
	for attempt := uint64(0); ; attempt++ {
		id := stream.At(attempt)
		if id == 0 {
			id = 1
		}
		if _, taken := f.firms[FirmID(id)]; !taken {
			return FirmID(id)
		}
	}
}

// itoa is the package-local int→string helper (no strconv.Itoa spam).
func itoa(i int) string { return strconv.Itoa(i) }

// satMul is a saturating multiply for the fixed-point scale arithmetic
// (GR#16: an overflow must saturate, never wrap).
func satMul(a, b int64) int64 {
	v, _ := num.SafeMul(a, b)
	return v
}
