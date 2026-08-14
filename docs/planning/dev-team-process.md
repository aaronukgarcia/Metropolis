# Metropolis Dev-Team Process v1.13

**2026-08-08/09 · directed by Aaron (v1.1: Tester + Documentation; v1.2: BA; v1.3: independent QA; v1.4: pipelined cadence; v1.5: Resource Manager, saturation rule, team caps, heavy checkpointing; v1.6: second Tester) · supersedes the wave-1 "lead reviews everything directly" flow**

## Second Tester (v1.6 — Aaron, 2026-08-09)

The Tester cap rises from 1 to **2**. The single Tester was the pipeline's queue bottleneck: a wave of parallel juniors delivers faster than one verifier can clear, and items sat waiting on a verdict while their dependants idled.

The two Testers are **independent verifiers, not a team**: they never communicate with each other, they own **disjoint items** (the lead assigns), and each reports its verdict directly and only to Bill. One item never gets two verdicts — that would invite verdict-shopping and dissolve accountability for a bad PASS. Everything else in the Tester mandate is unchanged: verification only, never edits, never suggests fixes, a FAIL goes straight back to the same junior.

## Saturation rule & Resource Manager (v1.5)

**No agent idles while non-blocking work exists.** A dedicated **Resource Manager (RM)** agent — persistent, reporting DIRECTLY to Bill — owns the assignment board:

- Maintains `docs/planning/team-board.md`: every agent, current assignment, status (busy/blocked/idle), what they return to when unblocked.
- When an agent blocks (e.g. a dev waiting on a Tester verdict), the RM proposes interim work for it and remembers the return point; when the blocker clears, the RM proposes the return.
- **Team caps** (RM enforces by flagging breaches to Bill): max **4 concurrent Jnr developers**, **2 Testers** (raised from 1 by Aaron, 2026-08-09, to cut verification-queue latency — see v1.6 note below), **3 Destructive agents** (added 2026-08-10 — the role shipped in v1.8 with *no documented cap at all*, which the RM correctly flagged: 3 have been running since the first sweep, so the precedent is written down rather than left implicit; they partition by layer — foundation+protocol, engine, UI+cmd+tooling), **BAs as needed** (cap lifted by Aaron, 2026-08-09 — but **disjoint ownership is absolute**: every acceptance file belongs to exactly one BA, assigned by the lead, and BAs never communicate with each other), **1 Documentation**, **1 QA**, **1 RM**. Growth beyond caps requires Aaron.
- The RM is **advisory**: it recommends dispatches/reassignments; Bill executes them (only the lead messages agents). RM never edits code and never talks to other agents.
- **At-risk parallel starts**: the lead may start sprint N+1 items whose dependencies are code-complete but whose sprint gate (e.g. Aaron's contract freeze) is pending — the RM tracks every at-risk item and its rebase exposure so a freeze-review change fans out correctly.

## Staging-area discipline (v1.5.1 — from the VERSION-fixture incident, 2026-08-09)

The git index is a **shared mutable resource** across concurrent agents. Rules:
- **Juniors**: never leave anything staged between tool calls — any stage→verify→reset test sequence must complete atomically inside a single command invocation.
- **`git stash` is BANNED for everyone except the lead** (added 2026-08-09 after a near-miss, self-reported). Stash operates on the *entire* working tree, not your files — with four juniors and three Destructive agents live, stashing sweeps away every other agent's uncommitted work, and popping it back is a merge, not an undo. If you need a "before" tree to compare against, use `git archive HEAD | tar -x` into a scratch directory outside the repo. That technique is safe, established, and used repeatedly today. The same reasoning bans `git checkout -- .`, `git reset --hard`, and `git clean`.
- **Choosing a "before" baseline (added 2026-08-10, from the SEC-031 evidence dispute).** `git archive HEAD | tar -x` into scratch is the established way to reproduce a pre-fix state — **and it is wrong whenever the fix under test is uncommitted AND sits on top of other uncommitted work.** With a parallel wave running, `HEAD` can be several layers behind the state a finding was made against, so a repro against it tests a different question and can appear to refute a correct finding. It happened: a Tester ran a SEC-031 repro against `HEAD` (which predated the SEC-020 guard entirely) and saw bytes reach a writer that, in the actual reported state, received none. Neither party was wrong about their own code state. **The correct baseline is the working tree with only the specific fix removed** — reconstruct it in scratch (e.g. reaching an unexported field from a same-package test) rather than reaching for `HEAD` by habit. And **state which baseline you used** in any report claiming a reproduction; "reproduced against HEAD" and "reproduced against the working tree minus the fix" are different claims.
- **Lead**: commits use explicit pathspecs (`git commit -m "..." -- <paths>`) or verify `git diff --cached --stat` matches the intended set immediately before committing — a concurrent agent's staged file must never ride along. (Incident: a junior's staged `VERSION` test fixture was swept into an unrelated docs commit; caught and reverted within two commits.)

## Shared-file staging & breaking-change dispatch (v1.13 — BUG-030, BUG-032, 2026-08-14)

Two coordination rules added after one wave produced, at the same time, a lead-side and a junior-side failure of the existing file-ownership rules.

**Never `git add` a shared file by path (BUG-030).** Commit 9edd307 was titled for FEAT-010 but silently carried MET-H100–H105 — six ui.harness error codes a different agent had added to the shared `data/errors.json` in the same window. The lead ran `git add data/errors.json` wholesale and did not review the staged diff before committing. v1.6.1 already forbids two agents writing one file; the lead staging a shared file without reading it is the same failure from the other direction, and it was this project's second mislabelled commit. Rule: during a multi-agent wave, never `git add` a shared file (`data/errors.json`, `code.json`, allowlists, generated registries) by path. Review `git diff --cached` first and stage only the hunks belonging to your commit, or wait for the owning agent to report. History is not rewritten for the prior incidents; the rule exists so there is not a third.

**Breaking signature changes get their own dispatch (BUG-032).** A breaking signature change (e.g. SEC-036's `RunCommandLoop` returning an error) makes every caller in the file's call graph part of the blast radius by definition, but v1.6.1 forbids two agents in one file — so the junior fixing the signature either touches another agent's files (and must flag every one, relying on judgement about what counts as mechanical) or leaves the build red. Rule: a breaking signature change gets its own dispatch, ahead of the feature that needs it, so the blast radius lands and stabilises before parallel work starts on top of it. The brief names the full call graph as owned (mechanically computable via `go/ast`); this feeds the `/brief` skill and `claude-dispatch-guard.js`, which currently claims declared paths and has no notion of a call graph.

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

### Weakness pattern #5: a guard must not damage what it protects (2026-08-10)

Three findings in one sweep, all the same shape — **the rejection path harming the thing the guard exists to defend**:

| Finding | The guard | What its own rejection did |
|---|---|---|
| SEC-026 | reports a premature engine exit so a silent death is visible | fired on *correct* shutdowns too — and an alarm that cries wolf gets ignored, restoring the silence |
| SEC-030 | rejects a copied screen and logs it | unthrottled at a 10Hz render tick, it filled the shared 200-entry error ring in ~20s, **drowning F12's error tail** — the instrument built so nothing gets lost |
| SEC-031(b) | rejects a copied logger and records the attempt | shared the finite ring with genuine audit records, so 500 rejected calls **evicted real evidence** |

Each guard was correct about *what* to reject. Each damaged the diagnostic capability it was protecting, in the act of protecting it.

**The rule:**

- **Ask what the rejection path consumes.** If it writes to a bounded shared resource — a ring buffer, a log, an alert channel, a queue — a caller in a loop will exhaust it. "Only a malicious caller would" is wrong: SEC-030 needed no malice, just a render loop.
- **A rejection that can fire per-frame must be throttled, coalesced, or quota'd.** Prefer coalescing (`MET-U101 ×4,127` tells an operator *more* than 200 identical lines), because it preserves the signal while bounding the volume.
- **Fix it centrally.** SEC-030 and SEC-031(b) were the same defect reached through different callers. Nine guards each solving their own flooding gives nine subtly different throttles and leaves the tenth guard with none.
- **Never let the alarm degrade the evidence.** A guard that fills the audit trail with its own noise has converted a specific failure into general blindness — which is worse than the failure it caught.

### Weakness pattern #6: a guard must be judged on cost-to-reach, not just correctness-once-reached (2026-08-10)

Destructive-1's sweep of the three commits landed that day found three P1s that all read as different bugs and are in fact one habit. Each guard is **correct about the thing it checks**. Each is silent about what it costs to arrive at the check, or about what a caller does with the answer.

| Finding | The guard is right about | What it never asked |
|---|---|---|
| SEC-037 | `Recorder.Records()` correctly refuses a struct copy and returns `nil` | what a *caller* does with that `nil` — `Save` wrote a valid gzip+SHA256 fixture containing **zero records** and returned `nil` error |
| SEC-038 | `Load`'s SHA256/ByteSize digest check is real, enforced, and survived every tampering attempt | what decompression costs *before* integrity is checkable — 65KB expanded to 67MB, ~1028x, unbounded |
| SEC-039 | SEC-009's clamp correctly bounds the derived grid | what it costs to *reach* the clamp — a 1x1 extent satisfying the clamp perfectly, carrying 300,000 cells, 198MB of wire JSON, 1.43s of decode |

This is pattern #5 one level up. #5 says the rejection path must not damage what it protects. #6 says the **approach** to the guard is part of the guard's attack surface, and so is **what happens after it returns**.

**The rule:**

- **Bound the input, not just the output.** A clamp on a derived value is not a bound on the data used to derive it. If an attacker controls the size of what you parse before you validate it, the parse is the vulnerability.
- **A guard that returns a sentinel has not finished the job.** `nil`, zero values and `false` collapse "rejected" into "legitimately empty", and then every caller must remember which it got. One caller will forget — SEC-037 is that caller, and it reported success while discarding the user's data. Make the rejection **impossible to confuse with a normal result**; an error return is usually the honest shape.
- **When a guard is bypassed by a call site rather than defeated, the guard is not the fix.** Adding a second check in `Save` leaves the class alive. Fix the ambiguity at source and audit every sibling caller in the same commit (GR#18).
- **Argument position is the blind spot.** `Save(rec *Recorder)` and `SetSink(l *Logger)` both take a guarded type as an argument rather than a receiver. Nine manual enumerations by four agents missed `SetSink` because they all enumerated *methods*. This is why BUG-024's gate must enumerate functions by parameter type too — a 15th guarded type reopens the gap otherwise.

**Negative results are part of the pattern and must be recorded.** `ValidateShardName` held against traversal, absolute paths, UNC, trailing dot/space and Windows device names; `limits.go`'s `maxGridSide` boundary was verified to have no off-by-one and no overflow; the `Load` digest check could not be defeated. Recording what resisted is what stops the next sweep re-attacking solved ground — and stops a later "fix" quietly removing a control that was working.

### Weakness pattern #4: a value in a privileged position is input, however inert it looks (2026-08-09)

`input-validation` is the largest class in the ledger — nine findings, across Go and JavaScript, engine and tooling, written by different hands. Grouping them shows it is **not** "the team forgets to validate". Every one of these packages validates its payload carefully. The defect is narrower and much more specific:

**The dangerous value was almost never the payload. It was the metadata around it** — a name, a size, a path, an identifier — used in a position where it stops being data and starts being *instruction*.

| Finding | The value | The privileged position it reached |
|---|---|---|
| SEC-001 / SEC-010 / SEC-013 | `ShardMeta.Name` | a **path segment** — arbitrary file read/write |
| SEC-009 | `Extent.Width/Height` off the wire | an **allocation size** — OOM from one patch |
| SEC-011 | any rendered string, e.g. an error message | **terminal control bytes** — escape injection |
| SEC-002 | a staged file path | **shell syntax** — `%VAR%` expanded, check silently defeated |
| SEC-008 / SEC-012 | a command string, a remote name | a **security decision** — guard fires or doesn't |
| SEC-015 / SEC-021 | an identifier, a test literal | a **heuristic's verdict** — false positives that train people to bypass |

`serialize`'s own doc comment correctly names it as the hostile-input surface, and it treats bytes as bytes throughout. The one field that escaped that discipline was the shard **name** — which reads like a label right up until you notice it is also a path component.

**The rule:**

- **Ask what a value *becomes*, not where it came from.** If it ends up as a path segment, an allocation size, a format string, shell or SQL syntax, a terminal byte sequence, a map key, or the basis of a security decision — it is input, and it needs a validated domain at the boundary, no matter how internal its origin looks.
- **State the allowed domain positively.** "Valid" is not a specification. `ValidateShardName` is the model: a single clean path component — not `/`, not `\`, not `:`, not `..`, not absolute, not volume-relative, no trailing dot or space. A future reader can check that; they cannot check "sanitised".
- **Reject, never sanitise — *for identity and structural data*.** Trimming a hostile *name*, *path*, *key* or *size* into a plausible one hides the attack and destroys the evidence. Every fix in that class rejects loudly with a registry-sourced error.
  - **The exception, and it matters (BA-1, ASM-077, 2026-08-10):** at a **display boundary**, escaping *is* the correct treatment and rejection is wrong. You cannot refuse to render a citizen's name because it contains an odd byte — the user needs to see something, and dropping the frame is a worse outcome than showing an escaped character. So: **identity/structural data is rejected; display text is escaped or filtered.** The distinguishing question is whether the value *controls* something (a path, an allocation, a lookup) or is merely *shown*. SEC-022's `%s` → `%q` is the escape form; `ValidateShardName`'s outright refusal is the reject form. Both are correct, for different reasons, and conflating them produces either a hole or an unusable UI.
- **BAs**: for each input crossing a trust boundary, criteria must name (a) the privileged position it reaches, (b) the exact allowed domain, and (c) that violations are rejected rather than repaired. A criterion saying "handles malformed input gracefully" is unverifiable and has already let three of these through.
- **Bound anything attacker-influenced that sizes work or memory** — `Extent`, tick counts, retry loops. An unbounded size taken from a peer is a denial of service with extra steps.

### Weakness pattern #3: fix the class, not the demonstrated instance (2026-08-09)

Every round of the SEC-003 → SEC-014 → SEC-016 → SEC-018 chain closed **exactly the path the proof-of-concept exercised**, and left structurally identical siblings standing:

| Round | Fixed | Left standing |
|---|---|---|
| SEC-003 | the unlocked `hooks` read | — |
| SEC-014 | copy detection on the two `hooks` paths | the check ran *after* the lock |
| SEC-016 | check moved before the lock, on those same two paths | **six other `e.mu.Lock()` sites** with no check at all |
| SEC-018 | (open) | — |

`Clock()`, `handleSetSpeed`, `handlePause`, `handleResume` and `Snapshot()` are reachable from any Command sent to a copied Engine, and every one of them hangs on the same mechanism. Only 2 of 8 lock sites were guarded. A Tester reproduced it: 1,786 of 3,000 calls returned; the rest wedged permanently.

The same shape appeared in `harness.stub` (bounded `AdvanceTicks` in `engine.core`, unbounded in the stub) and in the hooks (four bypasses fixed, a fifth in `secret-guard` left because it wasn't cited).

**The rule:**

- **When you fix a defect, grep for its shape before declaring done.** If the bug is "an unguarded call to X", find *every* call to X. If it is "unvalidated input to Y", find every path into Y. The PoC is one instance the attacker happened to show you, not the boundary of the problem.
- **Briefs must ask for the class.** A brief saying "fix this call site" will get that call site. Say "fix this and every structurally identical site, and tell me how you enumerated them."
- **Testers and Destructive agents should hunt siblings by default** — every round of the chain above, the sibling was found by a verifier, never by the fixer.
- If you deliberately leave a sibling — legitimately, e.g. it is out of scope — **log it as an assumption naming what you left**, so it is a decision rather than an oversight.

Fixing the instance feels like progress and produces a green test. It also guarantees the next round.

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

### Fixing a fix: log against the existing record as you go (v1.7.2 — 2026-08-09, on Tester-2's observation)

Second-order work — changing code that already carries `ASM-` history, usually because a Tester or Destructive agent bounced the first attempt — has produced an **unlogged judgement call on the first pass every single time** it has been done. Same file, two consecutive rounds, two different sets of hands, both times caught only by a Tester.

The likely cause is that the assumptions feel like they belong to the original piece of work, which is already recorded — so the new decisions taken *while repairing* it slip through as mere implementation detail. They are not: a decision that narrows, widens or reverses an earlier one is exactly the decision a future reader most needs explained, because it is the one that looks inconsistent with the record.

**So, for any dispatch that modifies code with existing `ASM-` history:** log against the existing item (or raise a new one) **as you make each call**, not after a verifier finds the gap. The dispatch brief should say so explicitly. This applies to the lead as well — see below.

**The lead is not exempt, and this has already been enforced twice.** A lead ruling delivered verbally in a brief is an assumption with no record. Both times it was a Tester that caught it, correctly, and the lead logged it afterwards. If you are the lead and you steer a decision in a message, log it — or expect to be bounced for it, which is the rule working.

### Fast path for paperwork-only FAILs (v1.7.1 — 2026-08-09, on Tester-2's proposal)

Within hours of v1.7 landing, two items FAILed on assumption-logging alone: content correct, behaviour correct, one sentence missing. The rule caught real forks both times — but the *enforcement* was uniformly heavy regardless of stakes, spending a full Tester → lead → junior → lead → Tester cycle on "write one BOW comment".

So, narrowly: where a FAIL is **assumption-logging only** — no code change wanted, no behaviour in question, the assumption is P3 — the Tester may bounce **straight to the junior** and re-confirm on return, without a lead round trip in each direction. The lead is told, not asked. Anything touching code, behaviour, or an assumption above P3 keeps the full loop.

**What was deliberately NOT adopted, and why it matters more than the shortcut:** the obvious cheap fix is to let the Tester log the assumption itself and pass. **Rejected.** The rationale has to come from the person who made the trade-off, not be reconstructed afterwards by whoever is verifying — a Tester-authored assumption puts words in the junior's mouth about its own reasoning, and the traceability the rule buys is precisely *who decided what, and why they thought it was right at the time*. Better to pay the cycle than to hollow out the record.

*(Origin: Tester-2 proposed both halves — the fast path and the argument against the shortcut — when asked whether the rule was over-firing. Recorded because a process change should carry its reasoning, not just its conclusion.)*

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

## A cited BOW code must be verified to resolve before it is relayed as fact (v1.10 — Bill ruling on BUG-075, 2026-08-13)

**Origin.** Two incidents in one day, same class, same mechanism (`node claude-bow.js show BUG-075` for the full record). First: Junior-Gate2's report on FEAT-040 stated `ASM codes logged: ASM-347, ASM-348, ASM-349` with a description of each — Tester-7 grepped the guard file, the test file, and the full comment history and found **none of the three exist anywhere**. They were never filed. The lead then relayed `ASM-347 flags this coupling as P1` into a second junior's brief and into the Tester's own brief as established fact, within minutes — a fabricated citation propagated into two downstream dispatches before anyone checked it. Second: a report on the four-guard port cited `BUG-053/BUG-054` for two quote-mask bypasses; both codes are real, but neither is those defects — the codes cited point at the wrong thing entirely, and the report used the (wrong) citation to justify a substantive claim (that the bypasses were already fixed) that four guard file headers then repeated as fact.

**Why the existing assumption-logging rule (v1.7, above) does not catch this.** v1.7 makes a Tester look for what is *absent* — an unlogged assumption. A fabricated or misattributed citation defeats that check by satisfying it *on paper*: the report names a code, so "is anything unlogged?" reads as answered, while the claim behind the citation is false. This is a distinct failure mode and needs its own duty, not a restatement of v1.7's.

**The duty, binding on both roles named:**

- **Testers (and anyone relaying a cited code) must verify claimed codes RESOLVE, not merely that "an assumption is logged."** Before a Tester — or anyone else — relays or accepts a report that cites an `ASM-`/`BUG-`/`FEAT-`/etc. code as justification for a claim, run `node claude-bow.js show <CODE>` (or, for several codes at once, `node claude-bow.js exists CODE1 CODE2 ...` — BUG-075's batch-check command, one DB round-trip) and confirm BOTH (a) it exists and (b) its content actually matches what the report claims it says. A code-shaped string appearing in the text proves nothing on its own.
- **The lead carries the identical duty before relaying a cited code into another agent's brief.** This is not a lesser or implied version of the rule above — BUG-075's first incident is exactly this failure: the lead relayed an unverified citation downstream within minutes, propagating the error into a second dispatch. A lead ruling that repeats an unverified citation is itself an unverified claim wearing the lead's authority.
- **"Exists" is necessary, not sufficient.** The second BUG-075 incident shows a citation can name a real code and still be wrong — checking existence catches incident one; checking content against the specific claim being made is what catches incident two. Both checks are required; neither substitutes for the other.

**No accusation of bad faith is implied or needed.** The likeliest cause both times was a report written from intent rather than from command output — exactly the class of error this project's verify-the-thing-not-the-report-of-the-thing standard already names elsewhere. The fix is a mechanical habit (run the command, read the output), not a trust judgement.

## An acceptance criterion's CHECK must be able to fail (v1.9 — 2026-08-10, from BUG-033)

Three criteria files in a single wave carried the same defect, and none of them
was catchable by reading. In each case an AC's **check** — written as a grep, a
type sketch, or an example filename — had drifted from the **rule** the AC
stated, and the drift only surfaced when a developer tried to satisfy both
halves at once.

| File | The rule says | The check says | What a violating build could do |
|---|---|---|---|
| `ui.keys` | a mapped key must resolve to a path **naming a registered action**, rejected per-entry if not | assert the top-level verbs `b z p s d i g t` are present | ship a token-substitution cipher that passes the grep while making the rule **unenforceable** — a token is not an action, so it cannot be checked against the registry |
| `harness.headless` | output is an `int.serializer` bundle (a **directory**) | example shows `-out snap.json` (a **file**) | build either, and be wrong against the other AC |
| `engine.invariant` | per-invariant ran/skipped, so "unregistered" is distinct from "conserved at zero" | sketches `Check(state) Violation` as an "e.g." | return a bare `Violation` that cannot carry the distinction |

The `ui.keys` row is the dangerous shape: **the check passed, the rule died.**
AC-11b was a weakness-pattern-#4 control over user-editable data reaching a
dispatch decision, and the delivered implementation satisfied its check while
leaving that control non-existent.

**The rule, and it is the same standard already applied to regression tests:**

- **Every AC's check must be capable of FAILING an implementation that
  satisfies the AC's prose but violates its rule.** Before writing a check,
  ask what a lazy-but-plausible implementation looks like, and confirm the
  check rejects it. A check that any reasonable build passes is documentation,
  not verification.
- **Where a check is a grep, state what a false pass looks like.** A grep finds
  a string; it cannot tell whether the string means what the rule needs.
- **Where a check is a type sketch, say which part is binding and which is
  illustrative.** "e.g." on a signature is fine; "e.g." on the information the
  signature must carry is not.
- **Where a check names an artifact, name its shape, not an example.**
  `snap.json` implied a file and cost a build.

**Why this is a BA rule and not a dev rule.** All three defects were found by
developers who escalated rather than guessing, which is the v1.7 duty working
exactly as intended. But each cost a bounce, and the `ui.keys` one would have
shipped a missing security control had the developer been slightly less
careful. The reciprocal duty is that criteria arrive verifiable — a dev cannot
be the only line of defence against criteria that contradict themselves.

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
| **BA** (one, persistent) | Sonnet | Writes the **acceptance criteria** for each item BEFORE the junior developer receives it: numbered, individually testable criteria derived from the item's spec_ref sections + the lead's brief, saved as `docs/planning/acceptance/<mkey>.md`. The criteria are the contract: the junior builds to them, the Tester verifies against them, Bill's final review confirms them. The BA never writes code and never relaxes a spec requirement — conflicts between spec and brief are escalated to the lead. **North-star vetting (v1.12 — FEAT-043, Aaron's design-north-star.md, 2026-08-11):** for any player-facing feature (a screen, a mechanic, a lever), the BA names which of the five-question test (`docs/planning/design-north-star.md`, also `code.json.designNorthStar`) the item answers — which conflicting demand it sharpens, what the player gives up. An item that answers none may still be necessary infrastructure (register it as such), but the acceptance doc must say so explicitly rather than silently omitting the check — the doc is deliberately 10,000 feet and is never decomposed into new ACs of its own. |
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
- The Tester's suite is derived from the item's brief (GR#15: expected results come from the brief/spec, not invented) — baseline for Go items: `go build ./...`, `go vet ./...`, **`golangci-lint run ./...`**, `go test <pkg> -race -count=1`, `gofmt -l .` empty, deliverables-vs-brief checklist, forbidden-touch check (`git status` shows only allowed paths).
  - **`golangci-lint run ./...` was added 2026-08-09 after a `staticcheck SA4006` dead store reached `main` past a junior, a Tester, a Destructive agent and the lead.** CI runs golangci-lint as a **blocking** job with a *stricter* rule set than `go vet`, and until that day nothing local matched it — so "vet and gofmt are clean" felt like lint coverage and wasn't. Run the same tool CI runs, or you are checking a different thing and calling it the same name.
  - **`-race` in this baseline was, until 2026-08-10 (SEC-028), the *only* place it ran at all** — CI's `go test ./...` in `build-test-vet` and `determinism-gate` never passed `-race`, so the entire SEC-003..SEC-020 concurrency chain was verified only by a Tester/Destructive agent remembering to type the flag locally. A scratch-copy proof showed CI's exact command passing 10/10 against a reintroduced SEC-003-shape defect while `-race` failed 5/5 against the same broken build. This is the **inverse** of the golangci-lint case above — that time local was weaker than CI; this time CI was weaker than local. Fixed by adding `-race -count=1` to both jobs' `go test` steps (`.github/workflows/ci.yml`) rather than by leaning harder on the Tester baseline, per the same "run the same thing CI runs" principle: a check that lives only in a human/agent's habit is not a gate. The Tester still runs `-race` locally per-package as fast, targeted feedback ahead of a CI round-trip — CI running it too is defense in depth, not a reason to drop it here. **Unverified as of this writing**: whether `-race` actually functions correctly on the `windows-latest` Actions runner (ASM-090) — watch the first post-SEC-028 CI run before trusting the gate is real.
- **Concurrency tests must be deterministic, not probable.** Twice in one day a test passed locally and failed on CI purely on scheduling (BUG-005's subscription pump; the seal-rejection assertion that needed a registration to land *after* the seal). Both were fixed by **constructing the state** rather than racing for the timing — drive the operation to completion, then assert — and in the second case by *removing* the ordering-dependent assertion outright rather than adding retries, sleeps or iteration counts. A concurrent hammer is still worth keeping, but it may only assert what it can guarantee under any schedule (e.g. "every result is success or this specific error, never a third outcome").

## Rationale (wave-1 lessons)

- Wave 1 worked but the lead personally caught a junior's basic robustness bug (BOM handling in the plan guard) — mechanical catches belong to a cheaper, dedicated verifier.
- Juniors' self-reported "verification output" is not trusted evidence; an independent runner is (the Tester re-runs everything from scratch).
- A single documentation owner prevents N juniors drifting into N house styles across `docs/design/`.

## First-pass quality mandate (v1.8 — Bill, directed by Aaron, 2026-08-12 late)

**Origin:** the 2026-08-12 evening wave shipped real features but burned heavy tokens on attack-fix-reattack churn: BUG-123 consumed 9+ rounds (three independently-bypassed hand-rolled quote scanners before the GR#3 extraction), BUG-119 consumed 10 (incremental key patching until a structural ruling), and FEAT-018's fatal copy-race was pre-announced by 19 astgate findings a junior blanket-dismissed. Counter-evidence from the same wave: FEAT-011 and FEAT-063 — both built against strong, code-grounded criteria — passed near-clean on round 1. Quality is upstream: it lives in the brief, the reuse decision, and how gate output is treated — not in more attack rounds. Aaron's directive: stop paying for crap first passes.

Six rules, binding from the next dispatch:

1. **Reuse-first (GR#3 at dev time, not attack time).** Every brief carries a prior-art paragraph. Before writing any new mechanism the junior greps the repo for an existing hardened implementation and checks the item's family history (`node claude-bow.js verdict <code>`, sibling items' comments). Delivering a parallel implementation of an in-repo capability is an automatic Tester FAIL. "Modeled on X" is the tell — reuse means require/import, not transcription.
2. **Fix the class, not the instance.** Every bounce fix must name the failure CLASS in its delivery note and ship a class-covering test matrix (generative/enumerated where feasible), not a regression test per found case. The same class bouncing twice on one item auto-escalates to the lead for a structural ruling — BUG-119 needed that at round 2 and got it at round 6; the gap was pure token burn.
3. **Mechanical gate output is load-bearing.** Any gate finding (astgate, golangci-lint, errs-registry) on new code requires per-finding resolution in the delivery. A blanket dismissal note is an automatic Tester FAIL regardless of test results — FEAT-018's crash was announced by its findings before an attacker had to prove it.
4. **Attack-first for known threat models.** Guard/security items (and any item whose criteria state a threat model) get their adversarial test suite written by a Destructive agent BEFORE dev dispatch; the junior builds until that suite passes; the post-build attack round then hunts only novel classes. Converts serial find-fix-reattack rounds into one parallel round.
5. **Design-first for repeat offenders and P0 security.** Any item at 3+ rounds, and every P0 security item, gets a lead-authored mechanism spec in the brief (the shape, the invariants, what to reuse — ten lines suffice) before re-dispatch. Juniors implement designs; they do not discover architecture by bounce.
6. **Model tier is a tool, not a fixed cost.** Worker default stays Sonnet; the dispatching window may escalate a security-critical or thrice-bounced item to a stronger model on its own judgement, recording the choice on the item. Six extra rounds cost more than one stronger first pass — spend where the loop history says the cheap tier is churning.

## Proportionality + game-first priority (v1.11 - Aaron, 2026-08-13)

**Origin:** Bob's utilisation deep-dive measured the 2026-08-13 wave at ~3 game-code commits out of 81 (the rest astgate/guards/bowcli/docs), with S3 untouched since S2 closed, and measured GR#23's overhead at one BOW item + one destructive agent + a recorded verdict for a one-line lint fix. Aaron's ruling, verbatim spirit: "we are not building NASA code to get men on the moon."

1. **GR#23 proportionality tier (FEAT-077):** commits whose staged diff is docs-only (`*.md`) or test-only (`*.test.js`/`*_test.go`) need Tester-level verification only - no Destructive verdict. Engine/UI/data code, guards/hooks, sync/bow CLIs, and anything in the commit/push path stay full-tier. Enforced mechanically by claude-destructive-guard.js's diff classifier, never by judgment. (Recorded trade-off: BUG-199 was a test-only time bomb; Aaron accepts that risk for throughput.)
2. **Game code beats framework.** Dispatch priority is S3/engine/UI/data items first; `tool.*`/harness-meta work is capped at background-lane levels (1-2 lanes) whenever game work is dispatchable. The `util` report's per-dispatch BOW codes make the mix auditable per hour.
3. **Perfection is not the bar.** Acceptance criteria ahead of the build queue stop at N+3 sprints (v1.4 cadence already says this - re-affirmed against the 128-doc estate reaching S11). Attack rounds beyond round 3 need the v1.8 rule-5 lead design ruling, not another bounce.
