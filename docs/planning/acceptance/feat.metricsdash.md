BOW code: FEAT-066

# Acceptance criteria — feat.metricsdash (FEAT-066)

**BOW code:** FEAT-066 — "Backend metrics dashboard + easy in-UI defect/query
logging" (GUID `eaf4c606-7ace-4ab2-933a-67a02d6632a8`, P1, created
2026-08-11).
**Spec refs:** none in `docs/METROPOLIS-MASTER-v2.1.md` — FEAT-066 is
**process/tooling** work, not simulation-spec work, so there is no §-section
to cite. Its real spec is this project's own existing operational surfaces:
`node claude-bow.js weakness` / `gate-status` / `lint` (`claude-bow.js`,
commands documented in its own `case` block), and H-SYNTH's perf-CI gate
(`internal/harness/synth/cmd/perfci/main.go`, `internal/harness/synth/baseline.go`,
`accepted.go`, `results.go`). Also: `dev-team-process.md`'s "Assumptions are
logged or the work is rejected" section (v1.7) and its weakness-pattern
write-ups — the dashboard's job is partly to make those patterns visible
without a human re-running `weakness` by hand.
**Date:** 2026-08-11
**Status:** done (closed 2026-08-13, Destructively ACCEPTed round 2)
**Package under test:** proposed `internal/harness/metricsdash/` for the
dashboard's data-gathering/formatting core, `internal/ui/screens/metrics/`
for the in-game screen if Escalation A below resolves toward an in-game
surface — **neither exists yet; see Escalation A, this is a genuine open
question, not a typo.**
**Standard gates:** see `README.md` — all apply once a package path is
confirmed at dispatch.

## Why this file exists before the FEAT-065/FEAT-066 boundary is fully settled

FEAT-065 ("dev mode: pause-anywhere debug console, object-metrics
inspection, in-game feedback captured into the BOW") is being written in
parallel by another BA, in `feat.devmode.md`, which this file does not
touch. The two items' BOW summaries overlap in vocabulary ("metrics",
"feedback"/"defect... logging") enough that building both without a stated
boundary risks two teams building the same BOW-writing path twice. Rather
than wait for that file to land first (the pipelined-cadence rule in
`dev-team-process.md` says BAs run ahead, not serially), this file states
the boundary I read from FEAT-066's own text and BOW-priors, and flags the
one part that cannot be resolved from FEAT-066's side alone (Escalation A).

## Boundary vs FEAT-065 (read from each item's own BOW text)

FEAT-065's summary is explicitly about the **running simulation**:
"pause-anywhere debug console, object-metrics inspection, in-game feedback
captured into the BOW." Every noun in it is scoped to a live game session —
pausing the sim, inspecting simulated objects (cells/citizens/firms), and
capturing feedback about what the *player/dev is currently seeing in the
game*.

FEAT-066's summary is "**backend** metrics dashboard + **easy** in-UI
defect/query logging." Two words carry the distinction:

- **"Backend"** — the dashboard's subject is the project's own tooling and
  pipeline, not the simulated city. There is no existing in-game telemetry
  this item could plausibly be asked to invent from nothing (GR#15: expected
  values come from data/runtime, never invented) — but there **is** a large,
  real body of operational data already produced by this project's own
  process: `claude-bow.js weakness`'s finding-class histogram, `gate-status`'s
  sprint-gate verdicts, `lint`'s prose-vs-graph drift report, and H-SYNTH's
  perf-CI baseline/regression history. FEAT-066 surfaces *that*, per GR#3
  (don't build a parallel reporting surface when a real one already exists).
- **"Easy"** — modifying "in-UI defect/query logging" against FEAT-065's
  fuller "in-game feedback captured into the BOW" architecture implies a
  **lighter-weight, lower-friction subset**, not a second full capture
  pipeline. The natural reading: FEAT-065 builds *the* mechanism that turns
  an in-game observation into a BOW record (with whatever context-capture,
  pause-state, entity-snapshot richness that needs); FEAT-066 needs only a
  **quick entry point** into that same mechanism — a shortcut for "log a
  one-line defect or question right now" that does not require entering
  FEAT-065's full pause/inspect flow first.

**Stated boundary (this file's working assumption — see Escalation A):**

| | FEAT-065 (`feat.devmode`) | FEAT-066 (`feat.metricsdash`, this file) |
|---|---|---|
| Subject | The running simulation (paused state, entity/object inspection) | This project's own build/test/security/perf pipeline |
| Data source | Live engine state (entities, ticks, world) | `claude-bow.js` (`weakness`/`gate-status`/`lint`) + perf-CI's results/registry files |
| Logging affordance | Full capture: pause, inspect, attach context, file to BOW | A quick one-key/one-line prompt that files a minimal BOW record — reusing FEAT-065's BOW-write mechanism rather than a second one (see Escalation A) |
| Owns the BOW-write plumbing | Yes (builds it) | No (consumes it, per GR#3 — do not duplicate) |

This file's ACs are written to hold under **either** outcome of Escalation
A (shared plumbing vs FEAT-066 needing its own minimal writer), by stating
the *requirement* ("logging a defect must produce a real BOW record with
these fields, reachable in at most N keystrokes from anywhere") rather than
prescribing which package owns the write call.

## User stories

- **US-1.** As **Bill (lead)**, I need a single screen/report that shows the
  project's current weakness-class histogram, sprint gate verdicts, BOW lint
  drift, and perf-CI regression trend without running four separate
  `claude-bow.js` commands by hand, so a wave review or a health check is
  one look, not a manual roll-up (GR#3 — reuse, don't re-derive).
- **US-2.** As **any dev-team agent or Aaron**, mid-session, I need a
  low-friction way to log "this looks wrong" or "what is this number" as a
  real, findable BOW record without leaving what I'm doing to run a CLI
  command by hand, so small observations don't get lost between sessions
  (dev-team-process.md's "an assumption nobody wrote down is indistinguishable
  from a fact until it is wrong" — the same principle applies to a
  fleeting "this looks off" that never gets recorded).
- **US-3.** As **the perf-CI gate's own operator** (`internal/harness/synth/cmd/perfci`),
  I need the accepted-regression registry's and baseline history's current
  state visible somewhere a human actually looks, so a permanently
  could-not-evaluate baseline (BUG-094's shape) doesn't sit silently
  unnoticed the way `main.go`'s own doc comment warns it can.
- **US-4.** As **the independent QA agent**, I need the weakness-pattern
  recurrence counts (`dev-team-process.md`'s "the pattern count is the real
  deliverable") visible on the same dashboard as gate verdicts, so a
  recurring class and a failing gate can be read together rather than
  cross-referenced by hand across two tools.

## Scope

A read-only aggregation/reporting layer over this project's **existing**
operational data sources (BOW weakness/gate/lint outputs via
`claude-bow.js`; H-SYNTH's perf baseline/accepted-regression files) —
this item does not invent a new metrics-collection mechanism, it surfaces
what already exists (GR#3) — plus a low-friction logging entry point that
produces a real BOW `bug`/`finding`/`assumption` record (whichever type fits
the input) from as few interaction steps as the dispatched implementation
can manage, reusing FEAT-065's BOW-write mechanism if Escalation A resolves
that a shared one exists by dispatch time.

## Acceptance criteria

### Functional — the dashboard

- **AC-1 (grounded sourcing, not invented telemetry).** Every metric the
  dashboard displays traces to one of: `node claude-bow.js weakness`'s
  finding-class counts/recurrence flags, `node claude-bow.js gate-status
  <sprint#>`'s per-check verdicts, `node claude-bow.js lint`'s drift-finding
  list, or H-SYNTH's perf history (`synth.LoadLatestBaseline`'s parsed
  records from the results NDJSON file, and `synth.LoadAcceptedRegistry`'s
  entries). A passing test asserts each displayed metric's value is read
  from one of these four sources' actual output (via the same functions/
  commands, not a re-implemented parallel query) — **not** a hand-authored
  or hardcoded sample value standing in for it. **What a lazy implementation
  looks like:** a dashboard that renders plausible-looking tiles from a
  fixture JSON the developer wrote by hand, which looks identical to a real
  build in a screenshot but shows nothing true once deployed. **Check:**
  `grep -rn "claude-bow\|synth\.LoadLatestBaseline\|synth\.LoadAcceptedRegistry\|synth\.PerfRecord" <package>/*.go` (or the equivalent call-through for whichever
  language the dashboard's data layer is written in) shows the dashboard's
  data-gathering code actually calling into these sources, not a hardcoded
  literal reproducing their shape.
- **AC-2 (weakness histogram + recurrence).** The dashboard shows the
  finding-class histogram `cmdWeakness` already computes (class name, total
  count, open count) and flags any class at or above the recurrence
  threshold (`>= 3`, the same threshold `cmdWeakness` uses — GR#15, derive
  from the tool, do not re-pick a number) exactly as "recurring." A passing
  test seeds a fixture with one class at exactly 3 findings and one at 2,
  and asserts the dashboard flags the first as recurring and not the
  second — proving the boundary is read from the real threshold, not
  approximated. **False-pass warning:** a test that only checks a class with
  6 findings is flagged would also pass a build that used a threshold of 1
  or 4 instead of 3; the boundary case is the one that actually
  distinguishes them.
- **AC-3 (gate verdicts).** For the current/most recent sprint gate run
  (`latestGateRun` / `deriveOverallVerdict`'s logic), the dashboard shows
  the overall verdict and each of the five named checks
  (`data-files`/`call-edges`/`tripwires`/`boundary-rulings`/`ready-queue`)
  with its individual verdict — a passing test seeds a fixture gate run with
  a mixed set of verdicts (some pass, one fail, one skipped) and asserts all
  five appear with the correct individual verdicts, not just the rolled-up
  overall one. **What a lazy implementation looks like:** showing only the
  overall pass/fail and dropping the per-check detail — technically "shows
  gate verdicts" but useless for the US-1 "one look instead of four
  commands" goal, since a failing overall verdict with no per-check detail
  sends the reader straight back to the CLI.
- **AC-4 (BOW lint drift).** The dashboard surfaces `cmdLint`'s current
  finding count and, for each finding, the BOW code and the gate/dependency
  name it names — a passing test seeds a fixture lint result with 2 findings
  and asserts both appear with their BOW codes.
- **AC-5 (perf-CI trend).** For each preset with recorded history
  (`synth.LoadLatestBaseline`'s per-preset replay), the dashboard shows the
  current baseline's monthly-tick time, whether the most recent run
  regressed/passed/could-not-evaluate, and whether that commit is currently
  covered by an accepted-regression registry entry — a passing test seeds a
  fixture results file with a regressed run followed by an accepted-registry
  entry naming that exact commit, and asserts the dashboard shows the
  post-acceptance state (accepted, not "still regressed"), proving it reads
  the registry the same way `perfci` itself does rather than only the raw
  comparison.
- **AC-6 (missing-data handling is not an error).** A results file, accepted-
  registry file, or a given preset with no history yet is a normal,
  expected state (per `perfci`'s own AC-8/BUG-097 handling — a project can
  genuinely have never run a preset), not a dashboard error/crash — a
  passing test points the dashboard at a fresh checkout with no
  `perf-results.ndjson` and no `perf-accepted-regressions.json` present (this
  is the actual current state of this repository as of this file's writing —
  neither file exists yet locally) and asserts the dashboard renders a clear
  "no data yet" state for that section rather than failing to render at
  all.

### Functional — easy in-UI defect/query logging

- **AC-7 (a real BOW record, minimal friction).** From wherever the
  logging affordance is reachable, submitting a defect/query note results in
  a real, findable BOW record — an `add bug`, `add finding`, or `add
  assumption` (whichever the submitted note's own flag/shape selects; a bare
  free-text note with no type hint defaults to `bug` per the item's own
  triage-later intent) — carrying the note text, a timestamp, and enough
  context (current screen/module/file if reachable, else "unspecified") to
  be actionable later. A passing test submits a note through the logging
  entry point and asserts a real BOW item exists afterward with the
  submitted text, not merely that a local success message was shown.
  **False-pass warning:** a test that only asserts a "logged!" UI toast
  appeared, without checking the BOW actually gained a record, would also
  pass a build that dropped the note on the floor — exactly the shape
  weakness-pattern-#5/#6 warn about ("the rejection/success path" doing
  something other than what it claims).
- **AC-8 (reuse, not a second writer — conditional on Escalation A).** If
  FEAT-065 has, by this item's dispatch time, shipped a BOW-write mechanism
  for in-game feedback capture, this item's logging affordance calls
  through to that mechanism rather than re-implementing a second BOW-writing
  path (GR#3 — no duplication without validation). If FEAT-065 has not yet
  shipped a usable mechanism at this item's dispatch time, the developer
  **must** log this as an assumption naming the minimal writer it built
  instead and flag it for reconciliation once FEAT-065 lands, per
  dev-team-process.md's "Fixing a fix: log against the existing record as
  you go." **This AC cannot be given a code-level check today** — reuse
  can only be verified once FEAT-065's own artifact exists — so it is
  written as a dispatch-time obligation rather than a testable assertion; a
  Tester verifying this item after FEAT-065 has landed should escalate to
  Bill rather than PASS silently if two independent BOW-writers exist.
- **AC-9 (reachable without a live session).** The "easy" logging affordance
  must be reachable in materially fewer steps than FEAT-065's full
  pause/inspect console flow (no requirement to pause the simulation or
  select an entity first) — a passing test/manual-check description states
  the exact key sequence or command and confirms it does not require
  entering debug/pause mode first. **What a lazy implementation looks
  like:** gating the "easy" log entry behind the same F12/pause console
  FEAT-065 builds — that satisfies "logging exists" but fails the word
  "easy" the BOW item's own title uses, and duplicates FEAT-065's own
  friction rather than undercutting it.

### Error handling

- **AC-10 (GR#7).** A failed BOW write (metro MariaDB unreachable, a
  malformed submission) surfaces a registry-sourced error (new `MET-E`-range
  code) to the submitter and does **not** silently discard the note —
  carrying forward weakness-pattern-#5's rule (a guard/write path's failure
  must not destroy the thing it exists to protect, here: the observation the
  user was trying to record). Check: `grep -n "MET-" <package>/*.go` (the
  logging affordance's package, confirmed at dispatch) finds a registry code
  reference on the failed-write path; a passing test forces the BOW write to
  fail and asserts the submitter sees an explicit failure — **GR#7 assertion,
  stated explicitly (BUG-100 convention):** the test asserts the returned
  error's registry code matches AND that the note text itself still exists
  somewhere retrievable (the local queue, if AC-10's own retry mechanism
  exists — see Escalation B — or an explicit "not recoverable" state the
  submitter is shown), not merely that a matching-named test function exists
  and that a "logged!" toast was suppressed.
- **AC-11.** A malformed/unreadable perf-results or accepted-registry file
  (a torn write, corrupt JSON) is reported as a visible dashboard warning
  for that section, consistent with `perfci`'s own BUG-054 handling
  (corrupt lines are skipped and reported, not silently dropped) — the
  dashboard must not claim a clean state when the underlying file was
  actually unreadable.

### Determinism & safety

- **AC-12 (GR#20).** If the dashboard/logging code lives under
  `internal/ui/...` (Escalation A, in-game surface), `go list -deps` shows
  no import of `internal/engine/...` beyond what `int.protocol`/`ui.core`
  already permit — this item reads BOW/perf-CI data (external to the
  simulation entirely) and must not become a backdoor import path into
  engine internals.
- **AC-13.** Nothing this item adds affects simulation determinism —
  reading BOW/perf-CI data and logging a defect note must not touch tick
  state, world seed, or any deterministic-replay-relevant path. A passing
  test asserts invoking the dashboard/logging affordance mid-tick leaves
  the engine's own determinism-gate checksum unchanged.

### Documentation

- **AC-14.** The package doc states the module key (`feat.metricsdash`,
  pending Escalation A/module-key confirmation), lists the four data
  sources named in AC-1 explicitly (so a future contributor extending the
  dashboard knows GR#3 already governs "where does a new metric come
  from" — the existing tool, not a new one), and documents the FEAT-065/
  FEAT-066 boundary from this file's table so it survives past this BA's
  session.

## Out of scope

- Building the BOW-write mechanism itself if FEAT-065 ships one first — see
  AC-8/Escalation A. This item only needs an entry point into it.
- In-game/simulation object inspection (entity/cell/citizen JSON dumps,
  pause-anywhere) — `feat.devmode` (FEAT-065).
- Any new metrics-collection instrumentation inside the simulation engine
  itself (e.g. per-tick performance counters beyond what H-SYNTH's perf
  harness already produces) — not asked for by FEAT-066's own text, and
  building it would violate AC-1's "grounded in real existing data" rule by
  definition (there is no existing source to ground it in).
- Historical trend charting/graphing UI polish beyond "show the current
  state and the most recent history" — not specified by FEAT-066's summary;
  flagged as a possible follow-up, not built here.

## Escalations

- **A. For Bill — the FEAT-065/FEAT-066 boundary is my best reading, not a
  ruling, and one sub-question genuinely cannot be settled from FEAT-066's
  side alone.** FEAT-066's BOW summary does not say whether "in-UI" means
  (a) a screen inside the game's own tcell TUI (consistent with `ui.dash`'s
  existing tile substrate — MOD-038 — which already builds exactly the kind
  of widget-grid dashboard this item needs, just pointed at project-pipeline
  data instead of city data), or (b) an out-of-band reporting surface for
  the dev team only (a CLI report akin to `/health-check`, never seen by a
  player). I have written this file's ACs to be agnostic between the two
  (AC-1 through AC-6 hold either way) but AC-12's GR#20 import-boundary
  check only applies under reading (a). **Recommend Bill rule on this
  explicitly** — it changes the package path (`internal/ui/screens/metrics`
  vs. a root-level Node/Go CLI tool parallel to `claude-bow.js`) and whether
  `ui.dash`'s existing drill-through machinery (MOD-038) is a dependency.
- **B. For Bill — no Go code in this repository currently talks to the metro
  MariaDB at all.** `grep -rn "mysql|mariadb|bow_items" internal/` returns
  nothing; every BOW read/write today goes through `claude-bow.js` (Node) on
  a dev machine, not the shipped game binary. AC-7/AC-8/AC-10 (the in-UI
  logging affordance producing "a real BOW record") **assume the running
  game process can reach the BOW**, either directly (a Go MariaDB driver
  newly added to the game binary — a real dependency/attack-surface
  addition worth a deliberate decision, not a side effect of this item) or
  indirectly (the game writes a local queue file that a separate synced
  step files into the BOW later, mirroring how FEAT-065's "in-game feedback
  captured into the BOW" presumably also needs to solve this same problem).
  This is the same open question for both FEAT-065 and FEAT-066 and should
  be resolved once, not twice — recommend Bill decide the mechanism before
  either item's developer is dispatched, since a junior handed AC-7 as
  written today has no protocol/package to write the BOW call against.
- **C. Module key `feat.metricsdash` is this BA's proposal, not yet
  registered.** No `feat.metricsdash`/`tool.metricsdash`/similar key exists
  in `code.json` today. I chose the `feat.*` namespace (sibling of
  `feat.debugmode`, `feat.skeleton`) over `tool.*` (the namespace used for
  root-level Node hooks/guards like `tool.bow`, `tool.planguard`) because
  this item's *consumers* are read via `claude-bow.js`/H-SYNTH but its own
  *deliverable* is most likely a Go package (dashboard screen or reporting
  binary), consistent with the `feat.*` items rather than the root-tooling
  `tool.*` ones — but this is a judgement call Escalation A's ruling could
  overturn (an out-of-band CLI report might belong in `tool.*` instead).
  Logged as **ASM-451**.
- **D. For Bill — plan-owned missing edge (routes through master-plan + generate.js, not a hand-edit).** This item's shipped code imports `internal/harness/synth` (`internal/harness/metricsdash/perf.go` calls `synth.LoadLatestBaseline` / `synth.LoadAcceptedRegistry` / `synth.CompareToBaseline` / `synth.PerfRecord`), but `code.json`'s `feat.metricsdash` entry lists only `feat.devmode` and `foundation.errors` in its `outbound.calls` — the `feat.metricsdash → harness.synth` edge is a **phantom import** (a real Go import with no registered edge). It must be added in `master-plan-v2.1.json` and regenerated via `tools/plan/generate.js`; do not hand-edit `code.json` or the BOW. Flagged here so the plan-drift is on record rather than silently absorbed.
- **Assumptions logged separately (see report to Bill).** ASM-451 (module
  key), ASM-452 (in-game screen vs out-of-band report — this is the load-
  bearing one, since it changes package path and GR#20 applicability), and
  ASM-453 (no Go↔MariaDB path exists yet for either FEAT-065 or FEAT-066's
  logging affordance — the shared blocker, recommend resolving once for
  both items rather than twice).
- **Confirm-and-close (prior CC, FEAT-084 batch 2): ASM-452** — FEAT-066 in-game vs CLI resolved by Bill (ASM-476): out-of-band CLI ACCEPTED.
- **Confirm-and-close (prior CC, FEAT-084 batch 2): ASM-476** — BILL RULING: out-of-band CLI is FEAT-066's v1 surface.
