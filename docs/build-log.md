# Metropolis Build Log

> A running narrative of what was built, what broke, what was decided — and **why**. The BOW records *state*; git records *changes*; this file records *reasoning*, which is the thing neither of those keeps and the thing a future session most needs.
>
> Append as work proceeds. Newest day first.

---

## 2026-08-12 — Ten-lane pipeline wave: two ACCEPTs on reattack, a real data race caught as a side effect, and BUG-119's Destructive process finally outrunning its own fix

*(Extended in place, same evening, same session — see "Later in the same wave" below the original state snapshot. Everything from here to that marker was written earlier in the session and is superseded/corrected by the later material where the two disagree; both are kept because each was accurate at its own point in time and the corrections are called out explicitly rather than silently rewritten.)*

A commit-ready-list session under the 2026-08-12 protocol (lead never a blocker; verdicts posted on BOW items, not fed prose). **Nothing has landed on `main` yet** — this entry documents in-flight/verdict-recorded state as of this wave, ahead of the lead's sweep-and-commit pass. All verdicts below are quoted from `bow_destructive_verdicts` via `node claude-bow.js verdict <CODE>`, not transcribed from the coordination feed.

### SEC-052 — commit-msg hook `-c`/env-var identity poisoning: three rounds to ACCEPT
Round 1 Destructive found a live bypass: `claude-author-identity.js`'s `configuredEmail()` shells out to `git config user.email`, which `-c user.email=...` (or the `GIT_CONFIG_COUNT`/`KEY_n`/`VALUE_n` env-var equivalent) poisons in the *same* invocation the hook is checking — both `git commit` and `git merge --no-ff` were fully bypassable, live-reproduced in a scratch repo. **REJECT**, bounced to the same junior. The fix scoped `configuredEmail()` to `--local`/`--global` reads specifically (bypassing `-c`/env overrides). Round 3's independent reattack (attacker "Osprey") tried six angles — `os.userInfo().homedir` immunity under simultaneous `HOME`/`USERPROFILE`/`GIT_CONFIG_GLOBAL` poisoning, `--file` redirection combined with the same poisoning, a symlink attack on the resolved config path (blocked by this machine's own privilege model, judged out of the fix's threat model), the `CLAUDE_AUTHOR_IDENTITY_TEST_FORCE_NO_CONFIGURED_EMAIL` escape hatch traced end to end, `--system`/`GIT_CONFIG_SYSTEM` reachability (grep-confirmed unreachable), and a from-scratch re-run of every earlier scenario through the real installed hook (`claude-committhook-install.js`, not a hand-rolled wrapper). No bypass found across ~10 attack combinations; full suite 102/102. **Verdict: ACCEPT** (Osprey, 2026-08-12 17:51:11).

### FEAT-065 — dev console + feedback pipeline: a same-correlationID data-loss bug, then ACCEPT under stress
Round 1 Destructive found feedback submissions sharing a caller-supplied `correlationID` silently overwrote each other's on-disk JSON file — a real data-loss bug, not a theoretical race. **REJECT.** Fixed by minting a per-call nonce (`errs.NewCorrelationID()`, crypto/rand-backed) and filing as `safeFilenameFragment(correlationID)-safeFilenameFragment(nonce).json`. Round 2's independent reattack (attacker "Vex") verified the entropy source, confirmed the nonce has no caller-controlled seam, re-ran the shipped 2-goroutine test plus its own 200-goroutine same-correlationID stress test (added temporarily, removed after) — 200/200 files survived with distinct bodies, zero overwrites, `-race` clean — and checked `claude-devfeedback-import.js`'s importer for any hard assumption of the old bare-correlationID filename format (found none). **Verdict: ACCEPT** (Vex, 2026-08-12 17:37:41).

### astgate cluster — SEC-044 through SEC-048, SEC-051: independently re-verified, all ACCEPT
Six findings against `internal/foundation/astgate/gate.go` (parameter scanning, mutex-alias resolution, orphaned-allowlist detection, error-wrapping), each carrying an independent Destructive reattack this session on top of its original fix verification, built from fresh fixtures rather than the fixer's own tests:
- **SEC-044** (Marlowe) — parameter scanning on receiver methods of non-candidate types; fresh `Sentinel`/`Poke` fixture, ACCEPT.
- **SEC-045** (Marlowe) — receiver-vs-parameter scan independence; fresh `Guarded.Merge` fixture, ACCEPT.
- **SEC-046** (Marsh) — mutex-alias resolution; fresh same-package alias fixture plus a cross-package negative control confirming the disclosed same-package-only blind spot is real, ACCEPT.
- **SEC-047** (Nyx) — `baseTypeName` slice/pointer-slice/variadic unwrapping; three fresh fixtures, ACCEPT.
- **SEC-048** (Corvid) — bare `fmt.Errorf` replaced with registry-sourced `errs.Wrap`/`errs.New` (MET-F700/F701/F702); grep-confirmed zero live bare-`Errorf` sites remain, ACCEPT.
- **SEC-051** — orphaned-allowlist-entry hard-fail; ACCEPT, with one disclosed residual (same-commit self-approval of a fabricated allowlist entry) judged acceptable given branch protection + GR#23's own per-commit Destructive requirement.

### BUG-119 — astgate ratchet key stability: six REJECT rounds, a structural ruling, round 7 in flight
**Live BOW state as of this entry: still `open`, current verdict REJECT** (attacker "Ashcombe", round 6, 2026-08-12 18:03:44) — this is a correction against this session's earlier working assumption that BUG-119 was already closed. The bug: `violationKey()`'s matching key is built from a hand-unwrapped subset of a node's identity, and each fix round has closed exactly the collision shape just found while leaving the next unenumerated shape open — three distinct collision mechanisms across three rounds (round 2: missing `ReceiverTypeName`; round 4: unhandled `IndexExpr`/`IndexListExpr` folding into the wrong sentinel; round 6: *multiple* unrecognized receiver shapes — e.g. `map[string]int` vs `chan int` receivers, both syntactically legal per `go/parser` though neither compiles — collapsing onto the *same* `unrecognizedReceiverSentinel`, live-reproduced with a two-shape fixture). Ashcombe's round-6 verdict includes an explicit engineering opinion: a string-concatenation key built from manually enumerated AST shapes cannot be complete by construction, and recommends keying on `go/types`-resolved identity (or an opaque per-node disambiguator) instead of continuing to chase individual shapes.

Bill's lead ruling (2026-08-12 18:07:26) accepted that diagnosis and mandated a **structural** round 7, not another single-case patch: (1) `violationKey` must derive from the node's complete identity — file, full enclosing declaration chain, kind, and the full type expression as printed via `types.TypeString`/`go/printer`, never a hand-assembled subset; (2) the gate must self-check at key-construction time — two distinct AST nodes producing the same key is now a hard error that fails the gate run, since silent merging is exactly how rounds 1–5 shipped incomplete; plus a generative/enumerated decl-shape test (plain, pointer, slice, variadic, generic instantiation, method vs func, nested types) in place of one regression test per found collision. Round 7 was dispatched against this ruling; no new verdict is on record yet as of this entry — **treat BUG-119 as open, not closed**, until a round-7 verdict lands.

### BUG-125 — Store[T, PT] generic copy-guard: a real data race, found as a side effect of BUG-119
Discovered, not assigned: BUG-119 round 5's `baseTypeName` generics fix (handling `IndexExpr`/`IndexListExpr` receivers) had a side effect — it made `internal/foundation/data/reload.go`'s `Store[T, PT]` (a two-type-parameter generic candidate type) visible to AC-3 for the first time; before that fix its receiver methods (`Get`, `OnChange`, `Reload`) were entirely absent from astgate's scan, not merely mislabeled. Filed as BUG-125 specifically to force review of a provisional allowlist entry rather than let it ship unreviewed. Fixed with the standard SEC-020 self-pointer pattern (`self` frozen at `NewStore`, checked before any lock is taken). Independent reattack (Osprey) used a **mechanically distinct** route from the fixer's own tests — `reflect.NewAt` + `unsafe.Pointer` copying instead of a raw byte-array cast, and a different generic instantiation (`Store[Modes,*Modes]` vs. the fixer's `Store[Policies,*Policies]`) — across five own-authored tests including a 500×2-goroutine `-race` run (5 repeats, 0 races), a double-copy-chain check, and a bare-zero-value/TOCTOU check. Confirmed zero real call sites of `Store`/`NewStore` outside `internal/foundation/data` itself, so the `Get`/`OnChange` signature change (added error return) breaks no caller. **Verdict: ACCEPT** (Osprey, 2026-08-12 18:28:04).

### BUG-123 (P0) and BUG-124 (P1) — new findings from an independent security sweep, not yet fixed
An independent sweep of the root guard scripts (not part of the ten allocated lanes; folded in because of severity) found two new live defects:
- **BUG-123 (P0, `tool.secretguard`)** — `claude-secret-guard.js`, `claude-version-guard.js`, and `claude-plan-guard.js` share a trigger regex that only tolerates a `-C <dir>` global option between `git` and `commit`. `git -c <key>=<value> commit ...` — an extremely common, unsuspicious idiom for overriding `user.email`/`user.name`/`commit.gpgsign` on one commit — fails to match, so each guard exits 0 *before* calling its actual scan. Live-verified: `GIT_COMMIT_RE.test('git -c core.pager=cat commit -m "steal secrets"')` is `false` for all three, and piping a real `{"command":"git -c foo=bar commit -m test"}` PreToolUse payload into `claude-secret-guard.js` produced a silent allow with no scan output. A real staged secret, hand-maintained version file, or plan/code.json drift would land completely unscanned behind any `-c` prefix. Lower-severity same-root-cause instances also flagged: `claude-pre-commit-check.js` (already advisory per BUG-088), `claude-bow-ref-check.js` ([mkey] reminder), `claude-pre-push-check.js` (GR#19 reminder), and `claude-codename-guard.js` (GR#22 fail-closed codename guard — regex-confirmed bypass, not live-verified since staging the forbidden string was avoided for safety).
- **BUG-124 (P1, `tool.startup`)** — `claude-startup.js` builds `` node claude-sync.js checkin --name ${requestedIdentity} `` where `requestedIdentity` comes straight from `process.env.CLAUDE_IDENTITY`, interpolated unsanitized into a template string passed to `execSync` (Node's default `shell:true`). Live-reproduced: setting the env var to `'Bill & echo INJECTED > injected_proof.txt & echo done'` and calling the real `tryCheckin()` executed the injected command and created the proof file (deleted immediately after confirming). The reproduction also performed one real checkin against the live `metro` coordination DB, immediately checked back out to avoid colliding with an active session — confirmed clean via `claude-sync.js read` afterward.

**Both remain `status: open` as of this entry**, filed 2026-08-12 17:40:39/17:40:40 with no BOW comment recorded since — a fix for BUG-123 was in progress in the mechanical lane at time of writing but had not landed a verdict; BUG-124 is unstarted. Do not treat either as fixed until a comment/verdict appears on the item.

### State snapshot at time of writing
Verdict-ACCEPT and reattack-clean this wave: SEC-052 (r3), FEAT-065 (r2), SEC-044/045/046/047/048/051, BUG-125. Still open, unresolved: BUG-119 (r6 REJECT, r7 dispatched), BUG-123 (P0, unfixed), BUG-124 (P1, unfixed). Nothing committed to `main` yet — this is pipeline-complete-and-verdicted state ahead of the lead's sweep.

---

### Later in the same wave — BUG-119 closes at round 10, three more ACCEPTs, a ten-lane crank-up, and BUG-123 still resisting

Continuation of the entry above, same session, same evening. Bill's 19:48:13 directive ("Aaron says CRANK IT UP... the board target is TEN") pushed the pipeline from BUG-123-round-6-only up to nine concurrent lanes; this section documents where each of those lanes landed. All BOW states quoted below were re-verified live (`node claude-bow.js show/verdict <CODE>`) while writing this entry, not transcribed from the coordination feed.

#### BUG-119 — closed, DONE, after 10 full Destructive rounds
**Status: `done`, closed 2026-08-12 19:48:42, on the strength of a round-10 ACCEPT from attacker "Oberon".** This corrects the earlier part of this entry, which left BUG-119 open at round 6/7. The full arc: rounds 1–6 each closed exactly the collision shape a Destructive round had just found (missing `ReceiverTypeName`; unhandled `IndexExpr`/`IndexListExpr`; multiple unrecognized receiver shapes collapsing onto one sentinel) while leaving the next shape open — Ashcombe's round-6 REJECT diagnosed this as structural: a hand-assembled string key built from manually enumerated AST shapes cannot be complete by construction. Bill's round-7 ruling (18:07:26) mandated the fix stop patching shapes and instead (1) derive `violationKey` from the node's complete identity via `go/printer`/`types.TypeString`, never a hand-assembled subset, and (2) hard-fail the gate run the instant two distinct nodes produce the same key, replacing "silently merge" with "abort loudly." Round 7 delivered both: `violationKey` now keys off `ReceiverExprPrinted`/`MatchedExprPrinted` (the receiver's and matched value's type expressions rendered verbatim via a new `printExpr` helper), and `Run` maintains a key→location map during the scan, hard-failing with a new registry code `MET-F703` naming both colliding locations if a collision is ever produced. Rounds 8–9 (not itemised here — see the item's own comment history for detail) closed further edge cases the structural fix's own formatting-invariance and canonicalization needed. **Round 10 (Oberon) is the final ACCEPT**: confirmed the key-derivation mechanism is genuinely closed, verified formatting-invariance via a `gofmt` round-trip (two syntactically-identical-but-differently-formatted nodes must still key identically), and confirmed a disclosed residual gap is inert in practice. **Commit-ready.**

#### FEAT-018 — Demographics screen: REJECT then ACCEPT on a real crash-causing copy-guard gap
Round 1 Destructive ("Widowmaker") found `demo.Screen` copyable in a way that let a caller take a struct copy and mutate it outside the guard's lock discipline — a live crash-causing gap in a package with 13 exported methods, not a theoretical one. **REJECT.** Fixed with the same `checkNotCopied`/copy-guard pattern already established by `MapScreen`, applied to all 13 exported methods with pre- **and** post-lock checks (defense-in-depth, not redundancy — verified below). Round 2's independent reattack (attacker "Copperhead") used a mechanically distinct repro from the fixer's own tests — a `reflect`+`unsafe` field-by-field walk (`fieldCopy`) rather than the fixer's whole-struct memcpy — and drove all 13 exported methods against a struct-copied `Screen`; every one rejected pre-mutation, none leaked into the original. Confirmed the `New()` TOCTOU window doesn't exist (write-once `self`, single call site, grep-confirmed, 50-goroutine concurrent-construction stress clean under `-race`) and confirmed the double pre/post-lock check is genuine defense-in-depth, not masking a real gap (could not construct a scenario where post-lock catches something pre-lock misses). Also independently re-verified astgate's re-triage of this package: exactly 6 remaining findings (all unexported helpers), each with real per-finding reachability justification, zero call sites outside the now-guarded `ApplyDelta` switch. `go test ./internal/ui/screens/demo/... -race` and `go test ./internal/foundation/astgate/... -race` both clean, `go build ./...` clean, `gofmt -l .` empty repo-wide. **Verdict: ACCEPT** (Copperhead, 2026-08-12 19:36:41). Commit-ready.

#### FEAT-011 — Save/load UX: REJECT then ACCEPT on a GR#7 unwrapped-error gap, plus a hygiene fix with its own follow-up
Round 1 Destructive ("Vex") found a real GR#7 violation: `Manager.Load` returned a bare unwrapped error for every corruption shape except a `FormatVersion` mismatch (SHA256 mismatch, size mismatch, missing header, shard-is-a-directory, bogus header fields all fell through to the same unwrapped fallback) — AC-10's skip-and-report *behaviour* still worked, but the reason wasn't registry-sourced. **REJECT**, atomic promotion and adversarial corruption handling otherwise held up clean. Also flagged, non-blocking: orphaned `.staging` dirs from a killed process are never cleaned up. Both were fixed in the same pass rather than deferred: a new registry code, **MET-E814**, wraps the corrupted-load bare error across all 5 corruption shapes plus `LoadLatest.SkipInfo.Reason`; and a new **`CleanupStaleStaging(root, olderThan, now)`** function (clock passed as a parameter, respecting the package's no-wall-clock constraint per AC-15) addresses the hygiene gap. Round 2's independent reattack (attacker "Sable") verified the MET-E814 wrapping with 5 *new* corruption scenarios (UTF-8-BOM header, same-length-different-content tamper, extra unexpected files, same-length-content SHA256 probe, a Windows-inconclusive chmod-0000 test) and confirmed diagnostic detail survives the wrap (`errors.Unwrap`, `e.Ctx[cause]`, `e.Error()` all still carry the original specific detail). Attacked `CleanupStaleStaging` specifically: correctly handles missing/file-not-dir edge cases, but is **dead code** — zero callers anywhere in the repo (the whole `save` package has no importers yet) — and Sable *proved* a real advisory-severity race: a live staging dir's mtime is set once at creation and never re-touched during a long write, so a slow save (this project's stated premise is up to 100M citizens) racing an aggressive cleanup threshold will sweep an in-flight staging dir mid-write. Severity is contained — the failure surfaces as a clean, already-wrapped `ErrParticipantWriteFailed`, nothing corrupts or half-promotes — and there's no live call site today, so it did not block the ACCEPT. Filed as **BUG-129 (P3, `feat.saveux`)**, a pre-wiring note: choose `olderThan` conservatively or synchronize against `Manager.mu` before this is ever wired to a real scheduler. **Verdict: ACCEPT** (Sable, 2026-08-12 19:43:30). Commit-ready. (FEAT-011 unblocked FEAT-064's AC-1/AC-2/AC-13 dependency the moment it ACCEPTed — see the crank-up section below.)

#### FEAT-063 — The Helper: 1 Destructive round, ACCEPT, with a liveness follow-up
Round 1 Destructive (attacker "Vex") read the package in full plus wrote adversarial scratch tests (deleted before finishing, tree confirmed clean). Confirmed no TOCTOU on the `AC-3` registry seal, `-race` clean across all 22 shipped tests plus a heavier own-authored concurrent load, no panics in the package's own code under zero-value/nil/20000-candidate stress, and `AC-8`'s read-only `Preview` guarantee (a counter-incrementing `ProjectConsequence` proved exactly one invocation, no mutation). Two informational findings logged as follow-ups rather than blockers: `MET-E705` (`ErrMalformedStateView`) is dead code — its only constructor site is never called anywhere in the package, fixtures, or tests — and a genuine **liveness gap**, not covered by any AC: `Recommend` holds its `RLock` across the *entire* candidate loop including calls into registrant code, so a hanging/slow `ProjectConsequence` combined with any concurrent `Register` call wedges the whole registry for every `Preview`/`Recommend`/`Register` caller (Go's `RWMutex` blocks new readers once a writer is queued — confirmed empirically). Neither `Preview` nor `Recommend` accept a context/timeout, so a caller has no way to bound this; no real (non-fixture) registrant exists yet to trigger it, so not reject-worthy for v1. Filed as **BUG-128 (P3, `engine.helper`)** — track before any real `ProjectConsequence` with unbounded work registers, especially once the Helper becomes UI-facing (a hung consequence-projector would freeze the whole subsystem, not just one recommendation). **Verdict: ACCEPT** (Vex, 2026-08-12 19:11:43). Commit-ready.

#### FEAT-066 — Metrics dashboard: built, bounced by Tester for two misattribution gaps, fixed, first Destructive round now in flight
Built this session: `internal/harness/metricsdash/` (doc.go/errors.go/source.go/weakness.go/lint.go/gatestatus.go/perf.go/feedback.go/report.go + tests) plus `cmd/metricsdash/main.go`. AC-1–AC-6 (dashboard sourced via `node claude-bow.js weakness/lint/gate-status` plus direct calls into `synth.LoadLatestBaseline`/`LoadAcceptedRegistry`/`CompareToBaseline`/`PerfRecord`), AC-7/AC-9 (one-command `LogNote` logging), AC-10/AC-11 (GR#7 registry errors MET-H400..H405), AC-13 (mechanically-checked absence of any `internal/engine/core`/`internal/protocol` import) all built and green (`go build ./...`, `go test -race ./internal/harness/metricsdash/...`). AC-8 reuses FEAT-065's shipped feedback-inbox/`claude-devfeedback-import.js` mechanism verbatim rather than a second writer — but the Tester caught that importer hardcoding attribution: **ASM-477** (`claude-devfeedback-import.js` hardcodes `--codejson feat.devmode`/`DEFAULT_CODE_PATH` for *every* imported record regardless of the submitting feature, so a metricsdash-originated note filed correctly as a real BOW item but tagged as if it came from `feat.devmode`) and **BUG-126** (the same file separately hardcodes `add bug` regardless of the submitting record's `-kind`, so `LogNote`'s `NoteBug`/`NoteFinding`/`NoteAssumption` selector never actually selects — every note becomes a bug-type BOW item; no existing test asserted the real `item_type`, so this passed a false-pass check). Bill ruled FEAT-066 is not done-eligible until the importer derives attribution from the submitting tool's declared mkey rather than hardcoding `feat.devmode` (GR#3 — parametrise, don't fork). **That fix landed and passed 422/422 before the ten-lane crank-up directive even went out** (Bob's 19:50:38 note) — `claude-devfeedback-import.js` now reads a per-record source-mkey field for both `--codejson`/`--code-path` attribution and BOW item type. **Both ASM-477 and BUG-126 remain `status: open` in the BOW as of this entry** — the code fix and Tester pass have landed, but neither item has been flipped to `done` yet (that's the lead's sweep, not automatic). FEAT-066 itself had **never had a Destructive round** despite the fix landing — caught as a gap during the crank-up backfill and dispatched as round 1 at 19:51:14. **No verdict on record yet for FEAT-066 — treat as in flight, check `node claude-bow.js verdict FEAT-066` for current state.**

#### BUG-123 (P0) — six rounds and counting, now under a GR#3 lead mandate
Continuing from the six-round-of-guard-bypass thread already summarized earlier in this entry: round 4 (attacker "Marrow" reattacking) landed a proper character-by-character `consumeShellToken` scanner replacing the earlier hand-rolled regex approach, fixed a genuine `quote-mask-drift.test.js` test-isolation race along the way, 469/469 suite. Round 5 found a **new** bypass in that same scanner: an *odd* count of backslash-escaped quotes inside a `-c` value (`git -c key="a\"b" commit`) mispairs and falls through, silently skipping the scan again — round 4's own added tests only exercised even counts, coincidentally. **REJECT.** Marrow's fix direction: this codebase already has an escape-aware, BUG-077-hardened quote scanner (`buildQuoteMask` in `claude-author-guard.js`) — stop maintaining a third parallel quote implementation, delegate to it. **Bill escalated round 6 to a GR#3 ruling (19:45:48), not a routine bounce**: rounds 3, 4, and 5 each *modeled* the hardened scanner instead of *reusing* it, and each hand-rolled copy shipped the exact gap its parent had already fixed. Round 6 is required to (1) export `buildQuoteMask` from a single shared module, (2) make `claude-git-commit-trigger.js` (and every other current owner, including `claude-pre-commit-check.js`) `require()` it — zero parallel quote-scanning implementations may remain anywhere in the guard estate, grep-verifiable — and (3) port the odd-escaped-quote attack cases into the shared scanner's own test file so every consumer inherits them for free. This directive was relayed verbatim to the in-flight round-6 agent with the requirement to report back with grep proof against requirement 2. **Round 6 was still running as of this entry — no verdict on record yet.** Check `node claude-bow.js verdict BUG-123` for current state before assuming it's resolved.

#### BUG-124 (P1) — ACCEPTed
`claude-startup.js`'s `CLAUDE_IDENTITY`-to-`execSync`-template-string command injection (documented earlier in this entry) was fixed by converting all three `tryCheckin()` call sites to `execFileSync('node', [...argv], {...})` — argv array, no shell, matching the pattern already used correctly elsewhere in this codebase (`claude-bow.js`'s `printGitCheck`, `claude-pre-push-check.js`'s `git()` helper). Round 1 Destructive (attacker "Nyx") went well beyond the fixer's own test: 17 injection payloads thrown directly at `execFileSync` (pipes, semicolons, backticks, `$()`, embedded newlines, Windows `%VAR%`/`%COMSPEC%` expansion, caret escapes, a 20000-char string, homoglyphs, double-quote and the classic Windows quote-escape breakout pattern, an embedded NUL byte) — every payload arrived at the child process's argv verbatim, none caused shell interpretation. Also researched CVE-2024-27980 ("BatBadBut", Node's real Windows `child_process` CVE) and confirmed it's structurally inapplicable here: the CVE requires the spawned target to resolve through `cmd.exe` (batch files / extension-less files), but `claude-startup.js` spawns `node` (`node.exe`, a real PE binary), never a batch file. Independently re-verified the legitimate-identity path end-to-end against the real `claude-sync.js` with a fresh fake window ID, confirmed clean checkin/checkout. Full suite: 449 tests, 448 pass (1 unrelated pre-existing drift-alarm failure, confirmed genuinely unrelated to this fix by inspection). **Verdict: ACCEPT** (Nyx, 2026-08-12 19:12:31). Commit-ready.

#### Registry corrections — ui.screen.demo's missing edges, and FEAT-011's "phantom registration" cleared as a non-issue
Two registry-drift instances discovered the same session, folded into one coherent registry-correction pass per Bill's ruling rather than landed as two piecemeal edits:
- **ASM-479** — FEAT-018's builder found `ui.screen.demo`'s `code.json` outbound edges only listed `engine.citizens`/`households`/`leisure`/`extcommute`, missing `ui.widgets`/`int.protocol`/`ui.core` that sibling `ui.screen.map` has registered. Fixed the correct way — through `docs/planning/master-plan-v2.1.json` → `tools/plan/generate.js` → regenerated `code.json`, never a hand edit (which `claude-plan-guard.js` blocks anyway). Verified: `ui.screen.demo` now carries 7 outbound edges, GUIDs stable, regeneration diff isolated to the intended addition (demo's own `outbound.calls` plus the 3 targets' `inbound.consumers` lists), every other module byte-identical, `go build`/`go vet` clean. **Status: `done`, closed 2026-08-12 19:23:40.**
- **FEAT-011's "phantom registration"** (Bill's framing, ASM-469/470 context: `code.json` registered `feat.saveux` while `internal/engine/save/` had zero code) was investigated and found to be a **non-issue**: the package is now fully built, its `doc.go` carries the correct GR#6 GUID header, the sole registered outbound edge (`int.serializer`) is accurate and grep-confirmed, and the apparent gap was purely *temporal* — the registration existed ahead of the code and resolved itself once the code was built faithfully to the registered path/GUID/edge. No master-plan or code.json edit was needed for `feat.saveux` itself.

#### The ten-lane crank-up — what's confirmed done vs. still in flight
Bill's 19:48:13 directive pushed to nine concurrent lanes (a tenth, this docs lane, makes ten) with a standing "never hold, backfill from the ready queue yourself" instruction. Status of each, re-verified live for this entry:
- **FEAT-064 (Checkpoint saves)** — **declined to build, twice**, correctly. FEAT-011's blocker resolved the moment FEAT-011 ACCEPTed (real `Manager`/`Participant` surface now exists), but `feat.checkpoint` itself remains **entirely unregistered** in `code.json` (`ASM-439`) — writing `internal/engine/checkpoint/*.go` now would mean inventing a source-header GUID and code.json edge that don't exist, a GR#6/GR#20 violation. Separately, `ASM-470` confirmed `harness.replay.Recorder` is **not** durable across a long session (buffers in memory, loses everything on crash, explicitly designed for short supervised sessions per its own doc.go) — this blocks AC-11/AC-12 regardless of registration. **Needs a Bill/Aaron `/register-guid` call (path `internal/engine/checkpoint/`, layer engine) plus a ruling on the Recorder gap before the next dispatch can build.**
- **FEAT-068 (Doom warnings)** — **declined to build, twice, same judgment as FEAT-064.** `feat.deathwarnings` has zero module entry in `code.json`, and even a registration alone wouldn't unblock a build today: its three engine-side dependencies (`engine.finance` MOD-022, `engine.spiral` MOD-030, `engine.projections` MOD-031) are registered in `code.json` but all still `status: open` with **no code on disk** (`internal/engine/finance`, `internal/engine/spiral`, `internal/engine/projections` all absent), and the UI consumer `ui.alerts`'s registered path (`internal/ui/screens/chrome/`) is also absent. **Needs `/register-guid` for `feat.deathwarnings` plus MOD-022/030/031 and `ui.alerts` built before this feature's dispatch makes sense.**
- **FEAT-069 (claude-sync unread-message delivery)** and **FEAT-070 (startup hook auto-arms `/loop`)** — dispatched as dev builds against `tool.syncmsg.md`/`tool.looparm.md`, both explicitly flagged for extra care since they touch the live `claude-sync.js` this very session depends on for coordination. **In flight; no BOW comment/verdict on either as of this entry — check current state before assuming either has landed.**
- **BUG-122 (`feat.devmode` composition-root wiring)** and **BUG-044 (P0, author-guard `-c` bypass, folding in SEC-052's hardened identity module)** — dispatched. **In flight; no BOW comment/verdict on either as of this entry.**
- **Tester sweep** of everything landed since the last baseline (FEAT-018 fix, FEAT-011 fix, BUG-124) — dispatched. **In flight; no reported result as of this entry.**
- **This docs lane** — build-log + checkpoint refresh, the entry you're reading.

### State snapshot at time of writing (superseding the earlier snapshot above)
**DONE / commit-ready-from-a-Destructive-standpoint:** BUG-119 (done, r10 ACCEPT), SEC-052 (r3 ACCEPT), FEAT-065 (r2 ACCEPT), SEC-044/045/046/047/048/051 (ACCEPT), BUG-125 (ACCEPT), FEAT-018 (r2 ACCEPT), FEAT-011 (r2 ACCEPT), FEAT-063 (r1 ACCEPT), BUG-124 (r1 ACCEPT), ASM-479 (done). **Nothing has landed on `main` yet** — this is still pre-lead-sweep state; that backlog is now substantially larger than the first snapshot in this entry recorded.
**Fix landed, verdict/BOW-status not yet recorded:** FEAT-066 (Destructive round 1 dispatched, no verdict yet), ASM-477/BUG-126 (fix landed + Tester-passed, BOW status still `open`).
**Still genuinely open / blocked:** BUG-123 (P0, round 6 in flight under a GR#3 mandate), FEAT-064 (blocked on `/register-guid` + a Recorder-durability ruling), FEAT-068 (blocked on `/register-guid` + three foundational modules + `ui.alerts` needing real code), FEAT-069, FEAT-070, BUG-122, BUG-044 (all in flight, no verdict/comment yet — check live BOW state), BUG-128 (P3, `engine.helper` liveness, advisory), BUG-129 (P3, `feat.saveux` staging-sweep race, advisory, dead code today).
**Recommended resume order:** (1) chase BUG-123 round 6's verdict — it's P0 and under a fresh GR3 mandate; (2) check FEAT-069/FEAT-070/BUG-122/BUG-044/FEAT-066/Tester-sweep for landed verdicts, since all five were still running when this entry was written; (3) lead-sweep-commit the now-large DONE/commit-ready list above; (4) `/register-guid` + rulings needed to unblock FEAT-064 and FEAT-068.

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

Three agents swept the codebase in parallel (foundation+protocol, engine, UI+cmd+tooling). **20 of 20 built modules scanned** (BUG-022, 2026-08-10: backfilled `tool.secretguard`, which had real adversarial attention — SEC-015, SEC-021 — but no ledger stamp, so it read as never-reviewed under the ledger's own absent-means-unscanned rule). Absent stamp = never scanned; unscanned must never be mistaken for clean.

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
4. **A value in a privileged position is input, however inert it looks.** The largest class (`input-validation` ×9) turned out *not* to be "the team forgets to validate" — every one of those packages validates its payload carefully. The dangerous value was almost always the **metadata**: a name that becomes a path segment, a wire-supplied size that becomes an allocation, a string that becomes terminal control bytes, a file path that becomes shell syntax, a remote name that becomes a security decision. **Rule:** ask what a value *becomes*, not where it came from; state the allowed domain positively (`ValidateShardName`'s "a single clean path component" is the model, "sanitised" is not); reject rather than repair; and bound anything attacker-influenced that sizes work or memory.

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

### 10. PAUSED — 2026-08-09, on Aaron's instruction

All agents stopped. State captured here so the next session resumes rather than reconstructs.

**Repository state at pause**
- HEAD `2487a0c`, pushed. Working tree **builds clean**, full suite green.
- One cleanup was needed: the stopped `InProcTransport` junior had added two imports and been stopped *before* writing the code that used them, leaving the build broken. Those two lines were removed by hand (a `git checkout --` revert was correctly refused as destructive). `transport.go` is byte-identical to its committed state; **no substantive work was lost** — the agent's last action was "now let's write the fix", so nothing had been written.

**Uncommitted, verified-but-unlanded — SEC-001 / SEC-013** (path traversal + Windows trailing-dot aliasing):
`internal/foundation/serialize/savebundle.go`, `savebundle_security_test.go` (new), `cmd/metctl/main.go`, `main_test.go`, `data/errors.json`.
This work is **complete and self-verified** by its junior (before/after attack demonstrated against a scratch pre-fix tree). It had FAILed once on v1.7 grounds, both gaps were closed, and it was mid-**re**-verification when paused. Tester-1's last observation before stopping: *"Confirmed pre-fix still leaks. Now confirm the current working tree (with SEC-013's added branch) still blocks it — full ordering re-check."* That is precisely where to pick up.

**Not started — SEC-020 wave 1** (`InProcTransport`). Brief is written and in the transcript; nothing on disk. This is the **highest-consequence remaining item**: a copy's independent `closeMu` reopens BUG-007's send-on-closed-channel panic, fixed earlier the same day.

**Resume order**
1. Finish Tester-1's SEC-001/013 re-verification → Destructive re-attack → commit. It is the oldest open P0 and the only verified work not yet landed.
2. SEC-020 wave 1: `InProcTransport`, then `StubEngine` + debug `State`, then the two Registries / `Logger` / `SeqTracker`, then the two UI screens. One agent per package; never two in the same package concurrently.
3. Then: the `input-validation` ×9 criteria rule (the last of the four pattern write-ups, still owed).

**Standing context a fresh session needs**: the dev-team process is at v1.8 (`docs/planning/dev-team-process.md`) — read the three weakness patterns and the assumption rules before dispatching anything. The BOW is the authority on item state (`node claude-bow.js list --by-seq`); this file is the authority on *why*.

### 13. The audit that ended the sweep — and the one line that protected all of it

Two things closed this arc, and neither was more security work.

**QA was asked the question the lead couldn't answer, and answered it.** Given an explicit invitation to say "this went too far", it did — with evidence rather than a feeling: **zero Sprint 2 movement across 38 commits** spanning two days; the pipelined N/N+1 cadence the process itself mandates fully *suspended* rather than slowed; 67 assumptions, 25 findings, four patterns and three process-version bumps produced by a project that had not yet built a second sprint of features; and **no written exit criterion anywhere** — no answer to "how much surface remains, and when does hand-sweeping stop?"

Its sharpest observation was a mirror:

> Weakness pattern #1 preaches *teach the class, don't audit every instance* — and it was never applied to the **audit itself**.

Five rounds of "guard this, then discover the structurally adjacent thing is unguarded" is the manual version of exactly what the pattern forbids. Accepted; the response was **BUG-024** — a mechanical, AST-derived, CI-enforced check for the copyable-mutex shape — and a declared stop to the hand-sweep.

It also caught the log rounding in its author's favour: "main was red for three commits" undercounts the **eight red runs** `gh run list` actually shows, because docs-only pushes inherit a red status. Corrected on the record. QA judged everything else it checked as plainly stated as the evidence supported, including the parts that reflect badly on the lead — but one flattering number is one too many in a document whose whole value is being trusted.

**SEC-028 — the highest-leverage finding of the session — came from auditing the lead's own work.** Destructive-3 was asked to audit the BUG-021 fix precisely because it had been dispatched, self-verified and committed **without a Tester or a Destructive pass**, while every other fix went through both. It confirmed the fix sound on all three counts, then found something bigger: **CI never ran `go test -race`. Anywhere.**

It demonstrated the consequence instead of asserting it: with SEC-003's original defect reintroduced, CI's exact command passed **10/10 on a fully broken engine**; the same command with `-race` failed 5/5. Every copy guard, the entire five-round chain, BUG-007's panic path — all protected by regression tests that only prove anything under a flag CI never passed.

One workflow line now guards all of it, confirmed green on the runner. Two days of hand-auditing were protected by a change that took minutes — which is QA's argument made concrete.

### 12. `main` went red for three commits — and the cause was the control I'd written that morning

After pushing the engine.core copy-safety chain I ran `gh run list`, saw the run **in progress**, and never went back. Two more commits landed on top before CI's failure was noticed. Both defects were test-only, and the determinism gate stayed green throughout — but `main` was red for three commits.

- `TestSEC003_ConcurrentRegisterDuringAdvanceTicks` was **scheduling-dependent**: it needed a registration to land *after* the seal, and on CI every one landed first. The invariant held perfectly; the test couldn't observe it. Same class as BUG-005 that morning.
- `staticcheck SA4006` flagged a dead store in a test — a rule `golangci-lint` enforces and `go vet` does not.

**This is BUG-006, the item I raised myself that morning**, whose interim control I wrote as *"after ANY push, run `gh run list` and eyeball it."* Watching a run **start** is not the control; confirming it **finished** is. A human-remembered check failed within hours of being written down — which is the argument *for* the branch-protection half Aaron approved, not against it. Logged as BUG-021, against myself.

**The systemic finding is the more useful one.** `golangci-lint run` was in nobody's habit. Testers ran `go build`, `go vet`, `gofmt`, `go test -race` — but CI runs golangci-lint as a **blocking** job with a stricter rule set, and nothing local matched it. So a lint error walked past a junior, a Tester, a Destructive agent *and* the lead, because everyone was running a different tool and calling it the same thing. It is now in the standard Tester baseline and every junior verify list.

Also folded into the process: **concurrency tests must be deterministic, not probable** — construct the state (drive the operation to completion, then assert) rather than racing for the timing, and delete an ordering-dependent assertion rather than padding it with retries. Twice in one day the same shape cost a red build.

### 11. New capability — Docker gives us a second GOOS (2026-08-09, post-pause)

Aaron advised that WSL and Docker are installed. Verified rather than assumed, and the real picture is narrower than the headline:

- **Docker works.** Desktop running, engine 29.6.2, **Linux** containers, x86_64. But the CLI is **not on PATH** — invoke by full path, `& "C:\Program Files\Docker\Docker\resources\bin\docker.exe"`.
- **WSL's only distro is `docker-desktop`**, Docker's internal utility VM. There is no Ubuntu or equivalent, so `wsl <cmd>` is *not* a route to arbitrary Linux tooling. Git Bash (the Bash tool) remains the POSIX shell; Docker is the route to genuine Linux.

**Why this is more than a convenience.** The most expensive lesson in this log is that *"it passes locally" was never evidence about CI* — BUG-004 made CI fail on **every run from the first commit** while `gofmt -l .` stayed clean on every developer machine, because Windows runners checked out CRLF files that `gofmt` rejects. The divergence between environments **was** the bug, so local checking could never have found it.

Docker removes that blind spot: `docker run --rm -v ${PWD}:/src -w /src golang:1.25 go test ./...` gives a **Linux** run before pushing. Directly relevant to ASM-037, which correctly scoped the stub/core drift test's guarantee to "whatever build config CI actually runs" — CI is `windows-latest` only today.

Recorded in `CLAUDE.md`'s Environment section, in Vestige, and in the session memory index.
