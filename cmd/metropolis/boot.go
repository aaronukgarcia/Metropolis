package main

import (
	"context"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	enginecore "github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/debug"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
	"github.com/aaronukgarcia/Metropolis/internal/ui/router"
	"github.com/aaronukgarcia/Metropolis/internal/ui/screens/devmode"
	financescreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/finance"
	mapscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/map"
	servicesscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/services"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// codeBootFailure is MET-E900 (data/errors.json, range E900-E999 reserved
// for feat.skeleton): the one registry-sourced code every boot-time
// failure in this package is raised under (AC-7 — GR#7 forbids ad hoc
// error strings even at the composition root). ctx["component"] names
// which wired dependency failed, so a startup failure is always traceable
// to a specific piece of the skeleton, never just "something broke."
const codeBootFailure = "MET-E900"

// codePumpShutdownTimeout is MET-E901 (data/errors.json, feat.skeleton's
// E900-E949 range): logged, never returned as a boot failure, when
// shutdown()'s bounded join on the subscription pump goroutine's done
// channel (F2/R3, FEAT-208 increment 1) times out — see shutdown()'s own
// doc comment for why this can happen (a DeltaSink that blocks
// indefinitely or reenters Publish, both prohibited by
// engine/core.DeltaSink's contract but not mechanically preventable) and
// why proceeding with transport.Close() anyway, rather than hanging
// forever, is the correct degrade.
const codePumpShutdownTimeout = "MET-E901"

// pumpShutdownJoinTimeout bounds shutdown()'s wait on the subscription
// pump goroutine's done channel (R3, independent round r2/r3, FEAT-208
// increment 1). No existing shutdown-timeout idiom was found elsewhere
// in this codebase to mirror (checked cmd/, internal/harness/headless —
// neither had one before this), so this uses the round's own
// fallback value. A package-level var, not a const, ONLY so
// feat208_pump_shutdown_test.go can lower it for a fast, deterministic
// proof of the timeout path itself (a real 5s wait would make that test
// needlessly slow) — production code never reassigns it; it is 5s for
// every real caller.
var pumpShutdownJoinTimeout = 5 * time.Second

// feat208PrimeTimeout bounds bootCore's synchronous Subscribe-priming
// handshake for each FEAT-208 increment-2 router-bound screen (finance,
// services) — see primeScreenSubscription's doc comment for why this
// handshake exists at all: protocol.CommandResult carries no
// SubscriptionID field, so the only way a caller learns the
// SubscriptionID a Subscribe command produced is from the FIRST Delta
// that echoes the Subscribe command's own CorrelationID
// (engine/core/subscribe.go's pendingCorrID discipline, "echoed on the
// next delta only, then cleared"). This handshake must complete BEFORE
// router.Router.Run starts draining the transport (a second concurrent
// reader of the same transport channels would split delivery
// non-deterministically — ui/router/doc.go's "single writer of the front
// snapshot" rule, GR#21) — bootCore is therefore the ONLY reader of
// transport.Deltas()/Results() during this window. Wall-clock bounded,
// not sim-Tick bounded, because this runs entirely at boot before the
// sim clock or Router are driving anything — mirrors
// pumpShutdownJoinTimeout's identical wall-clock-at-the-seam precedent
// immediately above. A package-level var, not a const, so a test can
// lower it for a fast, deterministic proof of the timeout path.
var feat208PrimeTimeout = 5 * time.Second

// feat208PilotServiceID/feat208PilotFundingStep/feat208PilotFundingKeyPath
// configure FEAT-208 increment 3's F4 funding-adjust input call site
// (servicesscreen.RegisterFundingAdjustKeys, wired in bootCore below).
// "clinic-1" is a documented PLACEHOLDER target, not a data-file-sourced
// roster read: engine.build has no automatic bridge into
// engine.services' instance registry yet in baseline one (the same
// documented gap compose/services_publish.go's own doc comment names for
// the capacityDemand publish side), so no real registered ServiceID is
// guaranteed live at boot time regardless of which one this constant
// names — a real multi-service slider selector is out of this pilot's
// rails (RegisterFundingAdjustKeys' own doc comment). The mnemonic path
// ("s" "f" as the leader prefix — "services funding" — "+"/"-" as the two
// terminal actions) is a plain leader-tree path, deliberately NOT
// digit-led: ui.keys' grammar.go Feed reserves every bare digit token at
// idle for its count-prefix accumulation (AC-5), so a path starting "4"
// (as in "F4") could never actually be reached via Feed — this is not a
// spec-mandated binding either way, since UI-SPEC names no funding-adjust
// keybinding today.
const (
	feat208PilotServiceID   = "clinic-1"
	feat208PilotFundingStep = 0.05
)

var feat208PilotFundingKeyPath = []string{"s", "f"}

// screenID{Map,Finance,Services} are FEAT-211 increment 1's
// core.ScreenID values (internal/ui/core/screen_registry.go) — the exact
// set the design's own increment-1 scope names (design §7(f) point 1):
// "map (F1), finance (F2), services (F4) — with the pilot funding keys
// reachable from a real keyboard once F4 is active." Plain, short,
// human-legible strings (ScreenID's own doc comment) rather than
// F-key-numbered constants, since a future increment (trade/census/proj/
// districts/menu/debug) will add more ScreenIDs than there are still-free
// low F-keys worth memorising by number.
const (
	screenIDMap      core.ScreenID = "map"
	screenIDFinance  core.ScreenID = "finance"
	screenIDServices core.ScreenID = "services"
)

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
// ui.router's Router (FEAT-208 increment 2 — see router field's own doc
// comment for the ViewsLoop-to-Router transition this dispatch performs)
// draining its Delta/Result/Event stream, and the real
// ui.screen.finance/ui.screen.services/ui.screen.map screens bound
// against it — exactly AC-1a's "real components end to end, nothing
// mocked." Screen/render/input wiring (the tcell-dependent half) is
// deliberately NOT part of this struct — see run.go — so tests can
// exercise this whole slice headless, without a terminal (tcell.Screen is
// only needed once rendering starts).
type skeletonWiring struct {
	correlationID string
	registry      *registry.Registry
	transport     *protocol.InProcTransport
	engine        *enginecore.Engine

	// viewStore is ui.core's ViewStore — kept ONLY because
	// core.NewRenderLoop's constructor (run.go) still requires one as a
	// parameter for mapDrawFunc's vm.Patches/vm.Stale reads. FEAT-208
	// increment 2 retires ui.core.ViewsLoop as this binary's transport
	// consumer (replaced by router, below) — viewStore is therefore now
	// PERMANENTLY EMPTY in this binary (NewViewStore's own initial empty
	// snapshot, never published to again): nothing writes into it any
	// more. This is not a functional regression — "f1.viewport" was
	// already never a served view before this increment (mapScreen's own
	// Subscribe call below is fire-and-forget and has always been
	// rejected by the real engine, see mapScreen's doc comment), so
	// ViewsLoop was already never populating viewStore with anything in
	// this binary even before the swap. A future increment that adds
	// "f1.viewport" to compose's viewRegistrationOrder (design's §6 last
	// fast-follow) will need to either (a) prime+bind mapScreen through
	// router the same way financeScreen/servicesScreen are primed below
	// and give mapDrawFunc a router-fed adapter instead of viewStore, or
	// (b) teach ui.core to expose a writable ViewStore surface router can
	// publish through (doc.go's own "widening core's exported surface...
	// is a ui.core change, out of scope here" note) — not decided here,
	// flagged for that follow-up.
	viewStore *core.ViewStore

	// router is ui.router's Router (BOW MOD-115, ASM-1482), FEAT-208
	// increment 2's boot-time swap: it now owns the transport's
	// Results()/Deltas()/Events() drain in place of ui.core's ViewsLoop
	// (which this binary constructed but never actually needed — see
	// viewStore's doc comment above). router.Run(ctx) is started as the
	// ONE dedicated transport-draining goroutine (ui/router/doc.go's
	// single-writer rule) only AFTER financeScreen/servicesScreen have
	// each been primed and bound (below) — never concurrently with the
	// priming reads, which read the SAME transport channels directly and
	// would otherwise race Router.Run for delivery (GR#21).
	router *router.Router

	// financeScreen/servicesScreen are FEAT-208 increment 2's two real,
	// router-bound F-screens: internal/ui/screens/finance's Screen
	// subscribed to "f2.finance" and internal/ui/screens/services'
	// Screen subscribed to "f4.services", each bound into router via
	// BindSubscription once primeScreenSubscription has learned their
	// real SubscriptionID (see that function's doc comment). Neither is
	// wired into any render/input path yet in this dispatch's scope
	// (that is F2/F4's own screen-rendering integration, not this
	// composition-root increment) — they exist here so their Delta
	// stream is genuinely live end to end, provable via HaveData()/
	// BalanceSheet()/CapacityDemand() from a test that reaches into
	// skeletonWiring, exactly as mapScreen already is for "f1.viewport".
	financeScreen  *financescreen.Screen
	servicesScreen *servicesscreen.Screen

	// keyGrammar is ui.keys' leader-key state machine (MOD-011), FEAT-208
	// increment 3's own real input call site for F4's funding sliders
	// (servicesscreen.RegisterFundingAdjustKeys, registered below against
	// this SAME instance). See that method's own doc comment for the
	// honestly-recorded scope boundary: this is a real, tested,
	// production KeyGrammar with a real action registered on it — but
	// run.go does not yet feed live tcell key events into it (no F-screen
	// has a "currently active" concept in this binary yet, mapScreen
	// being the only one ever rendered) — pre-existing screen-switching
	// infrastructure this dispatch's rails do not cover.
	keyGrammar *keys.KeyGrammar

	// screens is FEAT-211 increment 1's ActiveScreen state owner
	// (internal/ui/core/screen_registry.go, design doc
	// E:\git\metropolis-status\active-screen-design.md): holds map/
	// finance/services in registration order (GR#21), map first so it
	// stays the initially active screen (matching this binary's
	// pre-FEAT-211 baseline — mapScreen was always what rendered).
	// chromeGrammar is the always-fed global grammar F1/F2/F4 are
	// registered against (run.go's input routing feeds this FIRST, every
	// keystroke, before the active screen's own grammar — design §7(b)):
	// a screen switch is chrome-global by design, reachable regardless
	// of which screen is currently active or what leader sequence it may
	// have pending.
	screens       *core.ScreenRegistry
	chromeGrammar *keys.KeyGrammar

	mapScreen *mapscreen.MapScreen

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
	// independently of router's own Run goroutine and of wg.Wait() (which
	// blocks on BOTH). It exists so a caller can synchronize on "Run has
	// exited and engineRunErr is now readable" without first having to
	// cancel ctx (which would also be racing to stop the engine loop, the
	// exact hazard Run's own doc comment warns about) or wait for
	// router.Run to stop too. shutdown()'s w.wg.Wait() remains the primary,
	// production shutdown path; engineDone is the narrower signal
	// BUG-020's regression test needs to observe a premature Commands()
	// close deterministically, before anything cancels ctx.
	engineDone chan struct{}

	// pumpDone is the done channel StartSubscriptionPump returns (F2,
	// independent round r1, FEAT-208 increment 1): closed exactly once,
	// when the subscription pump goroutine actually exits. Previously
	// nothing tracked or joined this goroutine at shutdown at all —
	// shutdown() now selects on this before closing the transport,
	// mirroring engineDone's own close-then-select join idiom above,
	// applied to the pump goroutine instead of RunCommandLoop's.
	pumpDone <-chan struct{}

	// routerRunErr is router.Router.Run's return value, written exactly
	// once by the goroutine bootCore starts for it, mirroring
	// engineRunErr's own "write once, read only after the owning
	// goroutine is known to have exited" discipline. Router.Run returns
	// ctx.Err() (context.Canceled) on the ordinary shutdown path — that
	// is expected, not a failure — or nil if the transport's channels
	// were closed first (transport.Close() called before ctx cancel).
	// Not surfaced through a dedicated done channel the way engineDone
	// is: nothing in this binary today needs to observe router's exit
	// before shutdown()'s own w.wg.Wait() returns (unlike BUG-020's
	// engineDone need, which predates router's existence).
	routerRunErr error
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
	// FEAT-208 increment 2: router replaces ui.core.ViewsLoop as this
	// binary's transport consumer — see the router/viewStore field doc
	// comments above for the full rationale. Constructing it here does
	// NOT start it draining anything yet (router.New never touches the
	// transport's channels until Run is called) — Run is deliberately
	// started further below, only after financeScreen/servicesScreen
	// have been primed and bound via primeScreenSubscription, which reads
	// the same channels directly and must be the transport's ONLY reader
	// until it finishes (see feat208PrimeTimeout's doc comment).
	w.router = router.New(transport, router.WithCorrelationID(correlationID))
	// Each screen mints its OWN correlation ID (never bootCore's shared
	// correlationID param, which mapScreen below still reuses per its
	// pre-existing, pre-FEAT-208 construction) so each Subscribe command
	// below carries a distinct CorrelationID — primeScreenSubscription
	// matches its own screen's first Delta/Result by exactly this value,
	// and a shared ID across two concurrent Subscribes would make that
	// match ambiguous.
	financeCorrID := errs.NewCorrelationID()
	servicesCorrID := errs.NewCorrelationID()
	w.financeScreen = financescreen.New(financeCorrID)
	w.servicesScreen = servicesscreen.New(servicesCorrID)
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

	// FEAT-208: start the subscription pump so registered views (today:
	// "engine.status", plus whatever compose.Wire's viewRegistrationOrder
	// added — see internal/engine/compose/services_publish.go) actually
	// publish deltas. Previously never started in this binary at all
	// (the FEAT-208 design survey's own finding: "even engine.status is
	// not live in the shipped binary today") — StartSubscriptionPump can
	// only fail on a struct-copied Engine (BUG-019) or a second call on
	// the same Engine (F1a, ErrSubscriptionPumpAlreadyStarted), neither
	// of which engine here ever is/does, so this call cannot fail in
	// practice; wrapped the same loud-not-silent way every other
	// bootCore failure is (MET-E900) rather than assumed infallible.
	// pumpDone (F2) is stored on w below and joined in shutdown() before
	// transport.Close().
	pumpDone, err := engine.StartSubscriptionPump(ctx, transport)
	if err != nil {
		cancel()
		_ = transport.Close()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{
			"component": "engine.core.StartSubscriptionPump",
		})
	}
	w.pumpDone = pumpDone

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.engineRunErr = engine.RunCommandLoop(ctx, transport)
		close(w.engineDone)
	}()

	// FEAT-208 increment 2: prime + bind financeScreen ("f2.finance") and
	// servicesScreen ("f4.services") BEFORE router.Run starts (this is
	// the only window in which bootCore itself reads transport.Deltas()/
	// Results() directly — see primeScreenSubscription's doc comment).
	// Both views are real, registered compose.Wire entries
	// (viewRegistrationOrder, internal/engine/compose/{finance,services}_publish.go)
	// as of this increment, so both Subscribes are expected to be
	// accepted and to produce a first Delta — unlike mapScreen's
	// "f1.viewport" below, which is NOT registered and is not primed.
	//
	// primed is the shared "already bound during this priming window"
	// forwarding table both calls below share — see primeScreenSubscription's
	// doc comment for why this is required: signalSubscriptionPump's
	// coalescing means a SINGLE pump wake can publish deltas for EVERY
	// currently-live subscription in one cycle (subscribe.go's Publish
	// iterates all of them), not just the one just-subscribed. Priming
	// servicesScreen first and financeScreen second means financeScreen's
	// own Subscribe call can (and, empirically, sometimes does) wake a
	// pump cycle that ALSO republishes servicesScreen's already-bound
	// subscription — that second services delta must reach servicesScreen
	// too, not be silently dropped just because its CorrelationID isn't
	// financeScreen's.
	primed := make(map[protocol.SubscriptionID]func(protocol.Delta))
	if err := primeScreenSubscription(transport, w.router, primed, servicesCorrID, servicesscreen.ViewSubscriptionName,
		func() error { return w.servicesScreen.Subscribe(transport.SendCommand) },
		w.servicesScreen.BindSubscription,
		w.servicesScreen.ApplyDelta,
	); err != nil {
		w.cancel()
		w.wg.Wait()
		_ = transport.Close()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{
			"component": "ui.screen.services.Subscribe",
		})
	}
	if err := primeScreenSubscription(transport, w.router, primed, financeCorrID, financescreen.ViewSubscriptionName,
		func() error { return w.financeScreen.Subscribe(transport.SendCommand) },
		w.financeScreen.BindSubscription,
		w.financeScreen.ApplyDelta,
	); err != nil {
		w.cancel()
		w.wg.Wait()
		_ = transport.Close()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{
			"component": "ui.screen.finance.Subscribe",
		})
	}

	// FEAT-208 increment 3: the F4 funding-slider INPUT call site
	// (servicesscreen.RegisterFundingAdjustKeys' own doc comment has the
	// full scope note — this constructs the real, production
	// keys.KeyGrammar it registers against, and wires a send function
	// that ALSO registers the outgoing command's CorrelationID with
	// w.router BEFORE sending it, so its CommandResult is routed back to
	// servicesScreen.ApplyResult (router.RegisterResultHandler's own
	// contract: register before or at the SendCommand call, never after).
	//
	// Destructive round r1 correction (finding F-A): an earlier version of
	// this comment said servicesScreen reused ONE fixed CorrelationID
	// (s.correlationID) for every SetFunding command, mirroring
	// financescreen.BorrowLoan/RepayLoan/SetTaxRate's own convention. That
	// was a real, reachable bug for THIS screen specifically: two
	// SetFunding commands sharing one CorrelationID collapse onto
	// router.RegisterResultHandler's ONE pending-result slot per
	// CorrelationID (its own contract — "one CommandResult per registered
	// CorrelationID, then consumed"), so the second command's
	// CommandResult became an unrecoverable router.ErrRouteMiss, never
	// reaching ApplyResult — proven reachable through this EXACT
	// sendServicesCommand/RegisterFundingAdjustKeys call pattern (a
	// same-key double-press before the first result returns) by
	// cmd/metropolis/feat208_inc3_destructive_test.go's
	// TestRegression_DuplicateCorrelationID_BothCommandResultsDelivered.
	// servicesscreen.Screen.SetFunding now mints a FRESH
	// protocol.CorrelationID per call (screen.go) instead — this closure's
	// per-call RegisterResultHandler registration was already correct in
	// SHAPE (it always re-registers before every send, exactly as needed);
	// the bug was that every registration used the SAME key, silently
	// overwriting the previous one's still-pending entry. No change was
	// needed here beyond this correction — see screen.go for the actual
	// fix.
	w.keyGrammar = keys.NewKeyGrammar(nil, 0, 0, correlationID)
	sendServicesCommand := func(cmd protocol.Command) error {
		w.router.RegisterResultHandler(cmd.CorrelationID, w.servicesScreen)
		return transport.SendCommand(cmd)
	}
	if err := w.servicesScreen.RegisterFundingAdjustKeys(w.keyGrammar, sendServicesCommand, feat208PilotServiceID, feat208PilotFundingStep, feat208PilotFundingKeyPath); err != nil {
		w.cancel()
		w.wg.Wait()
		_ = transport.Close()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{
			"component": "ui.screen.services.RegisterFundingAdjustKeys",
		})
	}

	// FEAT-211 increment 1: the ScreenRegistry (internal/ui/core/
	// screen_registry.go) — the ActiveScreen state owner that makes
	// finance/services (and the funding keys just registered above)
	// actually reachable from a keyboard, closing the exact gap
	// RegisterFundingAdjustKeys' own doc comment named ("pre-existing
	// screen-switching infrastructure this pilot's rails do not cover").
	// map is registered FIRST so it stays the initially active screen —
	// Register's own documented default — matching this binary's
	// pre-FEAT-211 baseline (mapScreen was always what rendered).
	w.screens = core.NewScreenRegistry(correlationID)
	if err := w.screens.Register(core.ScreenEntry{ID: screenIDMap, Draw: mapDrawFunc(w.mapScreen)}); err != nil {
		w.cancel()
		w.wg.Wait()
		_ = transport.Close()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{"component": "ui.core.ScreenRegistry.Register", "id": string(screenIDMap)})
	}
	if err := w.screens.Register(core.ScreenEntry{ID: screenIDFinance, Draw: financeDrawFunc(w.financeScreen)}); err != nil {
		w.cancel()
		w.wg.Wait()
		_ = transport.Close()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{"component": "ui.core.ScreenRegistry.Register", "id": string(screenIDFinance)})
	}
	// services registers its OWN w.keyGrammar (constructed above,
	// carrying the real funding-adjust actions) — this is what makes "s
	// f +"/"s f -" reachable once F4 is active: run.go's input routing
	// feeds registry.ActiveGrammar() only when the chrome/global grammar
	// did not itself dispatch for a given keystroke (design §7(b)).
	if err := w.screens.Register(core.ScreenEntry{ID: screenIDServices, Draw: servicesDrawFunc(w.servicesScreen), Grammar: w.keyGrammar}); err != nil {
		w.cancel()
		w.wg.Wait()
		_ = transport.Close()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{"component": "ui.core.ScreenRegistry.Register", "id": string(screenIDServices)})
	}

	// chromeGrammar is the ALWAYS-fed global grammar (design §7(b)):
	// F1/F2/F4 fire regardless of which screen is currently active or
	// what leader sequence it may have pending (RegisterGlobal's own
	// "fires even mid-sequence" contract, ui/keys/grammar.go). Each
	// Action.Run calls straight back into w.screens.Activate — the ONE
	// switch primitive both F-key switching and (a future increment's)
	// drill-through share (design §7(e)) — and discards Activate's error
	// deliberately: every ID registered here is one of the three IDs
	// just registered above, in this same function, so an Activate
	// failure here would mean w.screens itself is broken (defensive
	// only, matches keys.Action.Run's signature, which cannot itself
	// return an error — the same constraint RegisterFundingAdjustKeys'
	// own countAdjust closure documents).
	w.chromeGrammar = keys.NewKeyGrammar(nil, 0, 0, correlationID)
	fKeyGlobal := func(id core.ScreenID, name string) keys.Action {
		return keys.Action{Name: name, Run: func(keys.ActionArgs) { _ = w.screens.Activate(id) }}
	}
	if err := w.chromeGrammar.RegisterGlobal(keys.Key{Special: "F1"}, fKeyGlobal(screenIDMap, "Switch to Map (F1)")); err != nil {
		w.cancel()
		w.wg.Wait()
		_ = transport.Close()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{"component": "ui.keys.RegisterGlobal", "key": "F1"})
	}
	if err := w.chromeGrammar.RegisterGlobal(keys.Key{Special: "F2"}, fKeyGlobal(screenIDFinance, "Switch to Finance (F2)")); err != nil {
		w.cancel()
		w.wg.Wait()
		_ = transport.Close()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{"component": "ui.keys.RegisterGlobal", "key": "F2"})
	}
	if err := w.chromeGrammar.RegisterGlobal(keys.Key{Special: "F4"}, fKeyGlobal(screenIDServices, "Switch to Services (F4)")); err != nil {
		w.cancel()
		w.wg.Wait()
		_ = transport.Close()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{"component": "ui.keys.RegisterGlobal", "key": "F4"})
	}

	// F1's own "f1.viewport" subscribe (AC-1a: issued through MapScreen's
	// real, already-accepted Subscribe method — internal/ui/screens/map's
	// own public API — never a hand-rolled Command literal standing in
	// for it). With the real engine, "f1.viewport" is STILL not a served
	// view this increment (only "f2.finance"/"f4.services"/"engine.status"
	// are registered — the design's §6 "f1.viewport" fast-follow has not
	// landed), so this Subscribe is issued and the engine rejects it — the
	// honest baseline-one state, recorded here: the map screen's real
	// viewport rendering is its own follow-up, not this flip's scope. It
	// is deliberately NOT run through primeScreenSubscription (which would
	// simply time out waiting for a Delta that can never arrive) — it
	// keeps its pre-existing fire-and-forget shape.
	if err := w.mapScreen.Subscribe(transport.SendCommand); err != nil {
		w.cancel()
		w.wg.Wait()
		_ = transport.Close()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{
			"component": "ui.screen.map.Subscribe",
		})
	}

	// NOW start router.Run — the ONE dedicated transport-draining
	// goroutine for the rest of this process's life (ui/router/doc.go).
	// Priming above is complete, so router.Run is the transport's only
	// reader from this point forward.
	w.wg.Add(1)
	go func() { defer w.wg.Done(); w.routerRunErr = w.router.Run(ctx) }()

	return w, nil
}

// primeScreenSubscription performs the one synchronous handshake every
// FEAT-208 increment-2 router-bound screen needs at boot: it calls
// subscribe() (the screen's own real Subscribe method, e.g.
// financeScreen.Subscribe(transport.SendCommand)), then reads
// transport.Results()/Deltas() DIRECTLY (bypassing router — router has
// not started yet, see w.router's own doc comment) until it has observed
// BOTH the accepted CommandResult and the first Delta carrying
// correlationID (the exact CorrelationID the screen's Subscribe call
// used) — subscribe.go's "pendingCorrID... echoed on the next delta
// only" contract is what makes this possible: the first Delta for a
// fresh Subscribe always carries the causing command's own
// CorrelationID, exactly once.
//
// Once both are observed, it calls bind(subscriptionID) (the screen's own
// BindSubscription) AND router.BindSubscription(subscriptionID, ...) so
// every SUBSEQUENT delta for this subscription — which router.Run will
// see once it starts — is routed to the same screen; then it calls
// applyDelta(firstDelta) directly (since router never saw this first
// delta — it was consumed here, before Run started) so the screen's
// first real data is not lost. It also registers subscriptionID into
// primed (see below) so a LATER call to primeScreenSubscription (priming
// a different screen) can still forward deltas belonging to THIS
// subscription if one arrives during its own wait.
//
// This exists because protocol.CommandResult has no SubscriptionID field
// (envelope.go) — router.RegisterResultHandler only delivers
// CommandResults, not Deltas, so it cannot by itself learn a Subscribe's
// resulting SubscriptionID. This handshake is boot.go's own bridge across
// that gap, not a router or protocol change (out of this dispatch's
// scope — see the FEAT-208 build brief).
//
// primed is a shared map every sequential primeScreenSubscription call in
// the same bootCore invocation passes the SAME instance of (bootCore's
// own `primed := make(map[...])`, threaded through both calls). It exists
// because engine.core's signalSubscriptionPump is coalescing
// (commands.go's own doc comment: "multiple commands... collapse into a
// single recompute") and subscribe.go's Publish republishes EVERY live
// subscription on each pump wake, not just the one that just subscribed
// — so priming a SECOND screen can (and, empirically, sometimes does)
// observe a pump cycle that ALSO republishes the FIRST screen's
// already-bound subscription (its Seq advancing past 1). Without
// forwarding, that delta's CorrelationID would not match `want` (only a
// subscription's very first delta ever carries one) and it would be
// silently dropped — losing a real update for the first screen AND
// leaving router's own SeqTracker (once it starts) expecting a Seq it
// will never see, misreporting a gap. Every delta whose SubscriptionID is
// already in primed is therefore forwarded to its owner's applyDelta
// directly here, exactly mirroring what router.handleDelta will do for
// every later delta once Run starts (minus router's own SeqTracker
// bookkeeping, which is irrelevant here since nothing has bound through
// router's Observe path yet during priming).
//
// applyDelta's type is func(protocol.Delta), matching every real screen's
// exported ApplyDelta method exactly (finance.Screen, services.Screen) —
// passed as a bound method value, never wrapped, so a panic inside a
// screen's own ApplyDelta during priming surfaces exactly as it would
// during a normal boot failure (loud, not swallowed) rather than being
// silently absorbed by an adapter.
func primeScreenSubscription(
	transport *protocol.InProcTransport,
	rt *router.Router,
	primed map[protocol.SubscriptionID]func(protocol.Delta),
	correlationID string,
	viewName string,
	subscribe func() error,
	bind func(protocol.SubscriptionID),
	applyDelta func(protocol.Delta),
) error {
	if err := subscribe(); err != nil {
		return err
	}
	want := protocol.CorrelationID(correlationID)
	deadline := time.After(feat208PrimeTimeout)
	gotResult, gotDelta := false, false
	for !gotResult || !gotDelta {
		select {
		case r, ok := <-transport.Results():
			if !ok {
				return errs.New(codeBootFailure, correlationID, map[string]any{
					"component": "primeScreenSubscription", "view": viewName, "cause": "transport.Results() closed while priming",
				})
			}
			if r.CorrelationID != want {
				// A CommandResult for a CorrelationID that is NOT this
				// handshake's own — and not any earlier handshake's
				// either, since each command produces exactly one
				// CommandResult and an already-primed screen's own
				// Subscribe result was already consumed by ITS OWN prior
				// primeScreenSubscription call. bootCore is documented
				// (and, for as long as priming runs, REQUIRED) to be the
				// transport's only reader — no other command can
				// legitimately be in flight during this window (mapScreen's
				// own Subscribe is issued strictly AFTER both primes
				// complete; render/input, which could issue arbitrary
				// commands, only start after bootCore returns). A stray
				// result here therefore means some caller broke that
				// invariant — a programming error, not a droppable event:
				// silently discarding it (the previous behaviour) would
				// consume a real Go channel receive that a router-
				// registered handler could never be delivered afterward
				// (GR#23 independent round r2/r3 finding — see
				// feat208_priming_destructive_test.go's own attack). Fail
				// loud instead, naming the foreign CorrelationID, so this
				// constraint violation surfaces as a boot failure rather
				// than a silently eaten result.
				return errs.New(codeBootFailure, correlationID, map[string]any{
					"component": "primeScreenSubscription", "view": viewName,
					"cause":                "observed a CommandResult for a foreign CorrelationID during the priming window — bootCore must be the transport's only reader while priming runs; a third concurrent command at boot is a programming error, not a droppable event",
					"foreignCorrelationID": string(r.CorrelationID),
				})
			}
			if !r.Accepted {
				return errs.New(codeBootFailure, correlationID, map[string]any{
					"component": "primeScreenSubscription", "view": viewName, "cause": "Subscribe rejected",
				})
			}
			gotResult = true
		case d, ok := <-transport.Deltas():
			if !ok {
				return errs.New(codeBootFailure, correlationID, map[string]any{
					"component": "primeScreenSubscription", "view": viewName, "cause": "transport.Deltas() closed while priming",
				})
			}
			if d.CorrelationID != want {
				// Not this handshake's own first delta. If it belongs to
				// an already-primed subscription (coalesced pump wake —
				// see primed's own doc comment above), forward it to that
				// subscription's owner directly rather than dropping it.
				if fn, ok := primed[d.SubscriptionID]; ok {
					fn(d)
				}
				continue
			}
			bind(d.SubscriptionID)
			rt.BindSubscription(d.SubscriptionID, deltaReceiverFunc(applyDelta))
			primed[d.SubscriptionID] = applyDelta
			applyDelta(d)
			gotDelta = true
		case <-deadline:
			return errs.New(codeBootFailure, correlationID, map[string]any{
				"component": "primeScreenSubscription", "view": viewName, "cause": "timed out waiting for Subscribe's own Result/Delta",
			})
		}
	}
	return nil
}

// deltaReceiverFunc adapts a bare func(protocol.Delta) into
// router.DeltaReceiver (an ApplyDelta(protocol.Delta) method), so
// primeScreenSubscription can bind router.BindSubscription directly
// against a screen's exported ApplyDelta method value without router
// needing to import (or know about) any concrete screen package —
// preserving ui/router/doc.go's "never imports a concrete screen
// package" contract.
type deltaReceiverFunc func(protocol.Delta)

func (f deltaReceiverFunc) ApplyDelta(d protocol.Delta) { f(d) }

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
// ALL of them (RunCommandLoop, ViewsLoop.Run, AND — F2, independent
// round r1 — the subscription pump) to fully exit, and only then closes
// the transport.
//
// Order matters here: RunCommandLoop, ui.core.ViewsLoop.Run, and the
// subscription pump goroutine all select on ctx.Done() as well as their
// respective channels, so cancelling ctx is sufficient to stop all three
// without needing transport.Close() at all — and closing the transport
// BEFORE they have actually exited would race their still-in-flight
// SendResult/SendDelta calls against Close()'s channel-close, which
// protocol.InProcTransport's own SendResult docs don't guarantee is race-
// free (the "stops accepting commands" check races the send it guards,
// same shape as any check-then-act on a separate signal channel).
// Waiting for every goroutine to return before closing sidesteps that
// window entirely rather than depending on a fix to that package (which,
// per this item's scope, belongs to int.protocol's own loop, not
// feat.skeleton's). Previously (before F2) w.pumpDone did not exist at
// all and the pump goroutine was never joined here — a leak this
// function's own "waits for both to fully exit" claim did not actually
// keep.
//
// BOUNDED join on w.pumpDone (R3, independent round r2/r3, FEAT-208
// increment 1): w.wg.Wait() above still blocks unconditionally on
// RunCommandLoop/ViewsLoop.Run (both are engine.core's own, trusted
// code, and ctx cancellation is proven sufficient to stop them promptly
// — no external DeltaSink implementation can misbehave there). The pump
// goroutine is different: it calls a caller-supplied DeltaSink
// (transport here, but SubscriptionServer.Publish's contract on
// DeltaSink — commands.go — is a caller-facing seam, not an internal
// implementation detail), and r2's independent round proved a
// DeltaSink that blocks indefinitely or reenters Publish (both
// documented-prohibited, neither mechanically preventable) can hang the
// pump goroutine forever. Waiting on w.pumpDone with NO timeout would
// therefore hang process shutdown forever too — exactly what r2's
// TestAttack_ReentrantDeltaSink_DeadlocksThePumpGoroutine warned about.
// pumpShutdownJoinTimeout bounds that wait: on timeout, MET-E901 is
// logged (registry-sourced, GR#1/GR#7 — never a silent give-up) and
// shutdown proceeds to transport.Close() anyway rather than hanging.
// transport is not InProcTransport for this binary today, so this is a
// belt-and-braces degrade against a future DeltaSink swap, not a
// currently-reachable production hang (InProcTransport's own SendDelta
// is documented non-blocking and never re-enters anything).
func (w *skeletonWiring) shutdown() {
	w.cancel()
	w.wg.Wait()
	if w.pumpDone != nil {
		select {
		case <-w.pumpDone:
		case <-time.After(pumpShutdownJoinTimeout):
			_ = errs.New(codePumpShutdownTimeout, w.correlationID, map[string]any{
				"timeoutMs": pumpShutdownJoinTimeout.Milliseconds(),
			})
		}
	}
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

// financeDrawFunc adapts financescreen.Screen into a core.DrawFunc
// (FEAT-211 increment 1, design §7(c)): unlike mapDrawFunc, it reads
// nothing from vm — financeScreen's own state is kept live by
// ApplyDelta, called directly from boot.go's router-bound priming/
// binding (primeScreenSubscription), never through ui.core's
// vm.Patches/vm.Stale path (that mechanism exists for "f1.viewport"
// only, mapScreen's own pre-FEAT-208 wiring — see viewStore's doc
// comment on skeletonWiring). This closure only lays out a 2x2 grid of
// financescreen's existing Render* package functions (RenderPL,
// RenderBalanceSheet, RenderLoans, RenderSliders — each a real, tested
// entry point this package already exports; no new render logic is
// invented here) over whatever the current terminal size is — no screen
// interface change, mirroring mapDrawFunc's own closure-adapter pattern
// exactly (design's own "no shim beyond the closure-adapter pattern
// mapDrawFunc already establishes").
func financeDrawFunc(fs *financescreen.Screen) core.DrawFunc {
	style := tcell.StyleDefault
	return func(back *core.Buffer, _ *core.ViewModels) {
		w, h := back.Size()
		col, row := w/2, h/2

		pl, havePL := fs.PL()
		financescreen.RenderPL(back, core.Rect{X: 0, Y: 0, W: col, H: row}, pl, havePL, style)

		bs, haveBS := fs.BalanceSheet()
		financescreen.RenderBalanceSheet(back, core.Rect{X: col, Y: 0, W: w - col, H: row}, bs, haveBS, style)

		loans, haveLoans := fs.Loans()
		rating, _ := fs.CreditRating()
		history, _ := fs.CreditRatingHistory()
		financescreen.RenderLoans(back, core.Rect{X: 0, Y: row, W: col, H: h - row}, loans, rating, history, fs.LoanRejectedReason(), haveLoans, style)

		sliders, haveSliders := fs.TaxSliders()
		financescreen.RenderSliders(back, core.Rect{X: col, Y: row, W: w - col, H: h - row}, sliders, haveSliders, style)
	}
}

// servicesDrawFunc adapts servicesscreen.Screen into a core.DrawFunc
// (FEAT-211 increment 1, design §7(c)) — same shape and same rationale
// as financeDrawFunc immediately above (servicesScreen's own state is
// kept live by ApplyDelta via router.BindSubscription, never vm). Lays
// out a 2x2 grid of servicesscreen's existing Render* package functions
// (RenderSliders — SVC-1's funding sliders, the exact figures "s f +"/
// "s f -" move — RenderCapacityDemand, RenderResponseTimes,
// RenderWaitingLists).
func servicesDrawFunc(ss *servicesscreen.Screen) core.DrawFunc {
	style := tcell.StyleDefault
	return func(back *core.Buffer, _ *core.ViewModels) {
		w, h := back.Size()
		col, row := w/2, h/2

		sliders, haveSliders := ss.Sliders()
		servicesscreen.RenderSliders(back, core.Rect{X: 0, Y: 0, W: col, H: row}, sliders, ss.FundingRejectedReason(), haveSliders, style)

		cd, haveCD := ss.CapacityDemand()
		servicesscreen.RenderCapacityDemand(back, core.Rect{X: col, Y: 0, W: w - col, H: row}, cd, haveCD, widgets.DefaultPalette, style)

		rt, haveRT := ss.ResponseTimes()
		servicesscreen.RenderResponseTimes(back, core.Rect{X: 0, Y: row, W: col, H: h - row}, rt, haveRT, style)

		wl, haveWL := ss.WaitingLists()
		servicesscreen.RenderWaitingLists(back, core.Rect{X: col, Y: row, W: w - col, H: h - row}, wl, haveWL, style)
	}
}
