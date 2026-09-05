package finance

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// mode.go is FEAT-143 (mkey feat.gameinit)'s finance-side half of the
// Real-vs-Unlimited-Money gate: a single injected policy every
// placement/OPEX/payroll check and the insolvency/debt-rating triggers
// consult, so the SAME finance code serves both modes (US-4 -- never two
// divergent implementations, one real and one "sandbox" fork).
//
// ModeGate is the shape FinanceAPI consumes feat.gameinit's locked
// initialization mode through (mirrors MilestoneGate's existing
// engine.unlocks-shaped injection exactly): this package does NOT import
// internal/engine/gameinit (no such edge is registered -- only
// feat.gameinit -> engine.finance exists in code.json), so a
// *gameinit.GameInit satisfies this interface structurally via its own
// Unlimited(correlationID string) (bool, error) method.
//
// FEAT-143 round finding P2-B: the original shape was a bare
// `Unlimited() bool`. gameinit.GameInit.Unlimited swallowed its own
// SEC-020 copy-guard error and returned the bool's zero value (false) on
// a struct-copied *GameInit, which made a copied Unlimited-mode session
// silently, undetectably report Real mode with no error on any channel
// (TestAttackFEAT143_CopiedGameInitSilentlyReportsRealMode). The error
// return lets unlimitedLocked (below) detect that failure and fail
// CLOSED toward Real -- the stricter mode -- rather than toward whichever
// mode happened to be the zero value, AND record the failure via the
// registry (GR#17) instead of swallowing it a second time here.
type ModeGate interface {
	// Unlimited reports whether the current session is running in
	// Unlimited Money mode. A nil ModeGate (the default -- see
	// NewFinanceAPI) is treated exactly like a gate that always returns
	// (false, nil): Real mode, the pre-FEAT-143 behaviour, byte-for-byte
	// unchanged for every existing caller that never calls SetModeGate.
	Unlimited(correlationID string) (bool, error)
}

// unlimitedLocked reports whether f's injected mode gate says the session
// is running in Unlimited Money mode (f.mu must be held by the caller, or
// this is called from a lock-free context that tolerates a benign race on
// f.modeGate's pointer read -- every call site below holds the lock).
// A nil gate always reads as Real mode, so this is safe to call from
// every existing code path unconditionally.
//
// FEAT-143 round finding P2-B: a gate that returns an error (in
// production, a struct-copied *gameinit.GameInit tripping its own
// SEC-020 copy-guard) is treated as Real mode -- the stricter of the two
// (a wiring bug must never silently grant the sandbox bypass) -- and the
// failure is recorded via ErrModeGateFailed (GR#17: a monitoring/gate
// FAILURE must itself write a registry error, never fail silently).
func (f *FinanceAPI) unlimitedLocked() bool {
	if f.modeGate == nil {
		f.lastModeGateErr = nil
		return false
	}
	unlimited, err := f.modeGate.Unlimited(f.correlationID)
	if err != nil {
		f.lastModeGateErr = errs.New(ErrModeGateFailed, f.correlationID, map[string]any{"cause": err.Error()})
		return false
	}
	f.lastModeGateErr = nil
	return unlimited
}

// ModeGateError returns the most recently recorded modeGate.Unlimited
// failure (FEAT-143 round finding P2-B, GR#17), or nil if the last check
// succeeded (or no gate has ever been set/consulted). A non-nil result
// means unlimitedLocked fell back to Real mode DESPITE the gate possibly
// having said Unlimited -- a wiring bug (typically SetModeGate having
// been handed a struct-copied *gameinit.GameInit), not a legitimate
// Real-mode session.
func (f *FinanceAPI) ModeGateError() error {
	if err := f.checkNotCopied("ModeGateError"); err != nil {
		return err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.lastModeGateErr
}

// SetModeGate installs the mode gate FEAT-143's finance bypass consults
// (AC-2). Mirrors SetMilestoneGate's existing post-construction wiring
// precedent exactly: no constructor argument, so every existing/future
// NewFinanceAPI call site (and every test that never calls SetModeGate)
// is unaffected -- a nil gate is the default, and behaves exactly like
// Real mode.
//
// FEAT-143 round finding P3 (informational, deliberately left as-is): a
// nil g is ACCEPTED (unlimitedLocked treats it identically to Real mode --
// see this method's own doc above), and SetModeGate is RE-CALLABLE at any
// time, with no lock of its own guarding "set exactly once"
// (TestAttackFEAT143_ModeGateSwappableAtRuntime pins both). This is
// intentional, not an oversight: AC-3's immutability guarantee lives
// entirely in gameinit.GameInit (the locked *value* never changes after
// construction) -- this seam is merely WHERE that already-locked value
// gets handed to finance, and re-callability here matches
// SetMilestoneGate's own precedent (also silently re-settable) plus
// SetOpexConfig's. Locking this setter would add a second, redundant
// immutability mechanism instead of relying on the one AC-3 already
// mandates, and would complicate legitimate re-wiring (e.g. a save/load
// cycle constructing a fresh *GameInit and re-installing it). The actual
// safety property this composition seam depends on is "the composition
// root calls this exactly once per session with the correct, uncopied
// *GameInit" -- a wiring discipline outside this package's power to
// enforce, and unrelated to whether the setter itself is re-callable.
func (f *FinanceAPI) SetModeGate(g ModeGate) error {
	if err := f.checkNotCopied("SetModeGate"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modeGate = g
	return nil
}
