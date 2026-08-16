# Acceptance criteria — tool.authoridentity (FEAT-116)

**BOW code:** FEAT-116
**code.json:** `tool.authoridentity` (GUID `b853fd24-0f59-47c0-96c1-4acd87445d5c`)
**Status:** RETROSPECTIVE — code committed. This file documents the shipped contract of
`claude-author-identity.js`, the shared sanctioned-identity derivation (BUG-035 lineage)
extracted out of `claude-author-guard.js` by FEAT-045 so two consumers require the same
code verbatim. Written after the code, to pin the contract and give the Tester a fail-able
checklist against already-landed behaviour, not to commission new work.
**Spec refs:** BUG-035 (original fabricated-author incident); FEAT-045 (the demotion that
produced this shared module — its `tool.committhook.md` AC-4/AC-5 are the forward-looking
spec this module's header cites); GR#2 (Version/Identity Discipline); GR#15 (validators
derive from data — the ASM-226 derivation); ASM-226 (history-scan cap derived from the
repo's real commit count, not hardcoded); BUG-052 (bounded history scan); BUG-036 (why
config is trusted unconditionally); SEC-052 round 1 & 2 (config-scope poisoning); BUG-136
(`GIT_DIR` redirection); BUG-042 (identity rewrite rationale).
**Date:** 2026-08-16
**Package under test:** `claude-author-identity.js` (shared module), consumed by
`claude-author-guard.js` (advisory, fail-open) and `githooks/commit-msg` (enforcing,
fail-closed).

Every criterion below must be individually verifiable and must be able to FAIL
(process v1.9). "the module" = `claude-author-identity.js`; "the guard" =
`claude-author-guard.js`; "the hook" = `githooks/commit-msg`.

> Note on numbering: the module header's internal `AC-4`/`AC-5`/`AC-8`/`AC-16` references
> point at `tool.committhook.md` (FEAT-045). This file has its own AC numbering; the
> mapping is called out where the two cross-reference.

## A. The three trust sources, in order

- **AC-1. `deriveSanctioned()` unions exactly three sources, and the module header states
  the trust order.** (1) `configuredEmail()` — the currently configured git identity,
  `git config user.email` local then global, **trusted unconditionally**; (2)
  `historyEmails()` — emails seen at or above `HISTORY_THRESHOLD` (3) times as author OR
  committer over the most recent `deriveScanLimit()` commits of the trunk branch; (3)
  `extraIdentities()` — `CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES`, an operator-set env var,
  for a legitimate contributor with no history yet. Check: source review — the header
  enumerates the three sources in this order and the unconditional-trust rationale;
  unit tests assert each source contributes to the union (a configured identity, a
  qualifying history email, an extra identity).

- **AC-2. Why (1) is trusted unconditionally and why (2) alone would be wrong here is
  restated in the module header.** The specific claim (BUG-036): a frequency-only
  derivation from this repo's own *rewritten* history would have sanctioned the wrong
  address and bricked every legitimate commit, because `git-filter-repo` rewrites history
  but does not touch local config — the locally configured `git config user.email` is the
  one source that survives a history rewrite unchanged, which is why it is trusted
  unconditionally rather than merely weighted highest. Check: grep floor —
  `history alone would sanction the wrong address` (or equivalent) and `BUG-036` both
  present in `claude-author-identity.js`; prose completeness is a human review
  (tool.committhook.md AC-5).

- **AC-3. Matching is by email only, lowercased.** Every source is normalised to a
  lowercased email before entering the union; history counting folds case
  (`line.trim().toLowerCase()`); `extraIdentities()` parses `Name <email>` (or a bare
  email) and lowercases. Matching is deliberately email-only, not name-sensitive (real
  evidence: same person, different name casing across author/committer on this repo's own
  HEAD at design time). Check: source grep for `.toLowerCase()` at each normalisation
  point; unit tests assert the union holds lowercased forms.

## B. Source 1 — configured identity, hardened

- **AC-4. `configuredEmail()` reads local then global, never the unscoped or `--global`
  forms.** Local scope is read via `git config --local user.email` (reads `.git/config`
  directly, immune to `-c`/env-var overrides); on no-local-value it falls through to
  `globalConfigPaths()` candidates read via `git config --file <path> user.email` — NOT
  `git config --global`, which SEC-052 round 2 proved redirectable via
  `GIT_CONFIG_GLOBAL`/`HOME`/`USERPROFILE`. Returns `null` only when both scopes yield
  nothing. Check: source checks — no `git(['config','user.email'])` (unscoped) and no
  `git(['config','--global'`; `--local` and `--file` present; unit tests with throwaway
  repos (local-only and global-only/no-local fixtures).

- **AC-5. The child git env is stripped of repo-discovery redirectors, and the global home
  is resolved env-immune.** `git()` deletes `GIT_DIR`, `GIT_WORK_TREE` and `GIT_COMMON_DIR`
  from the child env (BUG-136 — so an attacker-fabricated repo cannot redirect the
  `--local` read); `globalConfigPaths()` resolves the home directory via
  `os.userInfo().homedir`, not `os.homedir()` (which reads `USERPROFILE`/`HOME` and is
  just as poisonable as `--global`). Check: source checks (`os.userInfo().homedir` present,
  no `= os.homedir()` call) + poisoned-env unit tests (`GIT_CONFIG_PARAMETERS`,
  `GIT_CONFIG_COUNT/KEY_n/VALUE_n`, `GIT_CONFIG_GLOBAL`, `HOME`/`USERPROFILE`, `GIT_DIR`)
  + the real-hook end-to-end rejections in the test file.

## C. Source 2 — history, and the ASM-226 derived cap

- **AC-6. `trunkBranch()` picks `main` if it exists locally, else `master`, else the
  current branch.** Returns `null` only on an unborn HEAD (no commits), which yields an
  empty history set rather than a crash. Check: unit tests against throwaway repos.

- **AC-7. The history scan is bounded by a cap DERIVED from the repo's real commit count
  (ASM-226, GR#15), not a hardcoded 2000.** `historyEmails()` runs
  `git log <branch> --max-count=<limit> --format=%ae%n%ce` where `limit` comes from
  `deriveScanLimit()` = `min(realCommitCount, ceiling)`; `ceiling` =
  `CLAUDE_AUTHOR_GUARD_HISTORY_LIMIT` (a positive integer) if set, else
  `THRESHOLDS.HISTORY_SCAN_LIMIT` (2000). A young repo scans exactly the commits that
  exist (never asks git for 2000 of 5); a large/old repo stays bounded. Check: source grep
  for `--max-count=${limit}` and `rev-list --count`; unit tests — the execFileSync spy
  test asserts the *actual argv* handed to git carries `--max-count=<N>`, and a small
  bound still corroborates a recent real identity (behavioural, not just source-grep).

- **AC-8. `deriveScanLimit()` fails open to the ceiling, never throws, never scans zero.**
  On a failed/zero/`non-integer` `git rev-list --count HEAD` it returns the ceiling
  unchanged — the module has no error surface by design (see AC-10), so a failed
  derivation degrades to the documented default, not to a registry error. Check: unit
  test/`source review` of the try/catch and the `<= 0` guard.

## D. No fallback list, no error surface

- **AC-9. There is NO embedded fallback sanctioned-identity list anywhere.** Neither the
  module nor either consumer substitutes a second, undeclared source of truth when the
  module is broken or derives nothing — `deriveSanctioned()` returning empty is the true
  answer, and each consumer decides what "no answer" means for its own posture.
  Check: tool.committhook.md AC-4's "lazy implementation" tests — a throwing module makes
  the guard exit 0 silently and the hook exit non-zero (deny), with no fallback-list allow.

- **AC-10. The module has no error surface; throws propagate to the consumer.**
  `deriveSanctioned()` deliberately does NOT catch `historyEmails()`'s throw — it lets it
  propagate so each consumer decides fail-open (guard) vs fail-closed (hook) at its own
  call site, rather than the shared module silently picking one for both. The single
  exception is `deriveScanLimit()`'s fail-open-to-ceiling (a resource bound, not an
  error). Check: source review + the `CLAUDE_AUTHOR_IDENTITY_FORCE_ERROR` test proving the
  throw reaches both consumers and each treats it per its own posture.

## E. Test-only escape hatches (shrink-only)

- **AC-11. The two escape hatches can only SHRINK the sanctioned set, never add or
  substitute.** `CLAUDE_AUTHOR_IDENTITY_FORCE_ERROR=1` makes `historyEmails()` throw
  (to test each consumer's fail-open/fail-closed path without breaking git);
  `CLAUDE_AUTHOR_IDENTITY_TEST_FORCE_NO_CONFIGURED_EMAIL=1` makes `configuredEmail()`
  return `null` (to test the zero-sanctioned-identities case, which SEC-052 round 2's fix
  otherwise made un-fakeable). Both are safe to leave live in production: there is no way
  for an attacker to leverage either to get a fabricated identity accepted — at worst a
  legitimate identity is wrongly flagged, the same fail-closed-safe direction as
  `FORCE_ERROR`. Check: source review of both guards + unit tests asserting each flag's
  effect and direction.

## F. Tests

- **AC-12. `claude-author-identity.test.js` passes under `node --test`.** Coverage: AC-4
  delegation (both consumers hold the same function references — `guard.historyEmails ===
  identity.historyEmails`, `hook.deriveSanctioned === identity.deriveSanctioned`, and a
  `THRESHOLDS.HISTORY_THRESHOLD` mutation observed identically through all three entry
  points); AC-5 header grep; derivation unit coverage (configured, unborn HEAD, extra
  identities); BUG-052/ASM-226 (source + execFileSync spy + behavioural bounded-scan);
  SEC-052 round 1 & 2 (source checks + poisoned-env + real-hook end-to-end rejections with
  legitimate controls); BUG-136 (`GIT_DIR` + control). Every regression test has been
  shown able to fail. All fixtures run against throwaway repos under the OS temp dir,
  never this repo. Check: run `node --test claude-author-identity.test.js`.

## Assumptions

Logged via `node claude-bow.js add assumption` (see BOW):

- **ASM-788** — SEC-052 round 2's immunity rests on `os.userInfo().homedir` returning the
  real logged-in user's home unchanged under poisoned `HOME`/`USERPROFILE`/
  `GIT_CONFIG_GLOBAL` (verified live on this machine's Windows/Node binding). A change in
  Node's native binding or the OS could re-open the global-scope spoof.
- **ASM-789** — BUG-136 strips only `GIT_DIR`/`GIT_WORK_TREE`/`GIT_COMMON_DIR`; other git
  env redirectors (e.g. `GIT_CEILING_DIRECTORIES`, `GIT_DISCOVERY_ACROSS_FILESYSTEM`,
  `GIT_OBJECT_DIRECTORY`) are not stripped and are assumed not to affect `--local` config
  reads.
- **ASM-790** — The two test-only escape hatches are left live in production on the
  assumption they are shrink-only; a future edit making either able to add an identity
  would turn a test aid into a fabricated-identity bypass.

## Out of scope

- The PreToolUse advisory guard's command-string parsing (`claude-author-guard.js`) and its
  unsound-by-construction shell-tokenizer machinery (ASM-350) — this item is only the
  shared *sanctioned-identity derivation*, which the guard and the hook require verbatim.
- The enforcing hook's identity check, `git var` resolution, and codename content scan
  (FEAT-045/FEAT-046) — see `tool.committhook.md` and `tool.codenamehook.md`.
- Any change to the derivation *logic* itself (thresholds, sources, email-only matching) —
  this file documents the moved logic as-is; changing what it decides needs a fresh BOW
  item.
