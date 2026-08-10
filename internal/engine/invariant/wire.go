package invariant

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Wire registers a Hook (built from reg and provider, customized by
// opts) against phase on e, via e.RegisterPhaseHook (AC-7). Must be
// called before e's first AdvanceTicks call — see AC-7b below for what
// happens if it is not.
//
// # AC-7b: sealing
//
// If e has already sealed (its first AdvanceTicks call has already run —
// see core.Engine's sealed field doc comment), e.RegisterPhaseHook
// returns core.ErrEngineSealed (MET-E011); Wire detects that specific
// cause and returns it wrapped as ErrWiringAfterSeal (MET-E301) with
// this package's own correlation ID, rather than letting a caller
// mistake a generic-looking registration failure for something else, or
// worse, silently no-op — a caller-ordering mistake here means the
// invariant suite quietly never runs, which is exactly the kind of
// silent failure this whole package exists to prevent elsewhere. The
// original core.ErrEngineSealed cause remains reachable via
// errors.Unwrap (errs.Wrap preserves it) for a caller that wants to
// distinguish "sealed" from any other RegisterPhaseHook failure
// programmatically, not just by reading the message.
func Wire(e *core.Engine, phase core.PhaseKind, reg *Registry, provider SnapshotProvider, opts ...HookOption) error {
	hook := &Hook{engine: e, registry: reg, provider: provider}
	for _, opt := range opts {
		opt(hook)
	}

	if err := e.RegisterPhaseHook(phase, hook); err != nil {
		if regErr, ok := err.(*errs.E); ok && regErr.Code == core.ErrEngineSealed {
			return errs.Wrap(ErrWiringAfterSeal, errs.NewCorrelationID(), err, map[string]any{"phase": string(phase)})
		}
		return err
	}
	return nil
}

// WireDaily is the documented default wiring: registers against
// core.PhaseDailyTick, the one phase that runs on every
// AdvanceTicks-driven daily tick — the literal reading of §14's "hard
// assert in dev... every tick" (see doc.go's "Which phase this checker
// runs against" section and ASM-080). Boot wiring should call this
// unless it has a specific reason to register additional
// invariant-checking hooks against a monthly phase via Wire directly.
func WireDaily(e *core.Engine, reg *Registry, provider SnapshotProvider, opts ...HookOption) error {
	return Wire(e, core.PhaseDailyTick, reg, provider, opts...)
}
