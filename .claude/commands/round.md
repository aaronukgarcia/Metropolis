# /round — dispatch an independent destructive round (GR#23)

Compose and dispatch an independent Destructive attacker for a build/fix, from an item code + location. The lead runs this; the attacker is NEVER the author (Independence Amendment, 2026-08-18).

## Inputs to resolve before composing (look up, never guess — dispatch-guard will bounce stale facts)
- `node claude-bow.js show <CODE>` — the item, its mkey, prior verdict history (r-number = latest REJECT round + 1).
- Location: a scratch worktree (`E:\git\metropolis-prN`), a lane worktree (warn the owner off the touched paths first), or a pushed commit (create a detached scratch worktree from it — never attack a moving tree).
- The authority: the acceptance file, ICD, or ruling the work was built to.

## Composing the brief: name the AXES, never the conclusions (2026-08-21)
State where to attack and what standard applies; do NOT state what the defect is. A brief that asserts the finding teaches the attacker what to confirm, and it is often wrong — two briefs that night carried premises the attackers had to correct (a `cherry-pick -n` staged-not-committed state described as a rebase problem; "invented" ctx strings that turned out to be a pre-existing house convention two call sites already used). Give the attacker the builder's CLAIMS explicitly, labelled as claims to verify rather than facts, and say plainly that disproving the brief is a valid outcome.

## The house checklist (every round carries ALL of these — each was paid for)
1. **Provenance**: `git status`/`diff --stat` vs the claimed manifest; anything outside scope is a finding. Compare against **origin/main only**, never worktree copies (three strikes of stale-ref false findings).
2. **Evidence protocol**: sample ≥3–4 claimed tests by scratch-copy mutation (`cp f f.bak; edit; test; mv f.bak f` — NEVER git-reverting commands): production mutated → RED → restored → GREEN. A test that cannot fail is a finding.
3. **Tripwire honesty, both directions**: declared-BLOCKED ACs need mechanical tripwires that genuinely fire (materialise the trigger, prove RED); documented-not-detectable claims must be TRUE (probe for a detection point — a fabricated limitation is a finding).
3b. **Attack the GATE, not only the code** (2026-08-21, the highest-value find of that night): when the work under attack ships a new check — a detector, a linter, a validator, a ratchet — ask what it structurally *cannot* see, and prove the answer. A placeholder-render test whose regex assumed identifier-shaped tokens was hiding seven live broken renders in another module, because `errs.renderTemplate` accepts any bytes between braces. Demonstrate the blind spot by constructing an input the gate misses, then contrast: same break, widened gate, RED. A green suite that cannot fail on a real defect is worse than no suite, because it is *evidence* to the next reader.
3c. **Reproduce the defect on unpatched `origin/main` FIRST, and do not inherit the ticket's severity.** If it will not reproduce, that is the finding. If it reproduces worse than filed, say so: SEC-071 was filed as slice aliasing and was in fact a live `-race` data race on the render path. Then re-run the ORIGINAL repro verbatim against the fix — never only the fix's own new tests.
4. **Engine-mirror** (UI work): screen-side validation vs the engine's actual rules — subset is honest, divergence is the F2-360 class.
5. **Domain attacks by surface**: concurrency (lock windows, interleavings, -race -count=3), money (conservation both directions, saturation, no float), determinism (map-range/time.Now greps, byte-identical reruns), wire (NaN/Inf, oversize, stale, schema), copy-guards incl. **accessor aliasing** (the F8 lesson).
6. **Registry**: every code used is registered (tool check + source scan; MET-F003 fallback = finding); astgate rerun with cache cleared — verify any rekey is a genuine line-shift of the same closure, never a smuggled acceptance.
7. **Full gates rerun independently** — never trust the builder's transcript: gofmt -l, go vet, go test -race -count=2+ on touched packages, FULL `go test ./...`, golangci-lint. Package-scoped green is the #58 lesson.
8. **Report only attacks actually run.** Claimed-but-not-run is an integrity violation worse than no round. All claims from the tree (`go test -list`), never from memory (the flipped-then-lost incident).

## Verdict mechanics
- Attack tests with sustained value stay in the tree as permanent regressions; scratch files are removed; final `git status` must match entry state + deliberate additions.
- Record: `node claude-bow.js destructive <CODE> --verdict accept|reject --attacker "<name>" --note "..."` — **in its OWN command** (the guard pre-scans compounds), attacker ≤~60 chars, note ≤~1000 chars (column limits).
- REJECT bounces to the SAME builder with the attacker's findings as the next round's acceptance bar (attack tests flip to `TestRegression_` with inverted assertions on fix).
- On ACCEPT: verdict → commit (exact paths, `[mkey]` tag) → PR → watch → rebase-merge → `ref` (+ `done` if GR#12 permits).
