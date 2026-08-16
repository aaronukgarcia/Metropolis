BOW code: FEAT-130

# Acceptance criteria — tool.secretchecker (FEAT-130)

**BOW code:** FEAT-130 (P2) — "Secret/hardcoding checker backing tool.secretguard."
**Module key / GUID:** `tool.secretchecker` / `05345166-63fd-40bb-a3ac-b35bfaa38283`
**Spec refs:** GR#11 (Pre-Commit Security Review); GR#15 (Validators Derive From Data); GR#3 (Single Source of Truth); BUG-088 (the extraction this module came from — see the BUG-088 section of `tool.secretguard.md`, AC-D2); SEC-021 (the segment-aware high-entropy exemption and its disclosed residual); BUG-026 (Go package-identifier suppression); BUG-029 (camelCase-segmented identifier exemption, open second layer); BUG-148/BUG-150 (cross-literal reassembly and its narrowing); BUG-189 (camelCase "middle band" fix); ASM-386 (commit-msg verb-coverage gap for cherry-pick/revert/am).
**Date:** 2026-08-16
**Status:** **retrospective** — `claude-secret-checker.js` is already committed; this file documents its contract, not a build gate. A Tester/Destructive verifies fidelity rather than constructing new code. Framing logged as ASM-773.
**Package under test:** `claude-secret-checker.js` (repo root) — the Single Source of Truth for payload-inspection deciding whether staged content holds a secret or a GR#15 hardcoding smell. Allowlist: `claude-secret-guard.allow.json`. Tests: `claude-secret-checker.test.js`.
**Standard gates:** Node.js — `node --check claude-secret-checker.js`; `node --test claude-secret-checker.test.js`; stdlib-only (no new npm dependency); SG-6 (no Co-Authored-By).

## What this module is (read before the ACs)

BUG-088's finding was that `claude-secret-guard.js`'s *trigger* (a boundary-anchored regex over the raw command string) was defeatable, but its *payload* (`git diff --cached` read from real git state) was always sound. This module is that payload, extracted: same detectors, same entropy threshold, same allowlist file, same redaction — "relocated, not reimplemented" (AC-D2 of `tool.secretguard.md`'s BUG-088 section). It deliberately carries **none** of the sibling guards' boundary-regex/quote-mask/engage-decision trigger machinery (AC-B4). Its exported contract is a three-state `checkSecrets()` plus the pre-existing throwing `runScan()` kept for `claude-secret-guard.js`'s unchanged fail-closed catch block.

## Acceptance criteria

### Behaviour (the scan contract)

- **AC-1. `checkSecrets()` takes no arguments and returns a three-state result:** `{ status: 'clean' }`, `{ status: 'found-problems', findings: [{file, line, category, evidence, detail?}, ...] }`, or `{ status: 'internal-error', error: <Error> }` — the same discriminant AC-E1 of `tool.secretguard.md`'s BUG-088 section requires across all four checker modules. Check: the `checkSecrets()` clean / found-problems tests pass; a Tester confirms the three literal `status` values are the only ones produced.

- **AC-2. `runScan()` is retained with its pre-existing throw-on-internal-error contract** (it is what `claude-secret-guard.js`'s unchanged PreToolUse catch block relies on — AC-C1). `checkSecrets()` is the three-state wrapper around it and does not alter its behaviour. Check: the "runScan() still throws on internal error" test passes, and `claude-secret-guard.js`'s existing suite passes unmodified.

- **AC-3. The scan reads staged state only, via real git.** `runScan()` shells to `git diff --cached --name-only` and `git diff --cached -U0` (via `spawnSync`, `cwd: ROOT`, `maxBuffer` bounded), parses added lines, and never inspects the unstaged working-tree diff or untracked-file contents. Check: `grep -n "diff --cached" claude-secret-checker.js` matches both invocations; the throwaway-index fixtures in the test suite stage content and assert findings only for staged lines.

- **AC-4. The detector set is the relocated set from `claude-secret-guard.js`:** private-key blocks (`PRIVATE_KEY_RE`), API-key/token patterns (`API_KEY_PATTERNS`: aws-access-key-id, openai-style-secret, github-pat, slack-token, bearer-token, generic-key-assignment), connection-string passwords (`CONNECTION_STRING_RE`), high-entropy string literals (`looksHighEntropy`), GR#15 hardcoding smells (`HARDCODE_CMP_RE` + `HARDCODE_KEYWORDS`/`HARDCODE_EXEMPT_LITERALS`), and staged sensitive-extension files (`SENSITIVE_FILE_EXTENSIONS`, category `certificate-file`). Check: `grep -n` for each constant name finds its single definition in this file, and the fixture-parity test asserts a private-key + high-entropy fixture yields exactly `['high-entropy', 'private-key']` categories.

- **AC-5. Evidence is redacted, never echoed.** `redact()` masks a secret to at most 25% revealed (prefix/suffix split), and every finding's `evidence` field is the redacted form. Check: the `redact` helper is present; the fixture-parity test asserts categories/shape, and a Tester spot-checks that no test or scan path emits a raw candidate as `evidence`.

- **AC-6. The allowlist is the gate, not the detector.** `loadAllowlist()` reads `claude-secret-guard.allow.json` and strictly validates its shape — `allowedPaths` (array of `{path, reason}`, bare strings rejected, a `path` of exactly `**` or `*` rejected as over-broad) and `allowedPatterns` (array of `{id, type, value, reason}`, `type` ∈ `exact`|`regex`, `regex` values validated as compilable, `reason` mandatory). A malformed/missing allowlist **throws** (fail-closed). Check: the allowlist-validation tests and the scratch-checker-with-missing-allowlist tests pass.

- **AC-7. Allowlist matching is precise.** `allowedPatterns` of type `exact` match byte-for-byte; type `regex` are anchored whole-string (`^(?:value)$`) against the candidate, never a substring. `allowedPaths` use a minimal glob (`*` single-segment, `**` multi-segment) against forward-slash-normalised paths. Check: the `isAllowlistedValue`/`isAllowlistedPath`/`globMatch` exports are present; a Tester spot-checks the anchored-regex construction in `isAllowlistedValue`.

- **AC-8. The Micropounds allowlist entries are documented as-is.** `claude-secret-guard.allow.json` carries `balance-data-micropounds-field` (regex `[A-Za-z]+Micropounds`, matching GR#15 balance-number-regime field names like `landValueDragPerSeverityMicropounds`) and `logistics-field-holding-cost` (exact `holdingCostMicropoundsPerUnitPerTick`) — the committed sanction for Micropounds-suffixed identifiers, each with a non-empty `reason` and a DELETE-when-BUG-029-lands condition. Check: `grep -n "Micropounds" claude-secret-guard.allow.json` finds both entries; their `reason` fields are non-empty. Recorded as existing sanctioned entries, not re-validated here — ASM-774.

- **AC-9. The high-entropy classifier is the committed order-0 Shannon heuristic.** `looksHighEntropy(candidate)` requires length ≥ `ENTROPY_MIN_LENGTH` (20), is not word-segmented (`isWordSegmentedIdentifier`), matches the base64/hex shape (`TOKEN_SHAPE_RE`), and clears `ENTROPY_THRESHOLD` (3.7) by `shannonEntropy`. It is exempted structurally (not by threshold retune) for (a) `-`/`_`-segmented identifiers whose every segment is `[a-z0-9]` and which survive the per-segment (`SEGMENT_ENTROPY_MIN_LENGTH` 12) and near-uniform-reassembly (`SEGMENT_LENGTH_RANGE_TOLERANCE` 1) checks, and (b) camelCase-segmented identifiers via the stricter separate path (`CAMEL_REASSEMBLY_ENTROPY_THRESHOLD` 3.75). Check: the SEC-021/BUG-029/BUG-189 test sections pass — exemption fixtures return `false`, fabricated credential shapes return `true`.

- **AC-10. Same-line split secrets are reassembled within bounded windows (BUG-148/BUG-150).** `scanLine()` additionally concatenates 2- and 3-**adjacent** string literals' contents (source order, no separator) and re-runs both `API_KEY_PATTERNS` and `looksHighEntropy` — but only when the raw source between each consecutive pair matches the continuation-shape allowlist (`CONTINUATION_GAP_RE`: bare `+`, or a `; const/let/var name =` declaration). Check: the BUG-148/BUG-150 cases pass — the split AWS key and split 32-char secret are caught, while the unrelated-array/object-literal negative controls produce no findings.

- **AC-11. Go package identifiers are suppressed for the high-entropy check (BUG-026).** For `.go` files only, a high-entropy candidate that matches an identifier declared in the same Go package (`collectGoPackageIdentifiers`/`isGoPackageIdentifier`, comment/string-stripped) is skipped. Check: the `stripGoCommentsAndStrings`/`collectGoPackageIdentifiers`/`isGoPackageIdentifier` exports are present; a Tester confirms the `.go`-extension gate in the runScan filter.

- **AC-12. The module header documents its disclosed residuals and the ASM-386 gap.** It states, by name: the SEC-021 entropy-heuristic impossibility proofs (order-0 entropy cannot distinguish a multi-word identifier from a chunked secret; no fixed tolerance closes the class), the BUG-029 second-layer tracker, the BUG-148/BUG-150 cross-literal partial-mitigation scope, and ASM-386 (a commit-msg caller inherits the cherry-pick/revert/am non-firing gap). Check: reviewed by eye against the module header.

### Fail-open / fail-closed

- **AC-13. Internal error is its own state, never silently "clean" (fail-closed).** If `runScan()` throws — `git diff --cached` failure, missing/malformed allowlist, read error — `checkSecrets()` returns `{ status: 'internal-error', error }`, never `{ status: 'clean' }`. Check: the AC-F1 test (scratch checker with no allowlist) asserts `status === 'internal-error'` and `error instanceof Error`; the `runScan()`-throws test asserts the pre-existing throwing contract.

- **AC-14. This is the one check whose internal-error the caller must treat as fail-closed (deny).** Per `tool.secretguard.md`'s BUG-088 Section C table, a missed secret on a public repo is the worst outcome in the class, and the false-positive remedy is cheap and documented (the allowlist). This module itself encodes no deny logic — it returns the three-state and leaves the decision to the caller — but the AC documents that the caller's disposition of `internal-error` here is deny. Check: reviewed by eye against the module header and `tool.secretguard.md` Section C.

- **AC-15. A positive detection is never downgraded by an allowlist miss.** Findings pass through only after allowlist consultation (`isAllowlistedPath`/`isAllowlistedValue`/`isGoPackageIdentifier`); everything not so exempt is reported. Check: the SEC-021 end-to-end tests assert fabricated secrets are still flagged through `runScan()` while the allowlist-suppressed `hostile-sha256-bundle` case is the only suppression, proving the allowlist is the gate and not a blanket pass.

### Tests

- **AC-16. `claude-secret-checker.test.js` exists and passes**, and contains no trigger-machinery copy (AC-B4) and a header naming the ASM-386 gap (AC-B2). Check: `node --test claude-secret-checker.test.js` exits 0; the AC-B4/AC-B2 grep-style cases assert `buildQuoteMask|GIT_COMMIT_RE|isRealGitCommit` are absent from the source and `ASM-386`/`cherry-pick`/`revert`/`am` are present in the header.

- **AC-17. The suite proves "relocation, not reimplementation" via fixture parity** — the private-key + high-entropy fixture asserts the same categories/shape the original guard produced (AC-D2), not merely that *some* finding appeared. Check: the fixture-parity test asserts the exact `['high-entropy', 'private-key']` category list.

- **AC-18. The suite's internal-error tests never touch the real allowlist file.** The BUG-112 fix requires the missing-allowlist fixture to copy the checker into a scratch directory (so `ALLOWLIST_PATH` points at a path that never existed), not rename the shared `claude-secret-guard.allow.json`. Check: the test's `loadScratchCheckerMissingAllowlist()` helper and its comment are present; the real allowlist is asserted to exist and be untouched.

- **AC-19. The suite is stdlib-only and syntactically valid.** Check: `node --check claude-secret-checker.js` exits 0; `grep -n "require(" claude-secret-checker.js` lists only `fs`, `path`, `child_process` (plus the test's `crypto`/`os`) — no bare package name.

## Out of scope

- **A structurally different second detection layer** (wordlist, model-based) — tracked as BUG-029; the order-0 entropy residual is accepted and disclosed (SEC-021), not re-opened here — ASM-775.
- **Re-validating or re-scoping the Micropounds allowlist entries** — recorded as the committed, Aaron-approved sanction, not re-litigated — ASM-774.
- **Wiring this checker into a `commit-msg` dispatcher** — that is the follow-on integration `tool.secretguard.md`'s BUG-088 section (AC-B5) already reserves; this module only defines the call contract.
- **Closing ASM-386's cherry-pick/revert/am gap** — inherited and stated plainly, not re-solved.

## Assumptions logged

- **ASM-773** — retrospective framing: these ACs document the committed contract, and the tests ACs assert the existing suite already covers the named behaviours.
- **ASM-774** — the Micropounds allowlist entries are documented as existing sanctioned GR#15 entries (Aaron-approved 2026-08-15, DELETE-when-BUG-029-lands), not re-validated here.
- **ASM-775** — the high-entropy classifier's disclosed residuals (SEC-021 impossibility proofs, BUG-029 second layer) are documented as accepted limitations, not re-opened as a further Destructive round.

## Escalations

- None. Documentation-only pass over committed, tested code. One judgment call flagged for Bill's awareness rather than as a conflict: AC-14 documents the *caller-side* fail-closed disposition of `internal-error` (from `tool.secretguard.md`'s BUG-088 Section C) rather than asserting the module encodes a deny itself — the module's contract is the three-state return, and conflating the two would misrepresent where the decision lives.
