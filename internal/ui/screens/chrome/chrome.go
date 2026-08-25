package chrome

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// ViewChrome is the view-subscription name for the top-bar figures (AC-1).
// It follows int.protocol's view-name grammar (lowercase dot-separated
// segments, subscription.go) and is this package's own default — the spec
// names the six chrome figures but not the view that carries them (see the
// ASM logged for it). The wire schema for the patch is wire.go's
// wireFiguresPatch.
const ViewChrome = "chrome.topbar"

// SendCommandFunc issues one protocol.Command toward the engine — the same
// "the screen does not own the transport" seam ui.screen.map uses
// (mapscreen.SendCommandFunc). Chrome never holds a protocol.Transport;
// Subscribe hands its Subscribe command to this callback.
type SendCommandFunc func(protocol.Command) error

// Effects is the outbound control surface Chrome calls into when an alert
// is selected (AC-3) or a crisis fires (AC-7). Navigation is the canonical
// ui.dash.Navigator seam (MOD-038, internal/ui/dash) — Chrome consumes
// dash's DrillTarget/Navigator types rather than defining its own (GR#3,
// AC-3's "no second, parallel navigation path" rule). Pause is the only
// chrome-local effect, and it sends the shared protocol.KindPause command
// (AC-7's "equivalent to Space" — see PauseCommand).
//
//	Pause     → send(PauseCommand(correlationID))  // the same KindPause Space sends
//	Navigator → the dash drill-through jump, wired by the composition root
//
// Pause may be nil and Navigator may be nil, meaning "that effect is not
// wired yet"; a nil effect is skipped rather than panicking (a missing
// wiring must not crash the chrome render/control path — AC-11's spirit,
// GR#1).
type Effects struct {
	Pause     func()
	Navigator dash.Navigator
}

// Figures is the top bar's six vital signs (AC-1): date, clock-cycle
// position, speed multiplier, money, population, and credit rating. It is
// a plain value snapshot, immutable once published — Render reads a copy,
// never a live reference.
//
// BUG-324 corrected three of the per-field comments below. They were
// written before any engine-side publisher for "chrome.topbar" existed
// and described units the engine does not use; the real publisher
// (internal/engine/compose/chrome_publish.go, which carries the full
// per-field sourcing ledger) is now the ground truth and these comments
// follow it. Comments only — no field, tag, or rendered format changed.
type Figures struct {
	Date       string `json:"date"`       // month name + ordinal world year, e.g. "Jan Y1" (no real-world calendar year is pinned anywhere in the engine)
	ClockCycle int    `json:"clockCycle"` // 0..29 — which of the 30 logistics day-ticks within the month
	Speed      int    `json:"speed"`      // engine clock multiplier: 0 = paused, else 1/2/4/8 (engine.core Speed1x..Speed8xDebug)
	Money      int64  `json:"money"`      // city treasury in WHOLE POUNDS (the engine's own unit is micropounds; the publisher converts)
	Population int64  `json:"population"` // current citizen count
	Rating     string `json:"rating"`     // credit rating on engine.finance's own 0..1000 scale, e.g. "1000/1000" — there is no letter-grade scale in the engine
}

// Chrome is the persistent chrome: the top bar (figures) and the bottom
// prioritised alert stack, plus the crisis auto-pause control (AC-6
// through AC-10). See doc.go for the full contract.
//
// Concurrency: AddAlert/ResolveAlert/ApplyFiguresPatch are expected to be
// called from the delta/event-applying goroutine (T-VIEWS' caller) while
// Render runs on T-RENDER's goroutine; every exported method locks mu, so
// the two can safely run concurrently (AC-16's "-race clean" requirement).
// The alert stack is small (a handful of active alerts, §13's examples),
// so a mutex acquisition per call is not a contention concern.
type Chrome struct {
	mu sync.Mutex

	// self is set once, at the end of NewChrome, to the pointer NewChrome
	// itself returns — never reassigned after that. checkNotCopied
	// (copyguard.go) compares a receiver against self to detect a struct
	// copy (SEC-020). Lock-free (atomic.Pointer), so checkNotCopied can run
	// BEFORE mu is ever touched (SEC-016's pre-lock-ordering requirement).
	self atomic.Pointer[Chrome]

	correlationID string
	palette       widgets.Palette
	effects       Effects

	figures    Figures
	alerts     []Alert         // always kept sorted by lessAlerts
	seenCrisis map[string]bool // crisis IDs already auto-paused on (AC-8/AC-9 dedupe)
}

// NewChrome constructs an empty Chrome (no figures, no alerts). correlationID
// is used for this chrome's own registry-sourced errors (malformed patches,
// rejected alerts, copy guard) and as the CorrelationID on the Subscribe
// command Subscribe sends; pass errs.NewCorrelationID() if the caller has no
// more specific ID to thread through. palette supplies the alert tier
// colours (AC-2, reusing ui.widgets rather than hardcoding colour).
// effects is the outbound control seam (see Effects); nil entries are legal
// and treated as "not wired".
func NewChrome(correlationID string, palette widgets.Palette, effects Effects) *Chrome {
	c := &Chrome{
		correlationID: correlationID,
		palette:       palette,
		effects:       effects,
		seenCrisis:    make(map[string]bool),
	}
	// Stored once, last, before c ever escapes to a caller — see the self
	// field's doc comment and copyguard.go for why this is what makes
	// checkNotCopied work.
	c.self.Store(c)
	return c
}

// Subscribe sends the "chrome.topbar" Subscribe command via send (AC-1).
// It does not block on or read any CommandResult/Delta — that is the
// caller's transport-owning responsibility, per SendCommandFunc's doc
// comment.
func (c *Chrome) Subscribe(send SendCommandFunc) error {
	if err := c.checkNotCopied(map[string]any{"method": "Subscribe"}); err != nil {
		return err
	}
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(c.correlationID),
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: ViewChrome},
	}
	return send(cmd)
}

// PauseCommand returns the shared pause command — the exact command the
// Space global binding would send (protocol.KindPause, PausePayload). It
// exists so a composition root can wire Effects.Pause in one line
// (`Effects{Pause: func() { _ = send(PauseCommand(cid)) }}`) and so AC-7's
// "equivalent to Space, not a bespoke pause implementation" is a concrete,
// testable fact rather than a comment. Pause is idempotent by the
// protocol's own contract (PausePayload's doc comment), which is exactly
// what AC-10 relies on: Chrome may issue it freely on every new crisis and
// never toggles anything itself.
func PauseCommand(correlationID string) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(correlationID),
		Kind:            protocol.KindPause,
		Payload:         protocol.PausePayload{},
	}
}

// AddAlert inserts a into the stack (priority-ordered) and, if a is a
// crisis-tagged alert with an ID not yet seen, fires the auto-pause +
// redirect control (AC-6/AC-7) exactly once for that crisis identity
// (AC-8). It returns the registry error if a is malformed (missing target
// or ID, AC-11) — a rejected alert is NOT entered onto the stack.
//
// Edge-triggered semantics (AC-8/AC-9/AC-10): the dedupe is keyed on the
// alert's ID (the engine's stable per-instance crisis identity, FEAT-042
// AC-25b), and seenCrisis is never cleared by a manual resume or by a
// resolve. So the SAME crisis ID re-reported across consecutive deltas
// fires once; a manual resume does not re-arm it (AC-9); and a genuinely
// NEW crisis ID fires a fresh pause+redirect even while an earlier crisis
// is still on the stack or the world is already paused (AC-10).
func (c *Chrome) AddAlert(a Alert) error {
	if err := c.checkNotCopied(map[string]any{"method": "AddAlert"}); err != nil {
		return err
	}
	if err := a.validate(c.correlationID); err != nil {
		return err
	}

	c.mu.Lock()
	if err := c.checkNotCopied(map[string]any{"method": "AddAlert"}); err != nil {
		c.mu.Unlock()
		return err
	}
	c.alerts = insertSortedLocked(c.alerts, a)
	fire := false
	var tgt dash.DrillTarget
	if a.Crisis && !c.seenCrisis[a.ID] {
		c.seenCrisis[a.ID] = true
		fire = true
		tgt = a.target
	}
	c.mu.Unlock()

	// Fired OUTSIDE the lock: the effects are caller-supplied callbacks and
	// must never run under c.mu (a callback that re-enters Chrome — e.g.
	// Render for logging — would deadlock; the same rationale as ui.keys'
	// Feed running Action.Run after releasing its lock).
	if fire {
		c.fireCrisis(tgt)
	}
	return nil
}

// fireCrisis performs AC-7's pause-then-redirect for a newly-seen crisis.
// It never consults "are we already paused" — that state belongs to the
// engine, not to Chrome — so a new crisis while already paused still
// redirects (AC-10): the Pause is idempotent at the engine, and the
// Navigate is NOT skipped.
func (c *Chrome) fireCrisis(tgt dash.DrillTarget) {
	if err := c.checkNotCopied(map[string]any{"method": "fireCrisis"}); err != nil {
		return
	}
	if c.effects.Pause != nil {
		c.effects.Pause()
	}
	if c.effects.Navigator != nil {
		// fire-and-forget: the crisis path has no caller to propagate a
		// navigation error to (the selection path, JumpTo, surfaces it via
		// its bool return); the discard is explicit, per GR#1.
		_ = c.effects.Navigator.Navigate(tgt)
	}
}

// ResolveAlert removes every alert whose ID equals id from the stack
// (AC-12): the next delta reporting a resolved underlying condition drops
// the alert, rather than leaving a dead entry the player must dismiss.
// seenCrisis is deliberately NOT cleared here — a resolved crisis's ID is
// per-instance (FEAT-042 AC-25b) and a future recurrence is a new ID, so
// clearing here would wrongly re-arm a still-remembered identity.
func (c *Chrome) ResolveAlert(id string) {
	if err := c.checkNotCopied(map[string]any{"method": "ResolveAlert"}); err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkNotCopied(map[string]any{"method": "ResolveAlert"}); err != nil {
		return
	}
	c.alerts = removeByIDLocked(c.alerts, id)
}

// Top returns the current top-of-stack alert (the one `!` jumps to, AC-4):
// the highest tier, then oldest, then lowest ID per lessAlerts. ok is
// false when the stack is empty.
func (c *Chrome) Top() (Alert, bool) {
	if err := c.checkNotCopied(map[string]any{"method": "Top"}); err != nil {
		return Alert{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkNotCopied(map[string]any{"method": "Top"}); err != nil {
		return Alert{}, false
	}
	if len(c.alerts) == 0 {
		return Alert{}, false
	}
	return c.alerts[0], true
}

// Alerts returns a snapshot of the stack, sorted by lessAlerts (AC-2's
// render order and AC-5's prioritisation). The returned slice is a copy —
// callers may read it freely, never mutate it.
func (c *Chrome) Alerts() []Alert {
	if err := c.checkNotCopied(map[string]any{"method": "Alerts"}); err != nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkNotCopied(map[string]any{"method": "Alerts"}); err != nil {
		return nil
	}
	return snapshotAlerts(c.alerts)
}

// Figures returns the current top-bar figures snapshot (a value copy).
func (c *Chrome) Figures() Figures {
	if err := c.checkNotCopied(map[string]any{"method": "Figures"}); err != nil {
		return Figures{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkNotCopied(map[string]any{"method": "Figures"}); err != nil {
		return Figures{}
	}
	return c.figures
}

// JumpTo navigates to a's target (AC-3's drill-through jump). It is the
// single navigation path this package uses — selecting an alert (AC-3), a
// crisis redirect (AC-7), and `!` (AC-4) all funnel through here. It
// returns false when a carries no usable target, when no Navigator is
// wired, or when the Navigator reports a failed jump; a targetless alert
// cannot happen for an Alert produced by NewAlert but is still guarded
// rather than assumed.
func (c *Chrome) JumpTo(a Alert) bool {
	if err := c.checkNotCopied(map[string]any{"method": "JumpTo"}); err != nil {
		return false
	}
	if !validTarget(a.target) {
		return false
	}
	if c.effects.Navigator == nil {
		return false
	}
	return c.effects.Navigator.Navigate(a.target) == nil
}

// JumpToTop navigates to the current top-of-stack alert's target — the
// action `!` fires (AC-4). It resolves "top" at call time, so it always
// jumps to whichever alert is ranked first NOW, never a fixed or
// first-inserted alert. Returns false when the stack is empty.
func (c *Chrome) JumpToTop() bool {
	if err := c.checkNotCopied(map[string]any{"method": "JumpToTop"}); err != nil {
		return false
	}
	top, ok := c.Top()
	if !ok {
		return false
	}
	return c.JumpTo(top)
}
