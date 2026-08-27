package build

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// DefaultDistrict is the build-queue materials-draw district used when no
// district has been set via SetDistrict. It is a name, not a balance
// number — the district is the (district, commodity) key engine.logistics
// provisions construction-materials stock against (GR#15 concerns numeric
// data only).
const DefaultDistrict = "city"

// daysPerTick is the fixed simulation-time step one build-queue tick
// advances: one simulation day (§13-F3's lead times are expressed in
// simulation days; one tick = one day). It is a time-step convention, not
// a lead-time VALUE — the per-zone lead-time values are data
// (data/buildings.json), never a Go literal (GR#15, AC-8).
const daysPerTick = int64(1)

// BuildOrderID identifies one build order in the visible queue.
type BuildOrderID uint64

// BuildOrderStatus is a build order's derived queue state.
type BuildOrderStatus string

const (
	// OrderPendingMaterials: the order's materials bill has not yet been
	// fully drawn from engine.logistics — it will not complete, regardless
	// of lead time, until it is (AC-4).
	OrderPendingMaterials BuildOrderStatus = "materials-pending"
	// OrderPendingLabour: materials are drawn but the labour requirement is
	// not yet satisfied.
	OrderPendingLabour BuildOrderStatus = "labour-pending"
	// OrderInProgress: materials and labour are done; only lead time remains.
	OrderInProgress BuildOrderStatus = "in-progress"
	// OrderComplete: the order finished and its zone/structure landed.
	OrderComplete BuildOrderStatus = "complete"
)

// cellKey identifies one (tile, local-cell) position — the build module's
// own cell identity for its zone/structure state. world.TileCoord and
// world.CellLocal are comparable structs, so the pair is a valid map key.
type cellKey struct {
	tile  world.TileCoord
	local world.CellLocal
}

// buildOrder is the internal, mutable queue entry. Unexported — the only
// read surface is [BuildAPI.Queue]'s exported BuildOrder snapshot, and no
// consumer can write a queue entry's fields directly (AC-1).
type buildOrder struct {
	id                 BuildOrderID
	tile               world.TileCoord
	local              world.CellLocal
	zone               ZoneType
	materialsTotal     int64 // construction-materials budget (tonnes)
	materialsRemaining int64 // not yet drawn
	materialsDrawn     int64 // cumulative drawn (conservation ledger)
	labourRemaining    int64 // worker-days not yet satisfied
	leadTimeRemaining  int64 // effective lead time (simulation days)
	complete           bool
}

// status derives the order's queue state. Materials-pending takes
// precedence over labour-pending and in-progress: a starved-materials order
// stays materials-pending even after its lead time elapses (AC-4).
func (o *buildOrder) status() BuildOrderStatus {
	if o.complete {
		return OrderComplete
	}
	if o.materialsRemaining > 0 {
		return OrderPendingMaterials
	}
	if o.labourRemaining > 0 {
		return OrderPendingLabour
	}
	return OrderInProgress
}

// snapshot builds the exported, read-only BuildOrder view of an internal
// buildOrder.
func (o *buildOrder) snapshot() BuildOrder {
	return BuildOrder{
		ID:                 o.id,
		Tile:               o.tile,
		Local:              o.local,
		Zone:               o.zone,
		MaterialsBillTotal: o.materialsTotal,
		MaterialsDrawn:     o.materialsDrawn,
		MaterialsRemaining: o.materialsRemaining,
		LabourRemaining:    o.labourRemaining,
		LeadTimeRemaining:  o.leadTimeRemaining,
		Status:             o.status(),
	}
}

// BuildOrder is the read-only snapshot of one queue entry (AC-1: "queue
// visible"). Every field is derived from the internal buildOrder under the
// API's lock — a consumer can never write back through it.
type BuildOrder struct {
	ID                 BuildOrderID
	Tile               world.TileCoord
	Local              world.CellLocal
	Zone               ZoneType
	MaterialsBillTotal int64
	MaterialsDrawn     int64
	MaterialsRemaining int64
	LabourRemaining    int64
	LeadTimeRemaining  int64
	Status             BuildOrderStatus
}

// ZoneCommand zones an owned cell into one of the eight §34 land types.
type ZoneCommand struct {
	Tile    world.TileCoord
	Local   world.CellLocal
	OwnerID uint32
	Zone    ZoneType
}

// BuildCommand submits a build order for an owned cell.
type BuildCommand struct {
	Tile    world.TileCoord
	Local   world.CellLocal
	OwnerID uint32
	Zone    ZoneType
	// Month is the absolute simulation month at submission (0 = genesis),
	// used to source §9's construction-speed multiplier from engine.season.
	Month int64
}

// DemolishCommand demolishes the structure on an owned cell.
type DemolishCommand struct {
	Tile    world.TileCoord
	Local   world.CellLocal
	OwnerID uint32
}

// DemolishResult is SubmitDemolishCommand's return value: the documented
// compensation figure (micro-pounds, sourced from engine.finance's
// LandPrice — never a bare deletion with no financial consequence, AC-7).
type DemolishResult struct {
	Compensation int64
}

// ZoneInfo is one zone type's read-only catalogue record (AC-2/AC-8).
type ZoneInfo struct {
	Zone             ZoneType
	Name             string
	Materials        int64
	Labour           int64
	BaseLeadTimeDays int64
}

// BuildAPI is code.json's "engine.build" inbound contract (GUID
// 6fbc1a41-4d37-4ed5-81dc-3ae5e0ffa0a4): the §34 eight-way zone catalogue,
// the §7 ownership gate, the §13-F3 build queue, and demolition — build
// orders are commands, the queue is visible, and the catalogue is driven
// from data/buildings.json.
//
// The zero value is not usable; construct via [Load] or [LoadDefault]. A
// *BuildAPI is safe for concurrent use (AC-14): every mutable field is
// guarded by mu, and checkNotCopied rejects a method call on a
// struct-copied value (SEC-020-class, mirroring engine.finance's
// FinanceAPI and engine.logistics's LogisticsAPI).
type BuildAPI struct {
	mu            sync.RWMutex
	correlationID string

	catalogue     map[ZoneType]zoneRecord
	labourPerTick int64
	district      string

	zoneState  map[cellKey]ZoneType
	structures map[cellKey]BuildOrderID
	queue      []*buildOrder
	nextOrder  BuildOrderID
	demand     map[ZoneType]DemandInput

	world     *world.WorldAPI
	season    *season.SeasonAPI
	logistics *logistics.LogisticsAPI

	// self is the SEC-020 copy guard, stored exactly once in Load before
	// the value is returned to any caller (mirroring FinanceAPI/LogisticsAPI).
	self atomic.Pointer[BuildAPI]
}

// Load reads and validates data/buildings.json's "zones" array (via
// foundation.data.LoadBuildings) and returns a ready *BuildAPI with an
// empty zone/queue state. correlationID is attached to every error this
// call (and the returned API's methods) construct (GR#1). Every failure is
// a registry-sourced *errs.E — never a silent default substitution, never a
// panic (AC-11). The world/season/logistics dependencies are wired later
// via SetWorld/SetSeason/SetLogistics.
func Load(dir, correlationID string) (*BuildAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	buildings, err := data.LoadBuildings(dir, correlationID)
	if err != nil {
		// MET-G500's registered template has a "{cause}" placeholder —
		// populate it from the wrapped error's own text (BUG-099/BUG-191's
		// shared weakness class) so the rendered message names the real
		// failure instead of leaving the literal "{cause}".
		return nil, errs.Wrap(ErrZoneDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}

	catalogue, err := buildCatalogue(buildings.Zones)
	if err != nil {
		return nil, errs.Wrap(ErrZoneDataInvalid, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}

	api := &BuildAPI{
		correlationID: correlationID,
		catalogue:     catalogue,
		labourPerTick: buildings.ZoneMeta.LabourPerTick,
		district:      DefaultDistrict,
		zoneState:     make(map[cellKey]ZoneType),
		structures:    make(map[cellKey]BuildOrderID),
		demand:        make(map[ZoneType]DemandInput),
		nextOrder:     0,
	}
	// Armed exactly once, before api is returned to any caller (SEC-020).
	api.self.Store(api)
	return api, nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it — the convenience entry point for
// callers (boot wiring, tests) that don't already have a resolved data
// directory in hand.
func LoadDefault(correlationID string) (*BuildAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(dir, correlationID)
}

// checkNotCopied rejects a method call on a struct-copied *BuildAPI
// (SEC-020 family, mirroring engine.finance's FinanceAPI.checkNotCopied).
// Lock-free — a single atomic.Pointer.Load — and therefore safe to run
// before mu is ever touched.
func (b *BuildAPI) checkNotCopied(method string) error {
	if b.self.Load() != b {
		return errs.New(ErrCopiedValue, b.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetWorld wires the engine.world dependency used by the ownership gate
// and the land-price queries.
func (b *BuildAPI) SetWorld(w *world.WorldAPI) error {
	if err := b.checkNotCopied("SetWorld"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.world = w
	return nil
}

// SetSeason wires the engine.season dependency used for §9's construction
// slowdown at build-order submission.
func (b *BuildAPI) SetSeason(s *season.SeasonAPI) error {
	if err := b.checkNotCopied("SetSeason"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.season = s
	return nil
}

// SetLogistics wires the engine.logistics dependency used for the build
// queue's construction-materials draw.
func (b *BuildAPI) SetLogistics(l *logistics.LogisticsAPI) error {
	if err := b.checkNotCopied("SetLogistics"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.logistics = l
	return nil
}

// SetDistrict sets the district the build queue draws construction
// materials from. A non-empty string is required.
func (b *BuildAPI) SetDistrict(d string) error {
	if err := b.checkNotCopied("SetDistrict"); err != nil {
		return err
	}
	if d == "" {
		return errs.New(ErrInvalidDistrict, b.correlationID, map[string]any{"cause": "district must be non-empty"})
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.district = d
	return nil
}

// ZoneTypes returns the loaded §34 zone catalogue's types in ascending
// order — exactly eight for a well-formed catalogue (AC-2).
func (b *BuildAPI) ZoneTypes() []ZoneType {
	if err := b.checkNotCopied("ZoneTypes"); err != nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return sortedZoneTypes(b.catalogue)
}

// ZoneTypeByID resolves a zone type by its catalogue id (e.g. "dwelling",
// "heavy_industry"), reporting whether it is one of the loaded eight types.
func (b *BuildAPI) ZoneTypeByID(id string) (ZoneType, bool) {
	if err := b.checkNotCopied("ZoneTypeByID"); err != nil {
		return "", false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.catalogue[ZoneType(id)]
	return ZoneType(id), ok
}

// ZoneCatalogue returns the loaded zone catalogue in ascending zone-type
// order (AC-2/AC-8): every §34 type with its construction economics.
func (b *BuildAPI) ZoneCatalogue() []ZoneInfo {
	if err := b.checkNotCopied("ZoneCatalogue"); err != nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]ZoneInfo, 0, len(b.catalogue))
	for _, zt := range sortedZoneTypes(b.catalogue) {
		rec := b.catalogue[zt]
		out = append(out, ZoneInfo{
			Zone:             zt,
			Name:             rec.name,
			Materials:        rec.materials,
			Labour:           rec.labour,
			BaseLeadTimeDays: rec.baseLeadTimeDays,
		})
	}
	return out
}

// requireOwned enforces §7's ownership gate (AC-3): the issuing owner must
// own the target tile before any zone/build/demolish command is accepted.
// It validates the cell reference, then reads engine.world's tile-ownership
// state — rejecting with a registry-sourced error and touching no build
// state on failure.
func (b *BuildAPI) requireOwned(tile world.TileCoord, local world.CellLocal, owner uint32, op string) error {
	// Defence-in-depth checkNotCopied (mirroring engine.world's ensureTile):
	// every *WorldAPI-style caller already checks before calling, but this
	// helper re-checks so it can never run against a struct-copied value.
	if err := b.checkNotCopied("requireOwned"); err != nil {
		return err
	}
	if b.world == nil {
		return errs.New(ErrDependencyMissing, b.correlationID, map[string]any{"dependency": "world", "operation": op})
	}
	if !tile.InExtent() || !local.InBounds() {
		return errs.New(ErrCellOutOfBounds, b.correlationID, map[string]any{"tile": tile, "local": local})
	}
	info, err := b.world.TileAt(tile, b.correlationID)
	if err != nil {
		return err
	}
	if !info.Owned || info.OwnerID != owner {
		return errs.New(ErrCellNotOwned, b.correlationID, map[string]any{
			"tile": tile, "local": local, "owner": owner,
		})
	}
	return nil
}

// SubmitZoneCommand zones an owned cell into one of the eight §34 land
// types (AC-1/AC-3). It is a command: the ownership gate runs at acceptance
// time, and a rejection mutates no zone state.
func (b *BuildAPI) SubmitZoneCommand(cmd ZoneCommand) error {
	if err := b.checkNotCopied("SubmitZoneCommand"); err != nil {
		return err
	}
	if _, ok := b.catalogue[cmd.Zone]; !ok {
		return errs.New(ErrUnknownZoneType, b.correlationID, map[string]any{"zone": string(cmd.Zone)})
	}
	if err := b.requireOwned(cmd.Tile, cmd.Local, cmd.OwnerID, "SubmitZoneCommand"); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkNotCopied("SubmitZoneCommand"); err != nil {
		return err
	}
	b.zoneState[cellKey{tile: cmd.Tile, local: cmd.Local}] = cmd.Zone
	return nil
}

// SubmitBuildCommand enqueues a build order for an owned cell (AC-4). The
// order's effective lead time is computed at submission from §9's
// construction-speed multiplier (a live engine.season call — AC-6), so a
// winter order takes measurably longer than an otherwise-identical summer
// order.
func (b *BuildAPI) SubmitBuildCommand(cmd BuildCommand) (BuildOrderID, error) {
	if err := b.checkNotCopied("SubmitBuildCommand"); err != nil {
		return 0, err
	}
	rec, ok := b.catalogue[cmd.Zone]
	if !ok {
		return 0, errs.New(ErrUnknownZoneType, b.correlationID, map[string]any{"zone": string(cmd.Zone)})
	}
	if cmd.Month < 0 {
		return 0, errs.New(ErrInvalidMonth, b.correlationID, map[string]any{"month": cmd.Month})
	}
	if err := b.requireOwned(cmd.Tile, cmd.Local, cmd.OwnerID, "SubmitBuildCommand"); err != nil {
		return 0, err
	}
	if b.season == nil {
		return 0, errs.New(ErrDependencyMissing, b.correlationID, map[string]any{"dependency": "season", "operation": "SubmitBuildCommand"})
	}
	mult, err := b.season.ConstructionSpeedMultiplier(cmd.Month)
	if err != nil {
		return 0, err
	}
	if !(mult > 0) || math.IsNaN(mult) || math.IsInf(mult, 0) {
		return 0, errs.New(ErrInvalidSeasonalMultiplier, b.correlationID, map[string]any{
			"multiplier": mult, "month": cmd.Month,
		})
	}
	leadTime := effectiveLeadTime(rec.baseLeadTimeDays, mult)

	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkNotCopied("SubmitBuildCommand"); err != nil {
		return 0, err
	}
	b.nextOrder++
	order := &buildOrder{
		id:                 b.nextOrder,
		tile:               cmd.Tile,
		local:              cmd.Local,
		zone:               cmd.Zone,
		materialsTotal:     rec.materials,
		materialsRemaining: rec.materials,
		labourRemaining:    rec.labour,
		leadTimeRemaining:  leadTime,
	}
	b.queue = append(b.queue, order)
	return order.id, nil
}

// SubmitDemolishCommand demolishes the structure on an owned cell (AC-7):
// it is a distinct command from zoning/building, clears the cell's zone and
// structure, and returns a compensation figure sourced from engine.finance's
// LandPrice — never a bare deletion with no financial consequence.
func (b *BuildAPI) SubmitDemolishCommand(cmd DemolishCommand) (DemolishResult, error) {
	if err := b.checkNotCopied("SubmitDemolishCommand"); err != nil {
		return DemolishResult{}, err
	}
	if err := b.requireOwned(cmd.Tile, cmd.Local, cmd.OwnerID, "SubmitDemolishCommand"); err != nil {
		return DemolishResult{}, err
	}
	compensation, err := b.landPriceFor(cmd.Tile, cmd.Local)
	if err != nil {
		return DemolishResult{}, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkNotCopied("SubmitDemolishCommand"); err != nil {
		return DemolishResult{}, err
	}
	key := cellKey{tile: cmd.Tile, local: cmd.Local}
	if _, ok := b.structures[key]; !ok {
		return DemolishResult{}, errs.New(ErrNoStructure, b.correlationID, map[string]any{
			"tile": cmd.Tile, "local": cmd.Local,
		})
	}
	delete(b.zoneState, key)
	delete(b.structures, key)
	return DemolishResult{Compensation: compensation}, nil
}

// PurchasePrice returns a cell's purchase price in micro-pounds, sourced
// from engine.finance's finance.LandPrice (AC-9) — the figure a purchase UI
// displays/validates. engine.build never computes §7's land-price formula
// itself.
func (b *BuildAPI) PurchasePrice(tile world.TileCoord, local world.CellLocal) (int64, error) {
	if err := b.checkNotCopied("PurchasePrice"); err != nil {
		return 0, err
	}
	if b.world == nil {
		return 0, errs.New(ErrDependencyMissing, b.correlationID, map[string]any{"dependency": "world", "operation": "PurchasePrice"})
	}
	if !tile.InExtent() || !local.InBounds() {
		return 0, errs.New(ErrCellOutOfBounds, b.correlationID, map[string]any{"tile": tile, "local": local})
	}
	return b.landPriceFor(tile, local)
}

// landPriceFor maps a cell's terrain into finance's LandCell vocabulary and
// returns finance.LandPrice — the single place this package consumes
// engine.finance's pricing (AC-9). The non-terrain factors (junction, roads,
// services, coast view, pollution, city size) are left at their zero values:
// at Baseline One those modules are not yet wired, so LandPrice resolves to
// the terrain base price (a documented placeholder, GR#15 — the real factors
// are the composition root's later wiring, not a build-local re-derivation).
func (b *BuildAPI) landPriceFor(tile world.TileCoord, local world.CellLocal) (int64, error) {
	// Defence-in-depth checkNotCopied (mirroring engine.world's ensureTile):
	// the exported callers already check before calling, but this helper
	// re-checks so it can never run against a struct-copied value.
	if err := b.checkNotCopied("landPriceFor"); err != nil {
		return 0, err
	}
	cell, err := b.world.CellAt(tile, local, b.correlationID)
	if err != nil {
		return 0, err
	}
	lc := finance.LandCell{Terrain: terrainKindFor(cell.Surface)}
	return int64(finance.LandPrice(lc)), nil
}

// terrainKindFor maps engine.world's Surface onto finance's own TerrainKind
// vocabulary (GR#20: this package reaches finance through its public types,
// never re-deriving the mapping finance itself owns).
func terrainKindFor(s world.Surface) finance.TerrainKind {
	switch s {
	case world.SurfaceWoodland:
		return finance.TerrainWoodland
	case world.SurfaceShingle:
		return finance.TerrainShingle
	case world.SurfaceRock:
		return finance.TerrainRock
	case world.SurfaceWater:
		return finance.TerrainWater
	default:
		return finance.TerrainGrass
	}
}

// Tick advances the build queue one simulation day (AC-4/AC-12). For every
// non-complete order, in insertion order: (1) draw its remaining
// construction-materials bill from engine.logistics; (2) apply the
// data-driven labour gate; (3) elapse one day of lead time; (4) complete
// the order — landing its zone/structure on the cell — only when all three
// requirements are met. The tick is deterministic: it iterates the queue in
// order and mutates nothing else (GR#21).
func (b *BuildAPI) Tick(month int64) error {
	if err := b.checkNotCopied("Tick"); err != nil {
		return err
	}
	if month < 0 {
		return errs.New(ErrInvalidMonth, b.correlationID, map[string]any{"month": month})
	}
	if b.logistics == nil {
		return errs.New(ErrDependencyMissing, b.correlationID, map[string]any{"dependency": "logistics", "operation": "Tick"})
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkNotCopied("Tick"); err != nil {
		return err
	}

	for _, order := range b.queue {
		if order.complete {
			continue
		}

		// (1) Materials: draw through engine.logistics's shared Stock/Draw
		// mechanism — no bespoke materials-only path (AC-4). A shortfall
		// leaves MaterialsRemaining > 0, keeping the order materials-pending.
		if order.materialsRemaining > 0 {
			dr, err := b.logistics.Draw(b.district, market.ConstructionMaterials, order.materialsRemaining, logistics.ConsumerConstruction)
			if err != nil {
				return err
			}
			drawn := dr.Fulfilled
			if drawn > order.materialsRemaining {
				drawn = order.materialsRemaining // defensive clamp (Draw never over-fills, but never trust the boundary)
			}
			order.materialsRemaining = num.SatSub(order.materialsRemaining, drawn)
			order.materialsDrawn = num.SatAdd(order.materialsDrawn, drawn)
		}

		// (2) Labour: apply the data-driven placeholder gate (worker-days per
		// tick, from data/buildings.json's meta.labourPerTick).
		if order.labourRemaining > 0 {
			applied := b.labourPerTick
			if applied > order.labourRemaining {
				applied = order.labourRemaining
			}
			order.labourRemaining = num.SatSub(order.labourRemaining, applied)
		}

		// (3) Lead time: one simulation day elapses.
		if order.leadTimeRemaining > 0 {
			order.leadTimeRemaining = num.SatSub(order.leadTimeRemaining, daysPerTick)
		}

		// (4) Completion: all three requirements met.
		if order.materialsRemaining == 0 && order.labourRemaining == 0 && order.leadTimeRemaining == 0 {
			order.complete = true
			key := cellKey{tile: order.tile, local: order.local}
			b.zoneState[key] = order.zone
			b.structures[key] = order.id
			// Sync the structure reference to world.structureRef so the viewport publishes it
			if err := b.world.SetStructure(order.tile, order.local, uint32(order.id), b.correlationID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Queue returns a read-only snapshot of the build queue in insertion (order
// ID) order (AC-1: "queue visible").
func (b *BuildAPI) Queue() []BuildOrder {
	if err := b.checkNotCopied("Queue"); err != nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]BuildOrder, 0, len(b.queue))
	for _, o := range b.queue {
		out = append(out, o.snapshot())
	}
	return out
}

// ReportDemand sets the constraint signals feeding one zone's demand bar
// (US-2/AC-5), populated by the composition root from the modules that own
// each signal. engine.build derives the reason codes but does not compute
// the underlying network state itself (out of scope).
func (b *BuildAPI) ReportDemand(zone ZoneType, in DemandInput) error {
	if err := b.checkNotCopied("ReportDemand"); err != nil {
		return err
	}
	if _, ok := b.catalogue[zone]; !ok {
		return errs.New(ErrUnknownZoneType, b.correlationID, map[string]any{"zone": string(zone)})
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.demand[zone] = in
	return nil
}

// Demand returns a zone's self-explaining demand bar: the unfilled
// magnitude alongside the ordered reason codes explaining why it is
// unfilled (AC-5) — never a bare, mute number.
func (b *BuildAPI) Demand(zone ZoneType) (DemandBar, error) {
	if err := b.checkNotCopied("Demand"); err != nil {
		return DemandBar{}, err
	}
	if _, ok := b.catalogue[zone]; !ok {
		return DemandBar{}, errs.New(ErrUnknownZoneType, b.correlationID, map[string]any{"zone": string(zone)})
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	in := b.demand[zone]
	return DemandBar{Zone: zone, Unfilled: in.Unfilled, Reasons: reasonsFor(in)}, nil
}

// ZoneState returns a cell's currently-assigned zone type, and whether a
// zone has been assigned (US-1). A read-only query over the build module's
// own zone state.
func (b *BuildAPI) ZoneState(tile world.TileCoord, local world.CellLocal) (ZoneType, bool) {
	if err := b.checkNotCopied("ZoneState"); err != nil {
		return "", false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	zt, ok := b.zoneState[cellKey{tile: tile, local: local}]
	return zt, ok
}

// Structure returns a cell's structure reference (the completing build
// order's ID), and whether the cell carries a structure (US-6).
func (b *BuildAPI) Structure(tile world.TileCoord, local world.CellLocal) (BuildOrderID, bool) {
	if err := b.checkNotCopied("Structure"); err != nil {
		return 0, false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	id, ok := b.structures[cellKey{tile: tile, local: local}]
	return id, ok
}
