package freight

import (
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// fileContainerPort is data/containerport.json's filename, relative to the
// resolved data directory (see data.ResolveDataDir).
const fileContainerPort = "containerport.json"

// ContainerPort is the deep-sea container-terminal surface (feat.containerport,
// FEAT-099): the Felixstowe-class tier a rung ABOVE container_terminal in the
// §33 port ladder. It is a distinct surface from FreightAPI's port/chain/
// customs accessors, NOT a fork of it — every capacity/customs/trade figure
// reads THROUGH the *FreightAPI it holds (AC-2/AC-5/AC-6), the intermodal
// transfer point is engine.rail's (consumed through the RailIntermodal seam,
// AC-4), and permit/decommission obligations are feat.facilitypermits' and
// feat.decommission's (consumed through the PermitAuthority/DecommissionRegistrar
// seams, AC-7) — none of that machinery is reimplemented locally.
//
// The zero value is not usable; construct via [LoadContainerPort]. A
// *ContainerPort is safe for concurrent use (AC-12): every mutable field is
// guarded by mu, and checkNotCopied rejects a method call on a struct-copied
// value (SEC-020-class, mirroring FreightAPI).
type ContainerPort struct {
	mu            sync.RWMutex
	correlationID string
	cfg           containerPortConfig
	freight       *FreightAPI

	// Outbound seams (all unset until their modules land — see doc.go):
	// rail is engine.rail's intermodal transfer point, permit is
	// feat.facilitypermits' permit authority, decom is feat.decommission's
	// liability registrar. Each is a dependency-inversion seam this feature
	// consumes through, never a local reimplementation of the dependency.
	rail   RailIntermodal
	permit PermitAuthority
	decom  DecommissionRegistrar

	built      bool
	activeTier string

	self atomic.Pointer[ContainerPort]
}

// RailIntermodal is the dependency-inversion seam for engine.rail (MOD-060,
// open). engine.rail's RailAPI implements this when MOD-060 lands — the
// stub-for-baseline stand-in in internal/engine/rail already does — so the
// deep-sea terminal consumes the sea↔rail↔road intermodal transfer point's
// tonnes-conservation contract (engine.rail.md AC-3) rather than keeping a
// containerport-local parallel transfer ledger (AC-4). Until the seam is
// wired, IntermodalTransfer rejects every call as an unregistered intermodal
// point (AC-8).
type RailIntermodal interface {
	// IntermodalTransfer moves tonnes through the intermodal transfer point
	// from one mode to another, conserving tonnes (in == out + dwell).
	IntermodalTransfer(from, to Mode, tonnes int64) (IntermodalTransferResult, error)
	// IntermodalAccount returns the queryable in/out/dwell conservation
	// account (independently summable, AC-4's false-pass guard).
	IntermodalAccount() IntermodalAccount
}

// PermitAuthority is the dependency-inversion seam for feat.facilitypermits
// (FEAT-053, open). feat.facilitypermits' PermitAPI implements this when that
// feature lands; until then the seam is unset and Build rejects every build as
// unpermitted (AC-7/AC-8 — permit-gated, never silently buildable).
type PermitAuthority interface {
	// PermitGranted reports whether building tierKey is permitted at the
	// caller's current milestone.
	PermitGranted(tierKey string, milestone int) (bool, error)
}

// DecommissionRegistrar is the dependency-inversion seam for feat.decommission
// (FEAT-054, open). feat.decommission's DecommissionAPI implements this when
// that feature lands; until then the seam is unset and Build rejects (a
// day-one liability that cannot be recorded must not be silently dropped,
// AC-7).
type DecommissionRegistrar interface {
	// RegisterLiability records a facility's day-one decommission liability
	// (in micropounds, M0-ENG §1.2).
	RegisterLiability(facilityKey string, costMicropounds int64) error
}

// IntermodalTransferResult is the outcome of one intermodal transfer (AC-4):
// the tonnes accepted into the transfer point and the tonnes delivered out
// (equal for a conserving handoff; any difference is documented, queryable
// dwell).
type IntermodalTransferResult struct {
	Accepted  int64
	Delivered int64
	Dwell     int64
}

// IntermodalAccount is the queryable intermodal conservation account (AC-4):
// per-mode tonnes into, out of, and dwelling at the transfer point, each
// independently summable so a test can prove in == out + dwell without
// trusting a conservation-OK flag.
type IntermodalAccount struct {
	InTonnes    map[Mode]int64
	OutTonnes   map[Mode]int64
	DwellTonnes map[Mode]int64
}

// Unit-conversion constants (NOT capacity figures): £M → £ → micropounds
// (M0-ENG §1.2), used only to hand feat.decommission's seam a canonical
// money figure for the day-one decommission liability.
const (
	poundsPerMillion    = 1_000_000
	micropoundsPerPound = 1_000_000
)

// costMicropounds converts a data-file "costMillions" (£M) figure into
// micropounds via the project's saturating multiplier (GR#16).
func costMicropounds(costMillions int64) int64 {
	pounds, _ := num.SafeMul(costMillions, poundsPerMillion)
	micropounds, _ := num.SafeMul(pounds, micropoundsPerPound)
	return micropounds
}

// LoadContainerPort reads data/containerport.json from dir, validates it, and
// returns a ready *ContainerPort wired to the supplied *FreightAPI (the
// port/customs/trade model this tier extends). correlationID is attached to
// every error this call (and the returned surface's methods) construct
// (GR#1). The rail/permit/decommission seams start unset — wire them with
// WireRail/WirePermit/WireDecommission before IntermodalTransfer/Build.
func LoadContainerPort(dir, correlationID string, freight *FreightAPI) (*ContainerPort, error) {
	cfg, err := LoadContainerPortConfig(filepath.Join(dir, fileContainerPort), correlationID)
	if err != nil {
		return nil, err
	}
	cp := &ContainerPort{
		correlationID: correlationID,
		cfg:           cfg,
		freight:       freight,
	}
	cp.self.Store(cp) // armed exactly once, before cp is returned (SEC-020)
	return cp, nil
}

// checkNotCopied rejects a method call on a struct-copied *ContainerPort
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — so it is safe
// to run before mu is ever touched.
func (c *ContainerPort) checkNotCopied(method string) error {
	if c.self.Load() != c {
		return errs.New(ErrCopiedValue, c.correlationID, map[string]any{"method": method})
	}
	return nil
}

// WireRail wires the engine.rail intermodal seam (AC-4). Call before
// IntermodalTransfer; nil leaves the point unregistered (rejected loudly).
func (c *ContainerPort) WireRail(rail RailIntermodal) {
	if err := c.checkNotCopied("WireRail"); err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rail = rail
}

// WirePermit wires the feat.facilitypermits permit authority seam (AC-7).
// Call before Build; nil rejects every build as unpermitted.
func (c *ContainerPort) WirePermit(permit PermitAuthority) {
	if err := c.checkNotCopied("WirePermit"); err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.permit = permit
}

// WireDecommission wires the feat.decommission liability-registrar seam
// (AC-7). Call before Build; nil rejects every build (a day-one liability
// that cannot be recorded is never silently dropped).
func (c *ContainerPort) WireDecommission(decom DecommissionRegistrar) {
	if err := c.checkNotCopied("WireDecommission"); err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.decom = decom
}

// Tiers returns the port ladder in ascending (milestone, cost) order —
// cargo_port_small → container_terminal → deep_sea_terminal (AC-2). The
// order is fixed at load time (GR#21), so this is deterministic.
func (c *ContainerPort) Tiers() []PortTier {
	if err := c.checkNotCopied("Tiers"); err != nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]PortTier, len(c.cfg.tiers))
	copy(out, c.cfg.tiers)
	return out
}

// Tier resolves one ladder rung by key, or errors with
// ErrContainerPortUnknownTier for an unregistered key (AC-8) — never a
// silently-created zero-value tier.
func (c *ContainerPort) Tier(key string) (PortTier, error) {
	if err := c.checkNotCopied("Tier"); err != nil {
		return PortTier{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	tier, ok := c.cfg.byKey[key]
	if !ok {
		return PortTier{}, errs.New(ErrContainerPortUnknownTier, c.correlationID, map[string]any{
			"tier": key,
		})
	}
	return tier, nil
}

// DeepSeaTier returns the deep-sea tier config — the feature's distinct
// accessor (AC-1), a rung above container_terminal in the ladder.
func (c *ContainerPort) DeepSeaTier() (PortTier, error) {
	if err := c.checkNotCopied("DeepSeaTier"); err != nil {
		return PortTier{}, err
	}
	return c.Tier(c.cfg.deepSeaTier)
}

// TierPhysicalCapacity resolves a tier's §33 physical throughput — berths ×
// crane rate × hours — by calling FreightAPI's own capacity model
// (PortCapacityFor), never a local reimplementation of the formula (AC-2/AC-3).
// Errors with ErrContainerPortUnknownTier for an unregistered key.
func (c *ContainerPort) TierPhysicalCapacity(key string) (PortCapacity, error) {
	if err := c.checkNotCopied("TierPhysicalCapacity"); err != nil {
		return PortCapacity{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	tier, ok := c.cfg.byKey[key]
	if !ok {
		return PortCapacity{}, errs.New(ErrContainerPortUnknownTier, c.correlationID, map[string]any{
			"tier": key,
		})
	}
	return c.freight.PortCapacityFor(tier.Berths, tier.CraneRateTonnesPerHour, tier.OperatingHoursPerDay), nil
}

// PhysicalCapacity returns the deep-sea tier's physical throughput capacity,
// read through FreightAPI's berths × crane rate × hours model (AC-2/AC-3).
func (c *ContainerPort) PhysicalCapacity() (PortCapacity, error) {
	if err := c.checkNotCopied("PhysicalCapacity"); err != nil {
		return PortCapacity{}, err
	}
	return c.TierPhysicalCapacity(c.cfg.deepSeaTier)
}

// CustomsCapacity returns the deep-sea tier's customs throughput capacity — a
// figure SEPARATE from its physical berth/crane throughput, so the two can
// saturate independently (AC-5, reusing engine.freight.md AC-3's model).
func (c *ContainerPort) CustomsCapacity() (CustomsCapacity, error) {
	if err := c.checkNotCopied("CustomsCapacity"); err != nil {
		return CustomsCapacity{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	tier := c.cfg.byKey[c.cfg.deepSeaTier]
	return CustomsCapacity{TonnesPerDay: tier.CustomsCapacityTonnesPerDay}, nil
}

// CustomsSaturation returns the deep-sea tier's customs demand-vs-capacity
// reading: the demand accumulated in FreightAPI (every import/export passes
// customs, §28) against the deep-sea tier's own customs capacity — reusing
// FreightAPI's saturation model rather than a new smuggling model (AC-5).
func (c *ContainerPort) CustomsSaturation() (CustomsSaturation, error) {
	if err := c.checkNotCopied("CustomsSaturation"); err != nil {
		return CustomsSaturation{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	tier := c.cfg.byKey[c.cfg.deepSeaTier]
	base, err := c.freight.CustomsSaturation()
	if err != nil {
		return CustomsSaturation{}, err
	}
	return c.freight.CustomsSaturationFor(base.Demanded, tier.CustomsCapacityTonnesPerDay), nil
}

// SmugglingRisk returns the deep-sea tier's §28 smuggling-risk indicator,
// derived through FreightAPI's own model (SmugglingRiskFor): it rises as the
// deep-sea tier's customs saturation rises (AC-5). The returned float64 is
// the dimensionless [0,1] risk indicator — every tonnage/monetary state stays
// int64 (GR#16).
func (c *ContainerPort) SmugglingRisk() (float64, error) {
	if err := c.checkNotCopied("SmugglingRisk"); err != nil {
		return 0, err
	}
	sat, err := c.CustomsSaturation()
	if err != nil {
		return 0, err
	}
	return c.freight.SmugglingRiskFor(sat), nil
}

// IntermodalTransfer moves tonnes through the intermodal container-transfer
// point (sea↔rail↔road) by consuming engine.rail's registered intermodal
// surface (the RailIntermodal seam, AC-4). Errors with
// ErrContainerPortBuildRejected while the seam is unwired — an unregistered
// intermodal point is rejected, never silently skipped (AC-8).
func (c *ContainerPort) IntermodalTransfer(from, to Mode, tonnes int64) (IntermodalTransferResult, error) {
	if err := c.checkNotCopied("IntermodalTransfer"); err != nil {
		return IntermodalTransferResult{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.rail == nil {
		return IntermodalTransferResult{}, errs.New(ErrContainerPortBuildRejected, c.correlationID, map[string]any{
			"reason": "unregistered intermodal point — engine.rail seam not wired",
		})
	}
	return c.rail.IntermodalTransfer(from, to, tonnes)
}

// IntermodalAccount returns the queryable intermodal conservation account from
// engine.rail's transfer point (AC-4) — independently summable in/out/dwell
// figures, never a local containerport transfer ledger.
func (c *ContainerPort) IntermodalAccount() (IntermodalAccount, error) {
	if err := c.checkNotCopied("IntermodalAccount"); err != nil {
		return IntermodalAccount{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.rail == nil {
		return IntermodalAccount{}, errs.New(ErrContainerPortBuildRejected, c.correlationID, map[string]any{
			"reason": "unregistered intermodal point — engine.rail seam not wired",
		})
	}
	return c.rail.IntermodalAccount(), nil
}

// BalanceOfTrade returns FreightAPI's independently-sourced import/export
// ledgers (engine.freight.md AC-9) — the two totals the importer→exporter
// flip reads off, never one computed as the other's complement (AC-6).
func (c *ContainerPort) BalanceOfTrade() BalanceOfTrade {
	if err := c.checkNotCopied("BalanceOfTrade"); err != nil {
		return BalanceOfTrade{}
	}
	return c.freight.BalanceOfTrade()
}

// Import brings commodity tonnage into the city through the deep-sea terminal
// by delegating to FreightAPI's import command (the flow accrues FreightAPI's
// import ledger and customs demand — no containerport-local ledger, AC-6).
func (c *ContainerPort) Import(commodity Commodity, tonnes int64, mode Mode) (MovementResult, error) {
	if err := c.checkNotCopied("Import"); err != nil {
		return MovementResult{}, err
	}
	return c.freight.Import(commodity, tonnes, mode)
}

// Export records commodity tonnage departing the city through the deep-sea
// terminal by delegating to FreightAPI's export command (AC-6).
func (c *ContainerPort) Export(commodity Commodity, tonnes int64, mode Mode) (MovementResult, error) {
	if err := c.checkNotCopied("Export"); err != nil {
		return MovementResult{}, err
	}
	return c.freight.Export(commodity, tonnes, mode)
}

// ActiveTier returns the currently-built tier key ("" while nothing is built).
func (c *ContainerPort) ActiveTier() string {
	if err := c.checkNotCopied("ActiveTier"); err != nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.activeTier
}

// Build provisions a port tier (AC-7/AC-8). It is permit-gated through the
// PermitAuthority seam (feat.facilitypermits, FEAT-053) and registers a
// day-one decommission liability through the DecommissionRegistrar seam
// (feat.decommission, FEAT-054) — neither obligation is reimplemented
// locally, and no permit-state or liability-provision field lives on this
// type. The milestone gate is the tier's data-driven Milestone figure, and a
// second Build must be a STRICT upgrade on the (milestone, cost) key — a
// downgrade or repeat is rejected (never demotes activeTier, never registers
// a second liability). Every failure is registry-sourced and mutates nothing:
// built/activeTier are set only after every gate passes.
func (c *ContainerPort) Build(tierKey string, milestone int) error {
	if err := c.checkNotCopied("Build"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	tier, ok := c.cfg.byKey[tierKey]
	if !ok {
		return errs.New(ErrContainerPortUnknownTier, c.correlationID, map[string]any{
			"tier": tierKey,
		})
	}
	if milestone < tier.Milestone {
		return errs.New(ErrContainerPortBuildRejected, c.correlationID, map[string]any{
			"tier":              tierKey,
			"milestone":         milestone,
			"requiredMilestone": tier.Milestone,
			"reason":            "below milestone gate",
		})
	}
	// Upgrade guard (AC-7): once a tier is built, a subsequent Build must be a
	// STRICT upgrade on the (milestone, cost) key. A downgrade (or a repeat of
	// the same tier) is rejected before any permit check or liability
	// registration, so it can neither demote activeTier nor register a second
	// day-one decommission liability for the same terminal.
	if c.built {
		if current, ok := c.cfg.byKey[c.activeTier]; ok && !tierAbove(tier, current) {
			return errs.New(ErrContainerPortBuildRejected, c.correlationID, map[string]any{
				"tier":       tierKey,
				"activeTier": c.activeTier,
				"reason":     "not a strict upgrade — a built terminal cannot be downgraded or rebuilt at a lower tier",
			})
		}
	}
	if c.permit == nil {
		return errs.New(ErrContainerPortBuildRejected, c.correlationID, map[string]any{
			"tier":   tierKey,
			"reason": "no permit authority wired — permit-gated via feat.facilitypermits",
		})
	}
	granted, err := c.permit.PermitGranted(tierKey, milestone)
	if err != nil {
		return err
	}
	if !granted {
		return errs.New(ErrContainerPortBuildRejected, c.correlationID, map[string]any{
			"tier":   tierKey,
			"reason": "permit not granted",
		})
	}
	if c.decom == nil {
		return errs.New(ErrContainerPortBuildRejected, c.correlationID, map[string]any{
			"tier":   tierKey,
			"reason": "no decommission registrar wired — day-one liability cannot be recorded",
		})
	}
	if err := c.decom.RegisterLiability(tierKey, costMicropounds(tier.CostMillions)); err != nil {
		return err
	}

	c.built = true
	c.activeTier = tierKey
	return nil
}
