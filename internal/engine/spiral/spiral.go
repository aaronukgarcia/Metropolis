package spiral

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// DecayAPI is code.json's "engine.spiral" inbound contract (GUID
// 839b5566-8881-4322-adc8-461381e039ad, DecayAPI): decay states on
// cells/buildings plus subscribable spiral metrics. It owns the Detroit-
// spiral chain (AC-2), the per-cell abandonment decay model (AC-3/AC-4),
// the recovery commands (AC-5), the two death conditions (AC-6/AC-7) and
// the ghost-city epilogue (AC-8). It consumes sibling modules ONLY through
// their registered interfaces (GR#20): engine.finance for the insolvency
// signal, engine.projections for the ghost-city warning gate, and
// engine.world's public cell types for cell identity — it never reimplements
// their math.
//
// The zero value is not usable; construct via [New]. A *DecayAPI is safe for
// concurrent use: every mutable field is guarded by mu, and checkNotCopied
// rejects a method call on a struct-copied value (SEC-020-class).
type DecayAPI struct {
	mu            sync.RWMutex
	correlationID string
	cfg           config

	decay map[cellKey]*decayState

	// Append-only history (AC-8's epilogue source, AC-9's event sequence).
	events     []Event
	history    []HistoryEntry
	popHistory []int64 // month-indexed population, the provider's backing store

	// Population bookkeeping (AC-7's historic peak).
	historicPeak      int64
	historicPeakMonth int64

	// Spiral stage + shock state + the previous month's attractiveness (the
	// input to the attractiveness-decline derivative).
	stage              Stage
	shockRecorded      bool
	shockMonth         int64
	prevAttractiveness float64

	// Death verdict (set when a death condition fires).
	death DeathVerdict

	// Wired dependencies (SetFinance/SetProjections).
	finance     *finance.FinanceAPI
	projections *projections.ProjectionsAPI
	provider    *populationCurveProvider

	// Subscribers (AC-1's subscribable metrics surface).
	subscribers map[chan<- SpiralMetric]struct{}

	// self is the SEC-020 copy guard, stored exactly once in New before the
	// value is returned to any caller.
	self atomic.Pointer[DecayAPI]
}

// New constructs an empty, ready-to-drive DecayAPI from the embedded
// spiral.json (GR#15). correlationID is attached to every error this call
// (and the returned API's methods) construct (GR#1). A config that fails to
// load or validate is rejected with a registry-sourced error — never a
// silent default. The finance/projections dependencies are wired later via
// SetFinance/SetProjections.
func New(correlationID string) (*DecayAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	cfg, err := loadConfig(correlationID)
	if err != nil {
		return nil, err
	}
	d := &DecayAPI{
		correlationID: correlationID,
		cfg:           cfg,
		decay:         make(map[cellKey]*decayState),
		subscribers:   make(map[chan<- SpiralMetric]struct{}),
		stage:         StageStable,
	}
	d.self.Store(d)
	return d, nil
}

// checkNotCopied rejects a method call on a struct-copied *DecayAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and safe to
// run before mu is ever touched.
func (d *DecayAPI) checkNotCopied(method string) error {
	if d.self.Load() != d {
		return errs.New(ErrCopiedValue, d.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetFinance wires the engine.finance dependency used by the insolvency
// death condition (AC-6).
func (d *DecayAPI) SetFinance(f *finance.FinanceAPI) error {
	if err := d.checkNotCopied("SetFinance"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.finance = f
	return nil
}

// SetProjections wires the engine.projections dependency used by the
// ghost-city warning gate (AC-15) and registers this package's population
// curve provider under projections.CurveKeyGhostCityPopulation, so
// MarginToGhostCity can evaluate AC-7's dual threshold. The provider is
// created lazily on first call and registered with the supplied API.
func (d *DecayAPI) SetProjections(p *projections.ProjectionsAPI) error {
	if err := d.checkNotCopied("SetProjections"); err != nil {
		return err
	}
	if p == nil {
		return errs.New(ErrDependencyMissing, d.correlationID, map[string]any{
			"dependency": "projections", "operation": "SetProjections",
		})
	}
	d.mu.Lock()
	prov := d.provider
	if prov == nil {
		prov = &populationCurveProvider{d: d}
		d.provider = prov
	}
	d.projections = p
	d.mu.Unlock()

	// Register outside the lock — projections acquires its own lock, and the
	// provider reads back into d under d.mu, so registering while holding
	// d.mu would risk a lock-order inversion.
	return p.RegisterCurveProvider(projections.CurveKeyGhostCityPopulation, prov)
}

// Subscribe registers ch to receive a [SpiralMetric] on every AdvanceMonth
// (AC-1's subscribable metrics surface). It returns an unsubscribe function
// (idempotent). Sends are non-blocking: a full channel drops the update
// rather than stalling the simulation.
func (d *DecayAPI) Subscribe(ch chan<- SpiralMetric) func() {
	if err := d.checkNotCopied("Subscribe"); err != nil {
		return func() {}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.subscribers[ch] = struct{}{}
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		delete(d.subscribers, ch)
	}
}

// publish pushes a metric to every subscriber (non-blocking, drop-on-full).
func (d *DecayAPI) publish(m SpiralMetric) {
	if err := d.checkNotCopied("publish"); err != nil {
		return
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for ch := range d.subscribers {
		select {
		case ch <- m:
		default:
		}
	}
}

// ReportAbandonment records that cells became abandoned at month, starting
// their decay state at the data-sourced abandon severity (AC-3/AC-4). It is
// the seam through which the composition root / scenario reports real
// abandonment (buildings vacated by emigration) — this package models the
// decay that follows, it does not decide which cells empty.
func (d *DecayAPI) ReportAbandonment(cells []CellRef, month int64) error {
	if err := d.checkNotCopied("ReportAbandonment"); err != nil {
		return err
	}
	if month < 0 {
		return errs.New(ErrInvalidMonth, d.correlationID, map[string]any{"month": month})
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range cells {
		if _, exists := d.decay[c.key()]; exists {
			continue
		}
		d.decay[c.key()] = &decayState{
			cell:        c,
			abandonedAt: month,
			age:         0,
			severity:    d.cfg.Decay.AbandonSeverityStart,
		}
	}
	return nil
}

// MonthInput is one month's real, externally-owned inputs to [AdvanceMonth].
type MonthInput struct {
	Month          int64
	Attractiveness float64   // engine.attract A() score this month
	NetMigration   float64   // engine.attract signed net migration
	TaxDelta       int64     // engine.finance tax-receipts change
	InsolvencyRisk bool      // engine.finance fiscal distress signal
	Population     int64     // current population (engine.attract/citizens, per ASM-242)
	ShockRecorded  bool      // whether a shock has been recorded as of this month
	AbandonCells   []CellRef // cells newly abandoned this month
	Workers        int       // shard count for the decay-aging step (AC-9/AC-10)
}

// MonthResult is [AdvanceMonth]'s return: the derived stage, the ordered
// events this month produced, the death verdict, and the population figures
// recorded.
type MonthResult struct {
	Month        int64
	Stage        Stage
	Events       []Event
	Death        DeathVerdict
	DeathErr     error // gate rejection (ErrGhostCityNoWarning), nil otherwise
	Population   int64
	HistoricPeak int64
}

// AdvanceMonth advances the spiral one simulation month using real external
// inputs (AC-2), ageing decay and spreading blight (AC-3/AC-4), deriving the
// stage, feeding engine.projections' warning ledger (AC-15) and evaluating
// the death conditions (AC-6/AC-7). It is fully deterministic: the only
// inputs are MonthInput and the API's own state, the sharded aging step
// combines its worker results in a fixed order, and the blight frontier is
// the cellLess minimum (GR#21). No wall clock is read anywhere on the path
// (AC-13).
func (d *DecayAPI) AdvanceMonth(in MonthInput) (MonthResult, error) {
	if err := d.checkNotCopied("AdvanceMonth"); err != nil {
		return MonthResult{}, err
	}
	if in.Month < 0 {
		return MonthResult{}, errs.New(ErrInvalidMonth, d.correlationID, map[string]any{"month": in.Month})
	}
	// SEC-087: a negative population must be rejected at the boundary, before
	// it can reach EvaluateDeath — where float64(-N) < 10%-of-historic-peak
	// reads as "below threshold" and, with a prior qualifying warning on
	// record, fires a spurious ghost-city game-over. Mirror the Month<0 check.
	if in.Population < 0 {
		return MonthResult{}, errs.New(ErrNegativePopulation, d.correlationID, map[string]any{"population": in.Population})
	}
	workers := in.Workers
	if workers < 1 {
		workers = 1
	}

	// ---- State mutation phase (under d.mu) ----
	d.mu.Lock()
	d.recordPopulationLocked(in.Month, in.Population)
	peak := d.historicPeak

	prevAttractiveness := d.prevAttractiveness
	d.prevAttractiveness = in.Attractiveness

	var events []Event

	// Shock rising edge (AC-9's "shock recorded" event).
	if in.ShockRecorded && !d.shockRecorded {
		d.shockRecorded = true
		d.shockMonth = in.Month
		events = append(events, Event{Month: in.Month, Kind: EventShock})
	}

	// Abandonment (newly-abandoned cells).
	abandoned := 0
	for _, c := range in.AbandonCells {
		if _, exists := d.decay[c.key()]; exists {
			continue
		}
		d.decay[c.key()] = &decayState{
			cell:        c,
			abandonedAt: in.Month,
			age:         0,
			severity:    d.cfg.Decay.AbandonSeverityStart,
		}
		abandoned++
	}
	if abandoned > 0 {
		events = append(events, Event{Month: in.Month, Kind: EventAbandonment, Count: abandoned})
	}

	// Age decay (sharded; deterministic combine).
	d.ageCells(workers)

	// Blight spread: one deterministic frontier step.
	if next, ok := d.spreadOneStep(in.Month); ok {
		cell := next
		events = append(events, Event{Month: in.Month, Kind: EventBlightSpread, Cell: &cell})
	}

	// Derive the stage from real inputs (AC-2).
	stage := d.EvaluateStage(StageInputs{
		Attractiveness:     in.Attractiveness,
		PrevAttractiveness: prevAttractiveness,
		NetMigration:       in.NetMigration,
		TaxDelta:           in.TaxDelta,
		InsolvencyRisk:     in.InsolvencyRisk,
		AbandonedCells:     len(d.decay),
		ShockRecorded:      d.shockRecorded,
	})
	if stage != d.stage {
		d.stage = stage
		events = append(events, Event{Month: in.Month, Kind: EventStageTransition, Stage: stage})
	}
	decayed := len(d.decay)
	d.events = append(d.events, events...)
	d.mu.Unlock()

	// ---- Projections feed (outside d.mu) ----
	// Feed engine.projections' warning ledger — the "normal monthly
	// processing that feeds the curve provider" (AC-15(b)). projections
	// holds its own lock and the provider reads back into d under d.mu, so
	// this must not run while d.mu is held (no lock-order inversion).
	if p := d.projectionsSnapshot(); p != nil {
		if _, err := p.MarginToGhostCity(in.Month); err != nil {
			return MonthResult{}, err
		}
	}

	// ---- Death evaluation (outside d.mu) ----
	verdict, deathErr := d.EvaluateDeath(d.financeSnapshot(), in.Population, peak, in.Month)
	if verdict != DeathNone {
		deathEvent := Event{Month: in.Month, Kind: EventDeath, Death: verdict}
		d.mu.Lock()
		d.death = verdict
		d.events = append(d.events, deathEvent)
		d.mu.Unlock()
		events = append(events, deathEvent)
	}

	// ---- Append history + publish ----
	d.mu.Lock()
	d.history = append(d.history, HistoryEntry{
		Month:          in.Month,
		Stage:          stage,
		Population:     in.Population,
		Attractiveness: in.Attractiveness,
		NetMigration:   in.NetMigration,
		TaxDelta:       in.TaxDelta,
		Death:          verdict,
	})
	d.mu.Unlock()

	d.publish(SpiralMetric{Month: in.Month, Stage: stage, Population: in.Population, DecayedCells: decayed})

	return MonthResult{
		Month:        in.Month,
		Stage:        stage,
		Events:       events,
		Death:        verdict,
		DeathErr:     deathErr,
		Population:   in.Population,
		HistoricPeak: peak,
	}, nil
}

// recordPopulationLocked appends/overwrites the population history and
// updates the historic peak. Caller holds d.mu.
func (d *DecayAPI) recordPopulationLocked(month, population int64) {
	if err := d.checkNotCopied("recordPopulationLocked"); err != nil {
		return
	}
	if population < 0 {
		population = 0
	}
	if month == int64(len(d.popHistory)) {
		d.popHistory = append(d.popHistory, population)
	} else if month >= 0 && month < int64(len(d.popHistory)) {
		d.popHistory[month] = population
	}
	if population > d.historicPeak {
		d.historicPeak = population
		d.historicPeakMonth = month
	}
}

// financeSnapshot returns the wired finance dependency, or nil.
func (d *DecayAPI) financeSnapshot() *finance.FinanceAPI {
	if err := d.checkNotCopied("financeSnapshot"); err != nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.finance
}

// Stage returns the current spiral stage.
func (d *DecayAPI) Stage() Stage {
	if err := d.checkNotCopied("Stage"); err != nil {
		return StageStable
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.stage
}

// Death returns the death verdict recorded so far (DeathNone until a death
// condition fires).
func (d *DecayAPI) Death() DeathVerdict {
	if err := d.checkNotCopied("Death"); err != nil {
		return DeathNone
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.death
}

// HistoricPeak returns the highest population ever recorded, and the month
// it was recorded (AC-7's historic-peak input, AC-8's peak value/date).
func (d *DecayAPI) HistoricPeak() (int64, int64) {
	if err := d.checkNotCopied("HistoricPeak"); err != nil {
		return 0, 0
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.historicPeak, d.historicPeakMonth
}

// Events returns the ordered event log (AC-9's event sequence) as a copy.
func (d *DecayAPI) Events() []Event {
	if err := d.checkNotCopied("Events"); err != nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]Event(nil), d.events...)
}

// History returns the ordered monthly history log (AC-8's epilogue source).
func (d *DecayAPI) History() []HistoryEntry {
	if err := d.checkNotCopied("History"); err != nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return append([]HistoryEntry(nil), d.history...)
}

// StateHash returns a canonical SHA-256 hash of the current simulation state
// (AC-9's "final simulation state hash"): every decayed cell in deterministic
// order with its severity/age, the historic peak, the stage and the death
// verdict. Two runs that agree on the ordered event sequence AND this hash
// are byte-identical in outcome; a scalar (e.g. final population) standing
// in for the whole run would not catch a different blight order, which is
// exactly what this hash is built to catch.
func (d *DecayAPI) StateHash() string {
	if err := d.checkNotCopied("StateHash"); err != nil {
		return ""
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	var b strings.Builder
	for _, k := range d.sortedDecayKeysLocked() {
		st := d.decay[k]
		fmt.Fprintf(&b, "cell(%d,%d,%d,%d):s%d:a%d;",
			k.tile.X, k.tile.Y, k.local.Row, k.local.Col, st.severity, st.age)
	}
	fmt.Fprintf(&b, "peak:%d@%d;stage:%s;death:%s",
		d.historicPeak, d.historicPeakMonth, d.stage, d.death)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// sortedDecayKeysLocked returns the decay map's keys in deterministic order
// (caller holds d.mu for at least a read). Never map-iteration order.
func (d *DecayAPI) sortedDecayKeysLocked() []cellKey {
	if err := d.checkNotCopied("sortedDecayKeysLocked"); err != nil {
		return nil
	}
	out := make([]cellKey, 0, len(d.decay))
	for k := range d.decay {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		return cellLess(CellRef{Tile: out[i].tile, Local: out[i].local},
			CellRef{Tile: out[j].tile, Local: out[j].local})
	})
	return out
}
