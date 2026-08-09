package core

import (
	"encoding/json"
	"io"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// This file is T-PERSIST's hook (M0-ENG §1.1): copy-on-write snapshot
// marshalling that runs concurrently with the tick loop, so saves never
// stall gameplay (AC-8). Engine state is tiny today (tick/month/seed) —
// the pattern is what matters at this stage, not the payload size (see
// doc.go's determinism/persist notes).

// snapshotMeta is the "meta" NDJSON shard's one record: the engine
// state a save needs to resume from (tick/month/seed). Real modules
// will add their own shards (citizens, cells, ...) alongside this one
// as they go real — engine.core only owns the "meta" shard.
type snapshotMeta struct {
	Tick  int64  `json:"tick"`
	Month int64  `json:"month"`
	Seed  uint64 `json:"seed"`
}

// Snapshot produces a serialize.Header plus one "meta" NDJSON shard
// (written to w) describing the Engine's current tick/month/seed —
// exactly the hashable/serializable world-state snapshot hook
// feat.detgate's determinism gate needs (AC-15; this package only
// exposes the hook, the 120-month×2-runs×worker-count gate itself is
// FEAT-004's job, per the acceptance doc's "Out of scope").
//
// Copy-on-write discipline: Snapshot takes Engine's lock only long
// enough to copy three int/uint64 fields (snapshotStateLocked below),
// then releases it before doing any marshalling or I/O — the
// AdvanceTicks tick loop is therefore never blocked for the duration of
// a Snapshot call, no matter how large a future snapshot's payload
// grows or how slow w is (AC-8; see persist_test.go's concurrency
// assertion, which uses a deliberately slow w and a channel/counter,
// never a wall-clock timing assertion).
func (e *Engine) Snapshot(w io.Writer, correlationID string) (serialize.Header, error) {
	tick, month, seed := e.snapshotStateLocked()

	header := serialize.NewHeader(int64(seed), tick, month, buildinfo.Version)

	meta := snapshotMeta{Tick: tick, Month: month, Seed: seed}
	written := false
	next := func() (serialize.Record, bool, error) {
		if written {
			return serialize.Record{}, false, nil
		}
		written = true
		data, err := json.Marshal(meta)
		if err != nil {
			return serialize.Record{}, false, err
		}
		return serialize.Record{Kind: "meta", Data: data}, true, nil
	}

	shardMeta, err := (serialize.NDJSONSerializer{}).WriteShard(w, serialize.ShardMeta{Name: "meta", Kind: "meta"}, next)
	if err != nil {
		return serialize.Header{}, errs.Wrap(ErrSnapshotFailed, correlationID, err, map[string]any{"shard": "meta"})
	}

	header.ShardIndex = []serialize.ShardMeta{shardMeta}
	return header, nil
}

// snapshotStateLocked copies the engine state a snapshot needs under
// mu, as briefly as possible — the copy-on-write cut point between "the
// live, still-advancing engine" and "the frozen values this particular
// snapshot describes".
func (e *Engine) snapshotStateLocked() (tick, month int64, seed uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock.Tick(), e.clock.Month(), e.worldSeed
}
