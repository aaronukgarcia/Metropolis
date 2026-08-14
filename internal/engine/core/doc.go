// Package core is the engine orchestrator (T-ENGINE, M0-ENG §1.1): the
// two-layer clock, the fixed deterministic phase pipeline over
// foundation.det's 256-shard POOL-SIM worker pool, the module registry
// instance, the command loop driving it from a protocol.Transport, the
// view-subscription server (T-SUBSCR), and the copy-on-write snapshot
// hook (T-PERSIST). It is the "heart of the machine" every other engine
// module (citizens, finance, world, ...) registers against.
//
// Module key: engine.core (see code.json; GUID 0bef8af8-0883-4604-bf98-71212100fffb)
// Spec ref:   §3 (Time); M0-ENG §1.1-1.3 (process/thread topology,
//
//	deterministic parallelism, memory budget); §9 (month index); A2
//	(amortised cold pass)
//
// # The two-layer clock (§3)
//
// One calendar month = one day-night cycle = 30 fixed logistics
// day-ticks (DailyTicksPerMonth, clock.go). AdvanceTicks drives the
// clock forward one day-tick at a time; every 30th day-tick also runs
// the monthly phase pipeline. Speed (pause/1x/2x/4x, 8x reserved for
// feat.debugmode) is stored and queryable but affects nothing else in
// this package — REAL wall-clock pacing (turning a speed into "call
// AdvanceTicks every N milliseconds") is the UI/transport layer's job,
// not the engine's; nothing in this package ever reads the wall clock
// (AC-12).
//
// # The fixed phase order (§3, AC-3, AC-16)
//
// Every daily tick runs DailyPhaseOrder (v1: just PhaseDailyTick, the
// barrier hook A2's amortised cold pass and §8's logistics resolution
// register against once their owning modules go real). Every 30th
// daily tick additionally runs MonthlyPhaseOrder, in this exact,
// never-reordered sequence:
//
//	production -> logistics settlement -> consumption & shortfall ->
//	population -> land value & decay -> finance
//
// Each phase is a barrier (M0-ENG §1.2 point 2): a phase's hooks run
// shard-parallel over foundation.det's fixed 256 shards
// (det.NumShards, see phase.go's NumShards) via det.RunPhase, which
// merges per-shard results in shard order 0->255 and applies every
// cross-shard Effect in canonical (shard, sequence) order at the
// barrier — never submission order, never Go map iteration order
// (AC-6, AC-13). Phase k+1 only ever reads phase-k-committed state:
// this package enforces that by running phases strictly sequentially
// and aborting the rest of a tick's phases the instant one hook
// reports an error (AC-10), so no later phase ever sees a partially
// applied earlier one.
//
// # The walking-skeleton property
//
// Engine boots and runs correctly with zero registered PhaseHooks —
// empty phases are legal (M0-ENG §2's module-stubbing discipline; see
// the acceptance doc's "Out of scope": no real simulation content
// lives in this package). Modules register handlers through the
// PhaseHook interface (phase.go) one phase, one module, at a time as
// they go real, without engine.core's orchestration changing shape.
//
// # POOL-SIM (AC-4)
//
// Sized runtime.NumCPU()-2 ("leave 2 for UI+OS", M0-ENG §1.1),
// floored at 1 so a phase pipeline still runs correctly (just serially)
// on a 1- or 2-core machine — see engine.go's poolSizeForCPUs.
//
// # T-SUBSCR and T-PERSIST (AC-7, AC-8)
//
// subscribe.go's SubscriptionServer computes and pushes engine.status
// deltas from a dedicated goroutine, woken by a non-blocking signal
// from the command path — never inline in phase execution or command
// handling. persist.go's Snapshot copies the tiny engine state it needs
// under lock, then marshals and writes off-lock, so a save in progress
// never blocks the tick loop and the tick loop never blocks a save.
//
// # GC tuning (AC-14)
//
// This is T-ENGINE, the tick path M0-ENG §1.1's zero-heap-allocation
// discipline exists to protect from GC pauses. The deployment default
// this package's process runs under is GOGC=200 (starve the GC so it
// cannot stall the tick loop; already stated once, authoritatively, in
// foundation.det's package doc, restated here because this is the
// package that actually walks the tick path); GODEBUG=gctrace=1 is the
// documented way to verify GC pause behaviour in the perf harness.
// Neither is set by this package itself — both are process-launch
// concerns (env vars), tracked as follow-up work for build.ps1 /
// cmd/metropolis's main, not something a library package sets on its
// own behalf.
//
// # Determinism (AC-12, AC-13, AC-15)
//
// This package never imports math/rand and never calls the wall clock
// (grep -rn "time\.Now" internal/engine/core/*.go, excluding _test.go,
// returns no matches) — there is no randomness on the tick path yet
// (module content is out of scope, per the acceptance doc), and when
// one lands it goes through foundation.det's Stream, keyed
// (worldSeed, entityID, month, purpose), exactly like every other
// module. worldSeed is carried from NewEngine's construction
// (WithWorldSeed) unchanged for the Engine's lifetime. Snapshot is the
// hashable/serializable world-state hook feat.detgate's CI gate
// (FEAT-004) calls; a same-seed, same-command-log run at any POOL-SIM
// size produces byte-identical Snapshot output, because this package's
// own committed state (tick/month/seed) never depends on shard
// scheduling or goroutine order, only on how many AdvanceTicks calls
// were made — see determinism_test.go.
package core
