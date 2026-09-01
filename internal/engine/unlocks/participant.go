package unlocks

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079941 inc2 — engine.unlocks implements the save.Participant
// contract (edge engine.unlocks→int.serializer), mirroring the inc1
// engine.finance pilot exactly. It is the SECOND engine module to save
// its state through the per-module serialization pattern.
//
// Serialization here is DATA-ONLY, like finance: engine.unlocks has NO
// foundation/det RNG at all (no det import — verified by grep), so there
// is no mutable RNG cursor to persist. The reproducible-future inputs are
// (worldSeed, month) [in the save-bundle header]; a lossless save is
// exactly the module's mutable runtime state.
//
// The MUTABLE runtime state this participant persists (every other
// UnlocksAPI field is either the runtime lock/correlationID, the
// immutable config indexes loaded from data/unlock_trees.json —
// categories/nodes, which are NOT serialized — an injected dependency, or
// the SEC-020 copy-guard pointer):
//
//   - scalars (a single "unlocks.meta" record): xp, population, the
//     current milestone tier (the atomic.Int32), dp, dpSpent,
//     expansionPermits, debugTouched;
//   - one "unlocks.node" record per unlockedNodes entry (SORTED by node
//     id, GR#21); and
//   - one "unlocks.capacity" record per capacity entry (SORTED by off-map
//     kind, GR#21).
//
// SaveParticipant does NOT import internal/engine/save: it satisfies
// save.Participant STRUCTURALLY (Kind/Source/Handler), consuming only
// internal/foundation/serialize's Record/RecordSource/RecordHandler
// vocabulary — keeping this package on its single registered
// engine.unlocks→int.serializer edge.

const (
	// KindUnlocks is this participant's stable shard label. Must be unique
	// across a participant list; save.Load matches it against the shard
	// header's Kind to route the shard back here.
	KindUnlocks = "unlocks"

	recMeta     = "unlocks.meta"
	recNode     = "unlocks.node"
	recCapacity = "unlocks.capacity"
)

// unlocksMetaWire carries the UnlocksAPI's scalar runtime state — every
// mutable field that is not a map. Tier is the atomic.Int32's current
// value read as a plain int32 (the atomic is a concurrency mechanism, not
// serialized state of its own). Explicit json tags: the domain is never
// marshalled directly (the field-parity drift test guards this).
type unlocksMetaWire struct {
	XP               int64 `json:"xp"`
	Population       int64 `json:"population"`
	Tier             int32 `json:"tier"`
	DP               int64 `json:"dp"`
	DPSpent          int64 `json:"dpSpent"`
	ExpansionPermits int64 `json:"expansionPermits"`
	DebugTouched     bool  `json:"debugTouched"`
}

// unlockedNodeWire is one unlockedNodes entry on the wire: the map key
// (NodeID) plus its bool value. The value is carried (not assumed true)
// so the map round-trips exactly, faithful to the map[string]bool type.
type unlockedNodeWire struct {
	NodeID   string `json:"nodeID"`
	Unlocked bool   `json:"unlocked"`
}

// capacityWire is one capacity entry on the wire: the off-map kind (the
// map key) plus the purchased-tranche count.
type capacityWire struct {
	Kind   OffMapKind `json:"kind"`
	Amount int64      `json:"amount"`
}

// unlocksSnapshot is a point-in-time, deterministically-ordered copy of
// the mutable state, taken under the lock in one shot. Both map-backed
// collections are flattened to slices SORTED by key (GR#21) so the
// emitted record order — and therefore the saved bytes — is deterministic.
type unlocksSnapshot struct {
	meta       unlocksMetaWire
	nodes      []unlockedNodeWire // sorted by NodeID
	capacities []capacityWire     // sorted by Kind
}

// total is the number of records the snapshot emits: one meta record plus
// one per map entry.
func (s *unlocksSnapshot) total() int {
	return 1 + len(s.nodes) + len(s.capacities)
}

// recordAt marshals exactly the i-th record of the deterministic emission
// sequence (meta, nodes, capacities) — one record's bytes, on demand, so
// Source never materialises the whole encoded shard before its first
// yield.
func (s *unlocksSnapshot) recordAt(i int) (serialize.Record, error) {
	kind, value := s.locate(i)
	data, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("unlocks: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: data}, nil
}

// locate maps a global record index to its (Kind, wire value) without
// encoding anything — the pure index arithmetic behind recordAt.
func (s *unlocksSnapshot) locate(i int) (string, any) {
	if i == 0 {
		return recMeta, s.meta
	}
	i--
	if i < len(s.nodes) {
		return recNode, s.nodes[i]
	}
	i -= len(s.nodes)
	return recCapacity, s.capacities[i]
}

// snapshotForSave copies the full mutable state into a
// deterministically-ordered unlocksSnapshot under the read lock. It reads
// everything in one locked pass so the snapshot is internally consistent,
// then releases the lock — Source encodes from the snapshot, not the live
// state.
func (u *UnlocksAPI) snapshotForSave() (unlocksSnapshot, error) {
	if err := u.checkNotCopied("snapshotForSave"); err != nil {
		return unlocksSnapshot{}, err
	}
	u.mu.RLock()
	defer u.mu.RUnlock()

	snap := unlocksSnapshot{
		meta: unlocksMetaWire{
			XP:               u.xp,
			Population:       u.population,
			Tier:             u.tier.Load(),
			DP:               u.dp,
			DPSpent:          u.dpSpent,
			ExpansionPermits: u.expansionPermits,
			DebugTouched:     u.debugTouched,
		},
	}

	// Unlocked nodes — sorted by id (GR#21).
	nodeIDs := make([]string, 0, len(u.unlockedNodes))
	for id := range u.unlockedNodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	snap.nodes = make([]unlockedNodeWire, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		snap.nodes = append(snap.nodes, unlockedNodeWire{NodeID: id, Unlocked: u.unlockedNodes[id]})
	}

	// Off-map capacities — sorted by kind (GR#21).
	capKinds := make([]OffMapKind, 0, len(u.capacity))
	for k := range u.capacity {
		capKinds = append(capKinds, k)
	}
	sort.Slice(capKinds, func(i, j int) bool { return capKinds[i] < capKinds[j] })
	snap.capacities = make([]capacityWire, 0, len(capKinds))
	for _, k := range capKinds {
		snap.capacities = append(snap.capacities, capacityWire{Kind: k, Amount: u.capacity[k]})
	}

	return snap, nil
}

// resetForLoad clears the mutable state to empty under the write lock,
// before a Load streams records in. A load must REPLACE the state with
// the saved one, so every runtime scalar is zeroed and every runtime map
// emptied here — Handler then rebuilds them one record at a time. The
// immutable config indexes (categories/nodes) are left untouched: they
// are the same for a given data/unlock_trees.json and are not part of a
// save.
func (u *UnlocksAPI) resetForLoad() error {
	if err := u.checkNotCopied("resetForLoad"); err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.xp = 0
	u.population = 0
	u.tier.Store(0)
	u.dp = 0
	u.dpSpent = 0
	u.unlockedNodes = make(map[string]bool)
	u.expansionPermits = 0
	u.capacity = make(map[OffMapKind]int64)
	u.debugTouched = false
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect
// directly into the state under the write lock. Installing per record —
// rather than buffering the whole decoded shard and then assigning —
// keeps the load side O(1) per record and streaming, the mirror of
// Source's one-record-at-a-time emission. Returns a decode/kind error
// verbatim so ReadShard fails loud and closed rather than loading a
// partial state silently.
func (u *UnlocksAPI) applyLoadRecord(rec serialize.Record) error {
	if err := u.checkNotCopied("applyLoadRecord"); err != nil {
		return err
	}
	u.mu.Lock()
	defer u.mu.Unlock()

	switch rec.Kind {
	case recMeta:
		var m unlocksMetaWire
		if err := json.Unmarshal(rec.Data, &m); err != nil {
			return fmt.Errorf("unlocks: decoding %s record: %w", rec.Kind, err)
		}
		u.xp = m.XP
		u.population = m.Population
		u.tier.Store(m.Tier)
		u.dp = m.DP
		u.dpSpent = m.DPSpent
		u.expansionPermits = m.ExpansionPermits
		u.debugTouched = m.DebugTouched

	case recNode:
		var w unlockedNodeWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("unlocks: decoding %s record: %w", rec.Kind, err)
		}
		u.unlockedNodes[w.NodeID] = w.Unlocked

	case recCapacity:
		var w capacityWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("unlocks: decoding %s record: %w", rec.Kind, err)
		}
		u.capacity[w.Kind] = w.Amount

	default:
		return fmt.Errorf("unlocks: unknown unlocks save record kind %q", rec.Kind)
	}
	return nil
}

// SaveParticipant adapts a *UnlocksAPI to the save.Participant contract
// (Kind/Source/Handler) without this package importing engine/save — the
// interface is satisfied structurally. Construct via NewSaveParticipant;
// the wrapped UnlocksAPI is the live state Source snapshots on save and
// the target Handler rebuilds on load.
type SaveParticipant struct {
	u *UnlocksAPI
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing
// u's state. On save it snapshots u; on load it resets u's runtime state
// and rebuilds it from the streamed records — so a load target is
// typically a FRESH Load of the same data/unlock_trees.json whose runtime
// state is replaced by the saved one.
func NewSaveParticipant(u *UnlocksAPI) *SaveParticipant {
	// SEC-020 pre-lock guard (astgate live-tree): a copied UnlocksAPI is
	// still wrapped so the caller gets a non-nil participant, but every
	// method below re-checks checkNotCopied and fails closed, so a copy can
	// never actually read or mutate the state through this participant.
	_ = u.checkNotCopied("NewSaveParticipant")
	return &SaveParticipant{u: u}
}

// Kind returns the unlocks shard label. The SEC-020 guard mirrors every
// other method that reaches the wrapped candidate type (astgate
// live-tree): a copied UnlocksAPI yields the empty kind, which save.Load
// and registry validation reject rather than routing a shard to a copy.
func (p *SaveParticipant) Kind() string {
	if err := p.u.checkNotCopied("Kind"); err != nil {
		return ""
	}
	return KindUnlocks
}

// Source returns a fresh pull-iterator over the unlocks state. It
// snapshots the full mutable state under the lock once, up front, then
// yields one record at a time, marshalling each on demand — never
// buffering the whole encoded shard before the first yield. A
// copied-value guard failure (SEC-020) surfaces on the first pull.
func (p *SaveParticipant) Source() serialize.RecordSource {
	if err := p.u.checkNotCopied("Source"); err != nil {
		return func() (serialize.Record, bool, error) { return serialize.Record{}, false, err }
	}
	snap, snapErr := p.u.snapshotForSave()
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

// Handler returns a fresh sink that rebuilds the unlocks state from the
// streamed records. It clears the target's runtime state on the first
// record, then installs each record's effect directly under the lock —
// one record at a time, never buffering the whole shard.
func (p *SaveParticipant) Handler() serialize.RecordHandler {
	if err := p.u.checkNotCopied("Handler"); err != nil {
		return func(serialize.Record) error { return err }
	}
	reset := false
	return func(rec serialize.Record) error {
		if !reset {
			if err := p.u.resetForLoad(); err != nil {
				return err
			}
			reset = true
		}
		return p.u.applyLoadRecord(rec)
	}
}
