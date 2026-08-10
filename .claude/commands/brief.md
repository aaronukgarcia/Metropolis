---
description: Compose an agent brief from looked-up facts, not memory (Metropolis dev-team pipeline)
---

# /brief — write a dispatch brief that does not invent its own facts

Use before every `Agent` dispatch in the dev-team pipeline. `$ARGUMENTS` is the
BOW code (e.g. `MOD-017`), optionally followed by the role (`BA`, `dev`,
`tester`, `destructive`, `QA`).

This skill exists because of a measured failure rate, not a hunch. In one wave
on 2026-08-10 the lead made eight dispatch errors; **seven were facts that were
sitting in the BOW, in `code.json`, or on disk, and were typed from memory
instead**. Three dispatches commissioned criteria that already existed. One
briefed a module key that was a plural of the real one. One briefed a path that
would have tripped a lint ban. `claude-dispatch-guard.js` blocks those at send
time — this skill stops you writing them in the first place, and fills in the
mandatory blocks that otherwise get retyped from memory and quietly drift.

## GATE 0 — gather the facts. Do not draft anything before this.

Run these and read the output. Every field below goes into the brief verbatim.

```bash
node claude-bow.js show <CODE>            # title, status, mkey, deps, comments
node claude-bow.js ready                  # is it actually unblocked?
```

Then, keyed off the **mkey the BOW returned** — never off the name you were
about to type:

- `docs/planning/acceptance/<mkey>.md` — **does it already exist?** If yes, the
  work is not "write criteria". Read it. Either dispatch a dev against it, or
  brief a BA to extend it, naming the specific gaps.
- `code.json` — the module's registered **path**. Use that, not the path you
  assume. `ui.harness` lives at `internal/harness/uitest`, not
  `internal/ui/harness`; under `internal/ui` the GR#20 lint ban on
  `ui → engine` imports would have bitten on first contact.
- `git log --oneline -5 -- <path>` — what already landed there.
- Recall Vestige for the area (GR#14).

Check for related open findings (`SEC-`) and assumptions (`ASM-`) on the same
code path — an agent that rediscovers a logged finding has wasted its run.

## GATE 1 — scope, and check it is free

Name the **exact** files the agent owns. Ownership is transferred, never
duplicated (dev-team-process v1.6.1). The dispatch guard blocks overlaps
against live claims, but it can only see what you declare — so declare
precisely. "Do not touch X, Y, Z" for the areas other agents hold.

## GATE 2 — compose

Include every block. These are not boilerplate to trim; each one exists because
its absence cost a run.

1. **Role and orientation** — who they are; read `CLAUDE.md` and
   `docs/planning/dev-team-process.md` first, both binding.
2. **Task**, with the BOW code and the acceptance-criteria path if one exists.
3. **FILES YOU OWN** — the exact phrase, so the guard can parse and claim it.
4. **Grounding** — the specific files to read, and the sibling module whose
   conventions to follow (GR#3: do not build a second way of doing a thing).
5. **Golden Rules that bite here** — GR#7 registry errors, GR#15 derived not
   hardcoded, GR#20 contract-first, GR#1 error trapping. Name the ones that
   actually apply; a list of all twenty-one gets skimmed.
6. **Quality bar**, non-negotiable and stated every time:
   - Concurrency tests are **deterministic, not probable**. Construct the
     state; do not race for the timing.
   - **Every regression test must be demonstrated to FAIL against the unfixed
     code.** On this project a test that cannot fail is the *default* outcome,
     not the exception — three drafts of one test scored 0/500 against
     known-buggy code before one bit.
   - For DoS-shaped work, **quantify** (bytes, timing). A fix that pays the
     full attacker-controlled cost internally and errors at the end is still
     the vulnerability, and an error-return assertion cannot tell them apart.
7. **Baseline**, all clean before reporting done:
   `gofmt -l .` · `go build ./...` · `go vet ./...` ·
   `golangci-lint run ./...` · `go test ./... -count=1 -race`
8. **Assumption mandate (v1.7)** — verbatim:
   ```
   node claude-bow.js add assumption "<short title, under ~120 chars or the DB rejects it>" \
     --code-path "<file>" --codejson "<mkey>" --desc "<why, and what breaks if wrong>"
   ```
   For a dev: *if the criteria contain unlogged assumptions, REJECT the ask.*
   For a BA: *unlogged assumptions are grounds for the dev to reject your
   criteria outright.*
9. **Banned commands** — `git stash`, `git reset --hard`, `git checkout --`,
   `git clean`. Never. Do not commit; the lead commits.
10. **Report format** — what was built, baseline **verbatim**, ASM numbers,
    proof each regression test can fail, and anything left undone.

## GATE 3 — invite disagreement

End every brief with an explicit invitation to reject the premise. This is the
highest-value line in the whole template and it is the one most likely to be
dropped for brevity. Today it caught a lead brief that placed a size check
*after* `json.Unmarshal` had already paid the 198MB cost it was meant to
prevent — in a fix for a finding about bounding the wrong thing.

> Where you think this brief is wrong, incomplete, or badly scoped, say so
> plainly in your report. A disagreement found now is worth more than a clean
> build of the wrong thing.

## Anti-patterns

- **Do not** dispatch from a ready-queue title alone. That is how three
  duplicate BA runs happened in one wave.
- **Do not** write "audit the siblings" and consider it actionable. Name them.
  A vague instruction produced a narrow reading and a missed `Len()`.
- **Do not** send a follow-up to an agent id you have not just re-read. A
  correction went to the wrong agent today; the recipient correctly declined.
- **Do not** thin the mandatory blocks to save space. They are the parts that
  drift.

## Added from the 2026-08-10 waves

- **A breaking signature change gets its OWN dispatch, ahead of the work that
  needs it.** File ownership cannot be disjoint across one: every caller must
  compile against the new signature, and those callers live in files owned by
  someone else. A `RunCommandLoop` change forced edits to two files nobody had
  been given, and the brief had anticipated only one of them (BUG-032). Land
  the blast radius first, then parallelise.
- **Require the agent to state the exact revision it expects the repo to be at
  when it finishes.** An agent verifying git behaviour cleaned up its worktree,
  its temp config and its refs — and left the commit, on local `main`, authored
  as a fabricated identity. It surfaced only when a PR merge refused to
  fast-forward (BUG-035). A cleanup claim should name the **end state**, not
  list the steps taken.
- **Anything destructive happens in a throwaway repo under the scratchpad**,
  never in this one.
- **Tell the agent which numbers in the brief are derived and which are
  asserted.** A brief claimed "after the rewrite every commit carries the same
  address". It did not, and an agent that had trusted it would have built a
  guard around the wrong identity and blocked every future commit (BUG-036).
  Where you state a fact the agent will build on, say how you know it — or say
  that you have not checked.
- **Warn about in-flight neighbours by name.** When several agents share a
  build, tell each one which packages may break underneath it and that the
  correct response is to report, not to fix someone else's file.
