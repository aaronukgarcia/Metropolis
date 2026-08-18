package accelerator

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/consumption"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// CatalogueKey is the single accelerator taxonomy key this module
// reconciles against: data/buildings.json's hadron_research_ring entry
// (AC-1, shape (a) — the accelerator IS that entry, enriched in place).
const CatalogueKey = "hadron_research_ring"

// AcceleratorAPI is code.json's "engine.accelerator" inbound interface
// (GUID 21c44cd9-cad9-4c35-a98f-44cef0971bfa): the facility's own
// mechanics — the research-rate multiplier, the electricity/water draw into
// engine.consumption's UtilityAPI, the health spillover into engine.wellbeing,
// the FDI anchor draw, and the prestige output. It consumes its dependencies
// through the local seams in seams.go (GR#20); it never reimplements the
// education, wellbeing, FDI, permit, or decommission models.
//
// The zero value is not usable; construct via [New] or [Load]. A
// *AcceleratorAPI is safe for concurrent use (AC-16): every mutable field is
// guarded by mu, and checkNotCopied rejects a method call on a
// struct-copied value (SEC-020-class, mirroring engine.education).
type AcceleratorAPI struct {
	correlationID string
	seed          uint64
	cfg           Config // immutable after New

	// Dependencies, wired via Set* and read under mu.
	education    ResearchSource
	gate         ExpertGate
	utility      *consumption.UtilityAPI
	wellbeing    WellbeingSource
	fdi          FdiSource
	permits      PermitSource
	decommission DecommissionSource

	// built/online are the facility lifecycle flags (set together by Build).
	built  bool
	online bool

	// prestige is the accumulated prestige output (AC-10). int64, accumulated
	// through num's saturating helpers — never a float (AC-15).
	prestige int64

	// operated/lastTick make Operate idempotent per tick (a re-run of the
	// same tick cannot double-apply the spillover/prestige — GR#1), and make
	// the tick genuinely load-bearing in the (worldSeed, tick, ...) signature
	// (AC-14).
	operated bool
	lastTick int64

	mu sync.RWMutex

	// self is the SEC-020 copy guard (atomic.Pointer). Stored exactly once,
	// in New, before the value is returned to any caller.
	self atomic.Pointer[AcceleratorAPI]
}

// New constructs an AcceleratorAPI from a validated Config and a world seed
// (carried for the (worldSeed, tick, ...) determinism contract — AC-14).
// correlationID is attached to every error this call (and the returned API's
// methods) construct (GR#1). An invalid Config is rejected with a
// registry-sourced error — never a silently-defaulted placeholder. The
// dependencies are wired later via the Set* methods.
func New(cfg Config, seed uint64, correlationID string) (*AcceleratorAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.validate(correlationID); err != nil {
		return nil, err
	}
	a := &AcceleratorAPI{
		correlationID: correlationID,
		seed:          seed,
		cfg:           cfg,
	}
	a.self.Store(a)
	return a, nil
}

// checkNotCopied rejects a method call on a struct-copied *AcceleratorAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (a *AcceleratorAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return errs.New(ErrCopiedValue, a.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetEducation wires the engine.education research-output seam (the figure
// the expert gate measures).
func (a *AcceleratorAPI) SetEducation(s ResearchSource) error {
	if err := a.checkNotCopied("SetEducation"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.education = s
	return nil
}

// SetGate wires the shared expert gate (FEAT-055's contract shape; see
// [ThresholdGate] for the stub-forever standing-in).
func (a *AcceleratorAPI) SetGate(g ExpertGate) error {
	if err := a.checkNotCopied("SetGate"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.gate = g
	return nil
}

// SetUtility wires the engine.consumption UtilityAPI the draw resolves
// through (AC-4).
func (a *AcceleratorAPI) SetUtility(u *consumption.UtilityAPI) error {
	if err := a.checkNotCopied("SetUtility"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.utility = u
	return nil
}

// SetWellbeing wires the engine.wellbeing health-spillover seam (AC-8).
func (a *AcceleratorAPI) SetWellbeing(s WellbeingSource) error {
	if err := a.checkNotCopied("SetWellbeing"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.wellbeing = s
	return nil
}

// SetFdi wires the engine.fdi anchor-prospect seam (AC-9).
func (a *AcceleratorAPI) SetFdi(s FdiSource) error {
	if err := a.checkNotCopied("SetFdi"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fdi = s
	return nil
}

// SetPermits wires the feat.facilitypermits permit seam (AC-11).
func (a *AcceleratorAPI) SetPermits(s PermitSource) error {
	if err := a.checkNotCopied("SetPermits"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.permits = s
	return nil
}

// SetDecommission wires the feat.decommission liability seam (AC-12).
func (a *AcceleratorAPI) SetDecommission(s DecommissionSource) error {
	if err := a.checkNotCopied("SetDecommission"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.decommission = s
	return nil
}

// BuildCommand is the gated build request. Key must be [CatalogueKey].
type BuildCommand struct {
	Key string
}

// Build runs the accelerator's gated build path (AC-2/AC-3/AC-11/AC-12):
// the taxonomy key is checked, the shared expert gate verdict is consumed
// (rejected when the research output is below the data-file threshold), the
// inherited permit is checked, the FDI anchor draw is posted, the day-one
// decommission liability is accrued, and only then is the facility marked
// built + online with its base prestige granted. Any rejection leaves no
// partial state (AC-13): no facility record, no draw, no effect — the FDI
// draw is posted before the decommission liability (so the decommission
// accrual is the final external write, mirroring engine.spaceport's
// commit-adjacent accrual), and if the decommission accrual itself fails the
// FDI draw is compensated back, so either write's failure leaves zero
// observable effect.
func (a *AcceleratorAPI) Build(cmd BuildCommand) error {
	if err := a.checkNotCopied("Build"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if cmd.Key != CatalogueKey {
		return errs.New(ErrUnknownAccelerator, a.correlationID, map[string]any{"key": cmd.Key})
	}
	if a.built {
		return errs.New(ErrAlreadyBuilt, a.correlationID, map[string]any{"key": cmd.Key})
	}
	if a.education == nil || a.gate == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "education/gate", "operation": "Build",
		})
	}
	if a.permits == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "permits", "operation": "Build",
		})
	}
	if a.decommission == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "decommission", "operation": "Build",
		})
	}
	if a.fdi == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "fdi", "operation": "Build",
		})
	}

	// Consume the shared expert gate verdict (FEAT-055). The research output
	// is read through the education seam; this package never computes it.
	output := a.education.ResearchPoints()
	accepted, err := a.gate.Gate(output)
	if err != nil {
		return err
	}
	if !accepted {
		return errs.New(ErrExpertGateUnmet, a.correlationID, map[string]any{
			"threshold": a.cfg.ExpertGateThreshold,
		})
	}

	// Inherited §7 permit (FEAT-053).
	permitted, err := a.permits.HasPermit(cmd.Key)
	if err != nil {
		return err
	}
	if !permitted {
		return errs.New(ErrNoPermit, a.correlationID, map[string]any{"key": cmd.Key})
	}

	// FDI anchor draw (AC-9): the accelerator's presence raises the prospect
	// figure. Posted FIRST — before the decommission liability — so a failure
	// of the decommission accrual below leaves no phantom FDI anchor, and a
	// failure of the draw itself leaves nothing behind (AC-13).
	if err := a.fdi.AddAnchorProspect(a.cfg.FdiAnchorDraw); err != nil {
		return err
	}

	// Inherited §7 day-one decommission liability (FEAT-054): the final
	// external side effect, immediately before the (infallible) local commit
	// — the engine.spaceport pattern. If it fails, the FDI draw above is
	// compensated back so the rejection still leaves no partial state (AC-13).
	if err := a.decommission.AccrueLiability(cmd.Key); err != nil {
		if rmErr := a.fdi.RemoveAnchorProspect(a.cfg.FdiAnchorDraw); rmErr != nil {
			// The compensating rollback itself failed: surface both failures
			// (GR#1 — a silent rollback failure would leave the phantom draw
			// behind and read as a clean rejection).
			return errors.Join(err, rmErr)
		}
		return err
	}

	a.built = true
	a.online = true
	a.prestige = num.SatAdd(a.prestige, a.cfg.PrestigeBase)
	return nil
}

// Operate advances the accelerator one tick: it posts the health spillover
// into engine.wellbeing (AC-8) and accumulates prestige through num's
// saturating helpers (AC-15). It is idempotent per tick — re-running the
// same (or an earlier) tick is a no-op (GR#1), so the tick is a genuine
// input to the (worldSeed, tick, ...) signature (AC-14).
func (a *AcceleratorAPI) Operate(tick int64) error {
	if err := a.checkNotCopied("Operate"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.built || !a.online {
		return errs.New(ErrDrawUnbuilt, a.correlationID, nil)
	}
	if a.operated && tick <= a.lastTick {
		return nil // already processed this (or a later) tick
	}
	if a.wellbeing == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "wellbeing", "operation": "Operate",
		})
	}

	if err := a.wellbeing.PostHealthSpillover(a.cfg.HealthSpillover); err != nil {
		return err
	}
	a.prestige = num.SatAdd(a.prestige, a.cfg.PrestigePerTick)
	a.operated = true
	a.lastTick = tick
	return nil
}

// DemandEntity returns the accelerator's facility load as a
// consumption.DemandEntity (ClassRef = the data-sourced consumptionRef,
// Occupancy = the data-sourced throughput) — the entity the composition root
// feeds into UtilityAPI.SolveDailyTick alongside every other load, so the
// draw is conserved through the network solve rather than dropped or
// double-counted (AC-4/AC-6). The cfg is immutable after New, so this read
// needs no lock.
func (a *AcceleratorAPI) DemandEntity() consumption.DemandEntity {
	if err := a.checkNotCopied("DemandEntity"); err != nil {
		return consumption.DemandEntity{}
	}
	return consumption.DemandEntity{
		EntityRef: CatalogueKey,
		ClassRef:  a.cfg.ConsumptionRef,
		Occupancy: a.cfg.FacilityThroughput,
	}
}

// ResolvedDemand resolves the accelerator's base demand by posting into the
// wired engine.consumption UtilityAPI — coefficient × throughput via the
// consumptionRef class (AC-4). Requires the facility built and the utility
// wired; an unbuilt facility is rejected, never silently posted as zero.
func (a *AcceleratorAPI) ResolvedDemand(opts consumption.DemandOptions) (consumption.Demand, error) {
	if err := a.checkNotCopied("ResolvedDemand"); err != nil {
		return consumption.Demand{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.built {
		return consumption.Demand{}, errs.New(ErrDrawUnbuilt, a.correlationID, nil)
	}
	if a.utility == nil {
		return consumption.Demand{}, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "utility", "operation": "ResolvedDemand",
		})
	}
	return a.utility.ClassDemand(a.cfg.ConsumptionRef, a.cfg.FacilityThroughput, opts)
}

// PeakDemand returns the accelerator's peak demand: the base demand with
// electricity scaled by the data-sourced peak multiplier (> 1), so the peak
// electricity figure sits above the base figure (AC-5 — peak-load-aware).
func (a *AcceleratorAPI) PeakDemand(opts consumption.DemandOptions) (consumption.Demand, error) {
	if err := a.checkNotCopied("PeakDemand"); err != nil {
		return consumption.Demand{}, err
	}
	d, err := a.ResolvedDemand(opts)
	if err != nil {
		return consumption.Demand{}, err
	}
	d.Power *= a.cfg.ElectricityPeakMultiplier
	return d, nil
}

// ResearchMultiplier returns the data-sourced research-rate multiplier: the
// identity (1) while the accelerator is offline — so it leaves the figure
// unchanged — and cfg.ResearchRateMultiplier while online (AC-7). It is the
// same output the expert gate reads, so a running accelerator can push that
// figure further above the threshold (the deliberate end-game snowball).
func (a *AcceleratorAPI) ResearchMultiplier() float64 {
	if err := a.checkNotCopied("ResearchMultiplier"); err != nil {
		return 1
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.online {
		return 1
	}
	return a.cfg.ResearchRateMultiplier
}

// AppliedResearch applies the research-rate multiplier to a base research
// output, saturating the float→int64 conversion through foundation/num
// (GR#16). The accelerator exposes the multiplier; the wiring of it into
// engine.education's production is the unregistered edge AC-7 notes.
func (a *AcceleratorAPI) AppliedResearch(base int64) int64 {
	if err := a.checkNotCopied("AppliedResearch"); err != nil {
		return 0
	}
	return num.ClampInt64FromFloat(float64(base) * a.ResearchMultiplier())
}

// HealthSpillover returns the data-sourced health-spillover magnitude the
// accelerator posts into engine.wellbeing each tick: zero while offline,
// cfg.HealthSpillover while online (AC-8).
func (a *AcceleratorAPI) HealthSpillover() float64 {
	if err := a.checkNotCopied("HealthSpillover"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.online {
		return 0
	}
	return a.cfg.HealthSpillover
}

// FdiAnchorDraw returns the data-sourced FDI anchor-draw magnitude the
// accelerator posts into engine.fdi when built (AC-9). int64 — a point
// figure, never a float (GR#16).
func (a *AcceleratorAPI) FdiAnchorDraw() int64 {
	if err := a.checkNotCopied("FdiAnchorDraw"); err != nil {
		return 0
	}
	return a.cfg.FdiAnchorDraw
}

// Prestige returns the accelerator's accumulated prestige (AC-10): zero
// before the facility is operational, nonzero after. int64, accumulated via
// num's saturating helpers — never a float (AC-15).
func (a *AcceleratorAPI) Prestige() int64 {
	if err := a.checkNotCopied("Prestige"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.prestige
}

// ExpertGateThreshold returns the data-sourced expert gate threshold (AC-3),
// so callers and tests read the same figure the gate measures.
func (a *AcceleratorAPI) ExpertGateThreshold() int64 {
	if err := a.checkNotCopied("ExpertGateThreshold"); err != nil {
		return 0
	}
	return a.cfg.ExpertGateThreshold
}

// IsBuilt reports whether the accelerator has been built.
func (a *AcceleratorAPI) IsBuilt() bool {
	if err := a.checkNotCopied("IsBuilt"); err != nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.built
}

// IsOnline reports whether the accelerator is operational.
func (a *AcceleratorAPI) IsOnline() bool {
	if err := a.checkNotCopied("IsOnline"); err != nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.online
}
