package spaceport

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// The seam interfaces below are engine.spaceport's local contract shapes
// for its registered outbound edges (GR#20 contract-first, stub-forever).
// They are satisfied directly by the real modules once they land and are
// wired at the composition root; tests inject fakes. No seam reimplements
// the consumer's internal model — it only carries the verdict/demand the
// spaceport hands across the boundary.

// EducationGate is the local contract shape for engine.education's expert
// gate verdict — the single numeric education output (EducationAPI's
// accumulated research output) the spaceport compares against
// data/spaceport.json's expert threshold. engine.education's *EducationAPI
// satisfies this interface directly (ResearchPoints); the composition root
// wires it, and tests inject a fake. The spaceport consumes the verdict and
// never recomputes the accounting (AC-2 — FEAT-055/engine.education own the
// threshold logic; this package only reads the output).
type EducationGate interface {
	ResearchPoints() int64
}

// PermitGate is the local contract shape for §7's facility permit system
// (FEAT-053, feat.facilitypermits — "for ANY large facility"). The
// spaceport delegates permit-gating to it rather than reimplementing the
// three-route permit gate (AC-8). Unbuilt; wired at the composition root.
type PermitGate interface {
	PermitHeld(facilityKey string) (bool, error)
}

// DecommissionLiability is the local contract shape for §7's "put back to
// nature" day-one liability (FEAT-054, feat.decommission). The spaceport
// delegates liability accrual to it at build/permit time rather than
// keeping a local liability ledger (AC-9). Unbuilt; wired at the
// composition root.
type DecommissionLiability interface {
	Accrue(facilityKey string) error
}

// FdiDraw is the local contract shape for engine.fdi's prospect-arrival
// demand surface (the registered engine.spaceport → engine.fdi edge,
// inbound FdiAPI). The spaceport injects a measurable prospect draw;
// engine.fdi owns the bid/commitment model (AC-6). Unbuilt; wired at the
// composition root.
type FdiDraw interface {
	AddProspectDemand(amount int64) error
}

// TourismDraw is the local contract shape for engine.tourism's visitor-draw
// surface (the registered engine.spaceport → engine.tourism edge, inbound
// TourismAPI). The spaceport injects a measurable visitor draw;
// engine.tourism owns the visitor model (AC-6). Unbuilt; wired at the
// composition root.
type TourismDraw interface {
	AddVisitorDraw(amount int64) error
}

// LaunchEvent is one fired launch: its absolute month, the export value
// credited, and the prestige increment credited (both int64, saturating).
type LaunchEvent struct {
	Month    int64
	Export   int64
	Prestige int64
}

// BuildCommand starts a spaceport build. FacilityKey must equal the
// resolved catalogue anchor; SiteX/SiteY are the exclusion-contour centre.
type BuildCommand struct {
	FacilityKey string
	SiteX       int64
	SiteY       int64
}

// SpaceportAPI is the sole entry point into engine.spaceport's state
// (code.json's engine.spaceport inbound contract, GUID
// da932e36-b1a5-4e83-84a8-86611a79e99e). It owns the facility-specific
// mechanics — the multi-year build, the deterministic launch schedule with
// per-launch export/prestige, the launch-exclusion contour, and the
// FDI/tourism draw — and consumes the shared expert gate, the permit gate,
// and the decommission liability through seams (GR#20).
//
// The zero value is not usable; construct via [New] or [Load]. A
// *SpaceportAPI is safe for concurrent use (AC-13): every mutable field is
// guarded by mu, and checkNotCopied rejects a method call on a
// struct-copied value (SEC-020-class).
type SpaceportAPI struct {
	correlationID string
	seed          uint64
	cfg           Config

	// Seam dependencies, wired via the Set* setters and read under mu.
	education    EducationGate
	permits      PermitGate
	decommission DecommissionLiability
	fdi          FdiDraw
	tourism      TourismDraw

	// Facility state. sited is set when StartBuild is accepted (the contour
	// is a consequence of choosing the site); building→built is the
	// multi-year progression advanced one month per Tick.
	sited           bool
	building        bool
	built           bool
	buildRemaining  int64
	buildTotal      int64
	siteX           int64
	siteY           int64
	launchCountdown int64
	lastTickMonth   int64

	// Launch history and the saturating accumulators (GR#16/AC-12).
	launches    []LaunchEvent
	exportTotal int64
	prestige    int64

	mu sync.RWMutex

	// self is the SEC-020 copy guard (atomic.Pointer). Stored exactly once,
	// in New, before the value is returned to any caller.
	self atomic.Pointer[SpaceportAPI]
}

// New constructs a SpaceportAPI from a validated Config and a world seed.
// correlationID is attached to every error this call (and the returned
// API's methods) construct (GR#1). An invalid Config is rejected with a
// registry-sourced error — never a silently-defaulted gate or scale. The
// seams are wired later via the Set* setters.
//
// seed is retained for API symmetry and any future per-launch draw; this
// package currently has NO random draws (launch cadence, build duration,
// exclusion radius, and every magnitude are data-sourced), so its
// determinism is structural — byte-identical across repeated runs and
// worker counts, with no shared/global RNG (AC-11).
func New(cfg Config, seed uint64, correlationID string) (*SpaceportAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.validate(correlationID); err != nil {
		return nil, err
	}
	a := &SpaceportAPI{
		correlationID: correlationID,
		seed:          seed,
		cfg:           cfg,
	}
	a.self.Store(a)
	return a, nil
}

// checkNotCopied rejects a method call on a struct-copied *SpaceportAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and therefore
// safe to run before mu is ever touched.
func (a *SpaceportAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return errs.New(ErrCopiedValue, a.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetEducationGate wires the shared expert gate (engine.education's
// research output) dependency.
func (a *SpaceportAPI) SetEducationGate(g EducationGate) error {
	if err := a.checkNotCopied("SetEducationGate"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.education = g
	return nil
}

// SetPermitGate wires the §7 facility-permit dependency (FEAT-053).
func (a *SpaceportAPI) SetPermitGate(p PermitGate) error {
	if err := a.checkNotCopied("SetPermitGate"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.permits = p
	return nil
}

// SetDecommissionLiability wires the §7 decommission dependency (FEAT-054).
func (a *SpaceportAPI) SetDecommissionLiability(d DecommissionLiability) error {
	if err := a.checkNotCopied("SetDecommissionLiability"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.decommission = d
	return nil
}

// SetFdiDraw wires the engine.fdi demand-injection dependency.
func (a *SpaceportAPI) SetFdiDraw(f FdiDraw) error {
	if err := a.checkNotCopied("SetFdiDraw"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fdi = f
	return nil
}

// SetTourismDraw wires the engine.tourism demand-injection dependency.
func (a *SpaceportAPI) SetTourismDraw(t TourismDraw) error {
	if err := a.checkNotCopied("SetTourismDraw"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tourism = t
	return nil
}

// StartBuild begins the multi-year build, after every gate passes. The
// gates are checked — and rejected with a registry-sourced error and NO
// partial state — in this order: facility key (taxonomy), site coordinates,
// no double-build, the shared expert gate (education output ≥ threshold,
// AC-3), the §7 permit (FEAT-053, AC-8), and the §7 decommission accrual
// (FEAT-054, AC-9). No facility record is created, no launch scheduled, and
// no export/prestige credited on any rejection (AC-10). A single StartBuild
// never completes the build — it only sites the facility and marks it
// building; the build advances one month per Tick (AC-4).
func (a *SpaceportAPI) StartBuild(cmd BuildCommand) error {
	if err := a.checkNotCopied("StartBuild"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkNotCopied("StartBuild"); err != nil {
		return err
	}

	if cmd.FacilityKey != a.cfg.CatalogueAnchor {
		return errs.New(ErrUnknownFacilityKey, a.correlationID, map[string]any{
			"facilityKey": cmd.FacilityKey, "anchor": a.cfg.CatalogueAnchor,
		})
	}
	if cmd.SiteX < 0 || cmd.SiteY < 0 {
		return errs.New(ErrInvalidSite, a.correlationID, map[string]any{
			"siteX": cmd.SiteX, "siteY": cmd.SiteY,
		})
	}
	if a.sited || a.building || a.built {
		return errs.New(ErrAlreadyBuilt, a.correlationID, map[string]any{
			"anchor": a.cfg.CatalogueAnchor,
		})
	}

	if a.education == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "education", "operation": "StartBuild",
		})
	}
	rp := a.education.ResearchPoints()
	if rp < a.cfg.ExpertThreshold {
		return errs.New(ErrExpertGateUnmet, a.correlationID, map[string]any{
			"output": rp, "threshold": a.cfg.ExpertThreshold,
		})
	}

	if a.permits == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "permits", "operation": "StartBuild",
		})
	}
	held, err := a.permits.PermitHeld(a.cfg.CatalogueAnchor)
	if err != nil {
		return err
	}
	if !held {
		return errs.New(ErrPermitMissing, a.correlationID, map[string]any{
			"facilityKey": a.cfg.CatalogueAnchor,
		})
	}

	if a.decommission == nil {
		return errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "decommission", "operation": "StartBuild",
		})
	}
	// Accrue before mutating this package's own state, so a failed accrual
	// leaves no facility record behind (AC-10's no-partial-state).
	if err := a.decommission.Accrue(a.cfg.CatalogueAnchor); err != nil {
		return err
	}

	// Every gate passed and no state was written on the way in — now site
	// the facility and mark it building.
	a.sited = true
	a.building = true
	a.buildRemaining = a.cfg.BuildMonths
	a.buildTotal = a.cfg.BuildMonths
	a.siteX = cmd.SiteX
	a.siteY = cmd.SiteY
	return nil
}

// Tick advances the spaceport one simulation month. While building, one
// month of build progress elapses; the build completes when buildMonths
// months have elapsed (AC-4 — never completable by a single command, since
// each Tick removes exactly one month). Once built, the launch countdown
// decrements and a launch fires on the data-sourced cadence, crediting the
// per-launch export and prestige (saturating). Launches never fire before
// the build completes (AC-4). A negative month index is rejected (AC-10).
func (a *SpaceportAPI) Tick(month int64) error {
	if err := a.checkNotCopied("Tick"); err != nil {
		return err
	}
	if month < 0 {
		return errs.New(ErrInvalidMonth, a.correlationID, map[string]any{"month": month})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkNotCopied("Tick"); err != nil {
		return err
	}

	a.lastTickMonth = month
	switch {
	case a.building:
		a.buildRemaining = num.SatSub(a.buildRemaining, 1)
		if a.buildRemaining == 0 {
			a.building = false
			a.built = true
			a.launchCountdown = a.cfg.LaunchCadenceMonths
		}
	case a.built:
		a.launchCountdown = num.SatSub(a.launchCountdown, 1)
		if a.launchCountdown == 0 {
			a.fireLaunchLocked(month)
			a.launchCountdown = a.cfg.LaunchCadenceMonths
		}
	}
	// Not sited: nothing to advance — a spaceport with no build in flight
	// has no launches to schedule (AC-4).
	return nil
}

// fireLaunchLocked credits one launch's export and prestige and records the
// event. Caller holds mu. Accumulations route through num.SatAdd (GR#16).
func (a *SpaceportAPI) fireLaunchLocked(month int64) {
	export := a.cfg.ExportPerLaunch
	prestigeInc := a.cfg.PrestigePerLaunch
	a.exportTotal = num.SatAdd(a.exportTotal, export)
	a.prestige = num.SatAdd(a.prestige, prestigeInc)
	a.launches = append(a.launches, LaunchEvent{Month: month, Export: export, Prestige: prestigeInc})
}

// LaunchSchedule returns the next n deterministic launch events that will
// fire from the current countdown position — a read-only projection of the
// cadence-spaced schedule (AC-4's "reachable, queryable mechanic"), not a
// state mutation. Each event's Month is the absolute month it will fire at,
// and Export/Prestige are the per-launch amounts. Scheduling (projecting) a
// launch against an unbuilt or incomplete facility is rejected with
// ErrLaunchUnbuilt (AC-10). n < 0 is treated as 0.
func (a *SpaceportAPI) LaunchSchedule(n int) ([]LaunchEvent, error) {
	if err := a.checkNotCopied("LaunchSchedule"); err != nil {
		return nil, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.built {
		return nil, errs.New(ErrLaunchUnbuilt, a.correlationID, map[string]any{
			"facilityKey": a.cfg.CatalogueAnchor, "state": "unbuilt or incomplete",
		})
	}
	if n < 0 {
		n = 0
	}
	out := make([]LaunchEvent, 0, n)
	month := num.SatAdd(a.lastTickMonth, a.launchCountdown)
	for i := 0; i < n; i++ {
		out = append(out, LaunchEvent{
			Month:    month,
			Export:   a.cfg.ExportPerLaunch,
			Prestige: a.cfg.PrestigePerLaunch,
		})
		month = num.SatAdd(month, a.cfg.LaunchCadenceMonths)
	}
	return out, nil
}

// InjectDraws pushes the built facility's data-sourced draw into the wired
// engine.fdi / engine.tourism seams (AC-6). It is the composition root's
// injection point (the seams are unbuilt, so the draw is not injected from
// Tick itself); a call before the build completes injects nothing, matching
// the queryable FdiDrawAmount/TourismDrawAmount returning 0 pre-build. The
// first seam error is returned; a missing seam simply means that consumer
// is not yet wired.
func (a *SpaceportAPI) InjectDraws() error {
	if err := a.checkNotCopied("InjectDraws"); err != nil {
		return err
	}
	a.mu.RLock()
	built := a.built
	fdiAmount := int64(0)
	tourismAmount := int64(0)
	fdi := a.fdi
	tourism := a.tourism
	if built {
		fdiAmount = a.cfg.FdiDrawAmount
		tourismAmount = a.cfg.TourismDrawAmount
	}
	a.mu.RUnlock()

	if !built {
		return nil
	}
	if fdi != nil {
		if err := fdi.AddProspectDemand(fdiAmount); err != nil {
			return err
		}
	}
	if tourism != nil {
		if err := tourism.AddVisitorDraw(tourismAmount); err != nil {
			return err
		}
	}
	return nil
}

// Anchor returns the data/buildings.json entry id the spaceport resolves to
// (AC-1). Immutable after New.
func (a *SpaceportAPI) Anchor() string {
	if err := a.checkNotCopied("Anchor"); err != nil {
		return ""
	}
	return a.cfg.CatalogueAnchor
}

// BlightClass returns the catalogue anchor's blight class (AC-5 — the
// contour reflects it). Immutable after New.
func (a *SpaceportAPI) BlightClass() string {
	if err := a.checkNotCopied("BlightClass"); err != nil {
		return ""
	}
	return a.cfg.BlightClass
}

// ExclusionRadius returns the data-sourced exclusion radius in cells.
// Immutable after New.
func (a *SpaceportAPI) ExclusionRadius() int64 {
	if err := a.checkNotCopied("ExclusionRadius"); err != nil {
		return 0
	}
	return a.cfg.ExclusionRadius
}

// BlightFactor returns the per-mille land-value factor (1000 = no blight) a
// cell at (cellX, cellY) experiences from the launch-exclusion contour.
// Cells within ExclusionRadius of the site (Chebyshev distance — an
// integer, no float, GR#16) get ExclusionFactorPerMille; cells outside, or
// before the site is chosen, get 1000. This is the queryable contour the
// composition root feeds into engine.world's decay overlay / engine.finance's
// land-value surface (no registered engine.spaceport → engine.world/finance
// edge exists today, so the contour is exposed here rather than pushed).
func (a *SpaceportAPI) BlightFactor(cellX, cellY int64) int64 {
	if err := a.checkNotCopied("BlightFactor"); err != nil {
		return 1000
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.sited {
		return 1000
	}
	dx := cellX - a.siteX
	if dx < 0 {
		dx = -dx
	}
	dy := cellY - a.siteY
	if dy < 0 {
		dy = -dy
	}
	d := dx
	if dy > d {
		d = dy
	}
	if d <= a.cfg.ExclusionRadius {
		return a.cfg.ExclusionFactorPerMille
	}
	return 1000
}

// IsBuilt reports whether the multi-year build has completed.
func (a *SpaceportAPI) IsBuilt() bool {
	if err := a.checkNotCopied("IsBuilt"); err != nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.built
}

// BuildProgress returns the remaining and total build months. (0,0) before
// StartBuild.
func (a *SpaceportAPI) BuildProgress() (remaining, total int64) {
	if err := a.checkNotCopied("BuildProgress"); err != nil {
		return 0, 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.buildRemaining, a.buildTotal
}

// Launches returns a copy of the launch history in firing order.
func (a *SpaceportAPI) Launches() []LaunchEvent {
	if err := a.checkNotCopied("Launches"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]LaunchEvent, len(a.launches))
	copy(out, a.launches)
	return out
}

// ExportTotal returns the accumulated export value (saturating, AC-12).
func (a *SpaceportAPI) ExportTotal() int64 {
	if err := a.checkNotCopied("ExportTotal"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.exportTotal
}

// Prestige returns the accumulated prestige (saturating, AC-12). It is this
// facility's own output derived from launch history — not a shadow
// city-wide prestige store (AC-7).
func (a *SpaceportAPI) Prestige() int64 {
	if err := a.checkNotCopied("Prestige"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.prestige
}

// FdiDrawAmount returns the FDI prospect draw the built facility injects
// (0 before the build completes — AC-6's spaceport-conditional increase).
func (a *SpaceportAPI) FdiDrawAmount() int64 {
	if err := a.checkNotCopied("FdiDrawAmount"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.built {
		return 0
	}
	return a.cfg.FdiDrawAmount
}

// TourismDrawAmount returns the tourism visitor draw the built facility
// injects (0 before the build completes — AC-6).
func (a *SpaceportAPI) TourismDrawAmount() int64 {
	if err := a.checkNotCopied("TourismDrawAmount"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.built {
		return 0
	}
	return a.cfg.TourismDrawAmount
}
