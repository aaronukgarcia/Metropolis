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
func (s *State) recordCheatUsed(e CheatUsedEvent) {
	s.mu.Lock()
	s.cheatLog = append(s.cheatLog, e)
	s.mu.Unlock()
}

// CheatLog returns a snapshot (defensive copy) of every successfully
// invoked cheat since construction, in invocation order. This is the
// programmatic audit surface AC-6 asks for, independent of the shared,
// process-wide errs NDJSON sink (which InvokeCheat also writes to, but
// which is awkward for a test/consumer to scope to just this State).
func (s *State) CheatLog() []CheatUsedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CheatUsedEvent, len(s.cheatLog))
	copy(out, s.cheatLog)
	return out
}
