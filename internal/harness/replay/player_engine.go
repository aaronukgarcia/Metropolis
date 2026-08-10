package replay

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// EnginePlayer is replay mode (b): it replays a fixture's recorded
// Command sequence into a live engine for regression comparison (AC-3b).
//
// EnginePlayer implements the exact CommandSource-shaped surface
// engine.core.RunCommandLoop consumes from any Transport — Commands()
// and SendResult() below, deliberately duplicating that interface's
// METHOD SHAPE rather than importing internal/engine/core (this package
// has no need of anything else engine.core exports, and Go's structural
// typing means a future signature change there simply stops compiling
// at the call site, which is a safer failure mode than a value silently
// drifting — see doc.go). A caller drives replay with
// `go targetEngine.RunCommandLoop(ctx, enginePlayer)` and reads the
// result back from Replay, which BLOCKS the calling goroutine until
// either every command has a matching result or ctx is done.
//
// EnginePlayer never calls RegisterPhaseHook or any other boot-only
// registration method on anything (AC-3b) — its entire surface is
// Commands()/SendResult()/Replay(), the same post-boot seam any other
// CommandSource-consuming client uses. A fixture recorded against a
// differently-configured engine is a legitimate mismatch for Replay's
// CompareResult to surface, not something this type tries to paper over
// by touching the target's configuration.
type EnginePlayer struct {
	commands []protocol.Command       // from the fixture, immutable after construction
	recorded []protocol.CommandResult // from the fixture, immutable after construction
	cmdCh    chan protocol.Command    // fed once, fully, then closed — never re-opened

	mu      sync.Mutex
	results []protocol.CommandResult // observed via SendResult, arrival order
	notify  chan struct{}            // buffered 1, coalescing wakeups for Replay's wait loop

	// self mirrors Recorder.self / InProcTransport.self exactly (AC-13b)
	// — see record.go's Recorder.self doc comment for the full
	// pre-lock-ordering rationale. mu is a plain sync.Mutex VALUE, so a
	// struct copy gets its own, independent mu while ALIASING results
	// (a slice) and notify (a channel) — the same SEC-014/SEC-019 shape.
	self atomic.Pointer[EnginePlayer]
}

// NewEnginePlayer builds an EnginePlayer from f's recorded commands and
// results.
func NewEnginePlayer(f Fixture) (*EnginePlayer, error) {
	commands, err := f.Commands()
	if err != nil {
		return nil, err
	}
	recorded, err := f.Results()
	if err != nil {
		return nil, err
	}
	p := &EnginePlayer{
		commands: commands,
		recorded: recorded,
		cmdCh:    make(chan protocol.Command, len(commands)),
		notify:   make(chan struct{}, 1),
	}
	// Stored exactly once, here, before p is returned to any caller —
	// see Recorder.self's doc comment (record.go) for the identical
	// rationale.
	p.self.Store(p)
	return p, nil
}

func (p *EnginePlayer) checkNotCopied(correlationID string, ctx map[string]any) error {
	if p.self.Load() != p {
		return errs.New(codeEnginePlayerCopied, correlationID, ctx)
	}
	return nil
}

// closedCommandCh is the fail-closed value Commands() returns for a
// struct-copied EnginePlayer, mirroring InProcTransport's
// closedResultCh/closedEventCh/closedDeltaCh/closedCommandCh
// (internal/protocol/transport.go) — a receive on it returns the zero
// value with ok=false immediately, rather than a nil channel's "block
// forever" or the real, aliased channel's "silently drive the ORIGINAL's
// traffic from a copy".
var closedCommandCh = func() chan protocol.Command {
	ch := make(chan protocol.Command)
	close(ch)
	return ch
}()

// Commands implements the CommandSource-shaped surface (AC-3b): the
// channel a target's RunCommandLoop ranges over.
func (p *EnginePlayer) Commands() <-chan protocol.Command {
	if err := p.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return closedCommandCh
	}
	return p.cmdCh
}

// SendResult implements the CommandSource-shaped surface: called once
// per command by the target's RunCommandLoop, in the order it processed
// them. Always returns true unless called on a struct-copied
// EnginePlayer (false, matching InProcTransport.SendResult's "nothing
// was sent/accepted" contract for a rejected copy).
func (p *EnginePlayer) SendResult(res protocol.CommandResult) bool {
	if err := p.checkNotCopied(string(res.CorrelationID), nil); err != nil {
		return false
	}
	p.mu.Lock()
	if p.self.Load() != p {
		p.mu.Unlock()
		_ = errs.New(codeEnginePlayerCopied, string(res.CorrelationID), nil)
		return false
	}
	p.results = append(p.results, res)
	p.mu.Unlock()
	select {
	case p.notify <- struct{}{}:
	default:
	}
	return true
}

// ErrReplayTargetClosedEarly-equivalent is a registry-sourced *errs.E
// built with codeReplayTargetClosedEarly (MET-H004) — see
// errs.E.Is/checkNotCopied's convention: callers test for it via
// errors.Is(err, &errs.E{Code: codeReplayTargetClosedEarly}), mirroring
// MET-P094/codePrematureCommandsClose's own pattern
// (internal/engine/stub/engine.go) rather than a plain sentinel, so this
// failure surfaces its registry code end-to-end like every other
// Metropolis error (GR#1).

// Replay sends every recorded command onto Commands() (buffered to
// len(commands), so this never blocks on a slow or absent consumer —
// AC-13 posture applied to the sending side too) and then waits, up to
// ctx, for a SendResult callback to have arrived for every one of them.
//
// AC-3c (weakness pattern #1 — premature-close ambiguity, mirroring
// BUG-020/codePrematureCommandsClose): if ctx is done before every sent
// command has a matching observed result, Replay returns
// codeReplayTargetClosedEarly (MET-H004) — distinct from, and never
// confused with, a complete replay where every command was answered
// before ctx ended. See doc.go's "Premature-close ambiguity" section for
// why this is the shape that BUG-020's fix generalises to here: this
// type owns and is the only closer of Commands(), so "who closed it" is
// never ambiguous — the analogous risk this method guards is "did every
// dispatched command actually get answered", tracked explicitly via the
// results count rather than inferred from channel-close timing.
func (p *EnginePlayer) Replay(ctx context.Context) (*CompareResult, error) {
	if err := p.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return nil, err
	}

	// cmdCh is buffered to exactly len(p.commands), so every send below is
	// guaranteed non-blocking — deliberately NOT raced against ctx.Done()
	// in a select: with a guaranteed-ready send, selecting against
	// ctx.Done() too would let Go's "pick uniformly among ready cases"
	// rule occasionally choose the ctx branch even though the send would
	// never have blocked, which is exactly the kind of scheduling-
	// dependent flakiness docs/planning/dev-team-process.md v1.8 bans
	// ("construct the state, don't race for the timing"). ctx is only
	// consulted below, in the wait-for-results loop, where the outcome
	// genuinely depends on it.
	for _, cmd := range p.commands {
		p.cmdCh <- cmd
	}
	close(p.cmdCh)

	// SEC-032 (Tester-1, 2026-08-10): the ctx.Done() branch below must
	// re-check len(p.results) >= want ONE MORE TIME before declaring
	// failure — this is SEC-026/BUG-020's "an alarm that fires on
	// correct shutdowns gets ignored" pattern, reincarnated. Go's select
	// picks uniformly among ALL ready cases, not in priority order: if
	// the final SendResult lands (making notify ready) at essentially
	// the same instant ctx is cancelled (making ctx.Done() ready), both
	// cases are ready simultaneously and select may still choose the
	// ctx.Done() branch even though the work had already, genuinely,
	// completed. Tester-1 measured this at 38.6% (193/500) against a
	// target that answered every command. The outer loop's own top-of-
	// loop check (`if got >= want { break }`) does NOT cover this: that
	// check only runs BEFORE a select, never after one, so a race
	// resolved in favour of the ctx.Done() branch was never re-examined
	// before this method reported a false premature-close. The fix is
	// exactly SEC-026's fix, restated: re-check the completion condition
	// at the point of the alarm, not just before arming it.
	want := len(p.commands)
waitLoop:
	for {
		p.mu.Lock()
		got := len(p.results)
		p.mu.Unlock()
		if got >= want {
			break waitLoop
		}
		select {
		case <-p.notify:
		case <-ctx.Done():
			p.mu.Lock()
			got := len(p.results)
			p.mu.Unlock()
			if got >= want {
				// The work genuinely finished; ctx merely won a harmless
				// race against the final SendResult's notify signal —
				// not a premature close.
				break waitLoop
			}
			return nil, errs.New(codeReplayTargetClosedEarly, errs.NewCorrelationID(), map[string]any{
				"sent": want, "answered": got,
				"cause": "ctx done before every sent command received a CommandResult",
			})
		}
	}

	p.mu.Lock()
	results := append([]protocol.CommandResult(nil), p.results...)
	p.mu.Unlock()
	return compareResults(p.recorded, results), nil
}
