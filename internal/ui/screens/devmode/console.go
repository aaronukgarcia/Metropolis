package devmode

import (
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// RequireConsoleFunc mirrors debug.State.RequireConsole's signature
// exactly (func(correlationID string) error) — the gate Console.Open
// calls before doing anything else (AC-DM1). Wired via WithRequireConsole.
type RequireConsoleFunc func(correlationID string) error

// EnableFunc mirrors debug.State.Enable's signature, pre-bound by the
// caller to the appropriate EnableSource (AC-DM3 proposes SourcePalette
// — see feat.devmode.md's logged assumption) — this package never
// chooses or names an EnableSource itself, it only invokes whatever the
// caller closed over. Wired via WithEnable; nil means Open never calls
// Enable at all (a caller that only wants gate-checked opening, not
// touch-on-open — not this feature's documented shape, but a legal
// construction for a test or a future variant).
type EnableFunc func(correlationID string) error

// PauseFunc mirrors the pause action AC-DM2 requires the console's
// open-action to itself issue (RequireConsole gates reachability only,
// it does not pause). In production this closes over a command sent
// through internal/protocol to engine.core's existing pause handling
// (protocol.KindPause) — this package never talks to engine.core
// directly (GR#20). Wired via WithPause; nil means Open never attempts
// to pause (legal for a test exercising only the gate/enable behaviour).
type PauseFunc func(correlationID string) error

// IsPausedFunc reads back whether the sim is currently paused, through
// whatever surface any other pause query already uses (AC-DM2's "same
// clock/state surface" requirement) — never a devconsole-local "I think
// I paused it" flag. Wired via WithIsPaused; optional.
type IsPausedFunc func() bool

// InspectFunc mirrors debug.State.InspectEntity's signature exactly —
// Console.Inspect is a thin pass-through, never a second
// marshalling/inspection path (AC-DM5). Wired via WithInspect.
type InspectFunc func(correlationID, ref string) ([]byte, error)

// SubmitFeedbackFunc mirrors debug.State.SubmitFeedback's signature
// exactly (feedback.go) — Console.SubmitFeedback is a thin pass-through
// to the gated file-drop write path, never a second write mechanism
// (AC-DM8). Wired via WithSubmitFeedback.
//
// sourceMkey (ASM-477 / Bill's ruling) mirrors debug.State.SubmitFeedback's
// own added parameter — Console.SubmitFeedback below supplies the literal
// "feat.devmode" explicitly on every call, since this Console is
// feat.devmode's own UI surface.
type SubmitFeedbackFunc func(correlationID string, tick int64, body string, sourceMkey string) error

// DebugTouchedFunc reads back the active save header's DebugTouched bit
// — wired for verification/observability only (e.g. a caller's own
// health check), never consulted by Console's own gating logic, which
// routes exclusively through RequireConsoleFunc/EnableFunc's return
// values. Wired via WithDebugTouched; optional.
type DebugTouchedFunc func() bool

// CorrelationIDFunc produces a fresh correlation ID for an action that
// doesn't already have one supplied by its caller (e.g. a keybinding
// handler with no correlation ID of its own). Defaults to
// errs.NewCorrelationID if unset.
type CorrelationIDFunc func() string

// Console is FEAT-065's pause-anywhere dev console overlay (DoD #1) plus
// the object-metrics inspection (DoD #2) and feedback-submission (DoD
// #3) surfaces it gates access to. Every capability is delegated to a
// caller-injected seam over the real *debug.State — see doc.go's "A
// thin consumer, never a second gate" section. The zero Console is not
// ready to use; construct with New.
//
// Safe for concurrent use: open/close state is guarded by mu. The wired
// func seams themselves are plain values, set once at construction and
// never mutated afterward, so no lock is needed to read them.
type Console struct {
	mu   sync.Mutex
	open bool

	requireConsole RequireConsoleFunc
	enable         EnableFunc
	pause          PauseFunc
	isPaused       IsPausedFunc
	inspect        InspectFunc
	submitFeedback SubmitFeedbackFunc
	debugTouched   DebugTouchedFunc
	newCorrID      CorrelationIDFunc
}

// Option customizes a new Console.
type Option func(*Console)

// WithRequireConsole wires the gate Open calls before anything else
// (AC-DM1/AC-DM7). Required for Open to ever succeed — see
// ErrRequireConsoleNotConfigured.
func WithRequireConsole(fn RequireConsoleFunc) Option {
	return func(c *Console) { c.requireConsole = fn }
}

// WithEnable wires the sticky-touch call Open issues before the console
// is considered open (AC-DM3/AC-DM4). Optional — see EnableFunc's doc
// comment for the nil case.
func WithEnable(fn EnableFunc) Option {
	return func(c *Console) { c.enable = fn }
}

// WithPause wires the pause action Open issues once gated/enabled
// (AC-DM2). Optional — see PauseFunc's doc comment for the nil case.
func WithPause(fn PauseFunc) Option {
	return func(c *Console) { c.pause = fn }
}

// WithIsPaused wires the pause-state readback (AC-DM2's verification
// surface). Optional.
func WithIsPaused(fn IsPausedFunc) Option {
	return func(c *Console) { c.isPaused = fn }
}

// WithInspect wires the entity-inspection pass-through (AC-DM5/AC-DM6).
// Optional — Inspect returns ErrCapabilityNotConfigured if called unwired.
func WithInspect(fn InspectFunc) Option {
	return func(c *Console) { c.inspect = fn }
}

// WithSubmitFeedback wires the feedback file-drop pass-through
// (AC-DM8/AC-DM9). Optional — SubmitFeedback returns
// ErrCapabilityNotConfigured if called unwired.
func WithSubmitFeedback(fn SubmitFeedbackFunc) Option {
	return func(c *Console) { c.submitFeedback = fn }
}

// WithDebugTouched wires the header DebugTouched readback used for
// verification/observability (see DebugTouchedFunc's doc comment).
// Optional.
func WithDebugTouched(fn DebugTouchedFunc) Option {
	return func(c *Console) { c.debugTouched = fn }
}

// WithCorrelationIDFunc overrides the default correlation ID generator
// (errs.NewCorrelationID) used when a caller invokes Console's methods
// with an empty correlationID. Optional.
func WithCorrelationIDFunc(fn CorrelationIDFunc) Option {
	return func(c *Console) { c.newCorrID = fn }
}

// New constructs a Console. With no options it has no wired seams at
// all, so Open always fails with ErrRequireConsoleNotConfigured — never
// silently "opens" with nothing gating it.
func New(opts ...Option) *Console {
	c := &Console{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// correlationID returns id if non-empty, otherwise a freshly generated
// one via the wired (or default) CorrelationIDFunc.
func (c *Console) correlationID(id string) string {
	if id != "" {
		return id
	}
	if c.newCorrID != nil {
		return c.newCorrID()
	}
	return errs.NewCorrelationID()
}

// Open requests the dev console open (AC-DM1-4):
//
//  1. Calls the wired RequireConsoleFunc (debug.State.RequireConsole)
//     BEFORE anything else — with debug off, this returns
//     ErrDebugRequired and Open returns that error unwrapped: the
//     console's open/visible state is left exactly as it was (AC-DM1).
//  2. If a RequireConsoleFunc was never wired at all, Open refuses with
//     ErrRequireConsoleNotConfigured rather than treating an unwired
//     gate as "gate passed" (AC-DM1's "never a silent no-op").
//  3. Calls the wired EnableFunc (debug.State.Enable), if any, BEFORE
//     the console is considered open — this is what makes
//     Header.DebugTouched() true the moment the console becomes usable
//     (AC-DM3), and Enable's own documented idempotency means calling
//     it again when debug was already on is a harmless re-touch
//     (AC-DM4) — Open does not try to detect "was debug already on"
//     itself, it just always calls Enable if one is wired, exactly as
//     feat.debugmode's own Enable doc comment says is safe.
//  4. Calls the wired PauseFunc, if any, so opening the console actually
//     pauses the sim (AC-DM2) — RequireConsole only gates reachability,
//     it does not pause by itself.
//  5. Only after all of the above succeed is the console marked open.
//
// Any failure at steps 1-4 leaves the console's open state unchanged
// and returns the underlying error verbatim (never a panic, never a
// devconsole-local error code standing in for the real gate's).
func (c *Console) Open(correlationID string) error {
	cid := c.correlationID(correlationID)

	if c.requireConsole == nil {
		return errs.New(ErrRequireConsoleNotConfigured, cid, nil)
	}
	if err := c.requireConsole(cid); err != nil {
		return err
	}

	if c.enable != nil {
		if err := c.enable(cid); err != nil {
			return err
		}
	}

	if c.pause != nil {
		if err := c.pause(cid); err != nil {
			return err
		}
	}

	c.mu.Lock()
	c.open = true
	c.mu.Unlock()
	return nil
}

// IsOpen reports whether the console is currently open.
func (c *Console) IsOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.open
}

// Close marks the console closed. It does not disable debug or clear
// DebugTouched (that flag is sticky forever, per feat.debugmode's own
// contract — this package never touches it directly at all, only via
// the wired EnableFunc's one-directional Enable call).
func (c *Console) Close() {
	c.mu.Lock()
	c.open = false
	c.mu.Unlock()
}

// IsPaused reads back the wired pause-state surface, if any (AC-DM2's
// verification surface). Returns false if no IsPausedFunc was wired.
func (c *Console) IsPaused() bool {
	if c.isPaused == nil {
		return false
	}
	return c.isPaused()
}

// DebugTouched reads back the wired DebugTouched surface, if any — see
// DebugTouchedFunc's doc comment. Returns false if unwired.
func (c *Console) DebugTouched() bool {
	if c.debugTouched == nil {
		return false
	}
	return c.debugTouched()
}

// Inspect resolves ref through the wired InspectFunc (debug.State.
// InspectEntity), thin-consuming it exactly as AC-DM5 requires.
// Rejected with ErrConsoleNotOpen if the console is not currently open
// (AC-DM7 — carries AC-DM1's gate forward to this surface too, as a
// dedicated check independent of Open's own gating, in case a future
// change adds a second entry point that bypasses Open).
func (c *Console) Inspect(correlationID, ref string) ([]byte, error) {
	cid := c.correlationID(correlationID)
	if !c.IsOpen() {
		return nil, errs.New(ErrConsoleNotOpen, cid, map[string]any{"action": "inspect"})
	}
	if c.inspect == nil {
		return nil, errs.New(ErrCapabilityNotConfigured, cid, map[string]any{"capability": "inspect"})
	}
	return c.inspect(cid, ref)
}

// feedbackSourceMkey is the code.json key this Console attributes every
// feedback submission to (ASM-477 / Bill's ruling): "feat.devmode" is
// passed explicitly on every call below rather than left as an implicit
// default buried inside internal/engine/debug/feedback.go, so the
// attribution claude-devfeedback-import.js ultimately records is genuinely
// traceable to this call site.
const feedbackSourceMkey = "feat.devmode"

// SubmitFeedback submits body through the wired SubmitFeedbackFunc
// (debug.State.SubmitFeedback), thin-consuming it exactly as AC-DM8
// requires. Rejected with ErrConsoleNotOpen if the console is not
// currently open (AC-DM7's "dedicated test ... in case a future change
// adds a second entry point" — this is that independent check).
func (c *Console) SubmitFeedback(correlationID string, tick int64, body string) error {
	cid := c.correlationID(correlationID)
	if !c.IsOpen() {
		return errs.New(ErrConsoleNotOpen, cid, map[string]any{"action": "feedback-submit"})
	}
	if c.submitFeedback == nil {
		return errs.New(ErrCapabilityNotConfigured, cid, map[string]any{"capability": "feedback-submit"})
	}
	return c.submitFeedback(cid, tick, body, feedbackSourceMkey)
}
