package main

import (
	"context"
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/engine/stub"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
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
	stubEngine    *stub.StubEngine
	viewStore     *core.ViewStore
	viewsLoop     *core.ViewsLoop
	mapScreen     *mapscreen.MapScreen

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// newBootRegistry constructs the registry.Registry bootCore registers
// modules into. A package-level indirection (not a bare call to
// registry.NewRegistry inside bootCore) purely so tests can inject a
// pre-populated Registry and exercise AC-7's boot-failure path through
// run() itself, not just bootCore directly (see run_test.go).
var newBootRegistry = func() *registry.Registry { return registry.NewRegistry() }

// bootCore wires int.protocol + harness.stub + ui.core + ui.screen.map +
// the module registry (AC-1a/AC-2/AC-5a) and starts StubEngine.Run and
// ui.core.ViewsLoop.Run as background goroutines. It does not touch a
// tcell.Screen — see run.go's bootScreen/runInteractive for the
// screen-dependent half, kept separate so a screen-construction failure
// (AC-7's "e.g. no compatible terminal") never leaves engine/protocol
// goroutines dangling: callers must call (*skeletonWiring).shutdown() on
// any returned wiring, whether or not screen construction afterward
// succeeds.
//
// Any failure here — module registration, StubEngine construction, or
// MapScreen's initial Subscribe — is returned as a registry-sourced
// MET-E900 *errs.E (AC-7); bootCore never returns a partially-started
// skeletonWiring on error (any goroutines it already started are stopped
// and waited on before returning).
func bootCore(correlationID string, reg *registry.Registry) (*skeletonWiring, error) {
	if err := registerSkeletonModules(reg, correlationID); err != nil {
		return nil, err
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)

	engine, err := stub.NewStubEngine(transport)
	if err != nil {
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{
			"component": "harness.stub",
		})
	}

	ctx, cancel := context.WithCancel(context.Background())

	w := &skeletonWiring{
		correlationID: correlationID,
		registry:      reg,
		transport:     transport,
		stubEngine:    engine,
		viewStore:     core.NewViewStore(),
		mapScreen:     mapscreen.NewMapScreen(correlationID, widgets.DefaultPalette),
		ctx:           ctx,
		cancel:        cancel,
	}
	w.viewsLoop = core.NewViewsLoop(transport, w.viewStore, correlationID)

	w.wg.Add(2)
	go func() { defer w.wg.Done(); _ = engine.Run(ctx) }()
	go func() { defer w.wg.Done(); w.viewsLoop.Run(ctx.Done()) }()

	// F1's own "f1.viewport" subscribe (AC-1a: issued through MapScreen's
	// real, already-accepted Subscribe method — internal/ui/screens/map's
	// own public API — never a hand-rolled Command literal standing in
	// for it).
	if err := w.mapScreen.Subscribe(transport.SendCommand); err != nil {
		w.shutdown()
		return nil, errs.Wrap(codeBootFailure, correlationID, err, map[string]any{
			"component": "ui.screen.map.Subscribe",
		})
	}

	return w, nil
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
