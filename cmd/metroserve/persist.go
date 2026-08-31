package main

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// FEAT-1972079936 Phase 1 inc4 — metroserve durable persistence + rehydrate.
//
// This is the network host's half of Phase 1: it CONSUMES inc1's
// internal/persist Store and inc2's already-committed write-through journaler
// (compose.Deps.PersistStore) + genesis restore (compose.RestoreCommands),
// wiring them into metroserve so a persisted city SURVIVES A SERVER RESTART —
// the real server-side "kill localStorage" win. Nothing here re-implements
// persistence or restore; it only decides WHEN to turn them on and replays
// the journal on boot.
//
// # Known limitation (documented, not a defect — acceptance doc §"Known limitation")
//
// Rehydrate replays the FULL command journal from genesis, so a long-running
// server's restart cost is O(commands) — the tickLoop journals one
// AdvanceTicks per wall-clock interval, so a city advanced for many sim-years
// accumulates a long journal. Bounding that is exactly inc3's snapshot cadence
// (FEAT-1972079941), which is deferred; for Phase 1 the requirement is
// correctness (lossless resume), not restore speed.

// persistTenantID is the PLACEHOLDER tenant identity every metroserve-hosted
// city is keyed under in Phase 1. Metropolis Phase 1 is
// multi-tenant-single-player with one fixed tenant placeholder
// (FEAT-1972079936 epic open-Q #3); real per-account tenant identity arrives
// with Phase 2 auth. Per the balance-number regime this is a documented
// PLACEHOLDER, not a spec-transcribed value — nothing player-facing keys on
// it — and it is a named const rather than a string literal buried at the
// CityKey construction site so the Phase 2 swap is a single edit.
const persistTenantID = "local"

// rehydrateGuardStore wraps a durable persist.Store and SUPPRESSES
// AppendJournal while (and only while) the restored journal is being replayed
// back into the engine on boot.
//
// Why it exists: metroserve wires the engine with inc2's write-through
// journaler ON (so live commands are durably persisted), and the engine seals
// its journaler on the first command handled (core.SetCommandJournaler is
// rejected after seal). Rehydrate therefore has to replay the restored
// commands through the SAME persist-wired engine — but the engine's normal
// command path journals every accepted command, so a naive replay would
// APPEND every restored command back to the durable journal, doubling it on
// every restart (restart-twice would then replay 2N commands and diverge —
// silent, compounding data corruption, exactly the class this epic kills).
//
// The guard closes that hole entirely inside cmd/metroserve without touching
// inc2's adapter: appends are dropped while replaying is true, then flipped to
// pass-through for the live run. Reads (ReadJournal/Exists/…) are always the
// true durable state — restore reads the underlying Store directly, never this
// guard — so replay sees the real journal and only the RE-append is suppressed.
//
// replaying is an atomic.Bool: the flag is flipped to false on the boot
// goroutine before any server/command-loop goroutine starts, so live
// AppendJournal calls from the command loop are race-free (GR#21, -race clean).
type rehydrateGuardStore struct {
	persist.Store
	replaying atomic.Bool
}

// AppendJournal drops the write while replaying the restored journal (so
// re-applying restored commands does not re-persist them), and passes through
// to the wrapped Store once live.
func (g *rehydrateGuardStore) AppendJournal(ctx context.Context, city persist.CityKey, record []byte) error {
	if g.replaying.Load() {
		return nil
	}
	return g.Store.AppendJournal(ctx, city, record)
}

// setUpPersistence is the testable seam AC-4 factors out of run(): it
// constructs the durable Store, wires it into the engine via inc2's
// write-through journaler, rehydrates the city from its persisted journal (if
// any), and hands back the live Composition + Store. run() calls exactly this
// and then serves; a unit test calls it against a t.TempDir() Store without
// booting the HTTP server.
//
//   - persistDir == "" → persistence OFF: compose.Wire(e, nil) EXACTLY as
//     before this increment (byte-for-byte unchanged default), nil Store.
//   - persistDir != "" → open a DiskStore, wire inc2's journaler under
//     CityKey{persistTenantID, cityID}, and if a journal already exists for
//     that city, replay it into e (rehydrate) before returning.
//
// A store-construction error, an Exists probe error, a restore/decode error
// (corrupt journal), or a rejected replayed command are all returned as
// errors — the caller exits non-zero. A corrupt journal is FATAL by design:
// silently starting a fresh city over a persisted one would be the very
// data-loss this epic exists to prevent.
func setUpPersistence(e *core.Engine, persistDir, cityID string, stdout io.Writer) (*compose.Composition, persist.Store, error) {
	if persistDir == "" {
		comp, err := compose.Wire(e, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("compose.Wire failed: %w", err)
		}
		return comp, nil, nil
	}

	disk, err := persist.NewDiskStore(persistDir)
	if err != nil {
		return nil, nil, fmt.Errorf("open persist store %q: %w", persistDir, err)
	}
	city := persist.CityKey{TenantID: persistTenantID, CityID: cityID}

	comp, err := wireAndRehydrate(context.Background(), e, disk, city, stdout)
	if err != nil {
		return nil, nil, err
	}
	return comp, disk, nil
}

// wireAndRehydrate is the shared, single-source guarded-rehydrate seam
// (GR#3). It wires the engine to a durable Store with inc2's write-through
// journaler interposed behind a rehydrateGuardStore, then — if a journal
// already exists for city — replays it back through the engine's normal
// command path with re-append suppressed, so a restart resumes losslessly
// and a restart-twice never double-appends. Both metroserve's single-city
// setUpPersistence and Phase 2's CityHost (cityhost.go), which drives N
// cities over ONE shared Store, call exactly this so neither re-implements
// restore nor re-opens the double-append hole.
//
// A store-existence probe error, a restore/decode error (a corrupt journal),
// or a rejected replayed command are all returned as errors — the caller
// treats them as fatal for that city, exactly as inc4 requires: silently
// starting a fresh city over a persisted one is the data loss this epic
// exists to prevent. On any error the engine is left partly wired but the
// caller discards it (CityHost registers nothing; run() exits non-zero), so
// no half-live city escapes.
func wireAndRehydrate(ctx context.Context, e *core.Engine, store persist.Store, city persist.CityKey, stdout io.Writer) (*compose.Composition, error) {
	// Wire with the guard interposed so replayed commands are NOT re-appended.
	guard := &rehydrateGuardStore{Store: store}
	guard.replaying.Store(true)
	comp, err := compose.Wire(e, &compose.Deps{PersistStore: guard, PersistCity: city})
	if err != nil {
		return nil, fmt.Errorf("compose.Wire failed: %w", err)
	}

	exists, err := store.Exists(ctx, city)
	if err != nil {
		return nil, fmt.Errorf("persist existence check for city %q: %w", city.CityID, err)
	}
	if !exists {
		guard.replaying.Store(false)
		_, _ = fmt.Fprintf(stdout, "metroserve: no persisted journal for city %s — starting fresh\n", city.CityID)
		return comp, nil
	}

	// Rehydrate: read from the REAL store, replay through the engine's normal
	// command path (the same path inc2's restore test uses). A decode error
	// (corrupt journal) surfaces from RestoreCommands and is fatal here.
	cmds, err := compose.RestoreCommands(ctx, store, city)
	if err != nil {
		return nil, fmt.Errorf("rehydrate city %s (corrupt journal?): %w", city.CityID, err)
	}
	for i, cmd := range cmds {
		if res := e.HandleCommand(cmd); !res.Accepted {
			return nil, fmt.Errorf("rehydrate city %s: restored command %d (%s) rejected: %+v", city.CityID, i, cmd.Kind, res.Error)
		}
	}
	guard.replaying.Store(false)
	_, _ = fmt.Fprintf(stdout, "metroserve: rehydrated %d commands for city %s\n", len(cmds), city.CityID)
	return comp, nil
}
