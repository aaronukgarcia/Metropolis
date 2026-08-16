BOW code: FEAT-080

# Acceptance criteria — tool.worktreeguard (FEAT-080, archaeology)

**Module key:** tool.worktreeguard
**BOW code:** FEAT-080 (GUID `d9ecb82b-ffe9-468d-a837-01a20610a365`, created
2026-08-13). Git refs: `bcdc20299b`, `e1e4e5d5c3`. Bill's comment on the item
(2026-08-13) records the commit range (43bc119), the 22 unit/spawn tests, and
the standing order: **do NOT mark done until the Destructive round runs.**
**Spec refs:** GR#24 (No Code Left Behind, `CLAUDE.md` — the three mechanical
duties, especially (a) "Never destroy the working tree"); M0-ENG §5 (hooks);
the incident evidence in the guard's own header (the 2026-08-13
`git checkout --` loss of 211 lines of un-staged FEAT-077 work).
**Date:** 2026-08-16 (written after the fact — archaeology; the code is live
and tested, this file documents its contract so the pending Destructive round
has something to attack against).
**Status:** active — FEAT-080 is `open`, explicitly "PENDING its own
Destructive round" per Bill's comment.
**Package under test:** `claude-worktree-guard.js` (repo root, Node.js). The
command-recognition machinery is REUSED from `claude-author-guard.js`
(`gatherScanTexts`, `parseGitInvocation`, `resolveAlias`) and
`claude-quote-mask.js` — this file adds only the verb set and per-verb
argument classification (GR#3).
**Standard gates:** Node, not Go — SG-1/SG-2/SG-4/SG-7 do not apply. This
item's own gates: `node --check claude-worktree-guard.js`;
`node --test claude-worktree-guard.test.js`; SG-6 (no Co-Authored-By).

## Why this file exists — the evidence, not the principle

On 2026-08-13 a Destructive agent, part-way through a "prove the test can
fail" mutation cycle, ran `git checkout -- claude-destructive-guard.js` to
"restore" the file. `git checkout --` reverts to HEAD, not to the uncommitted
pre-mutation state — it silently destroyed 211 lines of un-staged FEAT-077
work that had never been committed and was therefore unrecoverable from any
git object. `dev-team-process` v1.5.1 already BANNED this class of command for
everyone but the lead, but the ban lived in prose subagents did not reliably
read. This hook turns "remember not to" into "the tool refuses" — the same
move `claude-codename-guard.js` made for GR#22 and `claude-destructive-guard.js`
made for GR#23.

## What this guard actually guarantees (read from the code, not assumed)

### A. What it denies (the destructive-verb set)

- **AC-1 (checkout).** `git checkout -- <path>`, `git checkout .`, and
  `git checkout -f ...` are DENIED; `git checkout <branch>` (a bareword that is
  not path-shaped) and `git checkout -b/-B/--orphan ...` (branch create/switch,
  which never discard working-tree content) are ALLOWED. The classifier
  (`isDestructiveInvocation('checkout', tokens)`) returns true on `--`, on
  `-f`/`--force`, or on any non-flag token that `looksLikePath` reads as a
  pathspec; `-b`/`-B`/`--orphan` short-circuit to false first. Check: unit
  tests assert `['--','claude-destructive-guard.js']` true (the exact loss
  command), `['.']` true, `['-f','main']` true, `['internal/engine/foo.go']`
  and `['README.md']` true, while `['main']`, `['develop']` and `['-b','feature/x']`
  are false.

- **AC-2 (restore).** `git restore <path>` (and `--worktree`/`-W`) is DENIED;
  `git restore --staged <path>` / `-S` (unstage-only) is ALLOWED — unless
  `--worktree`/`-W` is also present, in which case it is denied again (the
  combined form restores the working tree too). Check: unit tests assert
  `['foo.go']` true, `['--staged','foo.go']` false, `['--staged','--worktree','foo.go']`
  true.

- **AC-3 (reset).** `git reset --hard`, `--keep`, and `--merge` are DENIED;
  `git reset` (default/mixed), `--soft`, and `--mixed` are ALLOWED (they move
  HEAD/index but leave the working tree untouched). Check: unit tests assert
  `['--hard','HEAD~1']`, `['--keep','HEAD']` true and `['--soft','HEAD~1']`,
  `['HEAD']`, `['--mixed','HEAD']` false.

- **AC-4 (clean).** Any real `git clean` is DENIED — `-f`, `--force`, or any
  combined short flag containing `f` (`-fd`, `-xdf`); `git clean -n` /
  `--dry-run` is ALLOWED (shows, deletes nothing). Check: unit tests assert
  `['-f']`, `['-fd']`, `['-xdf']`, `['--force']` true and `['-n']`,
  `['--dry-run']` false.

- **AC-5 (stash).** `git stash` (bare == push), `stash push`, and `stash save`
  are DENIED (they sweep the whole working tree into a stash — in a shared
  multi-agent tree this takes every other session's uncommitted work);
  `stash list` / `show` / `pop` / `apply` are ALLOWED (read or restore, not
  discard). Check: unit tests assert `['']` true (bare), `['push']` true,
  `['save "wip"]` true, and `['list']`, `['show']`, `['pop']` false.

- **AC-6 (everything else allows).** Every non-git command and every
  non-destructive git subcommand (`commit`, `status`, `branch`, `log`, ...) is
  ALLOWED silently (exit 0, empty stdout). Check: spawn tests assert
  `git commit -m "x"` and `rm -rf /tmp/scratch` produce empty stdout.

### B. Path-vs-ref classification (the false-positive balance)

- **AC-7 (looksLikePath).** A bare token is read as a pathspec iff it is `.` or
  `*`, contains `/` or `\`, or ends in a listed extension (`PATHY_EXTENSIONS`:
  go, js, ts, md, json, txt, yml, yaml, sh, ps1, bat, mod, sum, lock, toml,
  css, html, py, rs). A token starting with `-` is never a path. A dotted
  tag/branch (`v1.2`, `v1.2.3`, `release-2024.08`) is NOT a path — deliberately
  NOT "any dotted token", so a legit safe tag/branch switch is not blocked.
  Check: `looksLikePath('.')`, `'a/b.go'`, `'foo.md'` are true and `'main'` is
  false; `isDestructiveInvocation('checkout', ['v1.2.3'])` is false while
  `['main.go']` and `['config.yaml']` are true (the tag/version false-positive
  fix test).

- **AC-8 (known open gap — false negative on a bareword filename).** A modified
  FILE whose name has no slash and no listed extension (e.g. `git checkout myfile`)
  is misclassified as a ref and ALLOWED — a false negative (missed). This is
  exactly the residual Bill named in FEAT-080's comment ("a modified file named
  exactly like a branch (no slash, no known extension)"). Non-catastrophic by
  design: the guard errs toward allowing because a false BLOCK is the more
  damaging failure, and a miss is "no worse than the zero protection that
  existed before this file". Written as a known gap, not a satisfied contract.

### C. Posture — fail-open, and the GR#24 wording reconciliation

- **AC-9 (fail-open on internal error, stated by name).** The guard is a SAFETY
  NET against an accidental keystroke, not a security boundary — nobody is
  adversarially trying to lose their own code. Any internal error (unparsable
  stdin JSON, a throw from `gatherScanTexts`, or any unexpected exception)
  ALLOWS and says so on stderr; the guard's own bug must never stop the team
  working. Check: source has a top-level `try/catch` around `main()` writing
  `worktree-guard: internal error, allowing — ...` then exit 0; the
  `gatherScanTexts` call is wrapped in a `try` that writes
  `could not scan command, allowing — ...` then allows; the stdin parse
  `catch` allows.

- **AC-10 (posture reconciliation — GR#24 "fail-closed" vs header "fail-open").**
  `CLAUDE.md`'s GR#24 row says `claude-worktree-guard.js` "blocks them
  fail-closed", while the guard's own header says "FAIL-OPEN, like
  claude-dispatch-guard.js and UNLIKE the security guards". Reconciled: the
  guard HARD-DENIES every recognised destructive invocation (that is the
  "fail-closed" block of the destructive action), but ALLOWS on its own parse
  or internal errors (that is the fail-open safety-net posture on itself). The
  two statements describe different failure axes, not a contradiction. ASM-753.

- **AC-11 (escape hatch, operator-only).** `CLAUDE_ALLOW_WORKTREE_RESET=1` is
  read from `process.env` in the guard's own process, never from inline
  `VAR=value` text inside the command being judged — a `PreToolUse` hook
  evaluates before the proposed command's own child shell exists, so an inline
  setting can never reach the hook judging it. Set it in the environment that
  LAUNCHES the session, before it starts. Check: the spawn test sets the var in
  the spawn env and asserts `git reset --hard` is allowed; the header states the
  inline-invisibility reasoning in the same terms as the sibling guards.

### D. Bypass-family coverage

- **AC-12 (reused recogniser — GR#3).** The command-recognition machinery is
  REUSED from `claude-author-guard.js` — `gatherScanTexts` (wrapper/heredoc/
  chain unwrapping, line-continuation normalisation, recursion to
  `MAX_WRAPPER_DEPTH`), `parseGitInvocation` (the `-c key=val` / `-C dir` /
  `--git-dir` / `--work-tree` / `--namespace` / benign-option run), and
  `resolveAlias` (git-alias resolution, bounded depth, cycle-protected). This
  file adds only `DESTRUCTIVE_VERBS` and the per-verb argument classifier —
  the recogniser is "the one Destructive-hardened git recogniser in this repo",
  never re-hand-rolled here. Check: source reading shows
  `require('./claude-author-guard.js')` and calls to `ag.gatherScanTexts`,
  `ag.parseGitInvocation`, `ag.resolveAlias`.

- **AC-13 (git.exe / git.cmd / quoted path — COVERED).** The guard's own
  git-token finder (`claude-worktree-guard.js:233`) matches `git`, `git.exe`,
  `git.cmd`, and quoted or unquoted paths to git (`"C:\Program Files\Git\bin\git.exe"`,
  `/usr/bin/git`) — a real Windows invocation is recognised. Check: source
  reading shows `git(?:\.exe|\.cmd)?` and the quoted-path alternatives in the
  finder regex.

- **AC-14 (`-c` global-option run — COVERED).** `git -c x=y checkout -- foo.go`
  still fires — the `-c` run between `git` and the verb does not hide the verb,
  because `parseGitInvocation` consumes the option run before reading the verb
  word (and its `-c` values are quote-aware via `consumeShellToken`). Check: the
  spawn test asserts `git -c advice.detachedHead=false checkout -- foo.go` is
  denied.

- **AC-15 (wrapper / chain — COVERED).** A destructive git command hidden after
  another command in a chain (`git status && git checkout -- foo.go`) still
  fires — `gatherScanTexts` returns the whole (normalised) text and the finder
  visits every `git` token left-to-right. Check: the spawn test asserts
  `git status && git checkout -- foo.go` is denied.

- **AC-16 (git aliases — COVERED via the shared recogniser, not this file's own
  tests).** `resolveAlias` resolves a non-known verb to a destructive verb via
  `git config --get alias.<word>` (bounded depth, cycle-protected, and honouring
  an inline `-c alias.<word>=...` override per BUG-231), so `git co -- x` where
  `co` aliases `checkout` is classified as destructive. Coverage note: this is
  exercised by `claude-author-guard.test.js`, not re-asserted in this guard's
  own test file. ASM-751.

- **AC-17 (known gap — prose false-positive, a divergence from author-guard).**
  Unlike author-guard's `scanGitInvocations`/`isProseGitToken`, this guard does
  NOT apply a quote-mask prose check — its finder + `parseGitInvocation` run
  directly, so a quoted prose MENTION of a destructive verb (e.g. inside a
  `git commit -m "..."` message such as "do not git checkout -- foo.go") can be
  misread as a real invocation and false-DENY. This is the "false BLOCK of a
  legitimate command" the header says is the more damaging failure; it is a
  real, documented divergence, not yet closed. ASM-750.

### E. Registry errors

- **AC-18 (no registry-sourced errors).** The guard emits NO registry-sourced
  `MET-xxx` error. Its entire error surface is a free-text
  `permissionDecisionReason` string (the deny message) and stderr notes, none of
  which carry a `data/errors.json` code. GR#7's error registry is Go-engine-
  scoped; these Node root-tooling hooks sit outside it. Check:
  `grep -n "MET-\|errors.json\|registry" claude-worktree-guard.js` finds no
  match (only the "internal error, allowing" stderr text). ASM-752.

### F. Test coverage

- **AC-19.** `claude-worktree-guard.test.js` covers unit classification
  (checkout loss command / `.` / `-f` / path-shaped bareword / branch-safe /
  `-b` safe; restore / `--staged` / `--staged --worktree`; reset hard/keep/merge
  vs soft/mixed/default; clean `-f`/`-fd`/`-xdf`/`--force` vs `-n`/`--dry-run`;
  stash bare/push/save vs list/show/pop; `looksLikePath` basics; tag/version-ref
  false-positive fix) and spawn end-to-end through the real hook (the loss
  command denied, `reset --hard` denied, `clean -fd` denied, `stash` denied,
  `&&` chain denied, `-c x=y checkout --` denied, branch switch allowed, commit
  allowed, operator override allows `reset --hard`, non-git allowed).

- **AC-20 (coverage gap).** The guard's own test file does NOT assert the
  `.exe`/`.cmd` suffix, the quoted Windows git path, or alias resolution —
  those shapes are covered only indirectly by the shared author-guard recogniser
  and its own suite. The prose false-positive (AC-17) is not asserted anywhere
  as a test. ASM-751.

### G. Determinism

- **AC-21.** The guard is deterministic given the same command and environment —
  no randomness, no wall-clock-dependent decision input. The only subprocess is
  alias resolution (`git config --get alias.<word>`), deterministic for a fixed
  config. Check: `grep -n "Date.now\|Math.random\|time.Now" claude-worktree-guard.js`
  finds no matches.

## Out of scope (stated, not silently absent)

- Preventing every conceivable way to destroy uncommitted work — the guard
  covers the specific git commands named in GR#24(a) and dev-team-process v1.5.1
  (`checkout --`/`.`/`-f`, `restore` non-`--staged`, `reset --hard`/`--keep`,
  `clean -f`/`-d`/`-x`, `stash` push/save). It is not a general shell parser and
  does not claim to be (same structural limit as author-guard's header).
- Detecting destructive invocations reachable only through unlisted wrapper
  shells (the `WRAPPER_PATTERNS` list in author-guard is the project's actual
  shell surface, not every shell that exists) — inherited from the shared
  recogniser, stated as a limit in author-guard's header.
- `git stash drop` / `branch -D` / `reflog`/`gc`/`prune`-class history
  destruction — not in `DESTRUCTIVE_VERBS`; this guard is scoped to
  working-tree-content discard, not ref/history mutation.

## Escalations

- **A. The prose false-positive (AC-17) is a real false-BLOCK, the failure
  direction the header says is the more damaging one.** It is not covered by
  any test. Recommend the Destructive round treat a `git commit -m "see git
  checkout -- x"` fixture as its first attack; if confirmed, the fix is to route
  this guard's scan through author-guard's `scanGitInvocations` (which already
  applies `isProseGitToken`), not to add a second quote-mask here. ASM-750.
- **B. FEAT-080 is `open`, pending its own Destructive round** (Bill's comment:
  "Do NOT mark done until the Destructive round runs."). This file is the
  contract that round attacks against; AC-8 names the already-known residual
  false-negative so the round has a stated position to argue against, not an
  implicit one.
