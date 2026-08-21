package projections

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ProjectionsAPI is code.json's engine.projections inbound interface:
// a curve-provider registry ("systems register curve providers")
// composed with a decision-marker/Slow-Fuse layer and a UI-facing
// query surface ("UI subscribes to named projections") — AC-1.
//
// The zero value is not usable; construct via [NewProjectionsAPI]. A
// *ProjectionsAPI is safe for concurrent use by multiple registrant/
// consumer modules or shards (AC-14): every mutable field is guarded
// by mu, and the copy-guard below (mirroring engine.invariant.
// Registry/engine.world.World/engine.market.MarketAPI/engine.citizens.
// CitizensAPI's identical SEC-020 precedent) rejects any method call on
// a struct-copied value rather than risking a torn map read or a
// second, independently-zeroed mutex racing the original over the same
// aliased maps.
type ProjectionsAPI struct {
	mu sync.RWMutex

	providers  map[string]CurveProvider
	thresholds map[string]float64
	decisions  map[string]*queuedDecision

	currentMonth  int64
	horizonMonths func() (int64, error)

	ledger *WarningLedger

	correlationID string

	// self is stored exactly once, in NewProjectionsAPI, before the
	// constructed value is ever returned to a caller — see
	// checkNotCopied's doc comment.
	self atomic.Pointer[ProjectionsAPI]
}

// Option configures NewProjectionsAPI.
type Option func(*ProjectionsAPI)

// WithHorizonProvider overrides the default (embedded-config, ASM-237)
// base horizon with a caller-supplied function — the seam Out of
// scope's "engine.unlocks reads/extends N" note names: once
// engine.unlocks exists, its own unlock-tier-aware horizon function
// can be plugged in here without any change to this package (US-6).
// The function's returned value is re-read on every HorizonMonths call
// (never cached), so a later unlock is visible immediately.
func WithHorizonProvider(fn func() (int64, error)) Option {
	return func(p *ProjectionsAPI) { p.horizonMonths = fn }
}

// WithCorrelationID sets the correlation ID attached to every error
// this *ProjectionsAPI constructs (GR#1). Defaults to a freshly-minted
// one if omitted.
func WithCorrelationID(correlationID string) Option {
	return func(p *ProjectionsAPI) { p.correlationID = correlationID }
}

// NewProjectionsAPI constructs an empty, ready-to-use *ProjectionsAPI.
func NewProjectionsAPI(opts ...Option) *ProjectionsAPI {
	p := &ProjectionsAPI{
		providers:  make(map[string]CurveProvider),
		thresholds: make(map[string]float64),
		decisions:  make(map[string]*queuedDecision),
		ledger:     newWarningLedger(),
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.correlationID == "" {
		p.correlationID = errs.NewCorrelationID()
	}
	if p.horizonMonths == nil {
		p.horizonMonths = func() (int64, error) {
			cfg, err := loadHorizonConfig(p.correlationID)
			if err != nil {
				return 0, err
			}
			return cfg.BaseHorizonMonths, nil
		}
	}
	// Stored exactly once, here, before p is returned to any caller —
	// no goroutine can have a reference to p to race this Store
	// against (mirrors NewRegistry/NewWorld/NewMarketAPI/NewCitizensAPI).
	p.self.Store(p)
	return p
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other *ProjectionsAPI value. Deliberately lock-free (a single atomic
// pointer load) so it is safe to call BEFORE p.mu is ever touched —
// see engine.invariant.Registry.checkNotCopied's doc comment for the
// full SEC-016 argument: a copy's mu can read as "currently locked" if
// it was copied while the original's mu was held, and calling Lock() on
// such a copy before rejecting it can hang forever.
func (p *ProjectionsAPI) checkNotCopied(ctx map[string]any) error {
	if p.self.Load() != p {
		return errs.New(ErrCopiedValue, p.correlationID, ctx)
	}
	return nil
}

// RegisterCurveProvider registers provider under key (US-1, AC-1).
// Rejects a nil provider (ErrNilCurveProvider) and a duplicate key
// (ErrDuplicateCurveProvider) — never a silent overwrite, matching
// every other Register-shaped surface in this codebase (engine.
// invariant.Registry, engine.helper.Registry, foundation.registry).
func (p *ProjectionsAPI) RegisterCurveProvider(key string, provider CurveProvider) error {
	if err := p.checkNotCopied(map[string]any{"method": "RegisterCurveProvider"}); err != nil {
		return err
	}
	if provider == nil {
		return errs.New(ErrNilCurveProvider, p.correlationID, map[string]any{"key": key})
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkNotCopied(map[string]any{"method": "RegisterCurveProvider", "key": key}); err != nil {
		return err
	}
	if _, exists := p.providers[key]; exists {
		return errs.New(ErrDuplicateCurveProvider, p.correlationID, map[string]any{"key": key})
	}
	p.providers[key] = provider
	return nil
}

// RegisterThreshold registers a UI-SPEC §4 danger-line value for key,
// retrievable independently of the value series itself via Threshold
// (AC-8) — so the UI can draw a threshold line without this package
// hardcoding what "danger" means for each system. Unlike
// RegisterCurveProvider, a later call for the same key overwrites
// (thresholds are a single scalar a registrant may legitimately want
// to update as its own tuning changes, unlike a provider's identity).
func (p *ProjectionsAPI) RegisterThreshold(key string, value float64) error {
	if err := p.checkNotCopied(map[string]any{"method": "RegisterThreshold"}); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkNotCopied(map[string]any{"method": "RegisterThreshold", "key": key}); err != nil {
		return err
	}
	p.thresholds[key] = value
	return nil
}

// Threshold returns the registered threshold for key (AC-8).
func (p *ProjectionsAPI) Threshold(key string) (float64, error) {
	if err := p.checkNotCopied(map[string]any{"method": "Threshold"}); err != nil {
		return 0, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	v, ok := p.thresholds[key]
	if !ok {
		return 0, errs.New(ErrUnknownCurveKey, p.correlationID, map[string]any{"commodity": key})
	}
	return v, nil
}

// SetCurrentMonth records the sim's current month index (engine.core's
// Clock.Month() convention: 0 = world genesis, monotonically
// increasing). Curve queries at or before this month are tagged
// Historical; queries after it are projected (AC-7). Defaults to 0
// (world genesis) until first called.
func (p *ProjectionsAPI) SetCurrentMonth(monthIndex int64) error {
	if err := p.checkNotCopied(map[string]any{"method": "SetCurrentMonth"}); err != nil {
		return err
	}
	if monthIndex < 0 {
		return errs.New(ErrNegativeMonthQuery, p.correlationID, map[string]any{
			"monthIndex": monthIndex,
			"cause":      "monthIndex is negative (before the world's epoch)",
		})
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentMonth = monthIndex
	return nil
}

// HorizonMonths returns the current forecasting horizon N, in months
// (§13 F7; ASM-237's documented default; GR#15 — always read via
// horizonMonths, never a bare Go literal).
func (p *ProjectionsAPI) HorizonMonths() (int64, error) {
	if err := p.checkNotCopied(map[string]any{"method": "HorizonMonths"}); err != nil {
		return 0, err
	}
	return p.horizonMonths()
}

// maxCurveQueryMonths bounds a single Curve query's [fromMonth,
// toMonth] width (BREAK-3, Destructive round, this build). Not a
// balance number (GR#15 targets game-economy magnitudes) — this is a
// structural safety ceiling, generously above any real horizon N this
// package's own default/override could ever plug in (ASM-237's base
// horizon is measured in decades of months at most), that exists
// purely to keep the make() call below inside sane, non-overflowing,
// non-memory-exhausting territory regardless of what a caller passes.
const maxCurveQueryMonths = 1_000_000

// Curve queries the registered provider at key for every month in
// [fromMonth, toMonth] inclusive, composing in any queued decision-
// marker steps (AC-4) that affect this key, and tagging each point
// Historical/Computed/Extrapolated per AC-6/AC-7. fromMonth/toMonth
// before the world's epoch (< 0), an inverted range (fromMonth >
// toMonth), or a range wider than maxCurveQueryMonths is rejected
// (AC-9); key must already be registered via RegisterCurveProvider
// (AC-9).
//
// BREAK-3 fix (Destructive round, this build — GR#7). The original
// code only checked fromMonth<0||toMonth<0 and then allocated
// `make([]Point, 0, toMonth-fromMonth+1)` directly. Curve("k", 0,
// math.MaxInt64) passed that check (both arguments non-negative) and
// then overflowed the capacity expression: toMonth-fromMonth+1 wraps
// past math.MaxInt64 into a negative int64, and make() with a negative
// capacity panics ("makeslice: cap out of range") rather than
// returning a registry-sourced error — a caller-controlled input
// crashing the process instead of failing loudly through the normal
// GR#7 error path. The range is now validated (inverted-range and
// width-ceiling checks, both reusing ErrNegativeMonthQuery — see that
// constant's doc comment in errors.go for the reuse rationale) BEFORE
// the make() call, so the capacity expression is only ever evaluated
// once fromMonth<=toMonth and the width is already known to be within
// maxCurveQueryMonths — never large enough for the +1 to overflow.
func (p *ProjectionsAPI) Curve(key string, fromMonth, toMonth int64) ([]Point, error) {
	if err := p.checkNotCopied(map[string]any{"method": "Curve"}); err != nil {
		return nil, err
	}
	if fromMonth < 0 || toMonth < 0 {
		return nil, errs.New(ErrNegativeMonthQuery, p.correlationID, map[string]any{
			"monthIndex": minInt64(fromMonth, toMonth),
			"cause":      "fromMonth/toMonth is negative (before the world's epoch)",
		})
	}
	if fromMonth > toMonth {
		return nil, errs.New(ErrNegativeMonthQuery, p.correlationID, map[string]any{
			"monthIndex": fromMonth,
			"cause":      "fromMonth exceeds toMonth (inverted range)",
		})
	}
	if toMonth-fromMonth >= maxCurveQueryMonths {
		return nil, errs.New(ErrNegativeMonthQuery, p.correlationID, map[string]any{
			"monthIndex": toMonth,
			"cause":      "query range exceeds maxCurveQueryMonths",
		})
	}

	p.mu.RLock()
	provider, ok := p.providers[key]
	currentMonth := p.currentMonth
	steps := decisionStepsForKey(p.decisions, key)
	p.mu.RUnlock()
	if !ok {
		return nil, errs.New(ErrUnknownCurveKey, p.correlationID, map[string]any{"commodity": key})
	}

	horizon, err := p.HorizonMonths()
	if err != nil {
		return nil, err
	}

	points := make([]Point, 0, toMonth-fromMonth+1)
	for m := fromMonth; m <= toMonth; m++ {
		v, verr := provider.Value(m)
		if verr != nil {
			return nil, verr
		}
		for _, step := range steps {
			if m >= step.completionMonth {
				v += step.delta
			}
		}

		confidence := ConfidenceComputed
		if m-currentMonth > horizon {
			confidence = ConfidenceExtrapolated
		}

		points = append(points, Point{
			Month:      m,
			Value:      v,
			Historical: m <= currentMonth,
			Confidence: confidence,
		})
	}
	return points, nil
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
