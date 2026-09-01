package traffic

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079941 inc6 — engine.traffic implements the save.Participant
// contract (edge engine.traffic→int.serializer), mirroring the inc1
// engine.finance pilot, the inc2 engine.unlocks example, the inc3
// engine.build example, the inc4 engine.market example, and the inc5
// engine.refuse example exactly. It is the SIXTH engine module to save its
// state through the per-module serialization pattern.
//
// Serialization here is DATA-ONLY, like finance/unlocks/build/market/refuse:
// engine.traffic has NO foundation/det RNG at all (no det import — verified by
// grep; the only sync.* references are the RWMutex guarding the mutable state
// and the atomic.Pointer copy-guard), so there is no mutable RNG cursor to
// persist. The reproducible-future inputs are (worldSeed, month) [in the
// save-bundle header]; a lossless save is exactly the module's mutable runtime
// state.
//
// # Durable-vs-derived analysis (the highest-value decision)
//
// engine.traffic ships the Baseline One coarse layer plus the Stage 1 network
// primitives — crucially it ships NO assignment loop (SUE is deferred, see
// doc.go), so there is NO derived-per-tick flow field recomputed from the
// network + demand. Every mutable field is therefore genuine DURABLE input
// state, not a computed cache:
//
//   - demands (map[uint64]int64): DURABLE. Written only by the user-command
//     surface (AddDemand / AddTripDemand / RegisterTrip, via addDemandLocked)
//     and cleared wholesale at the day boundary by AdvanceTick. It is NOT
//     recomputed from other serialized state — nothing can reconstruct the
//     demand accumulated so-far-today except the sequence of commands that
//     produced it. A mid-day save must preserve it, or the coarse
//     demandMultiplier (and therefore CommuteHours/Minutes/AccessMinutes)
//     silently resets to the empty-day value on load. The day-boundary WIPE is
//     a scheduled reset, NOT a per-tick recompute-from-other-state, so this is
//     NOT the refuse `provisioned`-cache class — it is serialized.
//   - nodes (map[uint64]*Node): DURABLE. Network topology, built by AddNode;
//     never recomputed. Node is ID-only today (junction model deferred).
//   - links (map[uint64]*Link): DURABLE. Network topology (ID/Start/End/
//     Length built by AddLink) PLUS the accumulated Link.Volume loaded by
//     AddLinkVolume. Volume is NOT reset by AdvanceTick (only demands is) and
//     is NOT recomputed from demands (no assignment loop exists to derive it),
//     so it is durable accumulated state that must round-trip — a lost Volume
//     would change every LinkTravelTime BPR result after a load.
//
// The EXCLUDED fields, each with its reason (the field-parity drift test
// enforces this allowlist):
//
//   - mu: runtime lock, not state.
//   - self: SEC-020 copy-guard atomic pointer, re-armed by New/Load.
//   - roads: injected dependency (engine.roads), re-wired by the composition
//     root via SetRoads on load — not part of a save.
//   - cfg: immutable config loaded from data/traffic.json (a save must not pin
//     old rules — FEAT-1972079897); re-seeded by New() defaults + LoadConfig.
//   - correlationID: per-instance error correlation, not simulation state.
//
// There are ZERO durable scalar fields, so — unlike refuse — this participant
// emits NO "traffic.meta" record; only the three map-backed collections
// produce records. Every wire projection carries explicit json tags: the
// domain structs (Node / Link) are never marshalled directly (the field-parity
// drift tests guard this).
//
// SaveParticipant does NOT import internal/engine/save: it satisfies
// save.Participant STRUCTURALLY (Kind/Source/Handler), consuming only
// internal/foundation/serialize's Record/RecordSource/RecordHandler vocabulary
// — keeping this package on its single registered
// engine.traffic→int.serializer edge.

const (
	// KindTraffic is this participant's stable shard label. Must be unique
	// across a participant list; save.Load matches it against the shard
	// header's Kind to route the shard back here.
	KindTraffic = "traffic"

	recTrafficDemand = "traffic.demand"
	recTrafficNode   = "traffic.node"
	recTrafficLink   = "traffic.link"
)

// trafficDemandWire is one demands entry on the wire: the destination ID (the
// flattened map key) plus the accumulated citizen-count demand. Explicit json
// tags: the map is never marshalled directly (the field-parity drift test
// guards this).
type trafficDemandWire struct {
	ID    uint64 `json:"id"`
	Count int64  `json:"count"`
}

// trafficNodeWire is one nodes entry on the wire: the node's full mutable state
// (ID-only today; the junction model is deferred, see doc.go).
type trafficNodeWire struct {
	ID uint64 `json:"id"`
}

// trafficLinkWire is one links entry on the wire: the link's full mutable
// state. Volume is the accumulated durable load (AddLinkVolume, never reset by
// AdvanceTick and never recomputed — no assignment loop exists), so it MUST
// round-trip or every post-load LinkTravelTime BPR result would change.
type trafficLinkWire struct {
	ID     uint64  `json:"id"`
	Start  uint64  `json:"start"`
	End    uint64  `json:"end"`
	Length float64 `json:"length"`
	Volume float64 `json:"volume"`
}

// trafficSnapshot is a point-in-time, deterministically-ordered copy of the
// mutable state, taken under the lock in one shot. Every map-backed collection
// is flattened to a slice SORTED by its uint64 key, numerically (GR#21). The
// emitted record order — and therefore the saved bytes — is deterministic.
type trafficSnapshot struct {
	demands []trafficDemandWire // sorted by destination ID
	nodes   []trafficNodeWire   // sorted by node ID
	links   []trafficLinkWire   // sorted by link ID
}

// total is the number of records the snapshot emits: one per demand, node, and
// link entry. There is no meta record (no durable scalar state).
func (s *trafficSnapshot) total() int {
	return len(s.demands) + len(s.nodes) + len(s.links)
}

// recordAt marshals exactly the i-th record of the deterministic emission
// sequence (demands, nodes, links) — one record's bytes, on demand, so Source
// never materialises the whole encoded shard before its first yield.
func (s *trafficSnapshot) recordAt(i int) (serialize.Record, error) {
	kind, value := s.locate(i)
	data, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("traffic: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: data}, nil
}

// locate maps a global record index to its (Kind, wire value) without encoding
// anything — the pure index arithmetic behind recordAt.
func (s *trafficSnapshot) locate(i int) (string, any) {
	if i < len(s.demands) {
		return recTrafficDemand, s.demands[i]
	}
	i -= len(s.demands)
	if i < len(s.nodes) {
		return recTrafficNode, s.nodes[i]
	}
	i -= len(s.nodes)
	return recTrafficLink, s.links[i]
}

// sortedUint64Keys returns m's keys sorted numerically (GR#21) — the single
// deterministic-ordering helper every collection flattens through, so a demand
// ID of 2 never sorts after 10 the way a lexical string sort would.
func sortedUint64Keys[V any](m map[uint64]V) []uint64 {
	keys := make([]uint64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// snapshotForSave copies the full mutable state into a
// deterministically-ordered trafficSnapshot under the read lock. It reads
// everything in one locked pass so the snapshot is internally consistent, then
// releases the lock — Source encodes from the snapshot, not the live state.
func (t *TrafficAPI) snapshotForSave() (trafficSnapshot, error) {
	if err := t.checkNotCopied("snapshotForSave"); err != nil {
		return trafficSnapshot{}, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	var snap trafficSnapshot

	// Demands — sorted by destination ID, numerically (GR#21).
	demandIDs := sortedUint64Keys(t.demands)
	snap.demands = make([]trafficDemandWire, 0, len(demandIDs))
	for _, id := range demandIDs {
		snap.demands = append(snap.demands, trafficDemandWire{ID: id, Count: t.demands[id]})
	}

	// Nodes — sorted by node ID, numerically (GR#21).
	nodeIDs := sortedUint64Keys(t.nodes)
	snap.nodes = make([]trafficNodeWire, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		snap.nodes = append(snap.nodes, trafficNodeWire{ID: t.nodes[id].ID})
	}

	// Links — sorted by link ID, numerically (GR#21). Each *Link is copied by
	// value into the wire projection, so the wire never aliases the live Link.
	linkIDs := sortedUint64Keys(t.links)
	snap.links = make([]trafficLinkWire, 0, len(linkIDs))
	for _, id := range linkIDs {
		l := t.links[id]
		snap.links = append(snap.links, trafficLinkWire{
			ID:     l.ID,
			Start:  l.Start,
			End:    l.End,
			Length: l.Length,
			Volume: l.Volume,
		})
	}

	return snap, nil
}

// resetForLoad clears the mutable state to empty under the write lock, before a
// Load streams records in. A load must REPLACE the state with the saved one, so
// every serialized runtime map is reset here — Handler then rebuilds them one
// record at a time. The immutable config (cfg), the injected dependency
// (roads), the per-instance correlationID, and the copy-guard (self) are left
// untouched: cfg/roads are re-seeded/re-wired by the composition root and are
// not part of a save.
func (t *TrafficAPI) resetForLoad() error {
	if err := t.checkNotCopied("resetForLoad"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.demands = make(map[uint64]int64)
	t.nodes = make(map[uint64]*Node)
	t.links = make(map[uint64]*Link)
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect directly
// into the state under the write lock. Installing per record — rather than
// buffering the whole decoded shard and then assigning — keeps the load side
// O(1) per record and streaming, the mirror of Source's one-record-at-a-time
// emission. Returns a decode/kind error verbatim so ReadShard fails loud and
// closed rather than loading a partial state silently.
func (t *TrafficAPI) applyLoadRecord(rec serialize.Record) error {
	if err := t.checkNotCopied("applyLoadRecord"); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	switch rec.Kind {
	case recTrafficDemand:
		var w trafficDemandWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("traffic: decoding %s record: %w", rec.Kind, err)
		}
		t.demands[w.ID] = w.Count

	case recTrafficNode:
		var w trafficNodeWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("traffic: decoding %s record: %w", rec.Kind, err)
		}
		t.nodes[w.ID] = &Node{ID: w.ID}

	case recTrafficLink:
		var w trafficLinkWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("traffic: decoding %s record: %w", rec.Kind, err)
		}
		t.links[w.ID] = &Link{
			ID:     w.ID,
			Start:  w.Start,
			End:    w.End,
			Length: w.Length,
			Volume: w.Volume,
		}

	default:
		return fmt.Errorf("traffic: unknown traffic save record kind %q", rec.Kind)
	}
	return nil
}

// SaveParticipant adapts a *TrafficAPI to the save.Participant contract
// (Kind/Source/Handler) without this package importing engine/save — the
// interface is satisfied structurally. Construct via NewSaveParticipant; the
// wrapped TrafficAPI is the live state Source snapshots on save and the target
// Handler rebuilds on load.
type SaveParticipant struct {
	t *TrafficAPI
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing t's
// state. On save it snapshots t; on load it resets t's runtime state and
// rebuilds it from the streamed records — so a load target is typically a FRESH
// New (same data/traffic.json cfg) whose runtime state is replaced by the saved
// one (then re-wired by the composition root via SetRoads).
func NewSaveParticipant(t *TrafficAPI) *SaveParticipant {
	// SEC-020 pre-lock guard (astgate live-tree): a copied TrafficAPI is still
	// wrapped so the caller gets a non-nil participant, but every method below
	// re-checks checkNotCopied and fails closed, so a copy can never actually
	// read or mutate the state through this participant.
	_ = t.checkNotCopied("NewSaveParticipant")
	return &SaveParticipant{t: t}
}

// Kind returns the traffic shard label. The SEC-020 guard mirrors every other
// method that reaches the wrapped candidate type (astgate live-tree): a copied
// TrafficAPI yields the empty kind, which save.Load and registry validation
// reject rather than routing a shard to a copy.
func (p *SaveParticipant) Kind() string {
	if err := p.t.checkNotCopied("Kind"); err != nil {
		return ""
	}
	return KindTraffic
}

// Source returns a fresh pull-iterator over the traffic state. It snapshots the
// full mutable state under the lock once, up front, then yields one record at a
// time, marshalling each on demand — never buffering the whole encoded shard
// before the first yield. A copied-value guard failure (SEC-020) surfaces on
// the first pull.
func (p *SaveParticipant) Source() serialize.RecordSource {
	if err := p.t.checkNotCopied("Source"); err != nil {
		return func() (serialize.Record, bool, error) { return serialize.Record{}, false, err }
	}
	snap, snapErr := p.t.snapshotForSave()
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

// Handler returns a fresh sink that rebuilds the traffic state from the
// streamed records. It clears the target's runtime state on the first record,
// then installs each record's effect directly under the lock — one record at a
// time, never buffering the whole shard.
func (p *SaveParticipant) Handler() serialize.RecordHandler {
	if err := p.t.checkNotCopied("Handler"); err != nil {
		return func(serialize.Record) error { return err }
	}
	reset := false
	return func(rec serialize.Record) error {
		if !reset {
			if err := p.t.resetForLoad(); err != nil {
				return err
			}
			reset = true
		}
		return p.t.applyLoadRecord(rec)
	}
}
