package headless

import (
	"context"
	"io"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/debug"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// pumpShutdownJoinTimeout bounds the shutdown closure's wait on the
// subscription pump goroutine's done channel (R3, independent round
// r2/r3, FEAT-208 increment 1) — mirrors cmd/metropolis/boot.go's
// identical constant/value exactly (no existing shutdown-timeout idiom
// was found elsewhere in this codebase to reuse instead; both packages
// independently fall back to the round's own 5s value). See
// ErrPumpShutdownTimeout's doc comment (errors.go) for why an unbounded
// wait here would be a real production hang, not a hypothetical one. A
// package-level var, not a const, ONLY so a test can lower it for a
// fast, deterministic proof of the timeout path itself — production
// code never reassigns it; it is 5s for every real caller.
var pumpShutdownJoinTimeout = 5 * time.Second

// joinPumpDone waits for the subscription pump goroutine's done channel
// to close, bounded by pumpShutdownJoinTimeout (R3, independent round
// r2/r3, FEAT-208 increment 1) — extracted from Run's shutdown closure
// into its own package-level function so
// feat208_pump_shutdown_test.go can exercise the timeout path directly,
// without driving a full Run() invocation just to reach it. On timeout,
// ErrPumpShutdownTimeout is logged (registry-sourced, GR#1/GR#7) and
// this function returns anyway — never blocks its caller past the
// bound.
func joinPumpDone(pumpDone <-chan struct{}, correlationID string) {
	select {
	case <-pumpDone:
	case <-time.After(pumpShutdownJoinTimeout):
		_ = errs.New(ErrPumpShutdownTimeout, correlationID, map[string]any{
			"timeoutMs": pumpShutdownJoinTimeout.Milliseconds(),
		})
	}
}

// Config specifies one headless run (AC-1, harness.headless.md). Seed
// and Months carry no defaults of their own — a caller (cmd/metropolis's
// -headless flag, or a future library caller) is responsible for its own
// "required" enforcement (AC-2) before calling Run; Run itself only
// refuses a non-positive Months (see driveTicks).
type Config struct {
	// Seed is the deterministic world seed (core.WithWorldSeed).
	Seed uint64

	// Months is the number of in-game months to advance. Must be
	// positive: Months <= 0, or a Months so large months*
	// core.DailyTicksPerMonth would overflow int64, is rejected by
	// driveTicks (MET-H207, BUG-305) rather than either advancing zero
	// ticks silently or wrapping into a bogus tick count — cmd/metropolis's
	// -headless flag also enforces > 0 at the CLI layer (AC-2), but Run is
	// a library entry point any other caller can reach directly, so the
	// guard lives here too rather than being trusted to every caller.
	Months int64

	// OutDir is the bundle directory Run writes the -out snapshot to
	// (AC-3): int.serializer's StateSerializer/bundle format — header.json
	// plus shards/ — not a bespoke ad-hoc JSON dump, so the output is
	// itself a valid fixture readable by `metctl verify`. Must not already
	// exist (serialize.CreateBundleDir's own "never silently merge into a
	// stale bundle" rule).
	OutDir string

	// ScenarioPath, if non-empty, names a JSON scenario script (AC-4) —
	// see LoadScenario. Every scenario command is sent and awaited BEFORE
	// tick advancement begins.
	ScenarioPath string

	// PoolSize overrides POOL-SIM's worker count (core.WithPoolSize). Zero
	// means "use engine.core's own default sizing."
	PoolSize int

	// Report, if non-nil, receives one NDJSON line per phase-timing
	// observation (AC-5) and one per daily-tick invariant check (AC-6).
	// Nil means neither stream is produced.
	Report io.Writer

	// CorrelationID roots every registry-sourced error and every
	// internally-minted command's correlation chain this run produces
	// (GR#1). Empty mints a fresh one via protocol.NewCorrelationID().
	CorrelationID string

	// Debug enables feat.debugmode (FEAT-008/FEAT-035) for this run via
	// debug.State's SourceFlag enable path (cmd/metropolis's -debug
	// flag). This is the wiring FEAT-035 exists to close: enabling it
	// sticky-flags the header this run's Snapshot writes to
	// OutDir/header.json (debug.WithHeader against the exact prior
	// header this call merges forward — see the doc comment on Run
	// below), and injects debug.State.AllowSpeed8x as this run's
	// core.Engine's Speed8xGate (AC-E3) so Speed8xDebug is genuinely
	// gated rather than always-denied (the default with no gate wired
	// at all) or always-allowed (the false-pass shape AC-E3 warns
	// against).
	Debug bool

	// SeedCitizenCount (BUG-665), when > 0, bulk-seeds this many real
	// citizens.ColdRecord values into the CitizensAPI Wire drives BEFORE
	// any tick advances — the plumbing that let a "1M citizens" preset
	// reach ONLY internal/harness/synth's throwaway Generate() and never
	// the ticked engine at all (harness.headless.Config had no
	// citizen-count field, so the engine that actually ticked always
	// held compose's own 64-citizen genesis seed regardless of what a
	// caller asked for). Records are generated deterministically from
	// cfg.Seed (see generateSeedPopulation — GR#21: no math/rand, no wall
	// clock, id-keyed det.Stream draws only) and injected via
	// compose.Deps.Citizens, the SAME test seam engine.compose's own
	// suite already uses to pre-construct a CitizensAPI — Run never
	// reaches into citizens.ColdShard directly. compose.Wire's own
	// unconditional 64-citizen founder seed (AC-8's non-zero seed
	// population) still runs on top, at disjoint ids (this population
	// starts at PerfSeedIDBase, far above compose's [1,64] founder range
	// and far below engine.attract's MigrantIDBase), so
	// Result.Population after a SeedCitizenCount=N run is N+64, not N —
	// callers that need the exact seeded count back should read
	// Result.Population and subtract the documented 64-citizen baseline
	// founder seed (compose.go's own seedCitizenCount constant), rather
	// than assuming population==N. Zero (the default) preserves this
	// package's prior behaviour byte-for-byte: no extra citizens, the
	// ordinary 64-citizen genesis-only population.
	SeedCitizenCount int64

	// InDir, if non-empty, names a prior headless bundle directory
	// (previously written via -out) whose header.json this run reads
	// before constructing its own header, carrying the prior run's
	// DebugTouched flag forward via serialize.Header's own
	// MergeDebugTouched (OR-merge, sticky) — ASM-403's reload mechanism,
	// built specifically so AC-M1's mandatory end-to-end test can prove
	// DebugTouched survives a second, genuinely separate `-headless`
	// invocation that never itself enables debug. Empty means this run
	// starts from a fresh, untouched header exactly as before this
	// field existed.
	InDir string
}

// Result summarises a completed headless run.
type Result struct {
	// Header is the serialize.Header written to OutDir/header.json —
	// callers that want WorldSeed/CreatedAtTick/GameMonth/ShardIndex
	// without re-reading the bundle from disk can read it directly here.
	Header serialize.Header

	// TicksAdvanced is the number of daily ticks actually advanced before
	// Run returned successfully.
	TicksAdvanced int64

	// PhaseHookCount is the number of PhaseHooks registered against the
	// engine this run drove (read from core.Engine.HookCount() after the
	// composition root wired it). It travels WITH the run so a consumer of
	// TicksAdvanced never mistakes a walking-skeleton zero for a real
	// simulation (BUG-034/ASM-422 — the runtime accessor
	// PhaseHookCountInHeadlessPath's own doc comment recommended).
	PhaseHookCount int

	// ScenarioCommands is the number of scenario-script commands sent and
	// accepted before tick advancement began (0 if Config.ScenarioPath
	// was empty).
	ScenarioCommands int

	// ReportWriteErr is the first error the -report stream encountered
	// while encoding, if any (see reportWriter's doc comment for why this
	// is surfaced rather than either silently dropped or treated as a
	// run-aborting failure).
	ReportWriteErr error

	// Population (BUG-665) is the CitizensAPI's TotalPopulation read
	// AFTER every tick has advanced — the count actually reached and
	// driven by AdvanceDayTick, not the count a caller merely asked
	// Generate/SeedCitizenCount to produce. This is the field a perf
	// probe or a test proves against, precisely because "the count
	// reaches Generate and nothing else" is the defect this item closes:
	// a caller that only checked its own input parameter, never this
	// output, would have stayed blind to that gap indefinitely.
	Population int

	// TickWallTime (BUG-665) is the wall-clock time spent strictly
	// inside driveTicks — i.e. advancing Config.Months worth of daily
	// ticks through the real protocol.Command path — excluding
	// compose.Wire, citizen seeding, scenario replay, and shutdown/
	// snapshot-write overhead. A caller computing a per-tick perf figure
	// (TickWallTime / TicksAdvanced) gets the tick loop's own cost in
	// isolation, not a figure diluted by one-time setup/teardown work
	// that would make a large-population probe's per-tick number look
	// artificially better than it is.
	TickWallTime time.Duration

	// Births/Deaths (BUG-665 round finding) are the cumulative real
	// per-citizen fertility births and mortality deaths the run's
	// composition produced (compose.Composition.VitalBirths/VitalDeaths),
	// read after every tick has advanced. An independent destructive
	// round proved a bulk-seeded population that never pairs into real
	// partnerships/households is invisible to fertility.go's
	// applyFertilityLocked (Partner==0 is skipped outright) — a
	// SeedCitizenCount run whose demographic engine never actually does
	// any work would exercise a materially cheaper code path than a real
	// population, silently understating tick cost. A caller (this
	// package's own population perf gate) asserts Births+Deaths > 0 as a
	// pinned liveness invariant, never merely hoped for.
	Births int64
	Deaths int64
}

// Run drives one headless simulation run to completion: constructs a
// real *core.Engine over a real *protocol.InProcTransport, executes any
// scenario script (AC-4), advances Months*DailyTicksPerMonth ticks
// exclusively through the real protocol.Command path (AC-1/AC-2 of
// engine.headless.md — never Engine.AdvanceTicks directly), then writes
// the -out snapshot bundle (AC-3).
//
// FEAT-035: this is also feat.debugmode's real wiring point. A
// debug.State is always constructed (debug.WithHeader against a small
// in-process header this function owns) and its AllowSpeed8x method is
// always injected as the Engine's Speed8xGate (core.WithSpeed8xGate) —
// this is what makes AC-E3's two-sided check possible: with cfg.Debug
// false, Speed8xDebug is denied because the (real, configured) gate
// says no, never because no gate was configured at all. If cfg.InDir
// names a prior bundle, its header's DebugTouched flag is read and
// merged into this run's own debug header BEFORE cfg.Debug is applied,
// so a prior debug-touched save carries forward even when this run
// never calls Enable itself (ASM-403's reload mechanism). The resulting
// header is passed to Engine.Snapshot as its `prior` argument in
// writeBundle, so the bundle Snapshot actually writes to
// OutDir/header.json is the one debug.State touched — not a
// separately-constructed header that merely shares field values
// (AC-E4).
//
// Run blocks until the run completes, ctx is done, or a step fails. On
// any failure it still performs the documented engine.core shutdown
// sequence (cancel, join RunCommandLoop's goroutine, THEN close the
// transport — see RunCommandLoop's "Exit contract" doc comment,
// internal/engine/core/commands.go) before returning, so a failed Run
// never leaks the command-loop goroutine.
func Run(ctx context.Context, cfg Config) (Result, error) {
	correlationID := cfg.CorrelationID
	if correlationID == "" {
		correlationID = string(protocol.NewCorrelationID())
	}

	// FEAT-035 DoD#1/DoD#2: carry a prior bundle's DebugTouched flag
	// forward (ASM-403) BEFORE this run's own debug.State is built, so
	// dbgHeader already reflects "was this lineage ever debug-touched"
	// regardless of whether cfg.Debug is set on THIS invocation.
	dbgHeader := &serialize.Header{}
	if cfg.InDir != "" {
		prior, err := serialize.ReadHeader(cfg.InDir)
		if err != nil {
			return Result{}, errs.Wrap(ErrInputReadFailed, correlationID, err, map[string]any{"path": cfg.InDir, "cause": err.Error()})
		}
		dbgHeader.MergeDebugTouched(prior.DebugTouched())
	}
	dbgState := debug.NewState(debug.WithHeader(dbgHeader))

	rw := newReportWriter(cfg.Report)

	opts := []core.Option{
		core.WithWorldSeed(cfg.Seed),
		core.WithPhaseObserver(rw.phaseObserver()),
		core.WithSpeed8xGate(dbgState.AllowSpeed8x),
	}
	if cfg.PoolSize > 0 {
		opts = append(opts, core.WithPoolSize(cfg.PoolSize))
	}
	e := core.NewEngine(opts...)

	// BUG-665 (round finding, 2026-09-04): when the caller asked for a
	// bulk-seeded population, build and pre-seed a *citizens.CitizensAPI
	// BEFORE Wire runs, then inject it via compose.Deps.Citizens — the
	// exact seam engine.compose's own test suite already uses to hand
	// Wire a pre-constructed API (see compose.Deps.Citizens' doc comment:
	// "nil means construct the default"). Wire's own genesis founder seed
	// (64 citizens, disjoint ids) still mints on top of this — see
	// SeedCitizenCount's doc comment on why Result.Population is N+64,
	// not N.
	//
	// Two more steps close the round's own "vacuity in a subtler coat"
	// finding, beyond the bare SeedColdRecords call the first landing
	// stopped at:
	//
	//  1. generateSeedPopulation's own second pass pairs a childbearing-
	//     age fraction of the seeded population into mutual partners
	//     (Household/Partner columns, GR#21-safe pure arithmetic — see
	//     its own doc comment), and SeedHouseholds registers those
	//     households as REAL, ValidateCitizen-visible entries — without
	//     both, fertility.go's applyFertilityLocked never fires
	//     (ColdRecord.Partner==0 skips a citizen outright) and even a
	//     paired-but-unregistered household would make every birth
	//     attempt fail ValidateCitizen's householdExists check (see
	//     SeedHouseholds' own doc comment for why this is a separate,
	//     explicit call rather than folded into SeedColdRecords).
	//  2. compose.Deps.SeedResidentIDBase/SeedResidentIDCount registers
	//     the seeded id range as compose-visible for moneycirc.go's four
	//     resident-scoped monthly passes (markEmploymentAndCount/
	//     employedResidentCount/formResidentHouseholds/
	//     distributeWagesToResidents) — without it, the round proved
	//     Composition.MoneyFlows is byte-identical whether
	//     SeedCitizenCount is 0 or 50,000, i.e. the seeded population is
	//     invisible to wage/tax/household-formation cost at ANY scale.
	var deps *compose.Deps
	if cfg.SeedCitizenCount > 0 {
		citizensAPI, ctErr := citizens.NewCitizensAPI(cfg.Seed, correlationID)
		if ctErr != nil {
			return Result{}, errs.Wrap(ErrSeedPopulationFailed, correlationID, ctErr, map[string]any{"stage": "NewCitizensAPI", "cause": ctErr.Error()})
		}
		records := generateSeedPopulation(cfg.Seed, cfg.SeedCitizenCount)
		if seedErr := citizensAPI.SeedColdRecords(records, correlationID); seedErr != nil {
			return Result{}, errs.Wrap(ErrSeedPopulationFailed, correlationID, seedErr, map[string]any{"stage": "SeedColdRecords", "cause": seedErr.Error()})
		}
		if hhErr := citizensAPI.SeedHouseholds(records, correlationID); hhErr != nil {
			return Result{}, errs.Wrap(ErrSeedPopulationFailed, correlationID, hhErr, map[string]any{"stage": "SeedHouseholds", "cause": hhErr.Error()})
		}
		deps = &compose.Deps{
			Citizens:            citizensAPI,
			SeedResidentIDBase:  PerfSeedIDBase,
			SeedResidentIDCount: cfg.SeedCitizenCount,
		}
	}

	// FEAT-082 (ASM-001/ASM-421): every headless/perfci/synth run now
	// drives a REAL simulation through the composition root, not a
	// zero-hook walking skeleton. compose.Wire is the single wiring path
	// (AC-1/AC-13 of feat.compositionroot); a wiring failure (e.g.
	// market.LoadDefault cannot load data/market.json) aborts the run with
	// a registry-sourced error before any tick advances.
	comp, err := compose.Wire(e, deps)
	if err != nil {
		return Result{}, err
	}

	if cfg.Debug {
		if err := dbgState.Enable(debug.SourceFlag, correlationID); err != nil {
			return Result{}, err
		}
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)

	loopCtx, cancel := context.WithCancel(ctx)

	// FEAT-208: start the subscription pump so any view compose.Wire
	// registered (viewRegistrationOrder) actually publishes deltas
	// against this headless run too, not only the interactive binary —
	// mirrors cmd/metropolis/boot.go's identical call, same ordering
	// (after compose.Wire/transport construction, before the command
	// loop starts). StartSubscriptionPump can only fail on a
	// struct-copied Engine (BUG-019) or a second call on the same Engine
	// (F1a, ErrSubscriptionPumpAlreadyStarted), neither of which e here
	// ever is/does. pumpDone (F2, independent round r1) is joined by the
	// shutdown closure below, before transport.Close() — previously
	// (before F2) this goroutine was never tracked or joined at all.
	pumpDone, err := e.StartSubscriptionPump(loopCtx, transport)
	if err != nil {
		cancel()
		_ = transport.Close()
		return Result{}, err
	}

	loopDone := make(chan error, 1)
	go func() { loopDone <- e.RunCommandLoop(loopCtx, transport) }()

	// shutdown implements the documented caller-side contract exactly:
	// cancel, WAIT for RunCommandLoop's goroutine (unconditionally — it
	// is engine.core's own trusted code, and ctx cancellation is proven
	// sufficient to stop it promptly) AND the subscription pump
	// goroutine (F2) to return (join), and only THEN close the transport
	// (AC-5's ordering requirement, and StubEngine.Run's identical
	// "cancel(); wg.Wait(); Close()" contract). Called on every exit
	// path below, success or failure, so this package never leaks
	// either goroutine.
	//
	// BOUNDED join on pumpDone (R3, independent round r2/r3): the pump
	// goroutine calls a caller-supplied DeltaSink (transport here), and
	// r2's independent round proved a DeltaSink that blocks indefinitely
	// or reenters Publish (both documented-prohibited on
	// engine/core.DeltaSink, neither mechanically preventable) can hang
	// that goroutine forever. Waiting on pumpDone with no timeout would
	// therefore hang Run's shutdown — and so Run itself — forever too.
	// pumpShutdownJoinTimeout bounds that wait: on timeout,
	// ErrPumpShutdownTimeout is logged (registry-sourced, GR#1/GR#7) and
	// shutdown proceeds to transport.Close() anyway. transport here is
	// InProcTransport, whose SendDelta is documented non-blocking and
	// never re-enters anything, so this is belt-and-braces against a
	// future DeltaSink swap, not a currently-reachable hang.
	shutdown := func() error {
		cancel()
		err := <-loopDone
		joinPumpDone(pumpDone, correlationID)
		_ = transport.Close()
		return err
	}

	scenarioCommands := 0
	if cfg.ScenarioPath != "" {
		cmds, err := LoadScenario(correlationID, cfg.ScenarioPath)
		if err != nil {
			_ = shutdown()
			return Result{}, err
		}
		for _, cmd := range cmds {
			if err := sendAndAwait(transport, cmd, correlationID); err != nil {
				_ = shutdown()
				return Result{}, err
			}
			scenarioCommands++
		}
	}

	// BUG-665: timed strictly around driveTicks (not Wire/seed/shutdown/
	// snapshot-write) so Result.TickWallTime is the tick loop's own cost
	// in isolation — see TickWallTime's doc comment.
	ticksStart := time.Now()
	ticksAdvanced, err := driveTicks(transport, cfg.Months, correlationID)
	tickWallTime := time.Since(ticksStart)
	if err != nil {
		_ = shutdown()
		return Result{}, err
	}

	// AC-4/AC-7 (engine.headless.md): this driver DOES control shutdown
	// ordering deterministically (cancel-then-join-then-close above), so
	// in the disciplined case loopErr is expected to be nil — but it is
	// still observed and surfaced, never discarded, because that is
	// exactly the "someone must observe this return value" AC-7 requires
	// of any genuinely unattended caller (the discipline lives in THIS
	// function; nothing stops a future refactor from breaking it, and a
	// discarded return would then silently hide that regression).
	if loopErr := shutdown(); loopErr != nil {
		return Result{}, errs.Wrap(ErrEngineRunFailed, correlationID, loopErr, map[string]any{"cause": loopErr.Error()})
	}

	header, err := writeBundle(cfg.OutDir, e, correlationID, *dbgHeader)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Header:           header,
		TicksAdvanced:    ticksAdvanced,
		ScenarioCommands: scenarioCommands,
		PhaseHookCount:   e.HookCount(),
		Population:       comp.Population(),
		TickWallTime:     tickWallTime,
		Births:           comp.VitalBirths(),
		Deaths:           comp.VitalDeaths(),
		ReportWriteErr:   rw.err,
	}, nil
}

// sendAndAwait sends cmd on t and blocks for the matching CommandResult.
// Safe because this package drives exactly one command at a time,
// sequentially, from a single goroutine (scenario commands, then
// AdvanceTicks batches) — there is never more than one in-flight command
// racing another caller for the next Results() value.
func sendAndAwait(t *protocol.InProcTransport, cmd protocol.Command, correlationID string) error {
	if err := t.SendCommand(cmd); err != nil {
		// BUG-219/BUG-220: this branch is a transport-layer send failure
		// (a failed cmd.Validate(), protocol.ErrTransportClosed, or
		// protocol.ErrCommandQueueFull from InProcTransport.SendCommand)
		// -- the command never reached the engine, so there is no
		// engine.core rejection code to report here (unlike the
		// !result.Accepted branch below, which surfaces a real one via
		// result.Error.Code). It uses its own dedicated code
		// (ErrCommandSendFailed / MET-H205) with a transport-neutral
		// {cause} placeholder rather than stuffing err.Error() into
		// ErrCommandRejected's {engineErrorCode} slot.
		return errs.Wrap(ErrCommandSendFailed, correlationID, err, map[string]any{"kind": string(cmd.Kind), "cause": err.Error()})
	}
	result := <-t.Results()
	if !result.Accepted {
		code := ""
		if result.Error != nil {
			code = result.Error.Code
		}
		return errs.New(ErrCommandRejected, correlationID, map[string]any{
			"kind": string(cmd.Kind), "engineErrorCode": code,
		})
	}
	return nil
}

// driveTicks advances months*core.DailyTicksPerMonth daily ticks
// entirely through the real protocol.Command path (engine.headless.md
// AC-1/AC-2 — never core.Engine.AdvanceTicks directly), chunked at
// core.MaxAdvanceTicksPerCall per command so an arbitrarily large months
// value still issues a bounded, small number of commands rather than
// either one over-limit command (rejected by the engine, AC-11 of
// engine.core.md) or one command per tick (needlessly many round trips).
// Returns the number of ticks actually advanced before any error.
//
// # BUG-305: months*DailyTicksPerMonth is REJECTED on overflow, never wrapped
//
// months is caller-supplied (cmd/metropolis's -months flag already
// enforces > 0 at the CLI layer, headless.go's runHeadless, but Run/
// driveTicks is also a library entry point any OTHER caller can reach
// directly, bypassing that CLI-only check). A bare `months *
// core.DailyTicksPerMonth` int64 multiply silently wraps on a
// sufficiently large or negative months — Go's defined two's-complement
// truncation, not a panic — which is exactly the poisoned-perf-baseline
// shape this fix closes: driveTicks would return a SUCCESSFUL Result
// carrying a wrapped, bogus TicksAdvanced instead of failing loudly.
// num.SafeMul (internal/foundation/num) is reused rather than
// reimplemented here — it already returns the overflow bool this
// function needs; the fix is to REJECT on that bool rather than accept
// SafeMul's saturated value, since saturating months into "advance the
// maximum representable number of ticks" would be exactly as wrong a
// silent substitution as the wrap it replaces (this substrate must never
// paper over an operator/caller error with a plausible-looking number).
// Non-positive months is rejected explicitly and separately, before the
// multiply, for the same reason: zero or negative ticks is never a
// legitimate "advance the simulation" request, and a caller-supplied
// negative months would otherwise reach SafeMul and either be flagged as
// overflow (large |months|) or silently produce a negative/zero total
// that the loop below simply never enters (small negative months) —
// hiding the caller error as "ran fine, advanced 0 ticks" instead of
// surfacing it as MET-H207.
func driveTicks(t *protocol.InProcTransport, months int64, correlationID string) (int64, error) {
	if months <= 0 {
		return 0, errs.New(ErrInvalidMonths, correlationID, map[string]any{
			"months": months, "cause": "months must be positive",
		})
	}
	total, overflowed := num.SafeMul(months, core.DailyTicksPerMonth)
	if overflowed {
		return 0, errs.New(ErrInvalidMonths, correlationID, map[string]any{
			"months": months, "dailyTicksPerMonth": core.DailyTicksPerMonth,
			"cause": "months*DailyTicksPerMonth overflows int64",
		})
	}
	remaining := total
	for remaining > 0 {
		n := core.MaxAdvanceTicksPerCall
		if remaining < n {
			n = remaining
		}
		corrID := protocol.NewCorrelationID()
		cmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   corrID,
			Kind:            protocol.KindAdvanceTicks,
			Payload:         protocol.AdvanceTicksPayload{N: n},
		}
		if err := sendAndAwait(t, cmd, string(corrID)); err != nil {
			return total - remaining, err
		}
		remaining -= n
	}
	return total, nil
}

// writeBundle writes e's current state as an int.serializer bundle at
// dir (AC-3): CreateBundleDir, one "meta" shard via Engine.Snapshot
// (engine.core's own T-PERSIST hook, persist.go), then WriteHeader — the
// exact three-step pattern serialize_test.go's TestBundleRoundTripAndValidate
// documents, so the result is a bundle `metctl verify` can validate, not
// a bespoke ad-hoc JSON dump.
//
// FEAT-035 AC-S3: prior is passed straight through to Engine.Snapshot's
// own variadic prior parameter, so DebugTouched carry-forward is
// exercised by this real production write path, not only by
// persist_test.go's unit tests.
func writeBundle(dir string, e *core.Engine, correlationID string, prior serialize.Header) (serialize.Header, error) {
	if err := serialize.CreateBundleDir(dir); err != nil {
		return serialize.Header{}, errs.Wrap(ErrOutputWriteFailed, correlationID, err, map[string]any{"path": dir, "cause": err.Error()})
	}

	meta := serialize.ShardMeta{Name: "meta", Kind: "meta", Encoding: "ndjson+gzip"}
	f, err := serialize.CreateShardWriter(dir, meta)
	if err != nil {
		return serialize.Header{}, errs.Wrap(ErrOutputWriteFailed, correlationID, err, map[string]any{"path": dir, "cause": err.Error()})
	}

	header, snapErr := e.Snapshot(f, correlationID, prior)
	closeErr := f.Close()
	if snapErr != nil {
		// Already a registry-sourced *errs.E (engine.core's ErrSnapshotFailed,
		// MET-E006) — propagate unchanged rather than re-wrapping it under
		// this package's own code, per Engine.Snapshot's own doc comment
		// convention.
		return serialize.Header{}, snapErr
	}
	if closeErr != nil {
		return serialize.Header{}, errs.Wrap(ErrOutputWriteFailed, correlationID, closeErr, map[string]any{"path": dir, "cause": closeErr.Error()})
	}

	if err := serialize.WriteHeader(dir, header); err != nil {
		return serialize.Header{}, errs.Wrap(ErrOutputWriteFailed, correlationID, err, map[string]any{"path": dir, "cause": err.Error()})
	}
	return header, nil
}
