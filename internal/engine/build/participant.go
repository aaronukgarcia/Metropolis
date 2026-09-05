package build

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079941 inc3 — engine.build implements the save.Participant
// contract (edge engine.build→int.serializer), mirroring the inc1
// engine.finance pilot and the inc2 engine.unlocks example exactly. It is
// the THIRD engine module to save its state through the per-module
// serialization pattern.
//
// Serialization here is DATA-ONLY, like finance and unlocks: engine.build
// has NO foundation/det RNG at all (no det import — verified by grep), so
// there is no mutable RNG cursor to persist. The reproducible-future
// inputs are (worldSeed, month) [in the save-bundle header]; a lossless
// save is exactly the module's mutable runtime state.
//
// The MUTABLE runtime state this participant persists — every other
// BuildAPI field is either the runtime lock/correlationID, the immutable
// config loaded from data/buildings.json (catalogue, labourPerTick — NOT
// serialized), an injected dependency re-wired by the composition root on
// load (world/season/logistics), or the SEC-020 copy-guard pointer:
//
//   - scalars (a single "build.meta" record): district (the materials-draw
//     district set via SetDistrict), nextOrder (the monotonic build-order
//     id counter — MUST round-trip so ids issued after a load never collide
//     with saved ones), and nextCompletionSeq (BUG-734 F1's monotonic
//     completion-order counter — the SAME round-trip requirement, a
//     separate counter from nextOrder because completion order is not
//     submission order);
//   - one "build.order" record per queue entry, emitted in SLICE order
//     (insertion / order-id order, the queue's own deterministic order —
//     GR#21). Each carries the FULL in-flight construction state: id, world
//     coordinates (tile+local), zone type, the materials ledger
//     (total/remaining/drawn), labourRemaining, leadTimeRemaining, and the
//     complete flag — a lost in-progress build (its drawn materials, its
//     remaining lead time) is exactly the save corruption this guards
//     against;
//   - one "build.zone" record per zoneState entry (SORTED by cell key,
//     GR#21): the cell's world coordinates + assigned zone type;
//   - one "build.structure" record per structures entry (SORTED by cell
//     key, GR#21): the cell's world coordinates + the completing order id it
//     references (a by-VALUE reference to a build.order id — no pointer
//     graph to rebuild); and
//   - one "build.demand" record per demand entry (SORTED by zone type,
//     GR#21): the zone + its constraint signals.
//
// The order/zone/structure records reference world coordinates
// (world.TileCoord, world.CellLocal) and — for structures — a build-order
// id, all carried BY VALUE on the wire, so a load reconstructs the state
// without needing the live engine.world or a pointer-fixup pass.
//
// SaveParticipant does NOT import internal/engine/save: it satisfies
// save.Participant STRUCTURALLY (Kind/Source/Handler), consuming only
// internal/foundation/serialize's Record/RecordSource/RecordHandler
// vocabulary — keeping this package on its single registered
// engine.build→int.serializer edge.

const (
	// KindBuild is this participant's stable shard label. Must be unique
	// across a participant list; save.Load matches it against the shard
	// header's Kind to route the shard back here.
	KindBuild = "build"

	recBuildMeta       = "build.meta"
	recBuildOrder      = "build.order"
	recBuildZone       = "build.zone"
	recBuildStructure  = "build.structure"
	recBuildDemand     = "build.demand"
	recBuildDemolition = "build.demolition"
)

// buildMetaWire carries the BuildAPI's scalar runtime state — every mutable
// field that is not a map or the queue slice. Explicit json tags: the
// domain is never marshalled directly (the field-parity drift test guards
// this).
type buildMetaWire struct {
	District  string       `json:"district"`
	NextOrder BuildOrderID `json:"nextOrder"`
	// NextCompletionSeq is BUG-734 F1's monotonic completion-order counter
	// (buildOrder.completionSeq's source), persisted BESIDE NextOrder —
	// distinct counter, same additive-field precedent this participant
	// already used for BuildingID (FEAT-build-services-bridge-2026-09-02):
	// omitted from a save written before this fix existed, decoding to the
	// zero value on an old bundle, which registerCompletedServicesLocked's
	// backfill pass (its own doc) then repairs deterministically on the
	// next Tick/RegisterCompletedServices call rather than ever installing
	// a wrong non-zero value. MUST round-trip so a completionSeq minted
	// after a load never collides with one minted before it (the exact
	// NextOrder precedent, mirrored for the second counter).
	NextCompletionSeq BuildOrderID `json:"nextCompletionSeq,omitempty"`
	// NextDemolitionSeq is BUG-743's monotonic demolition-order counter
	// (Demolition.DemolitionSeq's source), persisted beside NextOrder/
	// NextCompletionSeq — a THIRD distinct counter, same additive-field,
	// omitempty-on-a-pre-existing-save precedent NextCompletionSeq already
	// established for itself. A save written before this fix existed
	// decodes this to zero, which is correct: no demolition had ever
	// happened yet under that save's history, so zero is the true starting
	// point for the counter, not a backfill case (unlike
	// NextCompletionSeq's legacy-completionSeq-zero ambiguity — a
	// demolition record always carries its own real DemolitionSeq, so there
	// is no "was this ever demolished" ambiguity to repair on load).
	NextDemolitionSeq BuildOrderID `json:"nextDemolitionSeq,omitempty"`
}

// buildOrderWire is one queue entry on the wire — the full mutable
// construction state of a single build order. Emitted in slice order, so no
// key is carried: the queue is a slice and its order IS its identity.
type buildOrderWire struct {
	ID    BuildOrderID    `json:"id"`
	Tile  world.TileCoord `json:"tile"`
	Local world.CellLocal `json:"local"`
	Zone  ZoneType        `json:"zone"`
	// BuildingID is the optional catalogue-entry id this order builds
	// (FEAT-build-services-bridge-2026-09-02) — carried on the wire so a
	// reloaded in-flight order still knows which catalogue building (and
	// therefore which ServiceKind, if any) it completes as; omitted on the
	// wire for the common empty (plain zone order) case.
	BuildingID         string `json:"buildingID,omitempty"`
	MaterialsTotal     int64  `json:"materialsTotal"`
	MaterialsRemaining int64  `json:"materialsRemaining"`
	MaterialsDrawn     int64  `json:"materialsDrawn"`
	LabourRemaining    int64  `json:"labourRemaining"`
	LeadTimeRemaining  int64  `json:"leadTimeRemaining"`
	Complete           bool   `json:"complete"`
	// CompletionSeq is BUG-734 F1's monotonic completion-order stamp (see
	// buildOrder.completionSeq's doc): 0 for a never-completed order (the
	// common in-flight case — omitted on the wire), and for a complete
	// order saved before this fix existed (decodes to 0, backfilled
	// deterministically by registerCompletedServicesLocked rather than
	// ever treated as a real "first ever completion" fact learned from the
	// wire).
	CompletionSeq BuildOrderID `json:"completionSeq,omitempty"`
}

// buildZoneWire is one zoneState entry on the wire: the cell's world
// coordinates (the flattened map key) plus its assigned zone type.
type buildZoneWire struct {
	Tile  world.TileCoord `json:"tile"`
	Local world.CellLocal `json:"local"`
	Zone  ZoneType        `json:"zone"`
}

// buildStructureWire is one structures entry on the wire: the cell's world
// coordinates (the flattened map key) plus the completing build order's id
// it references (carried by value).
type buildStructureWire struct {
	Tile    world.TileCoord `json:"tile"`
	Local   world.CellLocal `json:"local"`
	OrderID BuildOrderID    `json:"orderID"`
}

// buildDemandWire is one demand entry on the wire: the zone type (the map
// key) plus that zone's constraint signals (the DemandInput value).
type buildDemandWire struct {
	Zone           ZoneType `json:"zone"`
	Unfilled       int64    `json:"unfilled"`
	LabourStarved  bool     `json:"labourStarved"`
	PowerStarved   bool     `json:"powerStarved"`
	FreightStarved bool     `json:"freightStarved"`
}

// buildDemolitionWire is one demolitions entry on the wire (BUG-743):
// emitted in the log's own append (DemolitionSeq) order, so — like
// buildOrderWire — no separate sort key is carried; the slice order IS the
// deterministic order.
type buildDemolitionWire struct {
	OrderID       BuildOrderID    `json:"orderID"`
	BuildingID    string          `json:"buildingID,omitempty"`
	DemolitionSeq uint64          `json:"demolitionSeq"`
	Tile          world.TileCoord `json:"tile"`
	Local         world.CellLocal `json:"local"`
}

// buildSnapshot is a point-in-time, deterministically-ordered copy of the
// mutable state, taken under the lock in one shot. Both map-backed
// collections are flattened to slices SORTED by key (GR#21); the queue is
// captured in its own slice order (already deterministic). The emitted
// record order — and therefore the saved bytes — is deterministic.
type buildSnapshot struct {
	meta        buildMetaWire
	orders      []buildOrderWire      // slice (insertion) order
	zones       []buildZoneWire       // sorted by cell key
	structures  []buildStructureWire  // sorted by cell key
	demands     []buildDemandWire     // sorted by ZoneType
	demolitions []buildDemolitionWire // slice (DemolitionSeq / append) order
}

// total is the number of records the snapshot emits: one meta record plus
// one per queue entry, zone entry, structure entry, demand entry, and
// demolition entry.
func (s *buildSnapshot) total() int {
	return 1 + len(s.orders) + len(s.zones) + len(s.structures) + len(s.demands) + len(s.demolitions)
}

// recordAt marshals exactly the i-th record of the deterministic emission
// sequence (meta, orders, zones, structures, demands) — one record's bytes,
// on demand, so Source never materialises the whole encoded shard before
// its first yield.
func (s *buildSnapshot) recordAt(i int) (serialize.Record, error) {
	kind, value := s.locate(i)
	data, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("build: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: data}, nil
}

// locate maps a global record index to its (Kind, wire value) without
// encoding anything — the pure index arithmetic behind recordAt.
func (s *buildSnapshot) locate(i int) (string, any) {
	if i == 0 {
		return recBuildMeta, s.meta
	}
	i--
	if i < len(s.orders) {
		return recBuildOrder, s.orders[i]
	}
	i -= len(s.orders)
	if i < len(s.zones) {
		return recBuildZone, s.zones[i]
	}
	i -= len(s.zones)
	if i < len(s.structures) {
		return recBuildStructure, s.structures[i]
	}
	i -= len(s.structures)
	if i < len(s.demands) {
		return recBuildDemand, s.demands[i]
	}
	i -= len(s.demands)
	return recBuildDemolition, s.demolitions[i]
}

// lessCellKey is the total order over cell keys used to sort the map-backed
// zoneState/structures collections into a deterministic emission order
// (GR#21): by tile.X, then tile.Y, then local.Row, then local.Col.
func lessCellKey(a, b cellKey) bool {
	if a.tile.X != b.tile.X {
		return a.tile.X < b.tile.X
	}
	if a.tile.Y != b.tile.Y {
		return a.tile.Y < b.tile.Y
	}
	if a.local.Row != b.local.Row {
		return a.local.Row < b.local.Row
	}
	return a.local.Col < b.local.Col
}

// snapshotForSave copies the full mutable state into a
// deterministically-ordered buildSnapshot under the read lock. It reads
// everything in one locked pass so the snapshot is internally consistent,
// then releases the lock — Source encodes from the snapshot, not the live
// state.
func (b *BuildAPI) snapshotForSave() (buildSnapshot, error) {
	if err := b.checkNotCopied("snapshotForSave"); err != nil {
		return buildSnapshot{}, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()

	snap := buildSnapshot{
		meta: buildMetaWire{
			District:          b.district,
			NextOrder:         b.nextOrder,
			NextCompletionSeq: b.nextCompletionSeq,
			NextDemolitionSeq: b.nextDemolitionSeq,
		},
	}

	// Queue — in its own slice (insertion) order; the slice order IS the
	// deterministic order, so no sort (GR#21).
	snap.orders = make([]buildOrderWire, 0, len(b.queue))
	for _, o := range b.queue {
		snap.orders = append(snap.orders, buildOrderWire{
			ID:                 o.id,
			Tile:               o.tile,
			Local:              o.local,
			Zone:               o.zone,
			BuildingID:         o.buildingID,
			MaterialsTotal:     o.materialsTotal,
			MaterialsRemaining: o.materialsRemaining,
			MaterialsDrawn:     o.materialsDrawn,
			LabourRemaining:    o.labourRemaining,
			LeadTimeRemaining:  o.leadTimeRemaining,
			Complete:           o.complete,
			CompletionSeq:      o.completionSeq,
		})
	}

	// Zone state — sorted by cell key (GR#21).
	zoneKeys := make([]cellKey, 0, len(b.zoneState))
	for k := range b.zoneState {
		zoneKeys = append(zoneKeys, k)
	}
	sort.Slice(zoneKeys, func(i, j int) bool { return lessCellKey(zoneKeys[i], zoneKeys[j]) })
	snap.zones = make([]buildZoneWire, 0, len(zoneKeys))
	for _, k := range zoneKeys {
		snap.zones = append(snap.zones, buildZoneWire{Tile: k.tile, Local: k.local, Zone: b.zoneState[k]})
	}

	// Structures — sorted by cell key (GR#21).
	structKeys := make([]cellKey, 0, len(b.structures))
	for k := range b.structures {
		structKeys = append(structKeys, k)
	}
	sort.Slice(structKeys, func(i, j int) bool { return lessCellKey(structKeys[i], structKeys[j]) })
	snap.structures = make([]buildStructureWire, 0, len(structKeys))
	for _, k := range structKeys {
		snap.structures = append(snap.structures, buildStructureWire{Tile: k.tile, Local: k.local, OrderID: b.structures[k]})
	}

	// Demand — sorted by zone type (GR#21).
	demandZones := make([]ZoneType, 0, len(b.demand))
	for z := range b.demand {
		demandZones = append(demandZones, z)
	}
	sort.Slice(demandZones, func(i, j int) bool { return demandZones[i] < demandZones[j] })
	snap.demands = make([]buildDemandWire, 0, len(demandZones))
	for _, z := range demandZones {
		in := b.demand[z]
		snap.demands = append(snap.demands, buildDemandWire{
			Zone:           z,
			Unfilled:       in.Unfilled,
			LabourStarved:  in.LabourStarved,
			PowerStarved:   in.PowerStarved,
			FreightStarved: in.FreightStarved,
		})
	}

	// Demolitions — already in strictly-ascending DemolitionSeq (append)
	// order (BUG-743: an append-only log, never a map — see the field's own
	// doc), so no sort here, mirroring the queue's own slice-order emission.
	snap.demolitions = make([]buildDemolitionWire, 0, len(b.demolitions))
	for _, d := range b.demolitions {
		// Demolition and buildDemolitionWire share identical field
		// names/types/order — a direct conversion, not a re-listed struct
		// literal, mirroring staticcheck's S1016 preference; the "no domain
		// type marshalled directly" doc rule (participant.go's package doc)
		// still holds, since buildDemolitionWire is its own distinct type
		// with its own json tags -- only the field COPY is done via
		// conversion instead of naming each field twice.
		snap.demolitions = append(snap.demolitions, buildDemolitionWire(d))
	}

	return snap, nil
}

// resetForLoad clears the mutable state to empty under the write lock,
// before a Load streams records in. A load must REPLACE the state with the
// saved one, so every runtime scalar/map/slice is reset here — Handler then
// rebuilds them one record at a time. The immutable config (catalogue,
// labourPerTick) and the injected dependencies (world/season/logistics) are
// left untouched: they are the same for a given data/buildings.json and are
// re-wired by the composition root, not part of a save. district is reset to
// DefaultDistrict as a valid baseline; the build.meta record (always emitted
// first) overwrites it with the saved value.
func (b *BuildAPI) resetForLoad() error {
	if err := b.checkNotCopied("resetForLoad"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.district = DefaultDistrict
	b.nextOrder = 0
	b.nextCompletionSeq = 0
	b.nextDemolitionSeq = 0
	b.queue = nil
	b.zoneState = make(map[cellKey]ZoneType)
	b.structures = make(map[cellKey]BuildOrderID)
	b.demand = make(map[ZoneType]DemandInput)
	b.demolitions = nil
	// serviceByOrder is NOT part of the save schema (out of scope per
	// FEAT-build-services-bridge-2026-09-02's "Composition root wiring"
	// note — engine.services registration is not itself persisted here),
	// so a load must not carry forward index entries naming orders the
	// freshly-reset queue no longer has.
	b.serviceByOrder = make(map[BuildOrderID]services.ServiceID)
	// BUG-586: a restore can bring back `complete` orders (via
	// applyLoadRecord below) with no serviceByOrder record — serviceByOrder
	// is not itself part of the save schema (see above) — so mark the sweep
	// dirty for the next Tick or explicit RegisterCompletedServices call
	// (compose.Load's post-restore call) to catch.
	b.servicesSweepDirty = true
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect
// directly into the state under the write lock. Installing per record —
// rather than buffering the whole decoded shard and then assigning — keeps
// the load side O(1) per record and streaming, the mirror of Source's
// one-record-at-a-time emission. Returns a decode/kind error verbatim so
// ReadShard fails loud and closed rather than loading a partial state
// silently.
func (b *BuildAPI) applyLoadRecord(rec serialize.Record) error {
	if err := b.checkNotCopied("applyLoadRecord"); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	switch rec.Kind {
	case recBuildMeta:
		var m buildMetaWire
		if err := json.Unmarshal(rec.Data, &m); err != nil {
			return fmt.Errorf("build: decoding %s record: %w", rec.Kind, err)
		}
		b.district = m.District
		b.nextOrder = m.NextOrder
		b.nextCompletionSeq = m.NextCompletionSeq
		b.nextDemolitionSeq = m.NextDemolitionSeq

	case recBuildOrder:
		var w buildOrderWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("build: decoding %s record: %w", rec.Kind, err)
		}
		b.queue = append(b.queue, &buildOrder{
			id:                 w.ID,
			tile:               w.Tile,
			local:              w.Local,
			zone:               w.Zone,
			buildingID:         w.BuildingID,
			materialsTotal:     w.MaterialsTotal,
			materialsRemaining: w.MaterialsRemaining,
			materialsDrawn:     w.MaterialsDrawn,
			labourRemaining:    w.LabourRemaining,
			leadTimeRemaining:  w.LeadTimeRemaining,
			complete:           w.Complete,
			completionSeq:      w.CompletionSeq,
		})

	case recBuildZone:
		var w buildZoneWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("build: decoding %s record: %w", rec.Kind, err)
		}
		b.zoneState[cellKey{tile: w.Tile, local: w.Local}] = w.Zone

	case recBuildStructure:
		var w buildStructureWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("build: decoding %s record: %w", rec.Kind, err)
		}
		b.structures[cellKey{tile: w.Tile, local: w.Local}] = w.OrderID

	case recBuildDemand:
		var w buildDemandWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("build: decoding %s record: %w", rec.Kind, err)
		}
		b.demand[w.Zone] = DemandInput{
			Unfilled:       w.Unfilled,
			LabourStarved:  w.LabourStarved,
			PowerStarved:   w.PowerStarved,
			FreightStarved: w.FreightStarved,
		}

	case recBuildDemolition:
		var w buildDemolitionWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("build: decoding %s record: %w", rec.Kind, err)
		}
		// Direct conversion (staticcheck S1016) — see snapshotForSave's
		// identical note on why this is not "marshalling the domain type
		// directly": buildDemolitionWire remains the distinct wire type
		// with its own json tags, only the field copy uses a conversion.
		b.demolitions = append(b.demolitions, Demolition(w))

	default:
		return fmt.Errorf("build: unknown build save record kind %q", rec.Kind)
	}
	return nil
}

// SaveParticipant adapts a *BuildAPI to the save.Participant contract
// (Kind/Source/Handler) without this package importing engine/save — the
// interface is satisfied structurally. Construct via NewSaveParticipant; the
// wrapped BuildAPI is the live state Source snapshots on save and the target
// Handler rebuilds on load.
type SaveParticipant struct {
	b *BuildAPI
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing b's
// state. On save it snapshots b; on load it resets b's runtime state and
// rebuilds it from the streamed records — so a load target is typically a
// FRESH Load of the same data/buildings.json whose runtime state is replaced
// by the saved one.
func NewSaveParticipant(b *BuildAPI) *SaveParticipant {
	// SEC-020 pre-lock guard (astgate live-tree): a copied BuildAPI is still
	// wrapped so the caller gets a non-nil participant, but every method
	// below re-checks checkNotCopied and fails closed, so a copy can never
	// actually read or mutate the state through this participant.
	_ = b.checkNotCopied("NewSaveParticipant")
	return &SaveParticipant{b: b}
}

// Kind returns the build shard label. The SEC-020 guard mirrors every other
// method that reaches the wrapped candidate type (astgate live-tree): a
// copied BuildAPI yields the empty kind, which save.Load and registry
// validation reject rather than routing a shard to a copy.
func (p *SaveParticipant) Kind() string {
	if err := p.b.checkNotCopied("Kind"); err != nil {
		return ""
	}
	return KindBuild
}

// Source returns a fresh pull-iterator over the build state. It snapshots
// the full mutable state under the lock once, up front, then yields one
// record at a time, marshalling each on demand — never buffering the whole
// encoded shard before the first yield. A copied-value guard failure
// (SEC-020) surfaces on the first pull.
func (p *SaveParticipant) Source() serialize.RecordSource {
	if err := p.b.checkNotCopied("Source"); err != nil {
		return func() (serialize.Record, bool, error) { return serialize.Record{}, false, err }
	}
	snap, snapErr := p.b.snapshotForSave()
	idx := 0
	return func() (serialize.Record, bool, error) {
		if snapErr != nil {
			err := snapErr
			snapErr = nil
			return serialize.Record{}, false, err
		}
		if idx >= snap.total() {
			return serialize.Record{}, false, nil
		}
		rec, err := snap.recordAt(idx)
		if err != nil {
			return serialize.Record{}, false, err
		}
		idx++
		return rec, true, nil
	}
}

// Handler returns a fresh sink that rebuilds the build state from the
// streamed records. It clears the target's runtime state on the first
// record, then installs each record's effect directly under the lock — one
// record at a time, never buffering the whole shard.
func (p *SaveParticipant) Handler() serialize.RecordHandler {
	if err := p.b.checkNotCopied("Handler"); err != nil {
		return func(serialize.Record) error { return err }
	}
	reset := false
	return func(rec serialize.Record) error {
		if !reset {
			if err := p.b.resetForLoad(); err != nil {
				return err
			}
			reset = true
		}
		return p.b.applyLoadRecord(rec)
	}
}
