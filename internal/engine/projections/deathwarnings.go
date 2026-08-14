package projections

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// isNonFinite reports whether v is NaN or +/-Inf — BREAK-2's shared
// guard (Destructive round, this build): a registered provider is
// external, untrusted input to this package exactly like any other
// query argument, and neither extrapolateToThreshold's slope
// arithmetic nor WarningLedger.observe's threshold comparison can be
// trusted to reason correctly about it once it goes non-finite (every
// IEEE-754 ordering comparison against NaN is false, and Inf minus Inf
// is NaN too).
func isNonFinite(v float64) bool {
	return math.IsNaN(v) || math.IsInf(v, 0)
}

// FEAT-068's "the projections engine must warn" half: MarginToInsolvency
// and MarginToGhostCity extrapolate a trend a consuming module (engine.
// finance / engine.spiral) itself registers via the ordinary
// RegisterCurveProvider surface — this file never reimplements either
// module's own missed-payment/credit or population/migration
// bookkeeping (Out of scope). It only asks: at the current trend's
// slope, how many months until the registered value reaches the
// consuming module's own documented death-condition threshold?

// CurveKeyFinanceInsolvencyRisk is the reserved key engine.finance
// registers its own missed-payment-streak trend under (AC-17). The
// registered provider's Value(monthIndex) is read as: the number of
// CONSECUTIVE months, as of monthIndex, engine.finance's own AC-7
// insolvency bookkeeping has been unable to meet obligations with no
// available credit. engine.finance AC-7's threshold is 3 consecutive
// such months.
const CurveKeyFinanceInsolvencyRisk = "engine.finance.insolvencyStreak"

// insolvencyStreakThreshold is engine.finance AC-7's own documented
// threshold ("3 consecutive months unable to meet obligations with no
// available credit") — quoted directly from that module's acceptance
// criteria, not a tunable balance number this package invents.
const insolvencyStreakThreshold = 3.0

// CurveKeyGhostCityPopulation is the reserved key engine.spiral (or
// engine.attract — see engine.spiral.md's own ASM-242) registers its
// live population trend under (AC-18). The registered provider must
// additionally implement GhostCityPeakProvider so MarginToGhostCity
// can evaluate engine.spiral.md AC-7's dual threshold.
const CurveKeyGhostCityPopulation = "engine.spiral.population"

// ghostCityPeakFloor / ghostCityPopulationFraction are engine.spiral.
// md AC-7's own documented dual threshold ("population below 10% of
// historic peak, where that peak has exceeded 50,000") — quoted
// directly from that module's acceptance criteria, not a tunable
// balance number this package invents (the actual balance-tunable
// warning LEAD TIME around this threshold is AC-20's data-sourced
// deathwarnings.json, not this structural definition).
const (
	ghostCityPeakFloor          = 50000.0
	ghostCityPopulationFraction = 0.10
)

// GhostCityPeakProvider is the extra shape a CurveKeyGhostCityPopulation
// registrant must implement, alongside CurveProvider, so
// MarginToGhostCity can apply engine.spiral.md AC-7's historic-peak
// half of its dual threshold.
type GhostCityPeakProvider interface {
	CurveProvider
	// HistoricPeak returns the highest population this city has ever
	// recorded, as of the most recent query.
	HistoricPeak() float64
}

// MarginResult is what MarginToInsolvency/MarginToGhostCity return —
// months remaining until the named death condition would fire if the
// current trend continues, plus AC-6's Computed/Extrapolated/
// Unavailable confidence marker.
type MarginResult struct {
	MonthsRemaining float64
	Confidence      Confidence
}

// noImminentRiskMonths is the sentinel "far enough away that this
// isn't a live worry" months-remaining figure MarginToInsolvency/
// MarginToGhostCity return when the registered trend is flat or
// improving. Deliberately a very large, obviously-not-a-real-schedule
// number (rather than +Inf, which several downstream comparisons —
// JSON encoding, WarningLedger threshold arithmetic — handle worse)
// so a caller comparing it against a lead-time figure always reads it
// as "not currently trending toward this death condition."
const noImminentRiskMonths = 1 << 30

// MarginToInsolvency extrapolates engine.finance's own registered
// CurveKeyFinanceInsolvencyRisk trend (AC-17): the slope between
// currentMonth and currentMonth-1's registered values, projected
// forward to insolvencyStreakThreshold. A flat or improving trend
// returns noImminentRiskMonths tagged Extrapolated (AC-6: a value this
// far beyond the horizon is never Computed); a worsening trend returns
// the real months-remaining figure tagged Computed. Every crossing of
// AC-20's data-sourced insolvency warning threshold is recorded in the
// WarningLedger (AC-19).
func (p *ProjectionsAPI) MarginToInsolvency(currentMonth int64) (MarginResult, error) {
	if err := p.checkNotCopied(map[string]any{"method": "MarginToInsolvency"}); err != nil {
		return MarginResult{}, err
	}
	if currentMonth < 0 {
		return MarginResult{}, errs.New(ErrNegativeMonthQuery, p.correlationID, map[string]any{"monthIndex": currentMonth})
	}

	p.mu.RLock()
	provider, ok := p.providers[CurveKeyFinanceInsolvencyRisk]
	p.mu.RUnlock()
	if !ok {
		return MarginResult{}, errs.New(ErrUnknownCurveKey, p.correlationID, map[string]any{"commodity": CurveKeyFinanceInsolvencyRisk})
	}

	result, err := extrapolateToThreshold(provider, currentMonth, insolvencyStreakThreshold)
	if err != nil {
		return MarginResult{}, err
	}

	cfg, err := loadDeathWarningConfig(p.correlationID)
	if err != nil {
		return MarginResult{}, err
	}
	p.ledger.observe(MetricMarginToInsolvency, currentMonth, result.MonthsRemaining, cfg.Insolvency.WarningThresholdMonths)

	return result, nil
}

// MarginToGhostCity extrapolates engine.spiral's own registered
// CurveKeyGhostCityPopulation trend the same way MarginToInsolvency
// does, against engine.spiral.md AC-7's 10%-of-historic-peak
// threshold (AC-18) — but first checks that provider's HistoricPeak()
// exceeds ghostCityPeakFloor; if it has never exceeded that floor, the
// margin is genuinely undefined (ConfidenceUnavailable), never a false
// "0 months" alarm, matching engine.spiral.md AC-7's dual condition.
func (p *ProjectionsAPI) MarginToGhostCity(currentMonth int64) (MarginResult, error) {
	if err := p.checkNotCopied(map[string]any{"method": "MarginToGhostCity"}); err != nil {
		return MarginResult{}, err
	}
	if currentMonth < 0 {
		return MarginResult{}, errs.New(ErrNegativeMonthQuery, p.correlationID, map[string]any{"monthIndex": currentMonth})
	}

	p.mu.RLock()
	raw, ok := p.providers[CurveKeyGhostCityPopulation]
	p.mu.RUnlock()
	if !ok {
		return MarginResult{}, errs.New(ErrUnknownCurveKey, p.correlationID, map[string]any{"commodity": CurveKeyGhostCityPopulation})
	}
	provider, ok := raw.(GhostCityPeakProvider)
	if !ok {
		return MarginResult{}, errs.New(ErrGhostCityProviderShape, p.correlationID, map[string]any{
			"commodity": CurveKeyGhostCityPopulation,
			"field":     "HistoricPeak",
		})
	}

	// BREAK-2 fix (Destructive round, this build): HistoricPeak() is
	// registrant-controlled input exactly like Value() — a NaN/Inf peak
	// must not reach the "<= ghostCityPeakFloor" comparison, since a
	// NaN there is also false and would fall through into computing a
	// NaN threshold below. Checked with isNonFinite BEFORE the
	// magnitude comparison, not folded into it.
	peak := provider.HistoricPeak()
	if isNonFinite(peak) || peak <= ghostCityPeakFloor {
		return MarginResult{MonthsRemaining: 0, Confidence: ConfidenceUnavailable}, nil
	}

	// extrapolateToThreshold's slope/remaining-distance arithmetic is
	// direction-agnostic (it compares signs, not magnitudes towards a
	// fixed positive direction), so the same function serves both
	// insolvency's RISING streak-count trend and ghost-city's FALLING
	// population trend without any special-casing: remainingToThreshold
	// and slope are both negative here (population above and falling
	// toward the lower threshold), and their ratio still comes out
	// positive and correct.
	threshold := peak * ghostCityPopulationFraction
	result, err := extrapolateToThreshold(provider, currentMonth, threshold)
	if err != nil {
		return MarginResult{}, err
	}

	cfg, err := loadDeathWarningConfig(p.correlationID)
	if err != nil {
		return MarginResult{}, err
	}
	p.ledger.observe(MetricMarginToGhostCity, currentMonth, result.MonthsRemaining, cfg.GhostCity.WarningThresholdMonths)

	return result, nil
}

// extrapolateToThreshold reads provider.Value at currentMonth and
// currentMonth-1, computes the slope, and — if the trend is moving
// TOWARD threshold (worsening) — projects forward the number of
// months remaining until it arrives. A flat or improving (moving away
// from threshold) trend returns noImminentRiskMonths/Extrapolated
// (AC-6, AC-17's "recovering" case).
//
// BREAK-2 fix (Destructive round, this build — CORE SAFETY). A
// provider returning NaN for now/prev used to fall all the way
// through to the bottom: slope and remainingToThreshold both become
// NaN, and the "flat or improving" escape hatch
// (`slope==0 || (remainingToThreshold>0)!=(slope>0)`) is ALSO false
// for NaN on every side (NaN==0 is false, NaN>0 is false, false!=false
// is false), so the function fell through to `months :=
// remainingToThreshold/slope` (NaN/NaN = NaN) and returned that NaN
// tagged ConfidenceComputed — a fabricated-looking "real" reading that
// WarningLedger.observe's `margin <= threshold` then silently read as
// "not crossed" (NaN<=x is always false), permanently blinding the
// ledger. now/prev are now checked with isNonFinite immediately after
// each Value() call, before any arithmetic touches them, and return
// ConfidenceUnavailable — never Computed, never a value that could be
// mistaken for a real extrapolation.
func extrapolateToThreshold(provider CurveProvider, currentMonth int64, threshold float64) (MarginResult, error) {
	now, err := provider.Value(currentMonth)
	if err != nil {
		return MarginResult{}, err
	}
	if isNonFinite(now) {
		return MarginResult{MonthsRemaining: now, Confidence: ConfidenceUnavailable}, nil
	}
	if currentMonth == 0 {
		// No prior sample to derive a slope from — treat as flat
		// (world genesis has no "trend" yet).
		return MarginResult{MonthsRemaining: noImminentRiskMonths, Confidence: ConfidenceExtrapolated}, nil
	}
	prev, err := provider.Value(currentMonth - 1)
	if err != nil {
		return MarginResult{}, err
	}
	if isNonFinite(prev) {
		return MarginResult{MonthsRemaining: prev, Confidence: ConfidenceUnavailable}, nil
	}

	slope := now - prev
	remainingToThreshold := threshold - now

	// Worsening means the slope carries the value TOWARD threshold:
	// same sign as remainingToThreshold, and non-zero.
	if slope == 0 || (remainingToThreshold > 0) != (slope > 0) {
		return MarginResult{MonthsRemaining: noImminentRiskMonths, Confidence: ConfidenceExtrapolated}, nil
	}

	months := remainingToThreshold / slope
	// BREAK-2b fix (Destructive round 2, this build — CORE SAFETY). The
	// isNonFinite guards above only check the INPUTS (now/prev); an
	// all-finite now/prev/slope/remainingToThreshold can still overflow
	// THIS division into +Inf (e.g. a near-zero slope against an
	// astronomically large remainingToThreshold), which would otherwise
	// fall straight into the months<0 clamp (false for +Inf) and get
	// returned tagged ConfidenceComputed — a "confidently-computed
	// infinite runway" handed to finance/spiral, the same AC-6 violation
	// class as BREAK-2's now/prev case, just one arithmetic step later.
	if isNonFinite(months) {
		return MarginResult{MonthsRemaining: months, Confidence: ConfidenceUnavailable}, nil
	}
	if months < 0 {
		// Already past threshold — zero months remaining, not negative.
		months = 0
	}
	return MarginResult{MonthsRemaining: months, Confidence: ConfidenceComputed}, nil
}

// --- WarningLedger (AC-19) --------------------------------------------

// Metric names the two FEAT-068 death-warning signals a WarningLedger
// entry can be recorded against.
type Metric string

const (
	MetricMarginToInsolvency Metric = "MarginToInsolvency"
	MetricMarginToGhostCity  Metric = "MarginToGhostCity"
)

// WarningLedgerEntry is one recorded threshold-crossing event (AC-19):
// the sim month it happened, which metric crossed, and the margin
// value at the moment of crossing.
type WarningLedgerEntry struct {
	Month  int64
	Metric Metric
	Margin float64
}

// WarningLedger is the queryable record engine.finance.md AC-29 and
// engine.spiral.md AC-15 check their own death-condition trigger
// against, to prove it was preceded by a warning (AC-19) —
// independent of whether any UI ever rendered the warning. Safe for
// concurrent use.
type WarningLedger struct {
	mu      sync.Mutex
	entries []WarningLedgerEntry
	crossed map[Metric]bool

	// self is the SEC-020 copy guard (atomic.Pointer, mirroring
	// engine.invariant.Registry.self / engine.world.World.self). A struct
	// copy of a *WarningLedger gets its own, independently-zeroed mu while
	// still ALIASING entries (a slice) and crossed (a map) — the same
	// "two locks, one referent" hazard the rest of the SEC-020 family
	// guards against. Stored exactly once, in newWarningLedger, before the
	// value is reachable from any caller.
	self atomic.Pointer[WarningLedger]
}

func newWarningLedger() *WarningLedger {
	l := &WarningLedger{crossed: make(map[Metric]bool)}
	// Armed exactly once, before l is returned to any caller (SEC-020).
	l.self.Store(l)
	return l
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other *WarningLedger value. Deliberately lock-free (a single
// atomic.Pointer.Load) so it is safe to call before l.mu is ever touched —
// see internal/foundation/errs/log.go's Logger.checkNotCopied for the full
// SEC-016 ordering argument.
func (l *WarningLedger) checkNotCopied() bool {
	return l.self.Load() == l
}

// observe records a rising-edge crossing: an entry is appended the
// FIRST time margin drops to/below threshold for metric, and not again
// until margin rises back above threshold and re-crosses (AC-19's
// false-pass note: a ledger that recorded an entry for every tick
// while below threshold would make the lead-time proof meaningless).
//
// BREAK-2 fix (Destructive round, this build — CORE SAFETY, defence in
// depth). `margin <= threshold` is false whenever margin is NaN, so a
// degenerate margin used to read as "not crossed" and vanish here even
// if it somehow reached this call — exactly the "provably-empty ledger
// while a death condition fires" failure the Destructive round found.
// extrapolateToThreshold (above) now stops a non-finite provider
// reading from ever being tagged Computed in the first place, so this
// package's own two call sites (MarginToInsolvency/MarginToGhostCity)
// should never hand observe a non-finite margin any more — but observe
// itself still treats one as an UNCONDITIONAL crossing (never silently
// "not crossed") if it ever does, so a degenerate signal is always
// surfaced in the ledger rather than swallowed, regardless of which
// call path produced it.
func (l *WarningLedger) observe(metric Metric, month int64, margin, threshold float64) {
	if !l.checkNotCopied() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	isCrossed := isNonFinite(margin) || margin <= threshold
	wasCrossed := l.crossed[metric]
	if isCrossed && !wasCrossed {
		l.entries = append(l.entries, WarningLedgerEntry{Month: month, Metric: metric, Margin: margin})
	}
	l.crossed[metric] = isCrossed
}

// Query returns every recorded entry for metric whose Month falls in
// [fromMonth, toMonth] inclusive, in the order recorded.
func (l *WarningLedger) Query(metric Metric, fromMonth, toMonth int64) []WarningLedgerEntry {
	if !l.checkNotCopied() {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []WarningLedgerEntry
	for _, e := range l.entries {
		if e.Metric == metric && e.Month >= fromMonth && e.Month <= toMonth {
			out = append(out, e)
		}
	}
	return out
}

// WarningLedger returns p's ledger (AC-19's "queryable by month range
// and metric" surface — engine.finance/engine.spiral call this
// directly to build their own AC-29/AC-15 lead-time proof).
func (p *ProjectionsAPI) WarningLedger() (*WarningLedger, error) {
	if err := p.checkNotCopied(map[string]any{"method": "WarningLedger"}); err != nil {
		return nil, err
	}
	return p.ledger, nil
}
