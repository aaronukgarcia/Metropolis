package debug

import (
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// CheatKind names one of §14's three debug cheats.
type CheatKind string

const (
	// CheatFreeMoney grants funds outside normal economy pacing.
	CheatFreeMoney CheatKind = "free_money"
	// CheatInstantBuild completes a construction immediately.
	CheatInstantBuild CheatKind = "instant_build"
	// CheatForceMilestone force-unlocks a milestone tier ahead of its
	// population threshold (§4's own "port testing pre-100k" example).
	CheatForceMilestone CheatKind = "force_milestone"
)

// CheatEffect is the actual domain state-mutation a cheat performs,
// injected by the caller that owns the affected state — engine.finance
// for free money / instant build, engine.unlocks (MOD-032, not yet
// built) for force milestone. This package owns only the debug gate and
// the balance-data-hygiene audit log (AC-6); it never applies a cheat's
// domain effect itself (see doc.go's "Out of scope" reasoning).
type CheatEffect func() error

// CheatUsedEvent is one audit-log entry for a successfully invoked
// cheat (AC-6: "cheats must be visible in the record, not silent").
type CheatUsedEvent struct {
	CorrelationID string
	Kind          CheatKind
	Ctx           map[string]any
	At            time.Time
}

// InvokeCheat gates, applies, and audits one debug cheat invocation.
//
//  1. With debug off, the request is rejected (AC-9/AC-11) before
//     effect is ever called — a cheat can never fire silently just
//     because a caller forgot to check IsOn() first.
//  2. effect must be non-nil (ErrNilCheatEffect) — this package has no
//     default domain behaviour to fall back to.
//  3. effect is invoked. If it fails, the failure is wrapped and
//     returned; the invocation is NOT recorded as a successful cheat use
//     (a failed effect never happened, per AC-6's "cheats must be
//     visible" — a no-op should not be visible as a use).
//  4. On success, the invocation is appended to CheatLog with a
//     timestamp from the injected Clock (never wall-clock, AC-14) and
//     also mirrored to the standard registry-sourced NDJSON log at warn
//     severity (M0-ENG §3's "cheats must be visible in the record").
func (s *State) InvokeCheat(correlationID string, kind CheatKind, ctx map[string]any, effect CheatEffect) error {
	if err := s.requireOn(correlationID, string(kind)); err != nil {
		return err
	}
	if effect == nil {
		return errs.New(ErrNilCheatEffect, correlationID, map[string]any{"kind": string(kind)})
	}
	if err := effect(); err != nil {
		return errs.Wrap(ErrCheatEffectFailed, correlationID, err, map[string]any{"kind": string(kind)})
	}

	at := s.nowFunc()
	s.recordCheatUsed(CheatUsedEvent{CorrelationID: correlationID, Kind: kind, Ctx: ctx, At: at})

	// Mirror to the standard NDJSON log sink (GR#1: selectable,
	// correlation-ID'd, structured) at warn severity. The constructed
	// *errs.E is deliberately discarded — this call's purpose is its
	// logEntry side effect (every errs.New/Wrap automatically logs, see
	// foundation/errs/errs.go's construct), not a failure being reported
	// to InvokeCheat's own caller, who already has a nil error at this
	// point. This is a documented, deliberate reuse of the
	// registry-sourced logging path for a non-error audit line — see
	// errors.go's codeCheatUsed doc comment.
	_ = errs.New(codeCheatUsed, correlationID, map[string]any{"kind": string(kind)})

	return nil
}

// recordCheatUsed appends e to the in-memory audit log under lock.
//
// SEC-020 wave 2: identity-checked BEFORE mu is touched and again after
// acquisition. InvokeCheat's own requireOn call already checked identity
// earlier in the same call, but that check ran before effect() and
// nowFunc() executed — re-checking here, immediately before the
// cheatLog mutation itself, is what actually guards THIS mu.Lock() site
// rather than relying on an earlier caller having checked a different
// one (Weakness pattern #3: guard the site that does the mutating, not
// just an upstream call on the same request). On a copy, the append is
// silently dropped — cheatLog is a slice a copy ALIASES with the
// original's backing array; a copy appending to its own (post-copy,
// possibly reallocated) cheatLog would diverge from the original's audit
// trail rather than corrupt it directly, but it is still not a
// legitimate operation on a value nothing constructed, so it is refused
// the same as every other guarded mutation in this package.
func (s *State) recordCheatUsed(e CheatUsedEvent) {
	if err := s.checkNotCopied(e.CorrelationID, map[string]any{"kind": string(e.Kind)}); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(e.CorrelationID, map[string]any{"kind": string(e.Kind)}); err != nil {
		return
	}
	s.cheatLog = append(s.cheatLog, e)
}

// CheatLog returns a snapshot (defensive copy) of every successfully
// invoked cheat since construction, in invocation order. This is the
// programmatic audit surface AC-6 asks for, independent of the shared,
// process-wide errs NDJSON sink (which InvokeCheat also writes to, but
// which is awkward for a test/consumer to scope to just this State).
//
// SEC-020 wave 2: identity-checked BEFORE mu is touched and again after
// acquisition. On a copy, returns an empty (non-nil) slice rather than
// reading the aliased original's cheatLog through the copy's own
// independent lock — a copy reporting the ORIGINAL's audit trail as its
// own would misrepresent which State's usage actually happened, which is
// exactly the kind of hygiene-adjacent misrepresentation this package
// exists to prevent (AC-6).
func (s *State) CheatLog() []CheatUsedEvent {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CheatLog"}); err != nil {
		return []CheatUsedEvent{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "CheatLog"}); err != nil {
		return []CheatUsedEvent{}
	}
	out := make([]CheatUsedEvent, len(s.cheatLog))
	copy(out, s.cheatLog)
	return out
}
