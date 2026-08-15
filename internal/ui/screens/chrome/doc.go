// Package chrome is the persistent chrome (FEAT-013): the top bar (date,
// clock-cycle, speed, money, population, rating — §13, line 246) and the
// bottom, prioritised, colour-coded alert stack, plus the crisis auto-pause
// control (§3, line 131: "Crisis events auto-pause and drop the camera/speed
// to the relevant queue").
//
// Module key: ui.alerts (see code.json)
// Spec refs:  §13 (docs/METROPOLIS-MASTER-v2.1.md line 246 — persistent
// chrome + the Alert stack); §3 (line 131 — crisis auto-pause); UI-SPEC §3
// (line 753 — "`!` jumps to top alert"); UI-SPEC §4 (the drill-through rule —
// an alert jumping to its screen is a drill-through instance, not a separate
// mechanism).
//
// # The crisis-vs-alert distinction (AC-6, AC-17)
//
// A crisis is a DISTINCT, EXPLICITLY-TAGGED subset of alerts, never derived
// from an alert's priority tier. Alert.Crisis is its own bool, independent of
// Tier: a P0/TierCritical "Loan payment due" is urgent but NOT a crisis (it
// must not auto-pause the game), while a crisis-tagged alert auto-pauses
// regardless of its display tier. This distinction exists because §3's
// auto-pause is about §3's kind of emergency (a service collapse, a terminal
// condition), not about "an important alert" — and conflating the two would
// make the game pause on routine urgent mail.
//
// # Auto-pause is a control, with edge-triggered + idempotent-redirect
// semantics (AC-8/AC-9/AC-10, AC-17)
//
// A future maintainer is most likely to "simplify" these three back into a
// bug, so each is stated as an invariant, not left implicit:
//
//   - Edge-triggered (AC-8): auto-pause fires ONCE per crisis identity, not
//     once per delta while the condition persists. The dedupe is keyed on
//     Alert.ID — the engine emitter's stable per-instance crisis identity
//     (FEAT-042 AC-25b) — and is recorded in seenCrisis.
//   - Re-arm (AC-9): a manual resume does NOT re-arm a still-ongoing crisis.
//     seenCrisis is never cleared by resume or resolve, so the same ID never
//     re-pauses, but a genuinely new crisis ID always does (AC-8's second
//     half is not broken by the resume path).
//   - Idempotent redirect (AC-10): Chrome never tracks "are we already
//     paused" — that state belongs to the engine. A new crisis therefore
//     ALWAYS issues both effects: Pause (idempotent at the engine) and
//     Navigate (NOT skipped just because the world was already paused).
//     Guarding the handler behind "if paused, return early" is the exact
//     lazy build these three ACs exist to reject.
//
// # Not built yet (dependency notes, not defects)
//
// Two carriers this package consumes do not exist in the tree at dispatch
// time, so Chrome consumes contract-first seams that stand in for them and
// are wired by the composition root when they land:
//
//   - Speed control. ui.core (MOD-009) has no pause/speed-control API yet,
//     so Chrome routes the pause through its own Effects.Pause seam;
//     PauseCommand returns the shared protocol.KindPause command the Space
//     global would send, so AC-7's "equivalent to Space, not a bespoke
//     pause implementation" is honoured by construction. Navigation, by
//     contrast, reuses the LANDED ui.dash seam (MOD-038, internal/ui/dash):
//     Chrome consumes dash.DrillTarget/dash.Navigator directly (AC-3's "no
//     second, parallel navigation path" rule, GR#3) rather than defining
//     its own Target type.
//   - The crisis tag's protocol home. int.protocol's Event now carries an
//     explicit, additive Crisis bool (ASM-222 / FEAT-042 AC-24), independent
//     of Severity and never derived from it. Chrome carries the same explicit
//     tag on its own Alert.Crisis and never derives it from tier (AC-6); the
//     Event→Alert mapping — including plumbing Event.Crisis through to
//     Alert.Crisis — is the composition root's job, not this package's: it
//     consumes the tagged Alert, it does not emit protocol Events. AC-13's
//     "no internal/engine import" holds regardless.
//
// # Files
//
//   - alert.go     — Tier, Alert, and NewAlert (the target-required
//     constructor, AC-3/AC-11; target is the consumed dash.DrillTarget).
//   - chrome.go    — Chrome, Effects, Figures, and the control surface
//     (AddAlert/ResolveAlert/Top/Alerts/JumpTo/JumpToTop/Subscribe/PauseCommand).
//   - stack.go     — lessAlerts and the sort/snapshot helpers (AC-5/AC-14).
//   - wire.go      — the "chrome.topbar" figures patch schema (AC-1).
//   - render.go    — Render and the top-bar/alert-stack drawing (AC-1/AC-2).
//   - bind.go      — RegisterBang, the `!` global via ui.keys (AC-4).
//   - copyguard.go — checkNotCopied (SEC-020 struct-copy rejection).
//   - errors.go    — the MET-U9xx registry codes this package claims.
//
// # Rules this package must not break
//
//   - No import of internal/engine (GR#20, AC-13): chrome figures, alerts,
//     and crisis events all arrive via int.protocol subscriptions/deltas/
//     events, decoded against this package's own wire shapes — never a
//     direct engine call.
//   - No wall-clock time (GR#21, AC-15): ordering, tie-breaks, and colour
//     use Alert.Tick (simulation time) and the alert's own fields; nothing
//     here samples the wall clock (foundation/errs stamps error records with
//     its own injectable clock, which is out of this package's control and
//     out of AC-15's grep scope).
//   - Deterministic ordering (GR#21, AC-14): the stack order is a pure
//     function of (Tier, Tick, ID) via lessAlerts, never Go map iteration.
package chrome
