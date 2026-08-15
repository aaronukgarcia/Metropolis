package main

import (
	"context"
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	enginecore "github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/debug"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/screens/devmode"
	mapscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/map"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// codeBootFailure is MET-E900 (data/errors.json, range E900-E999 reserved
// for feat.skeleton): the one registry-sourced code every boot-time
// failure in this package is raised under (AC-7 — GR#7 forbids ad hoc
// error strings even at the composition root). ctx["component"] names
// which wired dependency failed, so a startup failure is always traceable
// to a specific piece of the skeleton, never just "something broke."
const codeBootFailure = "MET-E900"

// skeletonModuleVersion is the placeholder semver every registerSkeletonModules
// entry reports (none of these wrappers are independently versioned yet —
// see wiredModule's doc comment).
const skeletonModuleVersion = "0.1.0-skeleton"

// skeletonModuleKeys is exactly the Sprint 1 dependency set this item's
// acceptance doc names in its Scope section — the modules feat.skeleton
// wires into one runnable binary. Registered in this fixed order (AC-16's
// "boot order" contract elsewhere in the registry package) so BootOrder()
// is deterministic run over run, never Go map iteration order (GR#21).
var skeletonModuleKeys = []string{
	"int.protocol",
	"foundation.errors",
	"harness.stub",
	"engine.core",
	"feat.detgate",
	"ui.core",
	"ui.widgets",
	"ui.screen.map",
}

// wiredModule is a minimal foundation.registry.Module adapter for a
// dependency that doesn't otherwise have a Go type implementing
// Name/Version/Health (most of the Sprint 1 dependency set: int.protocol
// is a package of pure functions/types, not a stateful "module"; ui.core,
// ui.widgets, ui.screen.map, foundation.errors are libraries, not
// swappable stub/real pairs the way engine.crime etc. eventually will be).
// It exists purely so AC-2's "every registered module reports stub/ok"
// has something concrete to query at boot — every entry here is
// permanently StatusStub (registry.Register's own default) with
// HealthOK, matching this item's "one module real at a time... starting
// state" framing (M0-ENG §2/§3).
type wiredModule struct{ name string }

func (m wiredModule) Name() string            { return m.name }
func (m wiredModule) Version() string         { return skeletonModuleVersion }
func (m wiredModule) Health() registry.Health { return registry.HealthOK }

// registerSkeletonModules registers every entry in skeletonModuleKeys into
// reg, each defaulting to StatusStub/HealthOK (AC-2). A duplicate key
// (e.g. reg already had one of these keys registered, most plausibly a
// programming error at a future call site, or an already-populated
// Registry a caller passed in) is a registry-sourced boot failure, not a
// silent overwrite — see registry.Register's own "duplicate keys are
// rejected" doc comment.
func registerSkeletonModules(reg *registry.Registry, correlationID string) error {
	for _, key := range skeletonModuleKeys {
		if err := reg.Register(key, nil, wiredModule{name: key}); err != nil {
			return errs.Wrap(codeBootFailure, correlationID, err, map[string]any{
				"component": "foundation.registry",
				"key":       key,
			})
		}
	}
	return nil
}

// skeletonWiring bundles every live component feat.skeleton wires
// together once boot succeeds: the module registry, the real
// int.protocol Transport, the real harness.stub StubEngine driving it,
// ui.core's ViewStore/ViewsLoop consuming its Delta stream, and the real
// ui.screen.map MapScreen subscribed to "f1.viewport" — exactly AC-1a's
// "real components end to end, nothing mocked." Screen/render/input
// wiring (the tcell-dependent half) is deliberately NOT part of this
// struct — see run.go — so tests can exercise this whole slice headless,
// without a terminal (tcell.Screen is only needed once rendering starts).
type skeletonWiring struct {
	correlationID string
	registry      *registry.Registry
	transport     *protocol.InProcTransport
	engine        *enginecore.Engine
	viewStore     *core.ViewStore
	viewsLoop     *core.ViewsLoop
	mapScreen     *mapscreen.MapScreen

	// debugState is feat.debugmode's (FEAT-008) single source of truth
	// for whether debug mode is on, and devConsole is feat.devmode's
	// (FEAT-065) pause-anywhere console wired against it (BUG-122). Both
	// are constructed unconditionally so devConsole.Open exists as a real,
	// reachable object in this binary's own composition root rather than
	// only inside devmode's own test files — closing the specific gap
	// BUG-122 found (`grep -rn "devmode.New|screens/devmode" internal/
	// cmd/`, excluding _test.go, previously returned zero matches).
	//
	// debugState is deliberately constructed with NO header wired
	// (debug.WithHeader) — this Sprint 1 walking skeleton has no save
	// header anywhere in its object graph yet (no serialize.Header is
	// ever constructed in this package), and fabricating one here purely
	// to unblock Enable would be duplicating FEAT-035's own scope ("Wire
	// feat.debugmode into cmd/metropolis so the DebugTouched hygiene
	// guarantee actually operates," still open). The practical
	// consequence, and the honest state of this binary today: debug is
	// permanently off (RequireConsole always denies, AC-DM1 holds
	// trivially and correctly), and even a caller who forced debug on
	// some other way could never successfully Enable it here, because
	// Enable refuses outright with ErrNoHeaderConfigured rather than
	// silently skipping the sticky DebugTouched flag (state.go's own
	// AC-3/AC-12 contract). Full end-to-end "the console can actually be
	// opened in a real build" reachability is FEAT-035's remaining work,
	// not invented here as a side effect of BUG-122.
	debugState *debug.State
	devConsole *devmode.Console

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// engineRunErr is core.Engine.RunCommandLoop's return value (the
	// BUG-020 discipline, carried over from the StubEngine-era boot). It
	// is written exactly once, by the Run goroutine started in bootCore,
	// before that goroutine closes engineDone (below). engineDone's
	// close-then-receive is the happens-before edge that makes this field
	// safe to read from any goroutine once engineDone has been observed
	// closed — including, but not limited to, after shutdown()'s
	// w.wg.Wait() has returned (Wait() cannot return before the same
	// goroutine's deferred wg.Done() runs, which is sequenced after
	// engineDone closes). Reading it any earlier is a data race and a
	// logic error: the Run goroutine may not have exited yet.
	//
	// RunCommandLoop returns nil on a clean ctx-cancelled shutdown and
	// core.ErrPrematureCommandsClose (MET-E014) when Commands() closes out
	// from under it while ctx is still live — the same
	// clean-shutdown-vs-premature-close distinction BUG-020 first fixed
	// for StubEngine.Run. EngineRunErr (below) is how a caller — today,
	// run.go's logEngineShutdown, and BUG-020's own regression test —
	// observes it instead of discarding it.
	engineRunErr error

	// engineDone is closed the instant the Run goroutine returns,
	// independently of viewsLoop's own goroutine and of wg.Wait() (which
	// blocks on BOTH). It exists so a caller can synchronize on "Run has
	// exited and engineRunErr is now readable" without first having to
	// cancel ctx (which would also be racing to stop the engine loop, the
	// exact hazard Run's own doc comment warns about) or wait for
	// viewsLoop to stop too. shutdown()'s w.wg.Wait() remains the primary,
	// production shutdown path; engineDone is the narrower signal
	// BUG-020's regression test needs to observe a premature Commands()
	// close deterministically, before anything cancels ctx.
	engineDone chan struct{}
}

// EngineRunErr returns the error core.Engine.RunCommandLoop exited with
// (BUG-020, carried over to the real engine). Only meaningful after
// engineDone has been observed closed (directly, or via shutdown() having
// returned) — see engineRunErr's doc comment for why reading it any
// earlier races the still-running Run goroutine.
func (w *skeletonWiring) EngineRunErr() error { return w.engineRunErr }

// newBootRegistry constructs the registry.Registry bootCore registers
// modules into. A package-level indirection (not a bare call to
// registry.NewRegistry inside bootCore) purely so tests can inject a
// pre-populated Registry and exercise AC-7's boot-failure path through
// run() itself, not just bootCore directly (see run_test.go).
var newBootRegistry = func() *registry.Registry { return registry.NewRegistry() }

// bootCore wires int.protocol + engine.core (via the composition root) +
// ui.core + ui.screen.map + the module registry (AC-1a/AC-2/AC-5a) and
// starts core.Engine.RunCommandLoop and ui.core.ViewsLoop.Run as
// background goroutines. It does not touch a tcell.Screen — see run.go's
// bootScreen/runInteractive for the screen-dependent half, kept separate
// so a screen-construction failure (AC-7's "e.g. no compatible terminal")
// never leaves engine/protocol goroutines dangling: callers must call
// (*skeletonWiring).shutdown() on any returned wiring, whether or not
// screen construction afterward succeeds.
//
// FEAT-082: this is the flip off StubEngine onto the real core.Engine,
// wired by internal/engine/compose (AC-12/AC-13 of
// feat.compositionroot) — the same compose.Wire the headless driver
// reaches. Any failure here — module registration, the composition root's
// wiring (e.g. market.LoadDefault), or MapScreen's initial Subscribe — is
// returned as a registry-sourced MET-E900 *errs.E (AC-7); bootCore never
// returns a partially-started skeletonWiring on error (any goroutines it
// already started are stopped and waited on before returning).
func bootCore(correlationID string, reg *registry.Registry) (*skeletonWiring, error) {
	if err := registerSkeletonModules(reg, correlationID); err != nil {
		return nil, err
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)

	ctx, cancel := context.WithCancel(context.Background())

	// BUG-122: construct feat.debugmode's State first so its AllowSpeed8x
	// method can be injected as the real Engine's Speed8xGate (the same
	// wiring headless.Run uses), then wire feat.devmode's Console against
	// it — see the debugState/devConsole field doc comment for why no
	// header is wired and what that means for Enable today.
	dbgState := debug.NewState()

	// FEAT-082: build the real engine and wire the baseline-one hook set
	// through the single composition path (AC-12/AC-13). A wiring failure
	// (e.g. market.LoadDefault cannot resolve data/market.json) is a loud
	// boot failure, never a partially-wired engine.
	engine := enginecore.NewEngine(enginecore.WithSpeed8xGate(dbgState.AllowSpeed8x))
	if _, err := compose.Wire(engine, nil); err != nil {
		cancel()
		_ = transport.Close()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{
			"component": "engine.compose",
		})
	}

	w := &skeletonWiring{
		correlationID: correlationID,
		registry:      reg,
		transport:     transport,
		engine:        engine,
		viewStore:     core.NewViewStore(),
		mapScreen:     mapscreen.NewMapScreen(correlationID, widgets.DefaultPalette),
		ctx:           ctx,
		cancel:        cancel,
		engineDone:    make(chan struct{}),
	}
	w.viewsLoop = core.NewViewsLoop(transport, w.viewStore, correlationID)
	w.debugState = dbgState
	w.devConsole = devmode.New(
		devmode.WithRequireConsole(w.debugState.RequireConsole),
		devmode.WithEnable(func(cid string) error {
			return w.debugState.Enable(debug.SourcePalette, cid)
		}),
		devmode.WithInspect(w.debugState.InspectEntity),
		devmode.WithSubmitFeedback(w.debugState.SubmitFeedback),
		devmode.WithPause(func(cid string) error {
			return sendPauseCommand(transport, cid)
		}),
	)

	w.wg.Add(2)
	go func() {
		defer w.wg.Done()
		w.engineRunErr = engine.RunCommandLoop(ctx, transport)
		close(w.engineDone)
	}()
	go func() { defer w.wg.Done(); w.viewsLoop.Run(ctx.Done()) }()

	// F1's own "f1.viewport" subscribe (AC-1a: issued through MapScreen's
	// real, already-accepted Subscribe method — internal/ui/screens/map's
	// own public API — never a hand-rolled Command literal standing in
	// for it). With the real engine, "f1.viewport" is not yet a served view
	// (v1 serves "engine.status" only), so this Subscribe is issued and the
	// engine rejects it — the honest baseline-one state, recorded here: the
	// map screen's real viewport rendering is its own follow-up, not this
	// flip's scope.
	if err := w.mapScreen.Subscribe(transport.SendCommand); err != nil {
		w.shutdown()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{
			"component": "ui.screen.map.Subscribe",
		})
	}

	return w, nil
}

// sendPauseCommand sends a real protocol.KindPause Command through
// transport (feat.devmode AC-DM2: opening the console pauses the sim) —
// mirrors MapScreen.Subscribe's exact command-construction pattern
// (internal/ui/screens/map/screen.go's Subscribe method) rather than a
// hand-rolled bypass of int.protocol's envelope. It does not wait for or
// interpret the returned CommandResult: devmode.PauseFunc's contract
// (console.go) is "report an error if the pause request itself could not
// be issued," and core.Engine.handlePause (internal/engine/core/commands.go)
// has no rejection branch for a well-formed Pause — the same
// fire-and-let-Validate-be-the-only-failure-mode posture bootCore's own
// mapScreen.Subscribe call above already uses.
func sendPauseCommand(transport protocol.Transport, correlationID string) error {
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(correlationID),
		Kind:            protocol.KindPause,
		Payload:         protocol.PausePayload{},
	}
	return transport.SendCommand(cmd)
}

// shutdown cancels the background goroutines bootCore started, waits for
// both to fully exit, and only then closes the transport.
//
// Order matters here: StubEngine.Run and ui.core.ViewsLoop.Run both
// select on ctx.Done() as well as their respective channels, so
// cancelling ctx is sufficient to stop them without needing
// transport.Close() at all — and closing the transport BEFORE they have
// actually exited would race the engine goroutine's still-in-flight
// SendResult/SendDelta calls against Close()'s channel-close, which
// protocol.InProcTransport's own SendResult docs don't guarantee is race-
// free (the "stops accepting commands" check races the send it guards,
// same shape as any check-then-act on a separate signal channel).
// Waiting for both goroutines to return before closing sidesteps that
// window entirely rather than depending on a fix to that package (which,
// per this item's scope, belongs to int.protocol's own loop, not
// feat.skeleton's).
func (w *skeletonWiring) shutdown() {
	w.cancel()
	w.wg.Wait()
	_ = w.transport.Close()
}

// mapDrawFunc adapts MapScreen into a core.DrawFunc (AC-1a's render half):
// each render tick, it applies whatever "f1.viewport" patch ui.core's
// ViewsLoop has most recently published (per protocol.md's "last frame
// stands" policy — see internal/protocol/transport.go's outbound
// drop/stale doc) and surfaces the same subscription's staleness flag,
// then draws.
//
// There is exactly one live subscription in this skeleton binary (F1's),
// so vm.Patches/vm.Stale hold at most one entry; the loop below takes
// that sole entry (if any) rather than needing to track its
// SubscriptionID explicitly — MapScreen's own doc comment describes
// exactly this caller responsibility ("the caller is expected to route
// only Deltas belonging to this screen's subscription into ApplyPatch").
// A future multi-subscription screen would need real ID-based routing;
// noted here rather than silently generalized, since inventing that
// routing is out of this item's scope (only one F-screen exists yet).
func mapDrawFunc(ms *mapscreen.MapScreen) core.DrawFunc {
	return func(back *core.Buffer, vm *core.ViewModels) {
		for id, patch := range vm.Patches {
			ms.ApplyPatch(patch)
			ms.SetStale(vm.Stale[id])
			break
		}
		w, h := back.Size()
		ms.Render(back, core.Rect{X: 0, Y: 0, W: w, H: h})
	}
}
