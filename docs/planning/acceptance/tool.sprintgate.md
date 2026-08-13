BOW code: FEAT-061

# Acceptance criteria — tool.sprintgate

**BOW code:** FEAT-061 — status `open`, priority P1.
**Module key:** tool.sprintgate (new key; not yet in code.json — see Escalation
E).
**Spec refs:** GR#12 (Dependency & Completeness Check — this whole item exists
to mechanize exactly that rule: never mark a sprint ready without ALL its
entry conditions verified); GR#15 (Validators Derive From Data — every check
below reads live data, never a hand-maintained expectation); GR#23/FEAT-040
(the "prose verdict is not a verdict" lesson this item's own verdict-recording
requirement is built to avoid repeating); FEAT-047 + ASM-215/223/274 (check 1
precedent — empty spec-required data file silently blocking a sprint's ACs);
BUG-058 + ASM-219/233/243/334 (check 2 precedent — spec-mandated call path
with no registered code.json edge); BUG-100 (check 3 precedent — the tripwire
standard for "Check (once unblocked)" ACs); ASM-247 (check 4 precedent — a
boundary ruling recorded in only one of two affected briefs); FEAT-060
(sibling item, may not exist yet — check 5's dependency); FEAT-062 (sibling
item, may not exist yet — check 2's reuse escalation).
**Date:** 2026-08-11.
**Status:** new — pre-dispatch, no code exists yet.
**Package under test:** not yet built. Expected shape per AC-19: a new
`claude-bow.js` command family (`gate`, `gate-status`) plus a `bow_gate_verdicts`
table, and a runner script (location junior's call — `tools/sprintgate/` is
suggested, not mandated) invoked before the first dispatch of a sprint.
**Standard gates:** whatever language the runner is written in (Node, to match
`claude-bow.js`, is the natural choice but not mandated by this file) —
`node --check`/`go vet` as appropriate; a passing automated test suite for the
runner itself; SG-6 (no Co-Authored-By).

## Why this file is structured the way it is

FEAT-061 is a checklist that mechanizes five DIFFERENT recurring failure
classes, each with its own concrete precedent (named above) and its own
already-established convention in this project's acceptance-file estate. This
file does not re-derive those conventions — it cites them and requires the
gate to *check compliance with* them. Where a convention does not yet exist
(the boundary-ruling tag in check 4, most notably), this file proposes one
explicitly, as a design decision, not a silent assumption.

The sixth requirement — verdict recording — is written last and is the
heaviest section, because the brief is explicit that this project just
measured the alternative failing: `bow_destructive_verdicts` sat at zero rows
while GR#23 verdicts lived as prose (FEAT-040's 2026-08-11 QA-audit comment).
This file's own verdict mechanism is designed so the same failure cannot
recur by construction — see Section G.

## A. Scope determination — which items and files "the sprint" means

- **AC-1 (sprint membership is derived, never copied).** The gate determines
  sprint N's member items by querying `bow_items` where `sprint = N`
  (the column already populated by `master-plan-v2.1.json`'s per-item
  `"sprint"` field via `claude-bow.js import` — confirmed present:
  `grep -n '"sprint"' docs/planning/master-plan-v2.1.json`), never by reading
  the sprint number's row out of `docs/planning/sprint-plan-v1.md`'s markdown
  table by hand. Check: a passing test seeds a fixture DB with items on
  sprints 2 and 3, runs the scope-resolution function for sprint 3, and
  asserts it returns exactly the sprint-3 set (`grep -rn
  "func Test.*[Ss]printScope\|test.*sprint.*scope"` in the runner's test
  file). **What a lazy implementation looks like:** hardcoding sprint N's item
  list from today's read of `sprint-plan-v1.md` — passes today, silently goes
  stale the moment an item's `sprint` value changes without the markdown table
  being hand-edited in lockstep (GR#3 violation, and the exact "validators
  derive from data" failure GR#15 exists to name).
- **AC-2 (item → acceptance-file resolution, gaps reported not skipped).** For
  every OPEN or IN_PROGRESS item in scope (AC-1), the gate resolves its
  acceptance file at `docs/planning/acceptance/<mkey>.md` (the established
  convention this file itself follows) and, if the file does not exist,
  records that as an explicit finding for check 1/2/3 purposes — "no
  acceptance file for a sprint-N item" is itself gate-relevant information
  (an item about to be dispatched with no criteria at all is a strictly worse
  version of every failure class checks 1–3 look for), not a silent skip.
  Check: a passing test with a fixture item whose mkey has no corresponding
  file asserts the gate's output names the item and the missing path rather
  than omitting it from the report. **False-pass warning:** a check that only
  iterates *existing* acceptance files would never notice a sprint item with
  zero criteria at all — the worst case, and the one most likely to slip
  through if this AC is skipped.
- **AC-3 (scope is items, not sprint-plan prose).** Items whose `mkey` appears
  in sprint N's `sprint-plan-v1.md` row but whose `bow_items.sprint` value
  disagrees (or is NULL) are flagged as a drift finding, not silently
  resolved either way — this is exactly the kind of doc-vs-data disagreement
  FEAT-060's lint (check 5) is separately built to catch estate-wide; this
  gate only needs to notice it for the ONE sprint being gated, right now,
  before dispatch. Check: a passing test seeds an item with `sprint = 4` while
  its mkey also appears in the sprint-plan-v1.md fixture text for sprint 3(a
  disagreement) and asserts the gate reports the mismatch rather than picking
  a side.

## B. Check 1 — data files exist, schema-valid, placeholders approved

- **AC-4 (data-file references, extracted mechanically).** For every
  acceptance file in scope (AC-2), the gate extracts every `data/*.json` (or
  project-equivalent data-directory) path literal appearing inside a `Check:`
  clause — the FEAT-047/ASM-215/223/274 class names exactly this shape: an
  AC's own stated check reads a specific data file. Check: a passing test
  with a fixture AC file containing `` `data/modes.json` `` inside a `Check:`
  sentence asserts the extractor returns that path.
- **AC-5 (existence and non-placeholder-empty).** Every extracted path (AC-4)
  is verified to exist AND to be non-trivially-empty — valid JSON that is not
  `{}`, `[]`, or a whitespace-only/near-zero-byte stub. An empty spec-required
  file is a hard FAIL for check 1, citing the FEAT-047 precedent by name in
  the finding text (not a generic "missing file" message — the point is this
  exact failure class has already cost this project real time and the report
  should say so). Check: a passing test with a fixture `data/x.json`
  containing only `{}` asserts a FAIL finding whose text names the
  FEAT-047/empty-stub class.
- **AC-6 (schema validity — deferred to the file's OWN declared check where
  one exists).** "Schema-valid" is defined per-file by that file's own
  acceptance criteria, not by a gate-invented schema — where the in-scope AC
  names a loader test (e.g. `internal/foundation/data/modes_test.go`'s
  `TestModesInvalid`-shaped tests, per `data.modes-naming.md`'s own
  convention), the gate runs it and treats non-passing as a check-1 FAIL. **What
  a lazy implementation looks like:** the gate inventing its own JSON-Schema
  file, duplicating validation logic the data-authoring item's own acceptance
  file already specifies (GR#3 — single source of truth: the loader test IS
  the schema check, the gate should not grow a second one). Check: a passing
  test asserts the gate shells out to (or imports and runs) the named loader
  test rather than re-implementing field checks inline.
- **AC-7 (no declared schema check — explicit degrade, not silent pass).**
  Where an in-scope data file has no loader test/schema check named anywhere
  in its acceptance file yet, check 1 reports that data file as
  "existence-only verified, no schema check available" rather than marking it
  fully PASS — an unchecked schema is not the same claim as a checked one, and
  conflating them is exactly the "gate that can't evaluate must not report
  success" standard. Check: a passing test with a fixture data file that
  exists and is non-empty but whose acceptance file names no loader test
  asserts the check-1 finding for that file is `partial`, not `pass`.
- **AC-8 (placeholder-approval marker — defined here since none is codified
  estate-wide).** A numeric leaf value in an in-scope data file counts as
  **approved-placeholder or cited** if it sits inside (a) a `provenance`
  object carrying `source`/`sourceType` per `data.modes-naming.md` AC-6's
  established shape, or (b) a sibling `comment`/`$comment` field whose text
  contains a placeholder/pending-tuning disclosure matching `data/market.json`'s
  "Plausible v1 static price, pending M2 Batch tuning" convention (cited
  verbatim by `data.modes-naming.md` AC-16 as the pattern to reuse). A bare
  numeric field with **neither** sibling is flagged — not auto-failed, since
  this is a heuristic over JSON shape, not a certainty (see false-pass warning
  below), but every flagged field must appear by name in the check-1 report.
  Check: a passing test with a fixture object `{"x": 5}` (no `provenance`, no
  `comment`) asserts it is flagged; a fixture `{"x": 5, "comment": "placeholder
  pending M2 Batch tuning"}` asserts it is NOT flagged.
  **False-pass warning:** this heuristic can only detect the ABSENCE of a
  marker, never the QUALITY of one — a `comment` field containing the word
  "placeholder" attached to a number that was never actually reviewed by
  Aaron still passes this check, matching `data.modes-naming.md` AC-6's own
  false-pass warning about lazy citation strings. A Destructive agent
  attacking this gate should specifically try a fixture with a marker present
  but content-free (e.g. `"comment": "placeholder"` with no "pending tuning"
  qualifier, or a `sourceType` outside the closed enum).
- **AC-9 (dependency on data-authoring items' own approval marking —
  escalation, not solved here).** This gate's ability to tell "placeholder"
  from "Aaron-approved final value" depends entirely on data-authoring items
  (FEAT-047 and its future siblings) actually marking approval status inside
  the file once Aaron signs off a balance pass — nothing in the current
  `data.modes-naming.md`/FEAT-047 criteria defines what an *approved* (as
  opposed to *disclosed-placeholder*) marker looks like, because no balance
  pass has happened yet for these files. Flagged as a forward dependency: when
  FEAT-047-class items reach their balance-approval stage, their acceptance
  criteria should define the "approved" marker shape (e.g. `sourceType:
  "approved"` alongside `"literature"`/`"derived-placeholder"`) so this gate
  can distinguish "flagged as placeholder, not yet reviewed" from "reviewed
  and accepted as-is." Until then, check 1 can only report presence/absence
  of a disclosure marker, not approval status — the gate's report must say so
  explicitly rather than imply it verified approval it cannot yet see.

## C. Check 2 — call edges registered in code.json

- **AC-10 (edge assertions, extracted mechanically).** For every acceptance
  file in scope, the gate extracts every specific module-to-module call
  assertion an in-scope AC's `Check:` clause names — both prose form
  ("a registered `engine.X`→`engine.Y` edge") and the `node -e` one-liner
  form BUG-100's tripwires already use (`m.outbound.calls.some(c=>c.key===
  'engine.Y')`). Check: a passing test with a fixture AC sentence containing
  either form asserts the extractor returns the `(source, target)` mkey pair.
- **AC-11 (edge existence verified against the live registry).** Each
  extracted pair is checked against the CURRENT `code.json` (loaded fresh at
  gate-run time, never a cached copy) for a real registered outbound call
  from source to target. Missing edges are a check-2 FAIL, citing the BUG-058
  precedent by name — an AC asserting a call path with no registered edge
  makes the corresponding code illegal to write under GR#20, which is the
  exact defect class BUG-058 exists to name. Check: a passing test with a
  fixture `code.json` lacking the pair from AC-10's fixture asserts a FAIL
  finding naming both mkeys.
- **AC-12 (escalation — reuse FEAT-062's verifier, do not duplicate).**
  FEAT-062 (code.json bidirectional consistency audit, sibling item, may not
  be built yet) is scoped to independently need "every registered call edge
  corresponds to an actual import/call relationship in the code" — the same
  underlying edge-resolution logic AC-11 needs. This file does NOT specify a
  second, independently-maintained edge-checking implementation. If FEAT-062
  exists and exposes a reusable check (function, CLI subcommand, or library
  import) by the time tool.sprintgate is built, check 2 MUST call it rather
  than re-implement edge lookup; if FEAT-062 does not yet exist or exposes no
  reusable interface, tool.sprintgate implements the minimal direct-lookup
  version in AC-11 and files a follow-up assumption to consolidate later
  (GR#3 — duplicated logic without a linking note is itself a violation).
  Check: this AC is a build-time design constraint, not independently
  testable by a fixture — the Tester verifies it by reading the runner's
  source for either a call into FEAT-062's exposed interface or a logged
  assumption cross-referencing it, one or the other, never neither.

## D. Check 3 — "Check (once unblocked)" tripwires are armed

- **AC-13 (deferred-check ACs, found by BUG-100's own phrase).** The gate
  scans every in-scope acceptance file for the literal phrase `Check (once
  unblocked)` — the exact wording BUG-100 established as the marker for a
  deliberately deferred check. Check: a passing test with a fixture file
  containing the phrase asserts it is found and its containing AC number is
  recorded.
- **AC-14 (armed = a real, independently-executable tripwire, not prose).**
  Every match from AC-13 must have, in the SAME AC block, a `Tripwire
  (mechanical...)` label immediately followed by a command that (a) is
  copy-pasteable and runs standalone (the `node -e "...code.json...
  process.exit(...)"` one-liner shape BUG-100's remediation already
  established across ~30 ACs is the reference example, cited by name) and (b)
  documents its OWN expected exit code for the still-blocked state. A "Check
  (once unblocked)" phrase with no adjacent tripwire block at all is an
  automatic check-3 FAIL — this is BUG-100's own "a deferred check needs a
  mechanical tripwire that fails when its blocker clears unarmed, not just a
  prose label" standard, applied mechanically instead of by audit. Check: a
  passing test with a fixture AC containing the phrase but no `Tripwire`
  block asserts FAIL; a fixture AC with both asserts PASS-eligible (subject to
  AC-15).
- **AC-15 (the tripwire's live exit code must match its own documented
  expectation).** The gate actually RUNS every tripwire command found by
  AC-14 and compares its live exit code to the code the AC text itself
  documents as "still blocked" (e.g. "`must exit 0` (edge still absent)").
  Agreement is a check-3 PASS for that AC; disagreement means the blocker has
  cleared and the AC is now stale, unarmed prose exactly as BUG-100's own
  files warn ("nonzero means re-arm this AC") — this is a check-3 FAIL, with
  the finding text explicitly instructing "re-arm before dispatch," not
  merely noting a discrepancy. Check: a passing test with a fixture tripwire
  one-liner and a fixture `code.json` engineered so the live exit code
  DIFFERS from the AC's documented expectation asserts a FAIL naming
  re-arming as the required action.
  **False-pass warning:** a check-3 implementation that only verifies a
  tripwire block's TEXT exists (AC-14) without executing it (AC-15) would
  pass a tripwire whose command has a typo and always exits 0 regardless of
  the real registry state — this is precisely BUG-100 item 5's "engine.mining
  AC-12's misspelled test-name grep" defect class, and check 3 exists to catch
  its NEXT occurrence mechanically rather than by a future audit.

## E. Check 4 — cross-module boundary rulings cited in both affected briefs

- **AC-16 (tagging convention — proposed here, since none exists today; a
  design decision, not a silent assumption).** Going forward, a BOW comment
  recording a cross-module boundary ruling SHOULD open with the literal
  marker `[boundary ruling: <mkeyA> <-> <mkeyB>]` before its prose (e.g.
  `[boundary ruling: engine.citizens <-> engine.households]`). This is a new
  authoring convention this file introduces; it does not retroactively apply
  to already-written comments like ASM-247's (tagged `[lead ruling]`, not this
  marker). Check: `grep -rn "\[boundary ruling:" `-style search across
  `bow_comments` (via a `claude-bow.js`-equivalent query, since comments live
  in MariaDB not files) finds newly-tagged comments once the convention is
  adopted.
- **AC-17 (retroactive/untagged detection — a heuristic fallback, belt and
  suspenders).** Because AC-16's marker cannot retroactively tag ASM-247-style
  comments, check 4 ALSO runs a keyword heuristic: a BOW comment or item
  description containing the word "boundary" (or "owns"/"ownership" adjacent
  to two distinct module mkeys within the same text) is flagged as a
  CANDIDATE boundary ruling requiring human confirmation, not an automatic
  finding — this heuristic will both over- and under-match, and the report
  must label it explicitly as a candidate list, never merged silently into
  AC-16's confirmed-tag findings. Check: a passing test with a fixture comment
  matching ASM-247's actual wording ("I resolved this as: engine.citizens
  owns...") asserts it appears in the candidate list.
- **AC-18 (cross-citation check, for confirmed and candidate rulings alike).**
  For every ruling identified by AC-16 or AC-17 naming modules A and B, the
  gate checks whether BOTH `docs/planning/acceptance/A.md` and
  `docs/planning/acceptance/B.md` (or, per the ASM-247 precedent, whichever
  two files/dispatch-brief locations the ruling names) contain either the
  ruling's text or an explicit citation to its BOW code (e.g. "ASM-247"). A
  ruling present in only one of the two is a check-4 finding, citing the
  ASM-247 precedent by name (the exact "flagging so the [two] devs agree on
  the boundary before both build" gap that ruling itself named). Check: a
  passing test with fixture files where module-A's acceptance file cites the
  ruling and module-B's does not asserts the finding names module-B as the
  missing side.
- **AC-19 (report-only, stated explicitly — this check does not block
  dispatch by itself).** Per the brief's own acknowledgment that check 4 is
  "harder to fully mechanize," a check-4 finding is recorded and surfaced in
  the gate verdict (Section G) but does NOT, on its own, force a FAIL verdict
  for the sprint the way checks 1–3 do — it is advisory, mirroring how the
  independent QA agent's own findings are advisory rather than blocking. This
  must be stated in the runner's own output, not left to be inferred, since a
  silently-non-blocking check is easy to mistake for "passing."

## F. Check 5 — ready-queue dependency truthfulness (FEAT-060)

- **AC-20 (graceful degrade when FEAT-060 does not exist yet).** The gate
  attempts to resolve FEAT-060's lint command; if `claude-bow.js` has no
  matching command/subcommand AND `node claude-bow.js show FEAT-060` reports
  a status other than `done`, check 5's verdict is `skipped` with detail text
  "FEAT-060 not yet available, skipped" — this is NOT a FAIL and NOT a silent
  omission from the report (Section G records `skipped` as a first-class
  verdict value, not an absent row). Check: a passing test with FEAT-060
  absent/open in a fixture BOW asserts check 5's recorded verdict is
  `skipped` with that exact detail text, and that a `bow_gate_verdicts` row
  still gets written for check 5 (AC-25 — no check is ever silently
  unrecorded).
- **AC-21 (once FEAT-060 exists, it must actually run and be green).** Once
  FEAT-060 ships a runnable lint, check 5 executes it and requires a clean
  (no-findings, or explicitly all-findings-acknowledged) result; a non-green
  FEAT-060 result is a hard check-5 FAIL — a lying ready-queue is exactly the
  BUG-012 class this item's own description cites ("BUG-012's own description
  names three gating items... none of which are wired as bow_dependencies
  rows"). Check: a passing test with a fixture FEAT-060 runner returning a
  nonzero/findings-present result asserts check 5 FAILs.
- **AC-22 (assumption never hardcoded that FEAT-060 exists — checked live,
  every run).** Check 5 re-resolves FEAT-060's availability on EVERY gate
  run, never caching "not available" from a prior run — FEAT-060 is being
  built in parallel by a sibling BA/junior right now, and a gate run that
  assumes yesterday's absence is still true today would itself be the exact
  "validators derive from data, not a hardcoded constant" violation GR#15
  exists to prevent. Check: a passing test runs the scope-resolution twice
  with FEAT-060 fixture status changed from open to done between runs and
  asserts the second run's check-5 verdict changes from `skipped` to a real
  pass/fail.

## G. The verdict-recording requirement (the item's own stated deliverable)

- **AC-23 (a new dedicated table, not a repurposed `bow_comments` row —
  justified against the destructive-verdict lesson).** The gate's PASS/FAIL/
  PARTIAL/SKIPPED verdict, per check, per sprint run, is written to a NEW
  table, `bow_gate_verdicts`, mirroring `bow_destructive_verdicts`' append-only
  shape (FEAT-040 precedent) rather than a `bow_comments` entry with an
  informal tag convention. **Why a new table and not a structured comment:**
  `bow_comments` already exists and a "structured/taggable format" inside it
  was the FIRST option this brief offered — but `bow_destructive_verdicts`
  was created for the identical reason a comment-only convention had already
  failed once (Destructive verdicts lived as prose comments for the entire
  life of GR#23 before FEAT-040, and the QA audit found zero rows anywhere
  queryable). A gate verdict has the SAME shape of risk (five distinct
  per-check values plus a derived overall, needed for a mechanical PreToolUse-
  style query later) and the SAME failure mode if left as prose (a lead
  writing "checked, looks fine" in a comment is indistinguishable, to a
  future query, from a lead who never checked at all). Proposed schema
  (build-time detail, not gate-runner logic — this is a `claude-bow.js`
  change, out of this BA's file-ownership scope, see Escalation D):
  ```sql
  CREATE TABLE IF NOT EXISTS bow_gate_verdicts (
    id            INT AUTO_INCREMENT PRIMARY KEY,
    guid          CHAR(36) NOT NULL UNIQUE,
    gate_run_guid CHAR(36) NOT NULL,        -- groups the (up to) 5 check rows of one gate run
    sprint        INT NOT NULL,             -- the sprint number gated (bow_items.sprint's own domain — AC-1)
    check_number  TINYINT NOT NULL,         -- 1..5, per this file's own section lettering
    check_name    VARCHAR(64) NOT NULL,     -- 'data-files' | 'call-edges' | 'tripwires' | 'boundary-rulings' | 'ready-queue'
    verdict       ENUM('pass','fail','partial','skipped') NOT NULL,
    runner        VARCHAR(128) NOT NULL,    -- who/what ran the gate (agent/session identity)
    detail        TEXT NULL,                -- findings, BOW codes filed, tripwire output, etc.
    created_at    TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    INDEX idx_gate_sprint (sprint),
    INDEX idx_gate_run (gate_run_guid)
  ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
  ```
  Note this keys on `sprint` (an integer, already a first-class `bow_items`
  column per AC-1) rather than inventing a fictitious "sprint anchor" BOW
  item — no such item currently exists anywhere in the BOW, and manufacturing
  one only to hang a foreign key off it would itself be a GR#15 violation (a
  hardcoded item standing in for data that already exists as a column). See
  Escalation A for why this reading of "the sprint's BOW anchor" was chosen
  over inventing a new item type. Check: once built, `grep -n "CREATE TABLE
  IF NOT EXISTS bow_gate_verdicts" claude-bow.js` shows the table; a passing
  test inserts and reads back a row.
- **AC-24 (CLI surface — record and query).** `claude-bow.js` gains two
  commands (exact usage, for the future implementer to match): `node
  claude-bow.js gate <sprint#> --check <1-5> --name <data-files|call-edges|
  tripwires|boundary-rulings|ready-queue> --verdict pass|fail|partial|skipped
  --runner "<name>" [--detail "..."]` to record one check's row, and `node
  claude-bow.js gate-status <sprint#>` to report the LATEST run's five rows
  plus the derived overall (AC-26) — mirroring `destructive`/`verdict`'s own
  record/query pairing (FEAT-040 precedent, cited directly). Check: once
  built, a passing test runs `gate` five times for one `gate_run_guid` then
  `gate-status` and asserts all five rows plus a correct derived overall are
  reported.
- **AC-25 (every check writes a row, every run — no check is ever silently
  unrecorded).** A gate run that completes writes exactly 5 rows (one per
  check, `check_number` 1–5) sharing one `gate_run_guid`, even where a check's
  verdict is `skipped` (AC-20) or where check 4 is advisory-only (AC-19) —
  "advisory" and "skipped" are still recorded verdicts, never an absent row.
  A gate run that crashes/errors before writing all 5 rows must not leave a
  PARTIAL set of rows presented as if it were a complete run — either it
  writes all 5 (possibly with a `verdict='fail', detail='gate runner
  crashed: <error>'` row for whichever check was mid-flight) or it writes
  none and the runner's own exit status makes clear no verdict was recorded
  at all. Check: a passing test forces the runner to throw mid-check-3 and
  asserts either 5 rows exist (with check 3 marked failed/crashed) or 0 rows
  exist — never a silent 2-row partial state queryable as if complete.
- **AC-26 (overall verdict is DERIVED from the 5 rows, never a 6th
  hand-set field).** `gate-status`'s reported "overall" verdict for a sprint
  is computed, every time it is queried, from the latest run's 5 rows —
  FAIL if any of checks 1/2/3/5 is `fail` (check 4 per AC-19 does not
  contribute to overall FAIL); PARTIAL if none `fail` but at least one is
  `partial` or `skipped`; PASS only if all of checks 1/2/3/5 are `pass` (check
  4's `pass`/finding state is reported alongside but does not gate). This is
  never stored as an independent field a human or agent could set
  inconsistently with the underlying rows (GR#15 — derive, don't duplicate;
  the exact drift risk a freeform 6th verdict field would reintroduce). Check:
  once built, a passing test seeds 5 rows with check 2 = `fail` and asserts
  `gate-status`'s reported overall is `FAIL`; a second test seeds all `pass`
  except check 5 = `skipped` and asserts overall is `PARTIAL`.
- **AC-27 (append-only, latest-run-wins — mirrors FEAT-040's own accepted
  design).** A re-run of the gate (e.g. after fixing a check-1 finding) INSERTS
  a new `gate_run_guid`'s worth of rows rather than mutating the prior run's
  rows — preserving the full history of what was found and when, the same
  reasoning Bill's FEAT-040 ruling gave for `bow_destructive_verdicts`
  ("a mutable field would erase the reject/fix/accept history... a weakness
  class that resisted is evidence, not noise" — the direct analogue here is
  that a check-1 FAIL that got fixed before a clean re-run is evidence the
  gate caught something real, and erasing it would erase that evidence).
  `gate-status` always reports the run with the greatest `created_at`
  (ties broken by `id`, matching `latestDestructiveVerdict`'s own tiebreak).
  Check: once built, a passing test records two runs for the same sprint and
  asserts `gate-status` reports only the second run's rows.
- **AC-28 (dispatch is blocked, not merely warned, on a missing or
  FAIL/overall verdict).** Whatever mechanism dispatches the first item of
  sprint N (today: a human/lead reading `/sprint` and choosing to brief a
  junior; potentially a future dispatch-guard hook) MUST treat "no
  `bow_gate_verdicts` rows exist for sprint N's latest run" identically to
  "overall verdict is FAIL" — an unrecorded gate is not a passed gate, the
  same standard GR#23's own commit gate applies to Destructive verdicts. This
  is a process requirement on `docs/planning/dev-team-process.md`/`/sprint`,
  not something `tool.sprintgate`'s own runner code can enforce by itself
  (the runner cannot force anyone to read its output) — flagged as
  Escalation C, not silently assumed solved by this file.

## Out of scope (stated, not silently absent)

- Automatic remediation of ANY finding (re-writing a data file, registering a
  code.json edge, re-arming a tripwire, adding a missing citation) — this
  gate reports and records, it never edits acceptance files, `code.json`, or
  data files itself. Doing so would also collide with `code.json` being
  generated-only (GR#3/plan-pipeline constraint FEAT-062 already states).
- A UI/dashboard presentation of gate history — `gate-status`'s console
  output (AC-24) is the only interface this file requires; a richer view is a
  separate item if wanted.
- Enforcing AC-16's new tagging convention retroactively across the existing
  BOW comment estate — AC-17's heuristic is the only retroactive coverage
  this file requires.

## Escalations

- **A. "The sprint's BOW anchor item" does not exist as a concept today —
  resolved here as "the sprint number," not a new item.** FEAT-061's own
  description says the output is "a recorded gate verdict on the sprint's BOW
  anchor" but no BOW item type or convention currently represents "sprint N"
  as a single addressable item — sprints are a `bow_items.sprint` integer
  column value shared by many items, not an item themselves. AC-23 resolves
  this by keying `bow_gate_verdicts` on the `sprint` integer directly rather
  than inventing a synthetic anchor item, on GR#15 grounds (don't manufacture
  data that already exists as a column). If Bill wants a real "sprint" BOW
  item type instead (so sprints can carry their own comments/dependencies the
  way modules/features do), that is a bigger, separate design change to
  `claude-bow.js`'s type system and should be rejected or accepted explicitly,
  not adopted implicitly by this file.
- **B. Checks 2 and 4 both depend on sibling items that may not exist when
  tool.sprintgate is built (FEAT-062, and the general boundary-ruling
  estate).** AC-12 and the FEAT-062 reuse note are written to degrade
  gracefully (implement the minimal direct version, log an assumption to
  consolidate later) rather than block tool.sprintgate's own dispatch on
  FEAT-062 landing first. Flagging for Bill: if FEAT-062 is expected to land
  well before tool.sprintgate is built, it may be cheaper to sequence
  tool.sprintgate's dispatch AFTER FEAT-062 explicitly, rather than paying for
  a throwaway direct-lookup implementation that gets replaced immediately.
  Not decided here — a dispatch-sequencing call, not a criteria call.
- **C. This file cannot make dispatch actually consult the gate.** AC-28
  states the requirement but the ENFORCEMENT point is `/sprint` and/or a
  future dispatch-guard hook, both outside `tool.sprintgate`'s own code. Until
  one of those is updated to query `gate-status` before a junior is briefed,
  this entire item is a checklist someone has to remember to run — the same
  "remember to do it" failure mode GR#23's own history (prose Destructive
  verdicts, unenforced for its whole life until FEAT-040) already
  demonstrated once on this project. Recommend Bill treat "wire gate-status
  into `/sprint`'s step 1" as a required follow-up, not an optional nice-to-have,
  given the precedent.
- **D. `claude-bow.js` changes (AC-23/AC-24's table and commands) are outside
  this BA's file ownership.** This file specifies the exact schema and CLI
  surface the junior building FEAT-061 must add to `claude-bow.js`; it does
  not (and per this dispatch's explicit instruction, must not) edit
  `claude-bow.js` itself. Flagged so the Tester checks the ACTUAL
  `claude-bow.js` diff against AC-23/24's stated shapes, not just the
  runner script.
- **E. `tool.sprintgate` is not yet a `code.json` module key.** This file
  cites it as the module key throughout (matching the file's own name,
  `tool.sprintgate.md`), but no `code.json` entry exists for it yet — flagged
  for whoever runs `/register-guid` or the master-plan update once this item
  is scheduled for build, so the acceptance file's own key isn't orphaned
  the way BUG-088's guard family already was for `tool.authorguard`.
