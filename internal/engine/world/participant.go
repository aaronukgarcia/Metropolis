package world

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079941 inc8 — engine.world implements the save.Participant
// contract (edge engine.world→int.serializer), mirroring the inc1
// engine.finance pilot and the inc2..inc7 examples exactly. It is the
// eighth engine module to save its state through the per-module
// serialization pattern.
//
// Serialization here is DATA-ONLY, like every prior inc: engine.world has
// NO foundation/det RNG at all (no det import — verified by grep; the only
// foundation/det MENTIONS in this package are doc-comment prose in
// concurrency_test.go / doc.go / geology.go explaining what this package
// deliberately does NOT use, keyed FNV hashes instead), so there is no
// mutable RNG cursor to persist. The reproducible-future inputs are
// (worldSeed, month) [in the save-bundle header]; a lossless save is
// exactly the module's mutable DURABLE runtime state.
//
// engine.world's state is overwhelmingly DERIVED, and a save must persist
// ONLY the small durable overlay, regenerating everything else on load —
// persisting a derived value is the "built but not serialized"'s mirror
// anti-pattern (stale derived state surviving a rules change). The
// durable-vs-derived split (verified against grid.go / types.go /
// tile_price.go / synth_terrain.go / geology.go / coastline.go):
//
//   DERIVED — NOT serialized, regenerated lazily on first access after
//   load (ensureTile → synthesizeTerrain / populateTerrainFromHeightmap,
//   deriveGeology, classifyLandSea), all PURE functions of TileCoord (+
//   the restored startHeight for the one start tile), never mutated at
//   runtime:
//     - tile.terrain (elevation/slope/surface): regenerated from coord /
//       startHeight;
//     - tile.geology: deriveGeology(coord);
//     - tile.onLand: classifyLandSea(coord);
//     - simGrid.landValue: seeded from terrainQualityFactor(tile) by
//       PurchaseTile / recomputed by re-import (BUG-066) — a pure function
//       of the (regenerated) terrain, so it recomputes to the same value
//       on load; persisting the stale copy would strand it against fresh
//       terrain, exactly BUG-066's silent mismatch;
//     - simGrid.traffic / utility / pollution / decay: OverlayScratch
//       recomputed every tick by the owning network modules (§2.4);
//     - a tile that is in the tiles map only because it was QUERIED
//       (terrain generated, never owned or prospected) is pure derived
//       terrain and is NOT emitted at all — it regenerates identically on
//       first query after load.
//
//   DURABLE — serialized (the small overlay set by user/world commands,
//   NOT derivable):
//     - a single "world.meta" record: startCoord and milestoneTier
//       scalars, plus startHeight (the imported ~200x200 OS heightmap —
//       an EXTERNAL import, not seed-derivable, so it MUST round-trip or
//       the one real start tile's terrain and every terrain-derived value
//       under it would be lost);
//     - one "world.tile" record per OWNED-or-PROSPECTED tile only (the
//       SPARSE overlay — at most TotalTiles=900, and in practice a
//       handful), emitted in DETERMINISTIC coord order (numeric by X then
//       Y — GR#21): its coord, owned flag, tile-level ownerID, prospected
//       flag, and — for an owned tile (simGrid != nil) — the simGrid's
//       DURABLE per-cell columns owner[] / zoning[] / structureRef[]
//       (each set only by ApplyOwnershipCommand / SetStructure, never
//       derived). The DERIVED simGrid columns (landValue + the four
//       overlay scratch bytes) are deliberately absent — they rebuild
//       from the regenerated terrain / by their owning modules.
//
// The restore side cannot go through the public command surface for every
// durable column: PurchaseTile takes no per-cell owner/zoning, there is no
// bulk per-cell setter, and startHeight / startCoord / milestoneTier have
// no public setter at all (startHeight is only ever set by the build-time
// importer via ImportAndPlaceStartTile, which needs a SourceGrid we do not
// persist). So Handler rebuilds through minimal, documented same-package
// restore helpers (resetForLoad / applyMetaRecord / applyTileRecord) that
// mirror ensureTile + PurchaseTile's own seeding exactly — regenerating
// terrain/geology/onLand from the coord, re-seeding landValue from the
// regenerated terrain, and installing the durable columns directly — so
// the reconstructed World is byte-observably the pre-save World for every
// durable field and RECOMPUTES every derived field to the same value.
//
// SaveParticipant does NOT import internal/engine/save: it satisfies
// save.Participant STRUCTURALLY (Kind/Source/Handler), consuming only
// internal/foundation/serialize's Record/RecordSource/RecordHandler
// vocabulary — keeping this package on its single registered
// engine.world→int.serializer edge.

const (
	// KindWorld is this participant's stable shard label. Must be unique
	// across a participant list; save.Load matches it against the shard
	// header's Kind to route the shard back here.
	KindWorld = "world"

	recWorldMeta = "world.meta"
	recWorldTile = "world.tile"
)

// worldMetaWire carries the World's durable scalar/import runtime state —
// the start-tile coordinate, the milestone tier, and the imported
// heightmap. startHeight is the external OS-Terrain import (not
// seed-derivable), deep-copied on snapshot so the wire never aliases the
// live slice. Explicit json tags: the domain is never marshalled directly
// (the field-parity drift test guards this).
type worldMetaWire struct {
	StartCoord    TileCoord   `json:"startCoord"`
	MilestoneTier int         `json:"milestoneTier"`
	StartHeight   [][]float32 `json:"startHeight"`
}

// worldTileWire is one owned-or-prospected tile on the wire: its coord (the
// flattened map key), the tile-level ownership/prospect flags, and — for an
// owned tile — the simGrid's DURABLE per-cell columns. For a
// prospected-but-unowned tile Owned is false and the three column slices
// are nil (there is no simGrid). The DERIVED columns (landValue + overlay
// scratch) are intentionally absent — see the field-parity drift test's
// simGrid allowlist. Column slices are copied defensively on snapshot so
// the wire never aliases the live simGrid arrays.
type worldTileWire struct {
	Coord        TileCoord `json:"coord"`
	Owned        bool      `json:"owned"`
	OwnerID      uint32    `json:"ownerID"`
	Prospected   bool      `json:"prospected"`
	Owner        []uint32  `json:"owner,omitempty"`
	Zoning       []Zoning  `json:"zoning,omitempty"`
	StructureRef []uint32  `json:"structureRef,omitempty"`
}

// worldSnapshot is a point-in-time, deterministically-ordered copy of the
// durable state, taken under the lock in one shot. The tiles map is
// flattened to a slice SORTED by coord (numeric X then Y, GR#21). The
// emitted record order — and therefore the saved bytes — is deterministic.
type worldSnapshot struct {
	meta  worldMetaWire
	tiles []worldTileWire // sorted by coord (numeric X then Y)
}

// total is the number of records the snapshot emits: one meta record plus
// one per owned-or-prospected tile.
func (s *worldSnapshot) total() int {
	return 1 + len(s.tiles)
}

// recordAt marshals exactly the i-th record of the deterministic emission
// sequence (meta, then tiles) — one record's bytes, on demand, so Source
// never materialises the whole encoded shard before its first yield.
func (s *worldSnapshot) recordAt(i int) (serialize.Record, error) {
	kind, value := s.locate(i)
	data, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("world: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: data}, nil
}

// locate maps a global record index to its (Kind, wire value) without
// encoding anything — the pure index arithmetic behind recordAt.
func (s *worldSnapshot) locate(i int) (string, any) {
	if i == 0 {
		return recWorldMeta, s.meta
	}
	return recWorldTile, s.tiles[i-1]
}

// lessTileCoord is the total order over tile coords used to sort the
// tiles collection into a deterministic emission order (GR#21): NUMERIC by
// X, then by Y — never lexical (a lexical order over the ints' string
// forms would sort "10" before "2", a prior finding these incs keep
// catching).
func lessTileCoord(a, b TileCoord) bool {
	if a.X != b.X {
		return a.X < b.X
	}
	return a.Y < b.Y
}

// snapshotForSave copies the full durable state into a
// deterministically-ordered worldSnapshot under the read lock. It reads
// everything in one locked pass so the snapshot is internally consistent,
// then releases the lock — Source encodes from the snapshot, not the live
// state. Only OWNED-or-PROSPECTED tiles are captured; a query-only tile
// (terrain generated but never owned/prospected) is pure derived terrain
// and regenerates identically on load, so it is skipped.
func (w *World) snapshotForSave() (worldSnapshot, error) {
	if err := w.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return worldSnapshot{}, err
	}
	w.mu.RLock()
	defer w.mu.RUnlock()

	snap := worldSnapshot{
		meta: worldMetaWire{
			StartCoord:    w.startCoord,
			MilestoneTier: w.milestoneTier,
			StartHeight:   deepCopyHeightmap(w.startHeight),
		},
	}

	// Tiles — only the durable (owned-or-prospected) overlay, sorted by
	// coord (numeric X then Y, GR#21). Column slices copied so the wire
	// never aliases the live simGrid arrays.
	coords := make([]TileCoord, 0, len(w.tiles))
	for c, tl := range w.tiles {
		if tl.owned || tl.prospected {
			coords = append(coords, c)
		}
	}
	sort.Slice(coords, func(i, j int) bool { return lessTileCoord(coords[i], coords[j]) })
	snap.tiles = make([]worldTileWire, 0, len(coords))
	for _, c := range coords {
		tl := w.tiles[c]
		wire := worldTileWire{
			Coord:      c,
			Owned:      tl.owned,
			OwnerID:    tl.ownerID,
			Prospected: tl.prospected,
		}
		if tl.owned && tl.sim != nil {
			wire.Owner = append([]uint32(nil), tl.sim.owner...)
			wire.Zoning = append([]Zoning(nil), tl.sim.zoning...)
			wire.StructureRef = append([]uint32(nil), tl.sim.structureRef...)
		}
		snap.tiles = append(snap.tiles, wire)
	}

	return snap, nil
}

// deepCopyHeightmap returns an independent copy of a [][]float32 heightmap
// (or nil for a nil input), so a snapshot's wire value never aliases the
// live World.startHeight slice while Source is still streaming.
func deepCopyHeightmap(src [][]float32) [][]float32 {
	if src == nil {
		return nil
	}
	out := make([][]float32, len(src))
	for i, row := range src {
		out[i] = append([]float32(nil), row...)
	}
	return out
}

// resetForLoad clears the mutable DURABLE state under the write lock,
// before a Load streams records in. A load must REPLACE the state with the
// saved one, so every serialized field (tiles, startHeight, milestoneTier)
// is reset here — Handler then rebuilds them: the world.meta record
// (always emitted first) overwrites startCoord/startHeight/milestoneTier,
// and each world.tile record re-establishes one durable tile. The DERIVED
// terrain/geology/onLand caches live on the *tile structs cleared with the
// map, so they too are dropped and regenerate lazily from the restored
// coord/startHeight on first access. The copy-guard (self) is left
// untouched — it is re-armed by nothing here; a copied World is rejected
// pre-lock by checkNotCopied.
func (w *World) resetForLoad() error {
	if err := w.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tiles = make(map[TileCoord]*tile)
	w.startHeight = nil
	w.milestoneTier = 0
	// startCoord is left as-is; the world.meta record (emitted first)
	// overwrites it with the saved value.
	return nil
}

// applyMetaRecord installs the world.meta record's durable scalars/import
// under the write lock: the start-tile coord, the milestone tier, and the
// imported heightmap. It MUST run before any tile record (Source emits meta
// first) so that ensureTile can populate the start tile's terrain from the
// restored startHeight when applyTileRecord regenerates it.
func (w *World) applyMetaRecord(m worldMetaWire) error {
	if err := w.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.startCoord = m.StartCoord
	w.milestoneTier = m.MilestoneTier
	w.startHeight = deepCopyHeightmap(m.StartHeight)
	return nil
}

// applyTileRecord re-establishes one durable tile under the write lock. It
// regenerates the tile's DERIVED terrain/geology/onLand from the coord (and
// the already-restored startHeight, for the start tile) via ensureTile,
// sets the durable ownership/prospect flags, and — for an owned tile —
// allocates a fresh simGrid, re-seeds the DERIVED landValue from the
// regenerated terrain exactly as PurchaseTile does (so it recomputes to the
// saved value), and installs the durable per-cell columns owner/zoning/
// structureRef directly (there is no public bulk per-cell setter, so this
// minimal same-package helper is the restore path). A wrong-length column
// on a corrupted save is rejected loud-and-closed rather than silently
// truncating.
func (w *World) applyTileRecord(rec worldTileWire) error {
	if err := w.checkNotCopied(errs.NewCorrelationID(), &rec.Coord); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	tl, err := w.ensureTile(rec.Coord)
	if err != nil {
		return err
	}
	tl.owned = rec.Owned
	tl.ownerID = rec.OwnerID
	tl.prospected = rec.Prospected
	if rec.Owned {
		if err := checkColumnLen(recWorldTile, "owner", len(rec.Owner)); err != nil {
			return err
		}
		if err := checkColumnLen(recWorldTile, "zoning", len(rec.Zoning)); err != nil {
			return err
		}
		if err := checkColumnLen(recWorldTile, "structureRef", len(rec.StructureRef)); err != nil {
			return err
		}
		sg := newSimGrid()
		// Re-seed landValue from the regenerated terrain, matching
		// PurchaseTile's own seeding (BUG-066): landValue is DERIVED, not
		// saved, so it must be reconstructed from terrain here.
		q := terrainQualityFactor(tl)
		for i := range sg.landValue {
			sg.landValue[i] = float32(q * 1000)
		}
		copy(sg.owner, rec.Owner)
		copy(sg.zoning, rec.Zoning)
		copy(sg.structureRef, rec.StructureRef)
		tl.sim = sg
	} else {
		// A prospected-but-unowned tile has no simGrid; ensure any stale
		// one (there should be none on a fresh reset) is cleared.
		tl.sim = nil
	}
	return nil
}

// checkColumnLen fails a load closed if a restored durable column is not
// exactly CellsPerTile long — a corrupted or truncated save must never
// install a short column that would silently leave later cells at their
// zero (unowned/unzoned/no-structure) value.
func checkColumnLen(kind, col string, n int) error {
	if n != CellsPerTile {
		return fmt.Errorf("world: decoding %s record: %s column has %d cells, want %d", kind, col, n, CellsPerTile)
	}
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect
// directly into the state. Installing per record — rather than buffering
// the whole decoded shard and then assigning — keeps the load side O(1) per
// record and streaming, the mirror of Source's one-record-at-a-time
// emission. Returns a decode/kind error verbatim so ReadShard fails loud
// and closed rather than loading a partial state silently.
func (w *World) applyLoadRecord(rec serialize.Record) error {
	if err := w.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return err
	}
	switch rec.Kind {
	case recWorldMeta:
		var m worldMetaWire
		if err := json.Unmarshal(rec.Data, &m); err != nil {
			return fmt.Errorf("world: decoding %s record: %w", rec.Kind, err)
		}
		return w.applyMetaRecord(m)

	case recWorldTile:
		var t worldTileWire
		if err := json.Unmarshal(rec.Data, &t); err != nil {
			return fmt.Errorf("world: decoding %s record: %w", rec.Kind, err)
		}
		return w.applyTileRecord(t)

	default:
		return fmt.Errorf("world: unknown world save record kind %q", rec.Kind)
	}
}

// SaveParticipant adapts a *World (reached via *WorldAPI, the type the
// composition root holds) to the save.Participant contract
// (Kind/Source/Handler) without this package importing engine/save — the
// interface is satisfied structurally. Construct via NewSaveParticipant;
// the wrapped World is the live state Source snapshots on save and the
// target Handler rebuilds on load.
type SaveParticipant struct {
	w *World
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing the
// state of the World underlying a. On save it snapshots that World; on load
// it resets the World's durable runtime state and rebuilds it from the
// streamed records — so a load target is typically a FRESH NewWorldAPI
// whose empty durable state is replaced by the saved one.
func NewSaveParticipant(a *WorldAPI) *SaveParticipant {
	// SEC copy-guard pre-lock guard (astgate live-tree): a copied World is
	// still wrapped so the caller gets a non-nil participant, but every
	// method below re-checks checkNotCopied and fails closed, so a copy can
	// never actually read or mutate the state through this participant.
	_ = a.w.checkNotCopied(errs.NewCorrelationID(), nil)
	return &SaveParticipant{w: a.w}
}

// Kind returns the world shard label. The copy-guard mirrors every other
// method that reaches the wrapped candidate type (astgate live-tree): a
// copied World yields the empty kind, which save.Load and registry
// validation reject rather than routing a shard to a copy.
func (p *SaveParticipant) Kind() string {
	if err := p.w.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return ""
	}
	return KindWorld
}

// Source returns a fresh pull-iterator over the world state. It snapshots
// the full durable state under the lock once, up front, then yields one
// record at a time, marshalling each on demand — never buffering the whole
// encoded shard before the first yield. A copied-value guard failure
// surfaces on the first pull.
func (p *SaveParticipant) Source() serialize.RecordSource {
	if err := p.w.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return func() (serialize.Record, bool, error) { return serialize.Record{}, false, err }
	}
	snap, snapErr := p.w.snapshotForSave()
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

// Handler returns a fresh sink that rebuilds the world state from the
// streamed records. It clears the target's durable runtime state on the
// first record, then installs each record's effect directly — one record
// at a time, never buffering the whole shard.
func (p *SaveParticipant) Handler() serialize.RecordHandler {
	if err := p.w.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return func(serialize.Record) error { return err }
	}
	reset := false
	return func(rec serialize.Record) error {
		if !reset {
			if err := p.w.resetForLoad(); err != nil {
				return err
			}
			reset = true
		}
		return p.w.applyLoadRecord(rec)
	}
}
