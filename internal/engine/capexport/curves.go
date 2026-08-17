package capexport

import (
	"math"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is US-3/AC-2: the contracted-vs-internal-demand curves registered
// with engine.projections' ProjectionsAPI so F7 (ui.screen.proj) can render
// the crossing years out — before it happens in-tick, not only after.

// DemandCurveKey returns the ProjectionsAPI curve key under which a line's
// internal-demand curve is registered. F7 (and the AC-2 test) query it via
// ProjectionsAPI.Curve.
func DemandCurveKey(line ExportableService) string {
	return "capexport.internal-demand." + string(line)
}

// HeadroomCurveKey returns the ProjectionsAPI curve key under which a line's
// internal-headroom curve (capacity − committed, the capacity left for
// internal demand) is registered. The crossing is where the internal-demand
// curve exceeds this headroom curve.
func HeadroomCurveKey(line ExportableService) string {
	return "capexport.internal-headroom." + string(line)
}

// RegisterContractCurves registers the internal-demand and internal-headroom
// curve providers for every bound line with the wired ProjectionsAPI (US-3,
// AC-2). It is not idempotent: ProjectionsAPI rejects a duplicate provider
// key, so the composition root calls this exactly once after wiring. Every
// provider is a pure function of the line's live ServicesAPI figures, the
// current month, and the data-sourced demand-growth rate (GR#21).
func (a *CapExportAPI) RegisterContractCurves() error {
	if err := a.checkNotCopied("RegisterContractCurves"); err != nil {
		return err
	}
	proj, err := a.requireProjections("RegisterContractCurves")
	if err != nil {
		return err
	}

	a.mu.RLock()
	lines := make([]ExportableService, 0, len(a.lines))
	for line := range a.lines {
		lines = append(lines, line)
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i] < lines[j] })
	a.mu.RUnlock()

	for _, line := range lines {
		if err := proj.RegisterCurveProvider(DemandCurveKey(line), demandCurveProvider{api: a, line: line}); err != nil {
			return err
		}
		if err := proj.RegisterCurveProvider(HeadroomCurveKey(line), headroomCurveProvider{api: a, line: line}); err != nil {
			return err
		}
	}
	return nil
}

// demandCurveProvider is the ProjectionsAPI CurveProvider for a line's
// internal demand. For months at or before the current month it returns the
// recorded (live) demand; for future months it compounds that demand forward
// by the data-sourced demand-growth rate (ASM-309's placeholder — the growth
// rate used to make the crossing reachable is a data placeholder, not a
// spec-fixed growth figure).
type demandCurveProvider struct {
	api  *CapExportAPI
	line ExportableService
}

// Value implements projections.CurveProvider. It is a pure function of
// monthIndex given the API's state at the time of the call (GR#21) — the same
// monthIndex passed twice returns the same value until ServicesAPI's demand or
// the growth rate is mutated.
func (p demandCurveProvider) Value(monthIndex int64) (float64, error) {
	if err := p.api.checkNotCopied("demandCurve.Value"); err != nil {
		return 0, err
	}
	p.api.mu.RLock()
	id, bound := p.api.lines[p.line]
	now := p.api.month
	g := p.api.demandGrowth
	p.api.mu.RUnlock()
	if !bound {
		return 0, errs.New(ErrNoBackingService, p.api.correlationID, map[string]any{"line": string(p.line)})
	}
	svc, err := p.api.requireServices("demandCurve.Value")
	if err != nil {
		return 0, err
	}
	base, err := svc.Demand(id)
	if err != nil {
		return 0, err
	}
	if monthIndex <= now || g == 0 {
		return base, nil
	}
	// SEC-186: the compounded demand is finite-guarded. For any positive growth
	// rate there is a large enough monthIndex where math.Pow(1+g, N) overflows
	// to +Inf (the shipped g=0.02 does so near month 35727; g=1e18 near month
	// 18), and a zero base turns +Inf into NaN (0 × Inf). No finite bound on g
	// prevents this — monthIndex is caller-controlled and can be arbitrarily
	// large — so the guard is on the OUTPUT: a non-finite point is rejected here
	// rather than propagated to the F7 projection surface (GR#16).
	v := base * math.Pow(1+g, float64(monthIndex-now))
	v, ok := num.GuardFinite(v)
	if !ok {
		return 0, errs.New(ErrInvalidContractInput, p.api.correlationID, map[string]any{
			"field":      "demandProjection",
			"line":       string(p.line),
			"monthIndex": monthIndex,
			"growth":     g,
		})
	}
	return v, nil
}

// headroomCurveProvider is the ProjectionsAPI CurveProvider for a line's
// internal headroom: capacity − committed, the capacity left for internal
// demand once contracts are honoured. It is constant across months at this
// depth (capacity and committed are held fixed by the projection), which is
// exactly the "fixed-capacity contract held constant" scenario AC-2 constructs.
type headroomCurveProvider struct {
	api  *CapExportAPI
	line ExportableService
}

// Value implements projections.CurveProvider.
func (p headroomCurveProvider) Value(_ int64) (float64, error) {
	if err := p.api.checkNotCopied("headroomCurve.Value"); err != nil {
		return 0, err
	}
	p.api.mu.RLock()
	id, bound := p.api.lines[p.line]
	committed := p.api.committed[p.line]
	p.api.mu.RUnlock()
	if !bound {
		return 0, errs.New(ErrNoBackingService, p.api.correlationID, map[string]any{"line": string(p.line)})
	}
	svc, err := p.api.requireServices("headroomCurve.Value")
	if err != nil {
		return 0, err
	}
	capacity, err := svc.Capacity(id)
	if err != nil {
		return 0, err
	}
	return capacity - committed, nil
}
