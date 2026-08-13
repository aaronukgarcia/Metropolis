// Package metricsdash is feat.metricsdash (module key pending
// confirmation — see ASM-451/Escalation C,
// docs/planning/acceptance/feat.metricsdash.md): a read-only
// aggregation/reporting layer over this project's own existing
// operational data sources, plus a low-friction logging entry point
// that files a real BOW record for a quick "this looks wrong"/"what is
// this number" observation.
//
// # Data sources (AC-1 / AC-14)
//
// Every metric this package displays traces to exactly one of:
//
//  1. `node claude-bow.js weakness` — the security/BOW finding-class
//     histogram and recurrence flags (weakness.go).
//  2. `node claude-bow.js gate-status <sprint>` — the current sprint
//     gate run's overall + per-check verdicts (gatestatus.go).
//  3. `node claude-bow.js lint` — the BOW prose-vs-dependency-graph
//     drift findings (lint.go).
//  4. H-SYNTH's perf-CI history — synth.LoadLatestBaseline's parsed
//     baseline/anchor records and synth.LoadAcceptedRegistry's
//     accepted-regression entries (perf.go).
//
// This package never invents a fifth source or re-derives any of the
// above from a parallel query (GR#3 — reuse, don't duplicate): sources
// 1-3 are read by shelling out to the existing `claude-bow.js` Node
// tool and parsing its real stdout (this package has no MariaDB driver
// and does not add one — see "Escalation B / ASM-453" below); source 4
// is read by calling directly into the already-exported Go functions
// H-SYNTH ships (synth.LoadLatestBaseline, synth.LoadAcceptedRegistry,
// synth.CompareToBaseline, synth.PerfRecord).
//
// # FEAT-065 / FEAT-066 boundary (AC-14)
//
// Stated here so it survives past the BA's own session (see
// feat.metricsdash.md's "Boundary vs FEAT-065" table verbatim):
//
//	                FEAT-065 (feat.devmode)          FEAT-066 (this package)
//	Subject         the running simulation            this project's own
//	                (paused state, entity/             build/test/security/
//	                object inspection)                 perf pipeline
//	Data source     live engine state (entities,       claude-bow.js
//	                ticks, world)                       (weakness/lint/gate-
//	                                                     status) + perf-CI
//	                                                     history
//	Logging         full capture: pause, inspect,       a quick one-call
//	affordance      attach context, file to BOW         note, reusing
//	                                                     FEAT-065's own
//	                                                     feedback-inbox
//	                                                     mechanism (AC-8)
//	Owns BOW-write  yes (builds it)                     no (consumes it,
//	plumbing?                                            GR#3)
//
// # Escalation A — resolved conservatively for this dispatch, flagged
//
// feat.metricsdash.md's Escalation A explicitly left open whether
// "in-UI" means an in-game tcell screen (internal/ui/screens/metrics)
// or an out-of-band CLI report, stated this could not be settled from
// FEAT-066's own text alone, and recommended Bill rule on it directly.
// No BOW comment on FEAT-066 recorded such a ruling as of this
// dispatch (checked via `node claude-bow.js show FEAT-066`). Per this
// dispatch's own standing instruction for a still-genuinely-ambiguous
// AC ("do not guess — build the most conservative/smallest-scope
// interpretation, clearly flagged"), this package is built as an
// OUT-OF-BAND CLI reporting tool (cmd/metricsdash) rather than an
// in-game screen: smaller surface (no internal/ui dependency, no GR#20
// import-boundary exposure, no new coupling to MOD-038 ui.dash), and
// the acceptance file itself says AC-1 through AC-6 hold either way.
// Logged as a fresh assumption (see the dispatch report for the ASM
// code). An in-game screen fronting this same package's exported
// BuildDashboard/Render/LogNote functions remains a reachable follow-up
// if Bill rules the other way — nothing here needs to be rebuilt, only
// a new UI-layer caller added.
//
// # Escalation B / ASM-453 — the MariaDB gap
//
// No Go code in this repository talks to the metro MariaDB directly
// (verified at the time of this dispatch: `grep -rn
// "mysql|mariadb|bow_items" internal/` returned nothing) — every BOW
// read/write goes through claude-bow.js (Node) today. This package
// does NOT add a Go-to-MariaDB path:
//
//   - The dashboard side (sources 1-3 above) shells out to the
//     existing Node tool rather than speaking SQL itself.
//   - The logging side (feedback.go) reuses FEAT-065's already-shipped
//     local-queue mechanism: internal/engine/debug's FeedbackRecord
//     JSON-file-per-submission, written to the same shared inbox
//     directory FEAT-065's debug.State.SubmitFeedback writes to, and
//     picked up by the same already-shipped claude-devfeedback-import.js
//     out-of-band script — not a second writer or a second importer.
//     See feedback.go's doc comment for the one known limitation of
//     that reuse (import attribution) and the ASM logged for it.
package metricsdash
