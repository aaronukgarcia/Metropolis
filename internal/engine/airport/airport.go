package airport

import (
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/freight"
	"github.com/aaronukgarcia/Metropolis/internal/engine/mining"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// fileAirport is data/airport.json's filename, relative to the resolved data
// directory (see data.ResolveDataDir).
const fileAirport = "airport.json"

// percentFull is the full-throughput percentage denominator. data/airport.json
// carries a per-tier surfaceAccessReducedPct in the open interval (0,100), and
// throughput is scaled by pct/percentFull. This is a UNIT convention (percent),
// not a balance figure — every balance number lives in data/airport.json (GR#15).
const percentFull int64 = 100

// AirportAPI is code.json's "engine.airport" inbound interface (GUID
// eeb7203d-d22b-4e09-87ce-647e4c9f28a8): the Heathrow-class international
// airport — a multi-component node (runways, terminals, freight apron) whose
// passenger throughput is a function of its parts, an access-tier step-change
// fed to engine.tourism, a runway-access/adjacency query fed to engine.fdi,
// an air-cargo modal arm handed into engine.freight's conserved-tonnes
// identity, a noise contour registered with engine.mining's BlightAPI, a
// surface-access (road/rail spur) throughput gate, and a permit + land gate
// via feat.facilitypermits.
//
// The zero value is not usable; construct via [Load] or [LoadDefault]. A
// *AirportAPI is safe for concurrent use (AC-13): every mutable field is
// guarded by mu, and checkNotCopied rejects a method call on a struct-copied
// value (SEC-020-class, mirroring engine.freight's FreightAPI).
type AirportAPI struct {
	mu sync.RWMutex
	// buildMu serializes Build calls. a.mu is never held across an external
	// seam call (SEC-119 — a seam that re-enters the airport or blocks would
	// otherwise deadlock the non-reentrant RWMutex), so a second lock orders
	// Builds against each other and keeps the "strict upgrade" and
	// "exactly one registered contour" invariants (SEC-116) intact under
	// concurrent Build. Only Build takes buildMu, so the re-entrant reads a
	// seam performs (Tick, RunwayCount, ...) never contend on it.
	buildMu       sync.Mutex
	correlationID string
	cfg           airportConfig

	// Outbound seams (all unset until their modules land — see doc.go):
	// freight is engine.freight's air-cargo modal arm, blight is engine.mining's
	// BlightAPI registration surface, permit is feat.facilitypermits' permit
	// authority, surface is engine.roads'/engine.rail's road/rail spur status
	// read. Each is a dependency-inversion seam this module consumes through,
	// never a local reimplementation of the dependency (GR#20).
	freight AirCargoMover
	blight  BlightRegistrar
	permit  PermitAuthority
	surface SurfaceAccess

	tick       int64
	built      bool
	activeTier string

	self atomic.Pointer[AirportAPI]
}

// AirCargoMover is the dependency-inversion seam for engine.freight (MOD-047,
// done). Air cargo is a NEW modal arm — freight's road/rail/sea modal caps
// (data/freight.json) do not yet model air — so the airport hands air-cargo
// tonnage into the conserved-tonnes identity through this consumer-driven
// seam, exactly as feat.containerport hands sea↔rail↔road transfers through
// its RailIntermodal seam. The freight-side adapter (wired by the composition
// root) implements this against FreightAPI's import/export surface; until
// wired, [AirportAPI.AirCargo] rejects loudly — never a silent drop and never
// an airport-local tonnage ledger (AC-4).
type AirCargoMover interface {
	// AirCargoMove hands `tonnage` of `commodity` into (inbound=true) or out of
	// (inbound=false) the city's conserved-tonnes identity through the airport.
	AirCargoMove(inbound bool, commodity freight.Commodity, tonnage int64) (freight.MovementResult, error)
}

// blightObjectKey is the stable object key under which the airport registers
// its noise contour with the BlightRegistrar seam. It is CONSTANT across every
// tier: an upgrade re-registers this same key with the new tier's class and
// radius, which the seam treats as an atomic replace (SEC-141). Because the key
// never changes, a strict upgrade is a single seam call — there is no
// deregister-then-register sequence whose second step could fail after the
// first already mutated the registrar, stranding a built airport with no
// contour (AC-10). engine.mining's BlightAPI keys off this value: it is the
// airport's identity in the blight registry, not the tier key, so one airport
// is always exactly one blighting object (SEC-116). This is an identity key,
// not a balance figure, so it is a Go constant rather than a data/airport.json
// value (GR#15).
const blightObjectKey = "airport"

// BlightRegistrar is the dependency-inversion seam for engine.mining's
// BlightAPI (MOD-046, open). engine.mining's BlightAPI implements this when
// MOD-046 lands; until then the seam is unset and Build rejects every build
// (an unregistered noise contour must not be silently dropped, AC-7/AC-10).
//
// The seam is a single-method atomic UPSERT (SEC-141): the airport registers
// under one STABLE object key for its whole life, and an upgrade re-registers
// that same key with the new tier's class/radius. Re-registering replaces the
// contour in one call, so Build has no deregister-then-register sequence whose
// second step could fail after the first already mutated the registrar. The
// composition root wiring engine.mining's BlightAPI must satisfy exactly this
// contract — a re-register is a replace, never a duplicate-key error.
type BlightRegistrar interface {
	// RegisterBlightingObject registers (or re-registers) the airport as one of
	// §32's blighting objects under objectKey, with its data-driven blight class
	// and contour radius. Re-registering an already-registered objectKey
	// atomically REPLACES that object's class and radius — an idempotent upsert,
	// never a duplicate-key error and never a stacked second contour. The
	// viewshed/noise effect computation is engine.mining's — never this
	// module's (AC-7).
	RegisterBlightingObject(objectKey string, class mining.BlightClass, contourRadiusM int64) error
}

// PermitAuthority is the dependency-inversion seam for feat.facilitypermits
// (FEAT-053, open). feat.facilitypermits' PermitAPI implements this when that
// feature lands; until then the seam is unset and Build rejects every build as
// unpermitted (AC-9 — permit-gated, never silently buildable).
type PermitAuthority interface {
	// PermitGranted reports whether building tierKey is permitted at the
	// caller's current milestone.
	PermitGranted(tierKey string, milestone int) (bool, error)
}

// SurfaceAccess is the dependency-inversion seam for engine.roads (MOD-024)
// and engine.rail (MOD-060). The airport only READS whether its required road
// and rail spur links exist — the construction of those links is engine.roads'/
// engine.rail's own job, never reimplemented here (AC-8 boundary). Until the
// seams land, nil reports no surface access, which degrades throughput per the
// data-driven factor.
type SurfaceAccess interface {
	// SurfaceAccess reports the current road and rail spur link status.
	SurfaceAccess() (road, rail bool)
}

// SurfaceStatus is the queryable surface-access reading (AC-8): the raw road
// and rail spur status plus the derived Full flag (road present AND, when the
// tier requires a rail spur, rail present).
type SurfaceStatus struct {
	Road bool
	Rail bool
	Full bool
}

// Load reads and validates data/airport.json from dir and returns a ready
// *AirportAPI with every outbound seam unset. correlationID is attached to
// every error this call (and the returned API's methods) construct (GR#1).
// Every failure is a registry-sourced *errs.E — never a panic or a silent
// default substitution.
func Load(dir, correlationID string) (*AirportAPI, error) {
	cfg, err := LoadAirportConfig(filepath.Join(dir, fileAirport), correlationID)
	if err != nil {
		return nil, err
	}

	api := &AirportAPI{
		correlationID: correlationID,
		cfg:           cfg,
	}
	api.self.Store(api) // armed exactly once, before api is returned (SEC-020)
	return api, nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers (boot wiring, tests) that don't already have a resolved data
// directory in hand.
func LoadDefault(correlationID string) (*AirportAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// checkNotCopied rejects a method call on a struct-copied *AirportAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load.
func (a *AirportAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return errs.New(ErrAirportCopiedValue, a.correlationID, map[string]any{"method": method})
	}
	return nil
}

// WireFreight wires the engine.freight air-cargo seam (AC-4). Call before
// AirCargo; nil leaves the arm unregistered (rejected loudly). A call on a
// struct-copied *AirportAPI returns ErrAirportCopiedValue and wires nothing
// (SEC-118 — the copy-guard is applied to every write/wire path, mirroring
// engine.freight's RegisterFirms).
func (a *AirportAPI) WireFreight(f AirCargoMover) error {
	if err := a.checkNotCopied("WireFreight"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.freight = f
	return nil
}

// WireBlight wires the engine.mining blight-registration seam (AC-7). Call
// before Build; nil rejects every build as an unregisterable noise profile.
// A call on a struct-copied *AirportAPI returns ErrAirportCopiedValue and
// wires nothing (SEC-118).
func (a *AirportAPI) WireBlight(b BlightRegistrar) error {
	if err := a.checkNotCopied("WireBlight"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.blight = b
	return nil
}

// WirePermit wires the feat.facilitypermits permit-authority seam (AC-9). Call
// before Build; nil rejects every build as unpermitted. A call on a
// struct-copied *AirportAPI returns ErrAirportCopiedValue and wires nothing
// (SEC-118).
func (a *AirportAPI) WirePermit(p PermitAuthority) error {
	if err := a.checkNotCopied("WirePermit"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.permit = p
	return nil
}

// WireSurface wires the engine.roads/engine.rail surface-access seam (AC-8).
// nil reports no surface access (degraded throughput), never a build failure.
// A call on a struct-copied *AirportAPI returns ErrAirportCopiedValue and
// wires nothing (SEC-118).
func (a *AirportAPI) WireSurface(s SurfaceAccess) error {
	if err := a.checkNotCopied("WireSurface"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.surface = s
	return nil
}

// Tick returns the current daily-tick index.
func (a *AirportAPI) Tick() int64 {
	if err := a.checkNotCopied("Tick"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tick
}

// AdvanceTick runs one daily tick. At this stub-for-baseline depth the
// airport's throughput is a pure function of the built tier and its
// surface-access links (neither changes per tick), so AdvanceTick only
// advances the simulation clock — but it advances by the tick counter, never
// the wall clock (AC-12/GR#21).
func (a *AirportAPI) AdvanceTick() error {
	if err := a.checkNotCopied("AdvanceTick"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tick++
	return nil
}

// Tiers returns the airport-tier ladder in ascending (milestone, cost) order —
// regional_airport → continental_hub → heathrow_class_international_airport
// (AC-2/AC-3). The order is fixed at load time (GR#21), so this is
// deterministic.
func (a *AirportAPI) Tiers() []AirportTier {
	if err := a.checkNotCopied("Tiers"); err != nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]AirportTier, len(a.cfg.tiers))
	copy(out, a.cfg.tiers)
	return out
}

// Tier resolves one ladder rung by key, or errors with ErrUnknownAirport for
// an unregistered key — never a silently-created zero-value tier.
func (a *AirportAPI) Tier(key string) (AirportTier, error) {
	if err := a.checkNotCopied("Tier"); err != nil {
		return AirportTier{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	tier, ok := a.cfg.byKey[key]
	if !ok {
		return AirportTier{}, errs.New(ErrUnknownAirport, a.correlationID, map[string]any{
			"tier": key,
		})
	}
	return tier, nil
}

// ActiveTier returns the currently-built tier key ("" while nothing is built).
func (a *AirportAPI) ActiveTier() string {
	if err := a.checkNotCopied("ActiveTier"); err != nil {
		return ""
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.activeTier
}

// activeTierLocked returns the active tier's config and whether the airport is
// built. The caller holds at least a.mu.RLock.
func (a *AirportAPI) activeTierLocked() (AirportTier, bool) {
	if !a.built {
		return AirportTier{}, false
	}
	return a.cfg.byKey[a.activeTier], true
}

// RunwayCount returns the active airport's runway count — the §MP "4 runways"
// component (AC-1/AC-2). Errors with ErrUnknownAirport while nothing is built
// (AC-10 — never a silently-zeroed component figure).
func (a *AirportAPI) RunwayCount() (int64, error) {
	if err := a.checkNotCopied("RunwayCount"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	tier, built := a.activeTierLocked()
	a.mu.RUnlock()
	if !built {
		return 0, errs.New(ErrUnknownAirport, a.correlationID, map[string]any{"component": "runways"})
	}
	return tier.Runways, nil
}

// TerminalGates returns the active airport's terminal gate count (AC-1/AC-2).
// Errors with ErrUnknownAirport while nothing is built.
func (a *AirportAPI) TerminalGates() (int64, error) {
	if err := a.checkNotCopied("TerminalGates"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	tier, built := a.activeTierLocked()
	a.mu.RUnlock()
	if !built {
		return 0, errs.New(ErrUnknownAirport, a.correlationID, map[string]any{"component": "terminalGates"})
	}
	return tier.TerminalGates, nil
}

// TerminalCapacity returns the active airport's terminal processing capacity
// (gates × pax per gate per day) — one of the two binding components of the
// throughput model (AC-1/AC-2). Errors with ErrUnknownAirport while nothing is
// built.
func (a *AirportAPI) TerminalCapacity() (int64, error) {
	if err := a.checkNotCopied("TerminalCapacity"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	tier, built := a.activeTierLocked()
	a.mu.RUnlock()
	if !built {
		return 0, errs.New(ErrUnknownAirport, a.correlationID, map[string]any{"component": "terminalCapacity"})
	}
	return safeMulNonNeg(tier.TerminalGates, tier.PaxPerGatePerDay), nil
}

// FreightApronCapacity returns the active airport's air-cargo freight-apron
// throughput (tonnes/day) — the §33 air-cargo modal arm's capacity figure
// (AC-1/AC-4). Errors with ErrUnknownAirport while nothing is built.
func (a *AirportAPI) FreightApronCapacity() (int64, error) {
	if err := a.checkNotCopied("FreightApronCapacity"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	tier, built := a.activeTierLocked()
	a.mu.RUnlock()
	if !built {
		return 0, errs.New(ErrUnknownAirport, a.correlationID, map[string]any{"component": "freightApron"})
	}
	return tier.FreightApronTonnesPerDay, nil
}

// PassengerThroughput returns the active airport's passenger throughput
// (pax/day), computed from its components: the binding constraint of
//
//	runwayCapacity  = runways × paxPerRunwayPerDay
//	terminalCapacity = terminalGates × paxPerGatePerDay
//
// then scaled down by the data-driven reduced-throughput percentage when the
// required surface-access spur(s) are absent (AC-2/AC-8). It is a pure
// function of the built tier and the current surface-access reading (GR#21).
// Errors with ErrUnknownAirport while nothing is built.
func (a *AirportAPI) PassengerThroughput() (int64, error) {
	if err := a.checkNotCopied("PassengerThroughput"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	tier, built := a.activeTierLocked()
	surface := a.surface
	a.mu.RUnlock()
	if !built {
		return 0, errs.New(ErrUnknownAirport, a.correlationID, map[string]any{"component": "paxPerDay"})
	}
	full := computePaxPerDay(tier.Runways, tier.PaxPerRunwayPerDay, tier.TerminalGates, tier.PaxPerGatePerDay)
	road, rail := readSurface(surface)
	if fullSurface(tier.RequiresRailSpur, road, rail) {
		return full, nil
	}
	return applySurfaceFactor(full, tier.SurfaceAccessReducedPct), nil
}

// AccessTier returns the active airport's §44 access-tier rung (domestic /
// continental / global), or AccessNone while nothing is built — the
// no-airport floor (AC-5). A tiered airport out-reaches a regional one, and a
// regional one out-reaches no airport.
func (a *AirportAPI) AccessTier() AccessTier {
	if err := a.checkNotCopied("AccessTier"); err != nil {
		return AccessNone
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.built {
		return AccessNone
	}
	return a.cfg.byKey[a.activeTier].AccessTier
}

// ReachMultiplier returns the active airport's §44 tourism-reach multiplier
// (the figure handed to engine.tourism, AC-5), or 0 while nothing is built —
// the lowest reach. The ladder is monotonic non-decreasing with a strict
// increase between adjacent rungs (validated at load).
func (a *AirportAPI) ReachMultiplier() int64 {
	if err := a.checkNotCopied("ReachMultiplier"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.built {
		return 0
	}
	return a.cfg.byKey[a.activeTier].ReachMultiplier
}

// RunwayAccess reports whether the airport offers runway access/adjacency for
// an aerospace prospect (§46, AC-6): true only while a built airport carries
// an international-scale runway (continental or global tier). A domestic-only
// regional airport has no international runway, and no airport has none.
func (a *AirportAPI) RunwayAccess() bool {
	if err := a.checkNotCopied("RunwayAccess"); err != nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.built {
		return false
	}
	return a.cfg.byKey[a.activeTier].AccessTier.isInternationalRunway()
}

// SurfaceAccessStatus returns the queryable surface-access reading (AC-8): the
// raw road/rail spur status and the derived Full flag. Errors with
// ErrUnknownAirport while nothing is built.
func (a *AirportAPI) SurfaceAccessStatus() (SurfaceStatus, error) {
	if err := a.checkNotCopied("SurfaceAccessStatus"); err != nil {
		return SurfaceStatus{}, err
	}
	a.mu.RLock()
	tier, built := a.activeTierLocked()
	surface := a.surface
	a.mu.RUnlock()
	if !built {
		return SurfaceStatus{}, errs.New(ErrUnknownAirport, a.correlationID, map[string]any{"component": "surfaceAccess"})
	}
	road, rail := readSurface(surface)
	return SurfaceStatus{Road: road, Rail: rail, Full: fullSurface(tier.RequiresRailSpur, road, rail)}, nil
}

// AirCargo hands air-cargo tonnage into engine.freight's conserved-tonnes
// identity through the AirCargoMover seam (AC-4). It requires a built airport
// and a wired freight seam, and a positive tonnage — every failure is a
// registry-sourced error and mutates nothing. There is no airport-local
// air-cargo tonnage ledger: the tonnage is handed off, never stored here.
func (a *AirportAPI) AirCargo(inbound bool, commodity freight.Commodity, tonnage int64) (freight.MovementResult, error) {
	if err := a.checkNotCopied("AirCargo"); err != nil {
		return freight.MovementResult{}, err
	}
	a.mu.RLock()
	built := a.built
	f := a.freight
	a.mu.RUnlock()
	if !built {
		return freight.MovementResult{}, errs.New(ErrUnknownAirport, a.correlationID, map[string]any{"component": "airCargo"})
	}
	if f == nil {
		return freight.MovementResult{}, errs.New(ErrAirportBuildRejected, a.correlationID, map[string]any{
			"reason": "air-cargo arm not wired — engine.freight seam unset",
		})
	}
	if tonnage <= 0 {
		return freight.MovementResult{}, errs.New(ErrAirportBuildRejected, a.correlationID, map[string]any{
			"reason":  "air-cargo tonnage must be positive",
			"tonnage": tonnage,
		})
	}
	return f.AirCargoMove(inbound, commodity, tonnage)
}

// Build provisions an airport tier (AC-9/AC-10). It is permit-gated through
// the PermitAuthority seam (feat.facilitypermits, FEAT-053), requires a
// data-sourced land footprint, and registers the airport's noise contour with
// the BlightRegistrar seam (engine.mining's BlightAPI, AC-7) — none of those
// obligations is reimplemented locally, and no permit-state or blight-effect
// field lives on this type. The milestone gate is the tier's data-driven
// Milestone figure, and a second Build must be a STRICT upgrade on the
// (milestone, cost) key — a downgrade or repeat is rejected. Every failure is
// registry-sourced and mutates nothing: built/activeTier are set only after
// every gate passes (AC-10), and the one external mutation (the blight contour
// registration) is a single atomic call, so no failure order leaves the
// registrar half-updated (SEC-141).
//
// Lock discipline (SEC-119): the external seam calls — PermitGranted and
// RegisterBlightingObject — are made with NO lock held, so a seam that
// re-enters the airport (e.g. calls Tick) or blocks on slow I/O cannot
// deadlock the non-reentrant RWMutex. Builds are serialized by buildMu, so the
// "strict upgrade" and "exactly one registered contour" invariants hold under
// concurrent Build; the state snapshot is taken under a.mu.RLock and the final
// commit under a.mu.Lock.
func (a *AirportAPI) Build(tierKey string, milestone int, availableLandHectares int64) error {
	if err := a.checkNotCopied("Build"); err != nil {
		return err
	}
	a.buildMu.Lock()
	defer a.buildMu.Unlock()

	// Snapshot every field and seam the rest of Build needs, under the read
	// lock, then run the seams with no lock held.
	a.mu.RLock()
	tier, ok := a.cfg.byKey[tierKey]
	built := a.built
	priorTier := a.activeTier
	permit := a.permit
	blight := a.blight
	a.mu.RUnlock()

	if !ok {
		return errs.New(ErrUnknownAirport, a.correlationID, map[string]any{
			"tier": tierKey,
		})
	}
	if milestone < tier.Milestone {
		return errs.New(ErrAirportBuildRejected, a.correlationID, map[string]any{
			"tier":              tierKey,
			"milestone":         milestone,
			"requiredMilestone": tier.Milestone,
			"reason":            "below milestone gate",
		})
	}
	// Upgrade guard (AC-10): once a tier is built, a subsequent Build must be a
	// STRICT upgrade on the (milestone, cost) key. A downgrade (or a repeat of
	// the same tier) is rejected before any permit check or blight registration,
	// so it can neither demote activeTier nor register a second blighting object.
	if built {
		if current, currentOK := a.cfg.byKey[priorTier]; currentOK && !tierAbove(tier, current) {
			return errs.New(ErrAirportBuildRejected, a.correlationID, map[string]any{
				"tier":       tierKey,
				"activeTier": priorTier,
				"reason":     "not a strict upgrade — a built airport cannot be downgraded or rebuilt at a lower tier",
			})
		}
	}
	if permit == nil {
		return errs.New(ErrAirportBuildRejected, a.correlationID, map[string]any{
			"tier":   tierKey,
			"reason": "no permit authority wired — permit-gated via feat.facilitypermits",
		})
	}
	granted, err := permit.PermitGranted(tierKey, milestone)
	if err != nil {
		return err
	}
	if !granted {
		return errs.New(ErrAirportBuildRejected, a.correlationID, map[string]any{
			"tier":   tierKey,
			"reason": "permit not granted",
		})
	}
	if availableLandHectares < tier.LandFootprintHectares {
		return errs.New(ErrAirportBuildRejected, a.correlationID, map[string]any{
			"tier":         tierKey,
			"available":    availableLandHectares,
			"requiredLand": tier.LandFootprintHectares,
			"reason":       "insufficient land footprint",
		})
	}
	if blight == nil {
		return errs.New(ErrAirportBuildRejected, a.correlationID, map[string]any{
			"tier":   tierKey,
			"reason": "no blight registrar wired — noise contour cannot be registered",
		})
	}
	// Register — or, on a strict upgrade, atomically REPLACE — the airport's
	// contour under the single stable blightObjectKey (SEC-141). This one call
	// is Build's entire external mutation: because the key is stable and the
	// seam is an idempotent upsert, an upgrade re-registers the same key with
	// the new tier's class/radius in a single call. There is no
	// deregister-then-register sequence, so a register failure cannot strand the
	// registrar with the prior contour already removed while built/activeTier
	// stay put (AC-10). If this call fails, nothing external has changed.
	if err := blight.RegisterBlightingObject(blightObjectKey, tier.BlightClass, tier.ContourRadiusM); err != nil {
		return err
	}

	a.mu.Lock()
	a.built = true
	a.activeTier = tierKey
	a.mu.Unlock()
	return nil
}

// tierAbove reports whether a sorts strictly above b on the (milestone, cost)
// key — the ladder's deterministic ordering (GR#21).
func tierAbove(a, b AirportTier) bool {
	if a.Milestone != b.Milestone {
		return a.Milestone > b.Milestone
	}
	return a.CostMillions > b.CostMillions
}

// computePaxPerDay returns the full (pre-surface-access) passenger throughput:
// the binding constraint of the runway capacity (runways × per-runway pax/day)
// and the terminal capacity (gates × per-gate pax/day) — the documented
// "runway count × per-runway slot rate vs terminal gate capacity" component
// model (§MP, AC-2). Both terms are computed with saturating arithmetic
// (GR#16); the result is a pure function of its inputs (GR#21).
func computePaxPerDay(runways, paxPerRunway, gates, paxPerGate int64) int64 {
	runwayCapacity := safeMulNonNeg(runways, paxPerRunway)
	terminalCapacity := safeMulNonNeg(gates, paxPerGate)
	if runwayCapacity <= terminalCapacity {
		return runwayCapacity
	}
	return terminalCapacity
}

// applySurfaceFactor scales a full-throughput figure down by the data-driven
// reduced-throughput percentage pct (AC-8). pct is in the open interval
// (0, percentFull), validated at load. The product is computed as
// q*percentFull + r so the intermediate full*pct can never overflow: q ≤
// MaxInt64/percentFull and pct < percentFull, so q*pct is always in range,
// and r < percentFull keeps r*pct/percentFull tiny (SEC-117).
func applySurfaceFactor(full, pct int64) int64 {
	if pct >= percentFull {
		return full
	}
	q := full / percentFull
	r := full % percentFull
	return q*pct + r*pct/percentFull
}

// readSurface reads the surface-access seam, treating a nil seam as no surface
// access (an airport without its road/rail links runs degraded, AC-8).
func readSurface(s SurfaceAccess) (road, rail bool) {
	if s == nil {
		return false, false
	}
	return s.SurfaceAccess()
}

// fullSurface reports whether surface access is complete: the road spur must
// be present, and the rail spur must also be present when the tier requires
// one (§MP "its own motorway/rail spurs required", AC-8).
func fullSurface(requiresRailSpur, road, rail bool) bool {
	if !road {
		return false
	}
	return !requiresRailSpur || rail
}

// safeMulNonNeg multiplies two non-negative int64s with saturation (GR#16).
// num.SafeMul already returns the saturated value (math.MaxInt64) when a
// positive product overflows, so the result saturates rather than collapsing
// to a silent 0 (SEC-117). The only case that still yields 0 is a negative
// product, which cannot arise from the load-validated (>0) figures this
// package multiplies — guarded here defensively rather than propagated.
func safeMulNonNeg(a, b int64) int64 {
	v, _ := num.SafeMul(a, b)
	if v < 0 {
		return 0
	}
	return v
}
