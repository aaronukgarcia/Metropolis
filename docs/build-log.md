# Metropolis Build Log

> A running narrative of what was built, what broke, what was decided — and **why**. The BOW records *state*; git records *changes*; this file records *reasoning*, which is the thing neither of those keeps and the thing a future session most needs.
>
> Append as work proceeds. Newest day first.

---

## 2026-08-09 — Sprint 1 closes; the first security sweep

A long session. Three things happened that are worth separating: Sprint 1 shipped, CI was discovered to have **never** been green and was fixed, and an adversarial review role was created that immediately found a chain of concurrency defects nobody's tests could have caught.

### 1. Sprint 1 delivered and closed

Recovered mid-flight from a session bounce: three juniors' work was sitting uncommitted in the working tree with Tester verdicts pending. Nothing was lost; nothing was redone.

| Item | Commit | Note |
|---|---|---|
| MOD-012 `engine.core` | `f81e5d7` | Two-layer clock, deterministic phase pipeline, shard pool, CoW persist |
| FEAT-004 `feat.detgate` + BUG-002 | `47de5d0` | Determinism gate armed in CI; golangci v2 migration |
| FEAT-005 `ui.screen.map` | `b721740` | F1 map screen |
| FEAT-007 `ui.screen.debug` | `190f02a` | F12 info panel |
| FEAT-008 `feat.debugmode` | `a364855` | Runtime debug switch, sticky `DebugTouched` |
| **FEAT-006 `feat.skeleton`** | `ad1c308` | **The walking skeleton — Sprint 1's exit gate** |

**The walking skeleton was nearly blocked by its own acceptance criteria.** AC-1 and AC-5 specified their verification method as scripted-key tests via `ui.harness` — a Sprint 2 module that does not exist. Rather than weaken the criteria so a junior could tick them, they were **split**: the parts provable now (real `int.protocol` API driving real `harness.stub` → `ui.core` → `ui.screen.map`, asserting the rendered buffer against Folkestone-64) stayed in the gate; the parts needing Sprint 2 became FEAT-032 and FEAT-033, tracked with dependencies.

This became the session's default move for scope pressure — **defer with a tracked item, never drop and never pretend**. Used four times: map overlays (FEAT-031), the pacing constant (FEAT-030), and the two skeleton halves.

**ASM-001 — what the exit gate does *not* prove.** The skeleton renders via `harness.stub`, **not** `engine.core`. `engine.core` is registered in the module registry, but the binary never constructs one; its determinism is proven separately and in isolation by `feat.detgate`. Both claims are true and valuable; they are not the same claim, and **nothing on screen would let a viewer tell them apart**. This is correct for Sprint 1 under the stub-everything discipline — but it is recorded, given its own heading in `cmd/metropolis/main.go`'s package doc, and stated aloud at any demo. It closes when `engine.core` actually drives the binary.

### 2. CI had never been green — not once, since the first commit

An independent QA audit found that **every CI run in the project's history had failed**, starting with commit #1. Nobody had ever looked. Two stacked defects, neither findable from a developer machine:

- **BUG-004** — `core.autocrlf=true` with no `.gitattributes`. GitHub's Windows runners checked out CRLF `.go` files; `gofmt` rejects CRLF. 100% reproducible on CI, *impossible* to reproduce locally. Fixed by pinning `eol=lf` (`d5c9c19`).
- **BUG-005** — a subscription test assumed the pump goroutine would be scheduled promptly. Under CI contention four signals correctly coalesced into one delta. Fixed with a deterministic synchronisation point, **not** a longer timeout (`76e1961`) — a bigger deadline hides a genuine dropped-wakeup race exactly as well as it hides slow runners.

First green build: run `31314318798`. Green on every push since.

**The real defect was BUG-006: nobody was watching.** No post-push check, no branch protection. A red build nobody polls is indistinguishable from a green one — GR#17 almost word for word. Aaron ruled for full PR-required branch protection; interim control is checking `gh run list` after every push.

**Lesson that recurred all day**: *"it passes locally" was never evidence about CI.* The divergence between the two environments **was** the bug, so no amount of local checking could have found it.

### 3. The Destructive agent — a new pipeline stage

Aaron created a role: after the Tester, an agent whose job is to **break things** — attack surfaces, misuse paths, insecure call-ability — with the right to reject work back to the developer, and findings logged as `SEC-` items carrying a **weakness class** so patterns can be counted.

Built and enforced, not merely described:
- New BOW type `finding` (`SEC-` codes) with `--code-path`, `--codejson` and `--class` **tool-enforced**.
- `node claude-bow.js weakness` — groups findings by class and flags any class recurring 3+ times.
- Scan stamps in `data/security-scans.json`, merged into `code.json`'s `securityScan` field by `generate.js`.

**One deliberate deviation from the instruction, and why.** Aaron asked for the stamp in `code.json`. But `code.json` is *generated* from the master plan and carries a do-not-hand-edit banner — a stamp written there would be silently wiped on the next regeneration, producing the worst possible outcome: something that **looks** scanned and is not. So the ledger is the SSOT and the generator merges it in. The stamp appears exactly where asked, and survives.

Three agents swept the codebase in parallel (foundation+protocol, engine, UI+cmd+tooling). **19 of 19 built modules scanned.** Absent stamp = never scanned; unscanned must never be mistaken for clean.

### 4. What the sweep found

**21 findings.** The ones that mattered:

- **SEC-001 (P0)** — path traversal / zip-slip. `ShardPath` built a file path from `ShardMeta.Name`, decoded straight out of an untrusted bundle's `header.json`. Arbitrary file **read** (`ValidateBundle` hashes whatever the traversal resolves to; `metctl export` dumps it) and arbitrary **write** (export's destination built the same way). Reachable because `metctl` is designed to operate on *shared* save and fixture directories.
- **SEC-004 (P2)** — command injection, **proven executing**: a branch named `evil&ver&` made Windows print its version banner through `git rev-list`, in code that runs automatically at every session start.
- **SEC-019 (P0)** — `SubscriptionServer` copy aliasing → `fatal error: concurrent map writes`. A hard crash, on an exported type, with the triggering lock pattern running live in `cmd/metropolis`.
- **Five hook bypasses** — our own guards were defeatable: `git commit -F file` walked past the trailer ban; a `%TEMP%` token in a path defeated the version guard *and* its catch swallowed the error into a pass; `git push upstream release` skipped the deploy check because the name lacked "main"; and plan-guard denied unrelated commands whose *prose* mentioned "git commit".

### 5. The concurrency chain — five rounds on one root cause

The single most instructive sequence of the session. Each round fixed the demonstrated instance and left siblings; each sibling was found by a **verifier**, never by the fixer.

| Round | Defect | Fix |
|---|---|---|
| SEC-003 | `Engine.hooks` read unlocked on the tick path → **fatal** concurrent map access | Sealing: reject late registration |
| SEC-014 | `e2 := *e` **aliases** the map while getting its own `mu` — two locks, one map | `self` identity check |
| SEC-016 | the check ran *after* `mu.Lock()`, so a copy taken mid-lock **hangs forever** before reaching it | `atomic.Pointer`, checked **before** the lock |
| SEC-018 | only 2 of 8 `e.mu.Lock()` sites were guarded | All sites enumerated and guarded |
| SEC-019 | `SubscriptionServer` had the *original* crash class, untouched | Same pattern applied |

Committed as `15bd4fd` after a final re-attack came back **clean** across 13 attack shapes — including method values bound to a copy, closure captures, and struct literals with `self` unset.

**Why this could not have been found by testing.** Every one of these passed its test suite. A test asks "does this do what I expect?"; the attack asks "what happens when someone uses it wrongly?" The transport `Close()` panic (BUG-007) had passed every test for the project's entire life, because the test that *looked* like it covered the race waited for its goroutines to finish before closing.

**SEC-020 — the class behind the chain.** Enumerating the shape found **nine** vulnerable types repo-wide: any struct holding a mutex *and* a shared reference field is copyable, and the copy aliases the referent while getting its own lock. Worst is `InProcTransport`, where a copy's independent `closeMu` **reopens BUG-007's send-on-closed-channel panic** — fixed earlier the same day.

> **Aaron's ruling: all nine, full pipeline.** The tiered option (guard the severe ones, document the rest) was recommended and overruled. The overrule is right: `go vet copylocks` covers our own literal copies, but this session twice demonstrated copy forms vet does not catch, and the observed consequences are a fatal map write, a permanent silent deadlock, and a panic. Choosing "document the convention" would have been the fifth consecutive round of trusting prose where code can enforce.

### 6. Process evolution — v1.6 → v1.8

Aaron added the assumption rule mid-session: **anything you decided that the spec, criteria or brief did not decide for you is an assumption, and it is logged or the work is rejected** — with reciprocal duties (a developer must *reject the ask* if the BA's criteria rest on unlogged assumptions; a Tester must hunt for them and FAIL work carrying unlogged ones *even if every criterion passes*).

What the rule turned out to be worth, in practice:

- **ASM-047 predicted its own failure.** A junior's whole-word matcher for the secret guard logged "an unusual-casing identifier where the camelCase split doesn't fire" as an unverified risk. It then broke on exactly that — `MAX_COUNT` shattered into single letters, silently un-guarding an entire naming convention. The record showed the author knew where the danger was.
- **ASM-041 produced a falsifier no verifier could have written.** Asked to justify using an `unsafe` byte-copy in a regression test, a junior identified that a typed `c := *e` gets a **GC write barrier** a raw memcpy skips — so if that equivalence ever lapsed, the test would guard a different mechanism *while still passing*. Both the Tester and the junior agreed this was only discoverable from the side that made the choice.
- **The rule caught the lead three times.** A verbal steer in a brief is an assumption with no record (ASM-043). Recorded rather than quietly complied with, because a rule that only points downwards isn't a rule.

Refinements, three of them proposed by agents rather than the lead:

- **v1.7.1** — a fast path for paperwork-only FAILs. Explicitly **rejected** the obvious shortcut of letting the Tester log the assumption itself: *the rationale must come from the person who made the trade-off*; a reconstructed rationale reads identically in the database and is worth far less.
- **v1.7.2** — "fixing a fix" work logs as it goes. Second-order changes produced an unlogged judgement call on the first pass **every single time**.
- **v1.6.1** — file ownership is transferred, never duplicated (after a lead sequencing error destroyed a BA's work).
- **`git stash` banned** for all non-lead agents — it operates on the whole shared tree. Added after a junior caught itself, reverted within seconds, and **reported the near-miss unprompted** when nobody would have known.

### 7. Weakness patterns — the actual deliverable

The point of classifying findings is that a recurring class is a statement about **how the team writes code**, and the response to that is teaching, not tickets.

1. **An invariant stated in prose is not enforced.** Three instances, three packages, three authors, in the first sweep. *"This order IS the contract — never reordered at runtime"* on an exported mutable slice; *"hooks are registered at boot only"* with nothing preventing it; a package correctly identifying itself as the hostile-input surface while treating the shard **name** — also a path component — as an inert label. **Rule:** if an invariant matters, the API must make violating it impossible or loud. BAs now add a criterion that an invariant **cannot be broken through the public API**.
2. **A value duplicated across a module boundary needs a drift test.** GR#20 legitimately forces duplication (the F12 phase mirror; the stub's tick limit). The duplication is fine; *silent* divergence is not. And the drift test must be **proven able to fail** — one was, by mutating the constant and reading the failure.
3. **Fix the class, not the demonstrated instance.** Four rounds of the chain closed exactly the path the PoC exercised. Briefs must **ask** for the class — a brief saying "fix this call site" gets that call site, so the miss belongs to the brief as much as the fixer.

### 8. Corrections made on the record

Kept here deliberately: a build log that only records successes teaches nothing.

- **SEC-013 severity was wrong.** Reported as a live gap on a Tester's evidence. The fixing junior tried to weaponise it and *couldn't* — an extension is always appended, so a trailing dot never lands last. Reclassified P2 → P3, defence-in-depth. The junior could have banked the fix and let it look like a closed vulnerability.
- **The "unreachable bypass" was not a bug.** Logged as a gap that `CLAUDE_DISABLE_SECRET_GUARD` can't be set from an agent shell. A junior pushed back: an agent able to self-authorise a bypass of a fail-closed guard would defeat it entirely — the unreachability **is** the design. Only the misleading comment needed fixing. Withdrawn.
- **A near-miss with the guard.** When the secret guard blocked a verified commit on false positives, the first instinct was the documented emergency bypass. It didn't work. The better answer — fix the guard's defect rather than route around it — should have been first. Recorded in ASM-045 rather than deleted once moot.

### 9. Where it stands

- Sprint 1 **closed**; CI **green** and holding.
- 21 findings raised, 13 closed, 8 open. `input-validation` ×9 and `concurrency-safety` ×7 both flagged as recurring; both being worked down.
- ~57 assumptions logged.
- Next: SEC-020 wave 1 (`InProcTransport`), then the remaining seven types, package by package, one agent per package.
- Outstanding: turn the `input-validation` recurrence into a criteria rule, the way the other three patterns were handled.

**Open questions for Aaron:** the contract freeze (OD f32-vs-f64 — ruling *against* f32 costs nothing since it's what's built; and the duplicate correlation-ID generators, where the real risk is their differing `crypto/rand`-failure fallbacks).
