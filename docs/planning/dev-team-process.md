# Metropolis Dev-Team Process v1.6

**2026-08-08/09 · directed by Aaron (v1.1: Tester + Documentation; v1.2: BA; v1.3: independent QA; v1.4: pipelined cadence; v1.5: Resource Manager, saturation rule, team caps, heavy checkpointing; v1.6: second Tester) · supersedes the wave-1 "lead reviews everything directly" flow**

## Second Tester (v1.6 — Aaron, 2026-08-09)

The Tester cap rises from 1 to **2**. The single Tester was the pipeline's queue bottleneck: a wave of parallel juniors delivers faster than one verifier can clear, and items sat waiting on a verdict while their dependants idled.

The two Testers are **independent verifiers, not a team**: they never communicate with each other, they own **disjoint items** (the lead assigns), and each reports its verdict directly and only to Bill. One item never gets two verdicts — that would invite verdict-shopping and dissolve accountability for a bad PASS. Everything else in the Tester mandate is unchanged: verification only, never edits, never suggests fixes, a FAIL goes straight back to the same junior.

## Saturation rule & Resource Manager (v1.5)

**No agent idles while non-blocking work exists.** A dedicated **Resource Manager (RM)** agent — persistent, reporting DIRECTLY to Bill — owns the assignment board:

- Maintains `docs/planning/team-board.md`: every agent, current assignment, status (busy/blocked/idle), what they return to when unblocked.
- When an agent blocks (e.g. a dev waiting on a Tester verdict), the RM proposes interim work for it and remembers the return point; when the blocker clears, the RM proposes the return.
- **Team caps** (RM enforces by flagging breaches to Bill): max **4 concurrent Jnr developers**, **2 Testers** (raised from 1 by Aaron, 2026-08-09, to cut verification-queue latency — see v1.6 note below), **BAs as needed** (cap lifted by Aaron, 2026-08-09 — but **disjoint ownership is absolute**: every acceptance file belongs to exactly one BA, assigned by the lead, and BAs never communicate with each other), **1 Documentation**, **1 QA**, **1 RM**. Growth beyond caps requires Aaron.
- The RM is **advisory**: it recommends dispatches/reassignments; Bill executes them (only the lead messages agents). RM never edits code and never talks to other agents.
- **At-risk parallel starts**: the lead may start sprint N+1 items whose dependencies are code-complete but whose sprint gate (e.g. Aaron's contract freeze) is pending — the RM tracks every at-risk item and its rebase exposure so a freeze-review change fans out correctly.

## Staging-area discipline (v1.5.1 — from the VERSION-fixture incident, 2026-08-09)

The git index is a **shared mutable resource** across concurrent agents. Rules:
- **Juniors**: never leave anything staged between tool calls — any stage→verify→reset test sequence must complete atomically inside a single command invocation.
- **Lead**: commits use explicit pathspecs (`git commit -m "..." -- <paths>`) or verify `git diff --cached --stat` matches the intended set immediately before committing — a concurrent agent's staged file must never ride along. (Incident: a junior's staged `VERSION` test fixture was swept into an unrelated docs commit; caught and reverted within two commits.)

## The Destructive agent (v1.8 — Aaron, 2026-08-09)

A new pipeline stage, sitting **after the Tester and before Documentation**:

```
BA criteria → Jnr builds → Tester (PASS/FAIL vs criteria) → DESTRUCTIVE (attack it) → Docs → Bill review → commit
                                                                  │ REJECT → same Jnr fixes → Tester again → Destructive again
```

**Why it exists.** The Tester asks *"does this do what the criteria say?"* Nobody was asking *"what happens when someone uses it wrongly, or maliciously?"* Passing tests and being hard to misuse are different properties, and today's own history proves it: a transport `Close()` that panicked on a live race passed every test in the suite for the project's entire life, because the test that looked like it covered it quietly didn't.

**Mandate.** The Destructive agent tries to break things. Per module it asks:
- **Input validation & trust boundaries** — what enters unvalidated? What does it trust that it shouldn't?
- **Bounds & overflow** — slice indexing, integer conversion (`int` → `int32`, unchecked `len()` arithmetic), off-by-one, unbounded growth.
- **Type safety & confusion** — `interface{}`/`any` round-trips, unchecked type assertions, JSON into loose types.
- **Encapsulation** — is the exported surface bigger than it needs to be? Can a caller reach into internal state and corrupt an invariant?
- **Insecure call-ability** — *can a caller use this API correctly-looking and get an unsafe result?* An API that is easy to hold wrongly is the finding, even if every current caller holds it right.
- **Concurrency** — races, deadlocks, TOCTOU, lock-order inversion, unsynchronised teardown.
- **Resource exhaustion** — unbounded allocation, goroutine leaks, work proportional to attacker-controlled input.
- **Error-path disclosure** — do errors leak internals, paths, or secrets?

**Rights and duties.**
- It **may reject work back to the developer**, exactly as a Tester FAIL does. A rejection returns to the *same* junior, is re-Tested, and comes back through Destructive again.
- Every rejection is logged as a **BOW `finding` (`SEC-` code)** carrying the rejection reason, the code location, the code.json reference, and a **weakness class** — all tool-enforced:
  ```
  node claude-bow.js add finding "<what breaks and how>" --priority P0..P3 \
    --code-path "<file:line>" --codejson "<module key>" --class <weakness-class> \
    --desc "<attack/misuse path, impact, and what the fix must achieve>"
  ```
- It **never edits code** and never fixes what it finds — same separation as the Tester. Finding and fixing in one head is how a fixer talks itself out of a finding.

**The pattern count is the real deliverable.** `node claude-bow.js weakness` reports findings grouped by class and flags any class recurring 3+ times. A single finding is a defect; the same class six times is a statement about **how the team writes code**, and the response to that is *teaching*, not tickets. The lead is expected to act on recurrence by changing briefs and criteria, not by filing more bugs.

**Scan stamps.** Each module's adversarial-review state lives in `data/security-scans.json` and is merged into `code.json`'s `securityScan` field by `tools/plan/generate.js`. `code.json` is generated and must never be hand-edited (GR#3/GR#6), so the ledger is the SSOT and the stamp still appears where readers look for it. **Absent = never scanned** — unscanned must never be mistaken for clean.

### Weakness pattern #1: an invariant stated in prose is not enforced (2026-08-09, first sweep)

The very first Destructive sweep produced the same root cause three times, in three different packages, written by three different juniors:

| Finding | The prose | The reality |
|---|---|---|
| SEC-005 | "this order IS the contract — never reordered at runtime" | exported mutable package-level slice; reversing it reversed actual execution |
| SEC-003 | "hooks are registered at boot only" | nothing prevented late registration; the unlocked map read is a **fatal** runtime crash |
| SEC-001 | the package doc correctly names itself the hostile-input surface | the shard *name* — also a path component — was the one field not treated as hostile |

**The rule that follows, binding on BAs and developers:**

- **A comment saying "never X at runtime" is a code smell, not a control.** If an invariant matters, the API must make violating it impossible or loud — an unexported var with a copy-returning accessor, a lock, a sealed state that errors on late mutation. `foundation.registry.List()` returning a defensive copy is the pattern this project already knows how to write; follow it.
- **BAs**: when criteria state an invariant, add a criterion that the invariant **cannot be broken through the public API**, not merely that correct use produces the right answer. "Verify the API cannot be misused to break this" is now expected wording wherever an invariant is asserted.
- **Developers**: if you write "callers must not…", stop and ask what happens when they do. If the answer is "it silently corrupts" or "it crashes", the comment is not the fix.
- **A field that becomes a path segment, a key, or an identifier is input**, however inert its name makes it look. `Name` read like a label; it was a path.

This is what the weakness count is for. Three instances of one class in one sweep is not three bugs — it is one habit, and the response is teaching, which is why it is written here rather than only in three BOW items.

### Weakness pattern #2: a value duplicated across a module boundary needs a drift test

GR#20 forbids some imports (notably `internal/ui` → `internal/engine`), and modules are deliberately decoupled elsewhere. That legitimately forces a value to exist in two places. It has happened twice already:

- `internal/ui/screens/debug/phase.go` mirrors `engine.core`'s six phase names.
- `internal/engine/stub` mirrors `engine.core`'s `MaxAdvanceTicksPerCall`, so the stub and the real engine agree on what input is legal.

**The duplication is acceptable. Silent divergence is not.** The standing remedy, both times:

1. Duplicate the value as a literal in the consuming package — no production import, so the boundary holds.
2. Add a **drift test in a `_test.go` file** that imports the real source (test-file imports are the sanctioned exemption) and asserts the two agree.
3. Make the failure message explain *why* the duplication exists and that changing one requires changing the other — a bare `got X, want Y` teaches a stranger nothing.
4. **Verify the drift test can actually fail.** A Tester had to prove exactly this about the phase mirror; a drift test that cannot fail is decoration, and it is worse than nothing because it looks like coverage.

If you find yourself copying a constant across a boundary and *not* doing this, you are choosing to be told about the divergence by a user instead of by CI.

## Assumptions are logged or the work is rejected (v1.7 — Aaron, 2026-08-09)

**The standard is that the criterion holds, not that the test passes.** A test proves what it asserts; a criterion states what must be true. The gap between those two is where assumptions live, and an assumption nobody wrote down is indistinguishable from a fact until it is wrong.

**Any agent may log an assumption. Every agent must.** An assumption is anything you decided that the spec, the criteria, or the brief did not decide for you: a chosen tolerance, a picked default, a read of an ambiguous requirement, a "this obviously means X", a scope boundary you drew yourself.

Log it with:

```
node claude-bow.js add assumption "<the assumption, stated as a claim that could be wrong>" \
  --priority P0..P3 \
  --code-path "<the file or directory it concerns>" \
  --codejson "<code.json module key or GUID>" \
  --desc "<why you assumed it, what you'd have needed to not assume, and what breaks if it's wrong>"
```

Both references are **mandatory and enforced by the tool** — an assumption that cannot be traced to code is a note, not a record, and the checks below are impossible without it. Assumptions get `ASM-` codes and live in the BOW alongside everything else.

**The reciprocal rejection duties — these are what give the rule teeth:**

| Role | Duty |
|---|---|
| **BA** | Logs every assumption made while writing criteria. Criteria that rest on an unlogged assumption are incomplete work. |
| **Jnr developer** | **Must reject the ask** if the BA's criteria contain assumptions that are not logged. Bounce it back to the lead — do not "just build it" and do not silently resolve the ambiguity yourself. If you resolve it, that is your assumption now, and you log it. |
| **Tester** | **Must actively look for assumptions** in the delivered work, not only verify criteria. An assumption found in the code or the tests that has no `ASM-` item is an automatic **FAIL**, regardless of whether every criterion passed. |
| **Lead** | Rules on logged assumptions: accept, correct, or escalate to Aaron. Also answerable to this rule — a lead ruling is itself an assumption unless it is written down. |

**Why the rejection is mutual rather than one-way**: a single checkpoint can be tired, rushed, or agreeable. Requiring the receiver of work to refuse unlogged assumptions means an assumption has to survive two people deciding to ignore the rule, not one.

**What this is not**: it is not a demand to log every keystroke-level choice. If the spec or criteria decided it, it is not an assumption. The test is simple — *could a reasonable person have decided this differently, and would the work still have passed?* If yes, log it.

### Mandatory spawn block (v1.7)

Agent transcripts do not survive a session, so a rule that lives only in the lead's head dies with the window. **Every agent spawn brief must carry this block verbatim**, adapted only in the role line:

> **Assumptions (GR-adjacent, mandatory).** Build so that the *criterion holds*, not merely so the *test passes* — a test proves what it asserts, a criterion states what must be true, and the gap between them is where silent assumptions live. Anything you decide that the spec, criteria or brief did not decide for you is an assumption: a chosen tolerance, a picked default, a reading of an ambiguous requirement, a scope boundary you drew yourself.
>
> Log every one before you report, with both references — the tool rejects an assumption that cannot be traced to code:
> ```
> node claude-bow.js add assumption "<the assumption, stated as a claim that could be wrong>" \
>   --priority P0..P3 --code-path "<file or dir>" --codejson "<code.json module key or GUID>" \
>   --desc "<why you assumed it, what you'd have needed in order not to, and what breaks if it's wrong>"
> ```
> Then cite the `ASM-` codes in your report.
>
> **Your rejection duty**: *(developer)* if the acceptance criteria rest on an assumption the BA did not log, **reject the ask** and bounce it to the lead — do not build it and do not quietly resolve the ambiguity yourself; if you resolve it, it is your assumption and you log it. *(Tester)* actively hunt for assumptions in the delivered code and tests, not just verify criteria — an assumption with no `ASM-` item is an automatic **FAIL** even if every criterion passed. *(BA)* log every assumption made while writing criteria; criteria resting on an unlogged assumption are incomplete work.

## File ownership is transferred, never duplicated (v1.6.1 — from the feat.skeleton incident, 2026-08-09)

Acceptance files are owned by exactly one BA. That rule already existed; what it lacked was a **handover procedure**, and the gap cost real work.

Incident: the lead hired BA-3 onto `feat.skeleton.md` while BA-1 was already mid-refresh of it under an earlier instruction, then messaged BA-1 to drop it. The message lost the race — BA-1 had already finished and written. With two agents writing one file, BA-1's version did not survive; the file was later found back at its committed state with the work gone. Detected only because BA-3, acting as reviewer, checked the disk against what it had been told and reported the discrepancy instead of assuming.

Rules:
- **Never assign a file to a second agent until the first has confirmed it has stopped.** An instruction to drop work is not the same as knowing it was dropped — messages are delivered at the recipient's next tool round, which may be after it has already written.
- **Reassignment is a transfer**: incumbent stops and confirms → lead commits or captures whatever the incumbent produced → new owner starts.
- **Commit delivered `.md` work promptly.** Agent output that exists only in the working tree is one concurrent write away from being lost; a committed file can always be recovered.
- Root cause was lead sequencing, not agent execution. Recorded here so the procedure changes, not so anyone is blamed.

## Heavy checkpointing (v1.5)

The session can die at any moment (token exhaustion) — mid-build, mid-test, mid-commit. Recovery must be possible from cold:

- **`docs/planning/checkpoint.md`** (RM maintains, Bill commits): current sprint state; every in-flight agent with its assignment, last known status and expected next event; pending verdicts/bounces; standing orders from Aaron; exact next actions with enough context to re-dispatch from scratch (agent transcripts do not survive a restart — checkpoint text must be self-sufficient).
- **Cadence**: updated at every pipeline transition (dispatch, verdict, bounce, doc pass, commit); committed at least at every commit-bearing event (it rides along with the commit).
- **BOW mirror**: every transition also writes a one-line BOW comment on the affected item (`dispatched to dev`, `tester FAIL #2`, `doc-passed`, ...) so item-level state survives even without the checkpoint file. The BOW `status` field + comments + checkpoint.md together are the full recovery surface.
- **Recovery protocol** (also in CLAUDE.md): a fresh session reads checkpoint.md, `node claude-bow.js list --by-seq` + recent comments, and `git log -10`, then reconstructs the board and re-dispatches — completed work is never redone (commits + BOW are the truth).

## Pipelined cadence (v1.4)

The stages run **concurrently across sprints**, not serially within one:

| Workstream | Working on |
|---|---|
| Jnr developers | Sprint **N** (current build sprint) |
| BAs | Sprints **N+1 … N+3** — user stories + acceptance criteria written ahead, so no developer ever waits on criteria |
| Tester | Sprint **N** items as they land (plus re-verifies after bounces) |
| Documentation | Sprint **N** passes + keeping the freeze packet current |
| QA | Trailing audits of committed sprints + pre-commit audits of N on Bill's demand |
| Bill | Reviews/commits test-clean **N** output; freezes contracts at sprint gates; briefs N+1 |

- **BA deliverable per item** (extended in v1.4): `docs/planning/acceptance/<mkey>.md` now opens with **user stories** ("As the <engine module/player/UI screen/harness>, I need … so that …", traced to spec §) followed by the numbered acceptance criteria as before.
- **Multiple BA agents are allowed** with disjoint sprint ownership (one sprint's files belong to exactly one BA); the lead assigns sprints.
- A sprint's build may not start until its criteria exist; criteria exist long before because BAs run ahead. Criteria for future sprints are drafts until the sprint's build starts — the owning BA refreshes them at dispatch time if the spec or contracts moved (the header carries `status: draft-ahead` vs `active`).

## Roles

| Role | Model | Mandate |
|------|-------|---------|
| **Lead (Bill, Fable 5)** | — | Architecture, briefs, dispatch, FINAL review of test-clean work only, commits, BOW state. Does not burn tokens on basic errors — those never reach him. |
| **BA** (one, persistent) | Sonnet | Writes the **acceptance criteria** for each item BEFORE the junior developer receives it: numbered, individually testable criteria derived from the item's spec_ref sections + the lead's brief, saved as `docs/planning/acceptance/<mkey>.md`. The criteria are the contract: the junior builds to them, the Tester verifies against them, Bill's final review confirms them. The BA never writes code and never relaxes a spec requirement — conflicts between spec and brief are escalated to the lead. |
| **Jnr developers** (per item) | Sonnet | Build to the brief. Fix their own test failures — every bounce goes back to the SAME junior with its context intact. |
| **Tester** (one, persistent) | Sonnet | Verification ONLY: runs the build/vet/test/-race/gofmt suite plus the item brief's specific checks, confirms deliverables match the brief. Output is PASS or FAIL with exact evidence. **Never edits a file, never suggests fixes** — a FAIL hands straight back to the junior. |
| **Documentation** (one, persistent) | Sonnet | Owns documentation consistency: house style, spec refs, code.json keys in package headers, `docs/design/` index + freeze-review packet. **May edit .md files only** — doc problems inside .go files go back to the junior as a FAIL item. |
| **QA** (one, persistent, INDEPENDENT) | Sonnet | Bill's eyes on the ground. Audits the pipeline itself, not just the work: checks the checker (independently re-verifies samples of the Tester's cited evidence), code.json/BOW/master-plan alignment (no drift), Golden Rules compliance, and spot-checks code quality — error trapping (GR#1/#7), inline documentation, naming quality, correct data types, capitalisation/idiom conventions. **Fully independent**: never communicates with BA/Tester/Docs/juniors, never edits any file, reports findings DIRECTLY and only to Bill, who decides the action (bounce, BOW bug, accept). Advisory — QA never blocks the pipeline; Bill does, on QA's evidence. Runs at least once per wave and on Bill's demand. |

## Flow per BOW item

```
Bill brief → BA acceptance criteria → Jnr builds to criteria → Tester verdict vs criteria
                                                                   │ FAIL → same Jnr fixes → Tester again (loop)
                                                                   │ PASS ↓
                                                          Documentation pass (.md only)
                                                                   ↓
                                                          Bill final review → commit [mkey] → BOW ref + done
```

- The BA's criteria file exists BEFORE the junior is dispatched; the junior's brief links to it. (Wave 2 items J4–J7 were dispatched pre-BA — their criteria are retrofitted and the Tester verifies against them.)
- The Tester's FAIL report is forwarded verbatim to the junior; the junior's fix returns to the Tester, not to Bill.
- Bill sees an item only when it is test-clean and doc-clean; his review is architectural (contracts, determinism, GR compliance), not mechanical.
- Nothing is committed that has not passed the Tester; nothing is `done` in the BOW without Bill's final review and a commit ref.
- The Tester's suite is derived from the item's brief (GR#15: expected results come from the brief/spec, not invented) — baseline for Go items: `go build ./...`, `go vet ./...`, `go test <pkg> -race -count=1`, `gofmt -l .` empty, deliverables-vs-brief checklist, forbidden-touch check (`git status` shows only allowed paths).

## Rationale (wave-1 lessons)

- Wave 1 worked but the lead personally caught a junior's basic robustness bug (BOM handling in the plan guard) — mechanical catches belong to a cheaper, dedicated verifier.
- Juniors' self-reported "verification output" is not trusted evidence; an independent runner is (the Tester re-runs everything from scratch).
- A single documentation owner prevents N juniors drifting into N house styles across `docs/design/`.
