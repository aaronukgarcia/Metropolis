// Package router implements ui.router (BOW MOD-115; ASM-1482;
// docs/planning/icd/ui.result-routing.md — "the ICD"), the transport-owning
// seam that drains protocol.Transport's three outbound channels
// (Results/Deltas/Events) on a single dedicated goroutine and dispatches
// each message to the screen that owns it.
//
// # Why this package exists (ASM-1482)
//
// internal/ui/screens/finance's ApplyResult(protocol.CommandResult) (once
// built) is correct but unreachable without a caller that actually drains
// Transport.Results() and routes each result to the screen that issued the
// causing command. No such caller existed anywhere in the codebase before
// this package: internal/ui/core's ViewsLoop (T-VIEWS) consumes only
// Deltas() and publishes raw patches to a ViewStore it never hands to a
// screen; there was no ui-side composition root at all. Router is that
// caller.
//
// # The ViewsLoop-composition choice (ICD Open Decision 1 + §7, Bev's call)
//
// The ICD leaves "new package vs. extend ui.core's ViewsLoop" as a
// build-time choice. This package is a NEW package (internal/ui/router,
// not an extension of internal/ui/core) that does NOT construct or run a
// core.ViewsLoop, and does NOT write into a core.ViewStore. Reasons:
//
//  1. ViewsLoop.Run drains transport.Deltas() on its OWN goroutine. The
//     ICD's single-writer discipline (§6/§7 — "the router is the sole
//     writer of the front snapshot") means at most one goroutine may ever
//     range over a given Transport's outbound channels; running Router.Run
//     and a separate ViewsLoop.Run side by side against the SAME transport
//     would split delta delivery between two independent readers
//     non-deterministically (Go channels have no fan-out — each delta goes
//     to whichever reader happens to receive it), which breaks GR#21's
//     "stable dispatch order" guarantee this whole seam exists to provide.
//     Composing "wrap ViewsLoop" therefore means REPLACING it, not running
//     both.
//  2. core.ViewStore's publish method is unexported — by design, only
//     ViewsLoop may write a front snapshot. This package's writable scope
//     for this increment is internal/ui/router/** only (Bev's build
//     brief); widening core's exported surface to let a second writer type
//     publish snapshots is a ui.core change, out of scope here.
//
// Router therefore owns its OWN minimal delta bookkeeping: a
// protocol.SeqTracker (the same exported, already-shared gap-detection
// primitive ViewsLoop itself uses) for per-subscription Seq-gap/duplicate
// detection, and nothing else — no shadow ViewStore, no independent
// staleness-dot state. A Delta is routed straight to the screen bound via
// BindSubscription (its ApplyDelta), which is where SF-screens already
// keep their own "have data / stale" bookkeeping (see e.g.
// internal/ui/screens/build/screen.go). Wiring Router into the real
// process composition root (FEAT-082, compose.go) in place of today's
// ViewsLoop — including whatever ViewStore/T-RENDER story that wiring
// needs — is a follow-up integration increment, NOT solved by this
// package. Until that wiring lands, ui.core's ViewsLoop/ViewStore continue
// to exist unchanged; this package does not modify or import them.
//
// # Routing surfaces (registration order, never map range — GR#21/§7)
//
//   - RegisterResultHandler(correlationID, ResultReceiver): one-shot,
//     per-command binding. Whoever mints a Command's CorrelationID (a
//     screen's command-issuing code) registers itself as that
//     CorrelationID's owner BEFORE (or at) SendCommand time. The
//     registration is consumed (deleted) the instant the matching
//     CommandResult is routed, so a correlation ID's owner is never called
//     twice. An owner that never receives its result (the command is still
//     in flight, or the CommandResult was evicted under the transport's
//     drop policy) is pruned once the router's Tick-tracked "how stale is
//     too stale" threshold (pendingTTLTicks, Tick-based per GR#21 — never
//     time.Now) is exceeded, logged via MET-V400 (kind
//     "result-stale-pruned") rather than leaking forever.
//   - BindSubscription(subscriptionID, DeltaReceiver): persistent binding,
//     one receiver per live subscription, mirroring ViewsLoop's
//     per-subscription model.
//   - RegisterEventRoute(kindPrefix, EventReceiver): an ordered slice of
//     (prefix, receiver) entries (never a map), applied in REGISTRATION
//     ORDER on every Event — every entry whose prefix matches
//     strings.HasPrefix(event.Kind, prefix) is dispatched to, in that
//     fixed order, so both ui.screen.ticker (kind-prefix routing) and
//     ui.alerts' crisis stack (severity/crisis routing, decided inside its
//     own ApplyEvent) can both receive the same Event without the router
//     picking a single winner.
//
// A message whose key (CorrelationID / SubscriptionID / Event.Kind) never
// matches any registration is a routing-table miss: it is NEVER silently
// dropped (GR#1/GR#17) — MET-V400 is raised with the miss kind and key in
// its context, and RouteMissCount() is incremented so pressure/misses are
// observable (ICD §10).
//
// # Error-code range note (2026-08-19)
//
// This package's registry codes are MET-V400..V402 (V400-V499), NOT
// V300-V399 — V300-V399 was originally claimed here but collided with an
// unmerged lane/ben claim for ui.screen.finance (BUG-276 first-claim-wins
// arbitration; verified by an independent round). Renumbered before
// commit; see errors.go for the full note.
//
// # Only finance is a real consumer today
//
// internal/ui/screens/finance does not exist as a Go package in this
// increment's worktree (it is registered in code.json at
// internal/ui/screens/finance/ but not yet built). Router therefore never
// imports any concrete screen package — every registration surface takes
// the small adapter interfaces (ResultReceiver/DeltaReceiver/EventReceiver)
// declared in router.go, never a screen's concrete type, so the router
// compiles and is fully testable (via stub receivers) whether or not any
// given screen package exists yet. build/map/proj/trade/chrome's
// ApplyResult additions (ICD Open Decision 3) are a LATER increment; the
// routing table above is table-driven specifically so those screens slot
// in later with no router change beyond a registration call at their own
// composition-root wiring site.
//
// # Receiver panics: recover, log, continue (GR#1)
//
// An independent Destructive round found this package had no recover()
// anywhere: a panicking receiver (a bug in some screen's ApplyResult/
// ApplyDelta/ApplyEvent) would propagate out of Run's goroutine and crash
// the ENTIRE UI process, not just drop the one poisoned message. Every
// receiver invocation therefore goes through a small invoke*Receiver
// wrapper (router.go) that defers a single recoverReceiverPanic call:
// on a panic, it recovers, raises MET-V403 (ErrReceiverPanic) naming the
// receiver kind ("result"/"delta"/"event"), the routing key
// (CorrelationID/SubscriptionID/Event.Kind), and the message's Tick,
// increments the observable PanicCount(), and returns normally so Run's
// select loop simply continues to the next message. This is deliberate
// recover-log-continue, not recover-log-reset-state: the router does NOT
// attempt to repair, retry, or roll back whatever partial mutation the
// panicking receiver made to its own internal state before panicking --
// that state is the receiving screen's own problem to keep consistent
// (or to crash-and-restart itself from, its own choice), exactly as T2's
// coalescible/lossy-by-design update class (§5) already treats one
// dropped/superseded message as an acceptable UX gap, never a router-level
// invariant break. The router's own state (pending/subs/eventRoutes,
// SeqTracker, currentTick) is never touched by a receiver's panic — the
// panic unwinds only as far as invoke*Receiver's own deferred recover,
// never past it — so a panic in one screen's handler cannot corrupt
// another screen's routing or the router's own bookkeeping.
//
// # What this package does NOT do (v1 scope)
//
//   - Does not construct, wrap, or write into a core.ViewStore (see above).
//   - Does not decide CommandResult's drop policy (ICD Open Decision 4 —
//     deferred to the freeze review); it only surfaces the current result
//     channel's occupancy (ResultBufferOccupancy) so pressure is
//     observable, per ICD §9's "surface result-buffer pressure" mandate.
//   - Does not interpret Delta.Patch payloads (protocol's own neutrality
//     rule, extended here exactly as ui.core's ViewsLoop already applies
//     it) — only validates it is well-formed JSON before handing it to the
//     bound receiver.
//   - Never calls time.Now (or any wall-clock source) anywhere in this
//     package's non-test source — every staleness/pruning decision is
//     driven by protocol.Tick values carried on the messages themselves
//     (GR#21; determinism_test.go greps for this mechanically, matching
//     the sibling screens' own SF-8/determinism_test.go convention).
package router
