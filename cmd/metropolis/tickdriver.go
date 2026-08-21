package main

import (
	"context"
	"time"

	enginecore "github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// ─────────────────────────────────────────────────────────────────────────
// BUG-322: the tick driver — the missing connection between the composition
// root and time.
//
// Before this file existed, cmd/metropolis booted a fully wired real engine
// (compose.Wire, nine phase hooks) and then never advanced it: ticks move
// only when a protocol.KindAdvanceTicks command arrives, and the only
// senders in the tree were internal/harness/headless and the replay
// generator. The interactive binary sent none, so it sat at
// {Tick:0 Month:0 Speed:1 Paused:true} for as long as you left it open, and
// no key was bound to Resume/SetSpeed/AdvanceTicks anywhere. A frozen sim
// and a broken binary looked identical.
//
// # WHERE THIS LIVES, AND WHY (design decision 1)
//
// package main, the composition root — NOT internal/ui, and NOT inside
// internal/engine/core.
//
//   - Not internal/ui: driving the simulation clock is not a rendering or
//     input concern, and GR#20 lint-bans internal/ui → internal/engine
//     imports outright. The driver reads engine.Clock() and sends
//     protocol.Commands; neither belongs behind a screen.
//   - Not internal/engine/core: engine.core is deliberately wall-clock-free
//     (doc.go and AC-12 — "nothing in this file reads the wall clock", and
//     clock.go's own advanceOneDay comment defers real-time pacing to "a
//     hypothetical future real-time autonomous driver owned by the
//     UI/transport layer"). Putting a time.Now() loop inside the
//     deterministic engine would poison exactly the property GR#21 protects.
//   - So: the composition root, where the engine, the transport, and the
//     process lifetime already meet. BUG-322's own finding calls this "not a
//     missing module — a missing connection", and a connection belongs where
//     the things being connected are already both in scope.
//
// It does NOT share the UI's cadence. ui.core's RenderLoop has its own 10 Hz
// ticker (run.go's core.RenderTick) and this driver has its own timer; the
// two are independent by construction — the driver never calls into the
// render loop and the render loop never calls into the driver. They meet
// only through the engine's own mutex-guarded state (Engine.Clock(), which
// is documented safe for concurrent use) and through the transport.
//
// # FALLING BEHIND THE WALL CLOCK (design decision 2)
//
// Policy: FIXED-STEP WITH BOUNDED CATCH-UP, DEGRADING TO SLOWDOWN.
// A tick is NEVER dropped, skipped, or coalesced.
//
// Every tick index the engine is asked for is contiguous: 1, 2, 3, … The
// driver's only freedom is WHEN it asks. Concretely:
//
//   - Real time is accumulated in `credit` nanoseconds. Each wake converts
//     as much credit as possible into whole ticks and keeps the sub-tick
//     REMAINDER, so the pacing has no rounding drift over a long session.
//   - A single AdvanceTicks command is capped at maxTicksPerBatch. Excess is
//     left in credit and emitted on the next wake — deferred, not dropped.
//   - If the simulation cannot keep up at all, credit is clamped at
//     maxCreditNanos (one game month of backlog). This is the deliberate
//     degrade: the GAME clock falls behind the WALL clock, and the player
//     sees the sim run slower than the requested multiplier. What it is not
//     is a dropped tick — the tick sequence stays contiguous, so a replay of
//     the same seed and the same command stream reproduces exactly (GR#21).
//     Without the clamp, a sim that ever fell behind would accumulate
//     unbounded debt and spiral: each wake asks for more work than the last.
//
// The alternative policies, and why not:
//   - "Drop ticks to stay on wall time" — rejected outright. Determinism is
//     defined on the tick count; a dropped tick makes the run unreproducible
//     and is exactly what GR#21 forbids.
//   - "Pure fixed-step, one tick per wake, no catch-up" — silently runs slow
//     the moment a wake is late (Windows timer granularity alone would do
//     it), which is drift by another name and is invisible to the player.
//
// # SHUTDOWN (design decision 3)
//
// The driver goroutine is owned by bootCore: started under w.ctx and tracked
// on w.wg, exactly like RunCommandLoop and router.Run. shutdown() cancels
// ctx, then wg.Wait()s, and only THEN calls transport.Close(). That ordering
// is what makes a send-on-closed-transport impossible here: the driver has
// provably returned before anything closes the channels it sends on. (Even
// if that ordering were ever broken, InProcTransport.SendCommand is
// close-safe by its own BUG-007 fix — it returns ErrTransportClosed rather
// than panicking — but the ordering, not that fallback, is the guarantee.)
// The driver owns no channel of its own, so there is nothing for anyone else
// to close, and it never touches the tcell screen, so it cannot race
// runInteractive's Fini/exit path.
//
// # PAUSED OR RUNNING AT BOOT (design decision 4)
//
// RUNNING. core.NewClock starts paused (genesis, Speed1x), so the driver
// issues one KindResume as its first act — see Run.
//
// Booting paused would be defensible only if the player could see that it
// was paused and had a key to un-pause it. Both now exist (the status line
// at the bottom of every screen shows RUNNING/PAUSED and the live tick, and
// Space toggles) — but "launch it and time is moving" is the whole point of
// this fix, and a binary that boots frozen is precisely what a player cannot
// distinguish from the bug being fixed here. So: running.
// ─────────────────────────────────────────────────────────────────────────

// driverPollInterval bounds how long the driver will sleep before
// re-reading the engine clock, regardless of how far away the next tick is.
//
// This is a RESPONSIVENESS bound, not a pacing value — no simulation
// quantity is derived from it, so GR#15's "values come from data" does not
// apply (the pacing itself comes entirely from data/pacing.json via
// secondsPerMonthAt1x). It exists because pause, resume and speed changes
// reach the engine ASYNCHRONOUSLY, as commands on the transport: when the
// player hits Space, the driver has no synchronous notification, it simply
// observes the new clock state on its next look. At 1x a tick is 16 seconds
// apart, so without this bound the driver would sleep straight through the
// keypress and the world would keep running for up to 16s after the player
// paused it.
//
// Deliberately NOT a wake channel signalled by the key handlers: a channel
// would have to be closed on shutdown (a whole class of send-on-closed and
// double-close hazards, plus a real race — a wake can arrive BEFORE the
// engine has processed the Resume it announces, so the driver would read
// "still paused" and go back to sleep forever). A bounded poll cannot get
// stuck. It costs one mutex-guarded struct copy per 50 ms.
const driverPollInterval = 50 * time.Millisecond

// maxTicksPerBatch bounds how many ticks one AdvanceTicks command may ask
// for while catching up. engine.core's own MaxAdvanceTicksPerCall is the
// hard ceiling the engine enforces (10 game years); this driver stays far
// under it deliberately — a batch is executed as one uninterruptible run
// inside the command loop, so an oversized batch makes the binary
// unresponsive to pause for the whole duration of it. One game month is the
// largest jump that still leaves the clock feeling controllable.
//
// Derived from engine.core's DailyTicksPerMonth rather than written as a
// literal, so it tracks the simulation's own definition of a month.
const maxTicksPerBatch = enginecore.DailyTicksPerMonth

// maxBacklogTicks caps the catch-up DEBT (see the "falling behind" section
// above): once the driver owes more than a game year of ticks — a laptop lid
// closed, a debugger breakpoint, a machine that simply cannot keep up —
// further wall-clock time stops accruing and the sim openly runs slower than
// real time instead of spiralling. Ticks already owed are all still
// delivered, in order, in maxTicksPerBatch-sized batches; nothing is skipped.
//
// It MUST be strictly larger than maxTicksPerBatch, and by a wide margin.
// This was originally set equal to it (one month each), which quietly made
// rule 2's "the excess stays in credit, deferred" untrue: the clamp trimmed
// the credit to exactly one batch's worth BEFORE the division, so a capped
// batch had nothing left to defer and the overflow really was dropped.
// TestBUG322_Accrue_BoundsOneBatchButDefersTheRest is the regression that
// found it and is what keeps the two bounds from collapsing together again.
const maxBacklogTicks = 12 * enginecore.DailyTicksPerMonth

// tickDriver advances the simulation in real time. Construct with
// newTickDriver and run exactly one Run per instance.
type tickDriver struct {
	// clock reads the engine's authoritative clock state (paused/speed).
	// A function, not the *Engine, so the driver depends on the one thing
	// it actually needs and a test can drive it without a full boot.
	// Engine.Clock is documented safe for concurrent use.
	clock func() (enginecore.Clock, error)

	// send issues one command toward the engine — transport.SendCommand in
	// production. Non-blocking by InProcTransport's own contract.
	send func(protocol.Command) error

	// secondsPerMonthAt1x is data/pacing.json's pacing knob, loaded ONCE at
	// boot and handed to BOTH the engine (WithSecondsPerMonthAt1x) and this
	// driver from that single load (GR#3, GR#15) — never re-read, never
	// re-derived from Clock.SecondsPerMonth(), whose integer division by
	// speed truncates (480/8 is exact, but a smaller pacing constant would
	// silently floor to zero and the driver would compute an infinite rate).
	secondsPerMonthAt1x int64

	// pollInterval is driverPollInterval in production; a field only so the
	// driver's own tests can tighten it without a package-level global that
	// parallel tests would share.
	pollInterval time.Duration

	// now is time.Now in production. Go's time.Now carries a monotonic
	// reading, so Sub is immune to wall-clock jumps (NTP, DST, the user
	// changing the system clock) — a tick driver that could be fast-
	// forwarded by an NTP correction would be a determinism hazard.
	now func() time.Time
}

// newTickDriver builds a driver over an engine clock reader and a command
// sender. secondsPerMonthAt1x must be the value already given to the engine.
func newTickDriver(clock func() (enginecore.Clock, error), send func(protocol.Command) error, secondsPerMonthAt1x int64) *tickDriver {
	return &tickDriver{
		clock:               clock,
		send:                send,
		secondsPerMonthAt1x: secondsPerMonthAt1x,
		pollInterval:        driverPollInterval,
		now:                 time.Now,
	}
}

// Run drives the clock until ctx is cancelled, then returns. It is the
// goroutine body bootCore starts; see the shutdown section of this file's
// header for why returning on ctx.Done() is sufficient and safe.
//
// Its first act is to issue KindResume (design decision 4: the binary boots
// running, and core.NewClock starts paused). A failed Resume is not fatal —
// the driver keeps polling and the world simply stays paused until the
// player presses Space, which is a visible, recoverable state now that the
// status line reports it, rather than a reason to kill the process.
func (d *tickDriver) Run(ctx context.Context) {
	d.resume()

	last := d.now()
	var credit int64 // nanoseconds of real time owed to the simulation

	timer := time.NewTimer(d.pollInterval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		now := d.now()
		elapsed := now.Sub(last).Nanoseconds()
		last = now
		if elapsed < 0 { // defensive: a non-monotonic now injected by a test
			elapsed = 0
		}

		c, err := d.clock()
		if err != nil {
			// Engine.Clock only fails on a struct-copied Engine
			// (ErrEngineCopied), which cannot happen for the engine bootCore
			// built and holds by pointer. If it somehow does, every
			// subsequent call fails identically, so spinning would be a hot
			// loop that never recovers: stop driving and leave the world
			// paused-in-effect rather than burn a core. The engine's own
			// registry-sourced error has already been constructed by
			// Clock(); nothing here can report it further (this goroutine
			// has no stderr and no correlation channel — the same
			// no-reporting-channel situation EngineStatusView documents).
			return
		}

		if c.Paused() || c.Speed() <= 0 {
			// Paused wall time must NEVER become catch-up debt: a world
			// paused for ten minutes must not sprint through 37 ticks the
			// instant it resumes. Dropping credit here is not dropping
			// ticks — no tick was ever owed while paused.
			credit = 0
			resetTimer(timer, d.pollInterval)
			continue
		}

		npt, ok := nanosPerTick(d.secondsPerMonthAt1x, c.Speed())
		if !ok {
			// An unusable pacing constant (non-positive, or so large the
			// nanosecond conversion overflows). NewClock already rejects
			// <= 0 at construction and data.LoadPacing validates the file,
			// so this is belt-and-braces: idle rather than divide by zero.
			resetTimer(timer, d.pollInterval)
			continue
		}

		var n int64
		n, credit = accrue(credit, elapsed, npt)
		if n > 0 {
			d.advance(n)
		}

		// Sleep until the next tick is due, but never longer than the poll
		// bound (so a pause/speed keypress is observed promptly).
		wait := time.Duration(npt - credit)
		if wait > d.pollInterval || wait <= 0 {
			wait = d.pollInterval
		}
		resetTimer(timer, wait)
	}
}

// accrue is the whole catch-up policy (design decision 2) as ONE pure
// function: given the sub-tick credit carried over from the last wake, the
// real nanoseconds that have elapsed since it, and the current nanoseconds
// per tick, it reports how many ticks to ask for now and what credit to
// carry into the next wake.
//
// Pure and total by design. Run's own loop is timing-dependent and therefore
// a poor place to prove anything: on a real timer, a wrong policy and a right
// one can produce the same tick count simply because the sleep length adapts.
// Extracted here, each of the three rules is independently falsifiable by a
// table test with no clock in it at all:
//
//  1. REMAINDER IS CARRIED. n is the whole ticks now due; the sub-tick
//     fraction stays in the returned credit. Discarding it would make the sim
//     run steadily slow whenever a wake overshoots (Windows timer granularity
//     alone guarantees it does), which is drift that nothing on screen would
//     reveal.
//  2. A BATCH IS BOUNDED. At most maxTicksPerBatch ticks per call; anything
//     over stays in credit for the next wake. Deferred, never dropped — the
//     tick the engine is next asked for is always the next tick in sequence.
//  3. BACKLOG IS BOUNDED. Credit is clamped to maxBacklogTicks' worth BEFORE
//     converting, so a sim that cannot keep up runs slower than the wall
//     clock instead of accumulating unbounded debt. Again: the clamp discards
//     WALL-CLOCK TIME, never a tick — nothing renumbers or skips a tick, so
//     GR#21 reproducibility is untouched.
//
// npt must be positive (nanosPerTick's contract); a non-positive npt returns
// (0, credit) rather than dividing by zero.
func accrue(credit, elapsed, npt int64) (n, remaining int64) {
	if npt <= 0 {
		return 0, credit
	}
	if elapsed > 0 {
		credit += elapsed
	}
	if maxCredit := npt * maxBacklogTicks; maxCredit > 0 && credit > maxCredit {
		credit = maxCredit // rule 3: slowdown, not tick loss
	}
	n = credit / npt // rule 1: integer division, the remainder survives below
	if n > maxTicksPerBatch {
		n = maxTicksPerBatch // rule 2: the excess stays in credit, deferred
	}
	return n, credit - n*npt
}

// resetTimer restarts an already-fired timer. Only ever called on a timer
// whose channel has just been drained by the select above (or by the
// previous iteration), which is the one case where a bare Reset is correct
// and no drain is needed.
func resetTimer(t *time.Timer, d time.Duration) {
	t.Reset(d)
}

// advance sends one AdvanceTicks command. Fire-and-forget: the driver does
// not await the CommandResult, matching bootCore's own sendPauseCommand
// posture. A send error (queue full, transport closed mid-shutdown) means
// this batch did not happen — the ticks stay owed in credit only if we did
// not already deduct them, so deliberately note: we DO deduct before
// sending, because a queue-full retry storm is worse than a lost catch-up
// batch, and the queue is only ever full when the engine is already
// saturated. The tick SEQUENCE is unaffected either way: ticks are numbered
// by the engine, never by this driver.
func (d *tickDriver) advance(n int64) {
	_ = d.send(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: n},
	})
}

// resume issues the boot-time KindResume (design decision 4).
func (d *tickDriver) resume() {
	_ = d.send(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindResume,
		Payload:         protocol.ResumePayload{},
	})
}

// nanosPerTick converts the data-sourced pacing constant and the current
// speed multiplier into the real-time interval between two logistics
// day-ticks.
//
//	secondsPerMonthAt1x seconds  = one game month at 1x
//	one game month               = DailyTicksPerMonth ticks (a fixed rule)
//	=> nanosPerTick = secondsPerMonthAt1x * 1e9 / (speed * DailyTicksPerMonth)
//
// At the shipped default (480 s/month, 1x) that is 16 s per tick; at 4x,
// 4 s. Computed in ONE expression from the un-truncated constant rather than
// via Clock.SecondsPerMonth(), which floors secondsPerMonthAt1x/speed and
// would report zero for any pacing constant smaller than the multiplier.
//
// Reports ok=false for a non-positive input or an overflowing conversion
// rather than returning a garbage interval (GR#16 boundary discipline).
func nanosPerTick(secondsPerMonthAt1x int64, speed enginecore.Speed) (int64, bool) {
	if secondsPerMonthAt1x <= 0 || speed <= 0 {
		return 0, false
	}
	totalNanos, overflowed := num.SafeMul(secondsPerMonthAt1x, int64(time.Second))
	if overflowed {
		return 0, false
	}
	divisor, overflowed := num.SafeMul(int64(speed), enginecore.DailyTicksPerMonth)
	if overflowed || divisor <= 0 {
		return 0, false
	}
	npt := totalNanos / divisor
	if npt <= 0 {
		// A pacing constant so small that a tick rounds to under a
		// nanosecond. Floor at 1 ns rather than dividing by zero later.
		npt = 1
	}
	return npt, true
}
