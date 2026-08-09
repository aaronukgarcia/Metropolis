# Metropolis Dev-Team Process v1.5

**2026-08-08/09 · directed by Aaron (v1.1: Tester + Documentation; v1.2: BA; v1.3: independent QA; v1.4: pipelined cadence; v1.5: Resource Manager, saturation rule, team caps, heavy checkpointing) · supersedes the wave-1 "lead reviews everything directly" flow**

## Saturation rule & Resource Manager (v1.5)

**No agent idles while non-blocking work exists.** A dedicated **Resource Manager (RM)** agent — persistent, reporting DIRECTLY to Bill — owns the assignment board:

- Maintains `docs/planning/team-board.md`: every agent, current assignment, status (busy/blocked/idle), what they return to when unblocked.
- When an agent blocks (e.g. a dev waiting on a Tester verdict), the RM proposes interim work for it and remembers the return point; when the blocker clears, the RM proposes the return.
- **Team caps** (RM enforces by flagging breaches to Bill): max **4 concurrent Jnr developers**, **1 Tester**, **2 BAs** (disjoint sprint ownership), **1 Documentation**, **1 QA**, **1 RM**. Growth beyond caps requires Aaron.
- The RM is **advisory**: it recommends dispatches/reassignments; Bill executes them (only the lead messages agents). RM never edits code and never talks to other agents.
- **At-risk parallel starts**: the lead may start sprint N+1 items whose dependencies are code-complete but whose sprint gate (e.g. Aaron's contract freeze) is pending — the RM tracks every at-risk item and its rebase exposure so a freeze-review change fans out correctly.

## Staging-area discipline (v1.5.1 — from the VERSION-fixture incident, 2026-08-09)

The git index is a **shared mutable resource** across concurrent agents. Rules:
- **Juniors**: never leave anything staged between tool calls — any stage→verify→reset test sequence must complete atomically inside a single command invocation.
- **Lead**: commits use explicit pathspecs (`git commit -m "..." -- <paths>`) or verify `git diff --cached --stat` matches the intended set immediately before committing — a concurrent agent's staged file must never ride along. (Incident: a junior's staged `VERSION` test fixture was swept into an unrelated docs commit; caught and reverted within two commits.)

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
