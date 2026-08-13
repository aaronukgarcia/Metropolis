BOW code: BUG-090

# Acceptance criteria — safer free-text input mode for claude-bow.js (BUG-090, control 1 of 3)

**BOW code:** BUG-090 (P1, open) — "A Destructive agent executed live attack commands
against the real repo and then reset away a lead commit while cleaning up."
**Spec refs:** BUG-090's own "three missing controls" breakdown — **this file covers
control (1) only**: "nothing stops an agent's `--desc` text being executed by the shell
that submits it, which is a command-injection surface in our own tooling and the second
time this session that quoting inside a BOW field caused real trouble (see BUG-043)."
Controls (2) (no agent is told how to recover safely) and (3) (the reset-ban lives in a
process document, nothing enforces it) are explicitly OUT of scope here — see
Escalations. BUG-043 (the prior instance, a guard false-firing on quoted prose containing
a git-commit phrase — a detection-side quoting defect, not an execution-side one; related
family, different mechanism, no shared fix). `claude-scratch.js`'s FEAT-058 header (the
existing precedent for "name the banned/risky pattern and the sanctioned alternative in
the tool's own header comment, not only in a process document"). `dev-team-process.md`
"An acceptance criterion's CHECK must be able to fail" (v1.9).
**Date:** 2026-08-12
**Status:** active — normal pipeline order (criteria written before junior dispatch)
**Package under test:** `claude-bow.js` — the `add` command's `--desc` flag, and by the
same shape the other free-text VALUE_FLAGS most likely to carry copy-pasted attack
strings, git commands, or code snippets: `--note` (used by `comment`, `depend`, `ref`,
`done`, `destructive`, `gate`) and `--detail` (`gate`). `comment`'s `--example`/
`--example-file` pair already has a file-based mode — see AC-1's precedent — and is
**not** touched by this item except to confirm its existing shape is the model, not a
gap.

## Why this is upstream of claude-bow.js, and what claude-bow.js can still do about it

`claude-bow.js`'s own argv parsing (`VALUE_FLAGS`, `claude-bow.js:103-112`) is plain
`process.argv` consumption — Node never re-interprets the string it receives. The actual
vulnerability sits one layer up: an agent using the Bash tool constructs a shell command
line such as `node claude-bow.js add bug "title" --desc "...contains \`some command\`..."`,
and the OUTER shell (bash) performs command substitution on the backtick-wrapped content
**before** `node` ever sees the string — this is BUG-090's confirmed mechanism. No change
inside `claude-bow.js` can make bash stop interpreting backticks inside a double-quoted
argument; that is bash's own quoting grammar and is outside this tool's control.

What `claude-bow.js` CAN do — and what this file specifies — is stop **requiring** an
agent to put risky content inside a shell-quoted argument in the first place, by offering
a file-based input mode as an alternative. If the risky text never appears on the command
line, the outer shell never gets a chance to interpret it, regardless of what characters
it contains. This is not a claim that `claude-bow.js` can detect or prevent shell
interpretation after the fact (AC-6 below is deliberately advisory, not a hard gate, for
exactly this reason) — it is a claim that the tool can make the safe path the easy path.

## Existing precedent (read before dispatch)

`cmdComment` already supports `--example-file <path>` as an alternative to
`--example "<code>"` (`claude-bow.js:438-440`, `fs.readFileSync(flags['example-file'],
'utf8')`) for exactly this reason — example code is the free-text field most likely to
contain shell-special characters. **This item extends that existing, working pattern to
`--desc` (and `--note`/`--detail` — see AC-2) rather than inventing a new mechanism.** A
junior building this should port the `--example-file` shape, not design from scratch.

## Acceptance criteria

### A. The safer input mode itself

- **AC-1. `add` accepts `--desc-file <path>` as an alternative to `--desc "<text>"`,
  reading the description from the file's content instead of the command line.** Shape:
  identical to `cmdComment`'s existing `--example-file` (`fs.readFileSync(path, 'utf8')`,
  trimmed or not exactly as `--example-file` currently is — no behavioural drift between
  the two file-reading code paths beyond which field they populate). Check: a passing
  test constructs a description string containing a backtick, a `$(...)` sequence, and an
  embedded double quote — the three shapes BUG-090 and BUG-043 both involve — writes it
  verbatim to a temp file, invokes `claude-bow.js add bug "title" --desc-file <path>` as
  a real subprocess (not a function call into the module — the point is proving nothing
  in the process re-interprets the content, and a same-process unit test cannot show
  that), and asserts the item's stored `description` column is **byte-identical** to the
  file's content. **What a lazy implementation looks like:** reading the file but then
  passing its content through the same code path used for `--desc` string concatenation
  before the DB write (e.g. re-serializing into a shell-invoked helper) — passes a naive
  "file was read" check while still being capable of re-exposing the content to a shell
  somewhere downstream. This AC's byte-identity assertion rejects that: any transformation
  (trimming beyond what `--example-file` already does, escaping, re-quoting) shows up as
  a mismatch. **False-pass warning:** a test that only checks the stored description
  **contains** the risky substring, rather than equals the file content exactly, would
  pass an implementation that mangled whitespace or partially escaped the content —
  equality, not containment, is the binding check.
- **AC-2. The same `--<field>-file` shape is added for `--note`** (the flag shared by
  `comment`, `depend`, `undepend`, `ref`, `done`, `destructive`, `gate`) **and `--detail`**
  (`gate`) — the other free-text VALUE_FLAGS most likely to carry copy-pasted shell
  content, per BUG-090's own citation of BUG-043 as "the second time... quoting inside a
  BOW field caused real trouble." Check: for at least `comment --note-file` and
  `done --note-file` (the two highest-traffic call sites per `dev-team-process.md`'s
  cited pipeline — commenting and closing items), a passing test proves the same
  byte-identity property as AC-1. **What a lazy implementation looks like:** adding
  `--desc-file` only and treating `--note-file`/`--detail-file` as a follow-up — this AC
  exists because BUG-090's own two-incident citation (this bug plus BUG-043) both involve
  free text reaching a BOW field, not specifically `--desc`; leaving `--note` unfixed
  reopens the identical hole under a different flag name the very next time an agent
  writes a comment about an attack command instead of a description of one.
- **AC-3. Both `--desc` and `--desc-file` (and the `--note`/`--note-file`,
  `--detail`/`--detail-file` pairs) may not be supplied together for the same field on
  the same invocation.** If both are present, `claude-bow.js` exits non-zero with a clear
  message naming which pair conflicted, rather than silently preferring one (silent
  precedence is a "which one actually landed" bug waiting to happen, and BUG-090's whole
  point is that surprising execution paths are the danger). Check: a passing test invokes
  `add` with both `--desc "x"` and `--desc-file <path>` set and asserts a non-zero exit
  and no row written to `bow_items` (query the table directly, not just the exit code —
  matching `tool.committhook.md`'s AC-3 evidentiary standard: prove the outcome, not just
  the return value).

### B. Backward compatibility — this is additive, not a breaking change

- **AC-4. The existing `--desc "<text>"` / `--note "<text>"` / `--detail "<text>"`
  direct-argument modes are unchanged in behaviour** — same flag names, same VALUE_FLAGS
  membership, same DB write path when the `-file` variant is absent. Check: the existing
  `add`/`comment`/`done`/etc. test coverage that currently exercises `--desc "<text>"`
  continues to pass unmodified (no test in the existing suite needs to change to
  accommodate this item); a new passing test additionally asserts that omitting both
  `--desc` and `--desc-file` on `add` produces the same "no description" behaviour as
  today (NULL/empty `description` column, whichever is current). **What a lazy
  implementation looks like:** refactoring `--desc` to be implemented in terms of
  `--desc-file` internally (e.g. writing the argv string to a hidden temp file and
  re-reading it) in a way that changes edge-case behaviour — empty string vs. absent,
  trailing-newline handling, etc. This AC's "unmodified existing tests still pass" check
  catches any such drift without the junior needing to enumerate every edge case by hand.

### C. The tool documents the sanctioned pattern in its own header/usage text

- **AC-5. `claude-bow.js`'s own usage/help text for `add` (and `comment`) names
  `--desc-file`/`--note-file`/`--example-file` as the recommended option when the field's
  content contains shell-special characters** (backtick, `$`, double quote, or is
  multi-line), mirroring how `claude-scratch.js`'s header states WHY `git stash` is
  banned and what to use instead, so the reasoning travels with the tool rather than
  living only in a process document. Concretely: the `Usage:` line(s) at
  `claude-bow.js:19` and `claude-bow.js:23` (and the corresponding `console.error` usage
  strings at the `add`/`comment` failure paths, e.g. `claude-bow.js:286`) are updated to
  show the `-file` alternative alongside the direct form, and a short comment block near
  `VALUE_FLAGS` or the `add`/`comment` command functions explains: risky content
  (backticks, `$(...)`, embedded quotes, multi-line text) belongs in a file passed via
  `--desc-file`/`--note-file`, not inline in the shell argument, because the outer shell
  invoking `node claude-bow.js` interprets those characters before Node ever sees them
  (BUG-090). Check: `grep -n "desc-file" claude-bow.js` and `grep -n "BUG-090\|shell"
  claude-bow.js` (case-insensitive) both find matches inside a comment or usage string,
  not only inside code logic. **False-pass warning:** a grep for `desc-file` alone would
  pass an implementation that added the flag but never explained *when* to prefer it —
  the second grep (for the rationale) is what proves the guidance, not just the
  capability, made it into the tool. **What a lazy implementation looks like:** documenting
  the new flag only in this acceptance file or in `dev-team-process.md` and leaving
  `claude-bow.js --help`/usage output unchanged — technically satisfies "a safer mode
  exists" while failing the actual point BUG-090's second self-report made: guidance that
  lives only in a document nobody re-reads under pressure doesn't change behaviour in the
  moment an agent is composing a risky `--desc`.
- **AC-6 (advisory, not gate-failing). `add`/`comment` may additionally warn — to
  stderr, non-fatally — when a `--desc`/`--note` value supplied directly (not via
  `-file`) contains a backtick, a `$(`, or an embedded double quote, suggesting the
  `-file` alternative.** This is a heuristic nudge, not a detector of successful shell
  injection (by the time `claude-bow.js` sees the string, any command substitution the
  outer shell was going to perform has already happened — the string it receives is
  already post-substitution, so the tool is warning about a *pattern* that suggests the
  content was risky, not confirming an injection occurred or was prevented). Explicitly
  does NOT block the write and does NOT fail the command. Check: a passing test supplies
  `--desc` containing a literal backtick character and asserts a stderr warning is
  produced and the item is still written successfully (non-fatal); a second test supplies
  a plain-text `--desc` with none of the trigger characters and asserts no warning.
  **What a lazy implementation looks like:** making this warning a hard failure (exit
  non-zero) — that would make `claude-bow.js` unusable for any genuinely descriptive text
  that happens to legitimately contain a dollar sign or a quoted phrase (e.g. describing
  a `$PATH` bug, or quoting a users's exact words), which is a worse regression than the
  problem being solved. This AC is deliberately scoped advisory for the same reason
  `tool.astgate.md`'s AC-6/AC-7 flooding-risk flag is advisory: a heuristic pattern-match
  on content is expected to have false positives, and a false positive that BLOCKS is a
  process own-goal, not a security win.

## Out of scope

- Detecting or preventing the outer shell's command substitution itself — not possible
  from inside `claude-bow.js` by construction (the substitution happens before Node
  starts); this item's only lever is making the safe path (`-file`) not require the
  risky path (inline shell-quoted content) in the first place.
- Control (2) from BUG-090 — telling an agent how to recover safely from a mess it made.
  Not code-testable in the shape this file's other criteria are; routed to Bill, see
  Escalations.
- Control (3) from BUG-090 — mechanically enforcing the non-lead `git reset` ban. Lives
  in `dev-team-process.md` today with no enforcement mechanism; routed to Bill, see
  Escalations.
- Retrofitting `--desc-file`-shaped input to every other VALUE_FLAGS entry not named in
  AC-2 (`--title`-adjacent fields, `--attacker`, `--findings`, etc.) — those are shorter,
  more structured fields (names, comma-lists) far less likely to carry copy-pasted shell
  content; if a future incident shows otherwise, that is a new BOW item, not silently
  folded in here.
- Any change to `--example`/`--example-file` on `comment` — already file-capable, already
  correct, cited here only as the precedent this item extends.

## Escalations

- **Control (2) from BUG-090 ("no agent is told how to recover safely when it does make
  a mess — the correct move was to report and stop, not to repair") is Bill's to address
  in `dev-team-process.md` directly.** Not attempted as acceptance criteria here — it is
  brief/process language, not a tool behaviour with a pass/fail check.
- **Control (3) from BUG-090 ("the ban on reset for non-leads lives in a process document
  and nothing enforces it") is Bill's to address in `dev-team-process.md` directly** (or
  as a separate, future mechanical-gate BOW item if Bill decides it should become one,
  parallel to how the author-identity ban moved from prose to `tool.committhook.md`'s
  enforcing hook — but that design decision is Bill's, not pre-empted here).
  Not attempted as acceptance criteria in this file.
- Whether `--desc-file`/`--note-file` should eventually be extended to the other
  VALUE_FLAGS text fields excluded under "Out of scope" is a call for Bill if a future
  incident shows a need — flagged so it isn't silently forgotten as a "someday" item.

## Assumptions logged (process v1.7)

- **ASM (this file, logged via `claude-bow.js` below) — scope of which VALUE_FLAGS get
  the `-file` treatment (AC-2).** Chosen as `--note` (all its call sites) and `--detail`
  (`gate`) rather than every free-text VALUE_FLAGS entry, on the reasoning that these are
  the fields BUG-090 and BUG-043 both actually involved (a description and a comment,
  both prose fields most likely to contain copy-pasted attack commands or example
  invocations) — see the item text logged to BOW for the "what breaks if wrong" detail.
- **ASM (this file, logged via `claude-bow.js` below) — AC-6's warning heuristic is
  advisory/non-fatal by design, mirroring `tool.astgate.md`'s AC-6/AC-7 pattern**, rather
  than Bill directing a stricter posture for this specific incident (BUG-090 is P1,
  higher than astgate's P1 flooding-risk item was scoped against, so a reasonable person
  could argue this warrants a harder gate) — see the item text logged to BOW for the
  "what breaks if wrong" detail.

---

BOW code: FEAT-044

# Acceptance criteria — general auditable amend command for BOW prose (FEAT-044)

**BOW code:** FEAT-044 (P2, open) — "General auditable amend command for BOW prose:
correct stale/wrong text in titles, descriptions and comments with a mandatory audit
trail."
**Spec refs:** FEAT-044's own description (filed by Aaron 2026-08-11 as the generalisation
of BUG-061's redact command — BUG-061 shipped narrow, GR#22-patterns-only, per Aaron's
2026-08-11 ruling quoted on that item: "Redact ships first, narrow (GR#22 only); amend
generalises its engine afterwards — shared code path, redact becomes a mode."). GR#22
(codename discipline — `redactText`/`loadCodenameGuardPatterns`, `claude-bow.js:2693-2741`,
the engine this item shares). GR#15 (validators/expected values derive from data, not
hardcoded constants — applies to AC-2's immutable-field list, which must be checked
against the actual `bow_items`/`bow_comments` schema, not a prose guess). BUG-017 (`set
--desc`/`set --desc-file`, `claude-bow.js:2588-2662` — this session's fix; read first, see
"Relationship to BUG-017" below). BUG-090/AC-1 (`resolveTextFlag`, `claude-bow.js:520` —
the mutual-exclusion and file-based-input pattern this item's `--reason`/`--reason-file`
pair must reuse verbatim, not reinvent).
**Date:** 2026-08-13
**Status:** active — normal pipeline order (criteria written before junior dispatch)
**Package under test:** `claude-bow.js` — a new `amend` command, sharing its text-mutation
engine with the existing `redact` command (`cmdRedact`, `redactText`,
`claude-bow.js:2664-2856`) per Aaron's binding design constraint that redact and amend are
"two commands, one engine."
**Standard gates:** root tooling, version-guard-exempt (matching `tool.bow`/`tool.redact`'s
posture) — AC-1 (`node --check claude-bow.js`), no Co-Authored-By, forbidden-touch (no file
outside `claude-bow.js` and this acceptance doc changes).

## Relationship to BUG-017's `set --desc`/`set --desc-file` — supersedes for prose, not for scope

BUG-017 (this session) added `--desc`/`--desc-file` to the existing `set` command
(`cmdSet`, `claude-bow.js:2588-2662`) as a repair path for a corrupted `description`
column — it reuses BUG-090's `resolveTextFlag` mutual-exclusion/file-input pattern, but it
is a **silent overwrite**: `set --desc` updates `bow_items.description` and prints a
one-line confirmation (`"${item.code} updated..."`, `claude-bow.js:2661`) with **no
before/after audit trail written anywhere** — no auto-comment, no old-value capture, no
reason argument. FEAT-044 does **not** remove `set --desc` (AC-9 keeps it working
unmodified, since other fields `set` touches — priority, status, mkey, seq — are correctly
out of `amend`'s scope per Aaron's own constraint below). What FEAT-044 supersedes is *the
practice of using `set --desc` to correct prose that is wrong, not merely absent* — `amend`
becomes the sanctioned path for that specific case (title/description/comment-body
correction with a mandatory trail), while `set --desc` remains what it always was: a
one-shot, unaudited field-fill primarily meant for populating a field that started empty
or was corrupted (BUG-017's own motivating case). AC-10 below documents this distinction
in `set`'s own usage text so a future user is pointed at `amend` for corrections.

## Acceptance criteria

### A. Scope — which fields `amend` may touch

- **AC-1. `amend <CODE> --field title|desc --to "<text>" --reason "<text>"` (and a
  `--comment <id> --field body` mode mirroring `redact`'s existing `--comment <id>`
  branch, `claude-bow.js:2752-2769`) updates exactly one of: an item's `title`, an item's
  `description`, or a single historical comment's `body`.** No other field name is
  accepted. Check: a passing test invokes `amend` with `--field priority` (or any field
  name outside this list) and asserts non-zero exit, a clear "unsupported field" message
  naming the rejected field, and no row changed (query `bow_items`/`bow_comments`
  directly, not just the exit code).
- **AC-2. `amend` refuses to touch `status`, `priority`, `deps` (`bow_dependencies`),
  `refs` (`bow_git_refs`), `mkey`, `seq`, `sprint`, or any other structured/validated
  column** — these already have sanctioned commands (`set`, `depend`/`undepend`, `ref`,
  `done`) with their own validation (e.g. `set`'s `PRIORITIES.includes(p)` check,
  `claude-bow.js:2594`), and Aaron's own FEAT-044 description states explicitly that
  `amend` "must not become a second, weaker write route around their validations." Check:
  `grep -n "amend" claude-bow.js` shows the command's field-name allowlist is a closed set
  (title/description/comment-body only) — a passing test attempts `amend <CODE> --field
  status --to done --reason x` and asserts it is rejected with the same "unsupported
  field" message as AC-1, not silently routed to `cmdSet`'s status-update logic.
- **AC-3. `guid`, `code`, `created_at`, and `closed_at`/`closed_note` are immutable via
  `amend`** (and via `set`, unchanged) — these are either identity fields (GUID/code, GR#3
  single-source-of-truth for BOW identity) or already have their own sanctioned mutation
  path (`closed_at`/`closed_note` are written only by `set --status done|cancelled`,
  `claude-bow.js:2601`). Check: the `amend` field allowlist (AC-1) does not include any of
  these names by construction — a passing test confirms `--field guid` (or `created_at`)
  is rejected the same way as AC-1/AC-2's out-of-scope fields.
- **AC-4. `amend --comment <id> --field body` targets a single existing comment row by
  its numeric `id`, exactly matching `redact`'s existing `--comment <id>` lookup
  (`claude-bow.js:2752-2758`: `Number(flags.comment)`, `SELECT * FROM bow_comments WHERE
  id = ?`, non-zero exit with a clear message if `Number.isFinite` fails or no row is
  found).** No bulk/pattern-based comment amendment (e.g. "amend all comments containing
  X") is in scope — one comment id per invocation, matching `redact`'s existing shape.
  Check: a passing test invokes `amend --comment <nonexistent-id> --field body --to "x"
  --reason "y"` and asserts the same "no comment with id N" class of error `redact`
  already produces for the identical case.

### B. The mandatory audit trail

- **AC-5. Every successful `amend` write inserts exactly one row into `bow_comments`
  (the same audit-trail table `redact`'s auto-comment already uses,
  `claude-bow.js:2848-2852`) recording: the field amended, the OLD value, the NEW value,
  the `--reason` text supplied, and the acting identity (`currentAuthor()`,
  `claude-bow.js:418-423` — same attribution mechanism every other comment-writing command
  already uses).** Unlike `redact` (which deliberately never quotes the pre-image, per
  Aaron's GR#22 constraint against re-exposing forbidden text one table over), `amend`'s
  whole purpose is correcting ordinary stale/wrong prose, so the audit comment DOES quote
  both old and new text in full — the GR#22 suppression is `redact`-mode-only (see AC-8).
  Check: a passing test amends an item's `description`, then queries `bow_comments` for
  that item's GUID and asserts a new row exists whose `body` contains the pre-amend text,
  the post-amend text, the supplied reason, and is attributed to the acting author — not
  merely that *some* comment was added (a false pass would be an implementation that logs
  "description amended" with no old/new/reason content).
- **AC-6. `--reason` is a mandatory argument — `amend` invoked without it exits non-zero
  before any write occurs, with a message stating the reason is required.** This is
  FEAT-044's own explicit design constraint ("records who/when/why (--reason mandatory)").
  Check: a passing test invokes `amend <CODE> --field title --to "New Title"` with no
  `--reason` flag and asserts non-zero exit, the "reason is required" message, and — query
  the DB directly — neither the target row nor `bow_comments` changed (no partial write:
  the mandatory-reason check must run before the update, not merely before the confirmation
  message).
- **AC-7. `--reason`/`--reason-file` follow the identical mutual-exclusion and file-based-
  input shape BUG-090 established for `--desc`/`--desc-file` (`resolveTextFlag`,
  `claude-bow.js:520`)** — a reason containing shell-special characters (backtick, `$(`,
  embedded quote) can be supplied via `--reason-file <path>` instead of inline, and
  supplying both `--reason` and `--reason-file` together is a non-zero-exit conflict, not
  silent precedence (mirroring BUG-090's AC-3 and its "silent precedence is a 'which one
  actually landed' bug waiting to happen" reasoning). The `--to` argument (the new field
  value itself) gets the same treatment: `--to`/`--to-file`, since a title/description/
  comment-body correction is exactly the kind of free text BUG-090 was written to protect.
  Check: a passing test supplies both `--reason` and `--reason-file`, and separately both
  `--to` and `--to-file`, and asserts each conflicting pair is rejected non-zero with no
  write, matching BUG-090/AC-3's evidentiary standard (query the table, not just the exit
  code).
- **AC-8. `amend`'s audit-trail engine is the same code path `redact` uses, with GR#22
  quoting-suppression as a mode flag, not a fork** — per Aaron's binding constraint on
  BUG-061 ("two commands, one engine... so the audit-trail discipline cannot drift between
  them"). Concretely: both commands share one underlying "apply field mutation + write
  audit comment" function; `redact`'s call site sets a suppress-old-text flag (GR#22),
  `amend`'s call site does not. Check: `grep -n "function.*[Aa]mend\|function.*[Aa]udit"
  claude-bow.js` shows `cmdRedact` and `cmdAmend` both calling into a shared helper — a
  passing test (code-shape, not just behavioural) fails if `cmdAmend` re-implements its own
  independent "insert into bow_comments" write rather than routing through the same helper
  `cmdRedact` was refactored to use. **What a lazy implementation looks like:** writing
  `cmdAmend` as a fresh, parallel function that never touches `cmdRedact`'s code — it could
  pass every behavioural AC above while silently drifting from `redact`'s audit format the
  first time either one is next edited, which is exactly the "audit-trail discipline
  drifts between them" outcome Aaron's ruling was written to prevent.

### C. Safety and backward compatibility

- **AC-9. `set --desc`/`set --desc-file` (BUG-017) are unchanged in behaviour** — `amend`
  is additive, not a replacement; existing `set` test coverage continues to pass unmodified.
  `set`'s own usage/help text (`claude-bow.js:2658`) is updated to note that `amend` is the
  audited alternative for correcting prose that was previously right and is now wrong (as
  opposed to filling a field that is empty or corrupted), per "Relationship to BUG-017"
  above. Check: existing `set` tests pass unmodified; `grep -n "amend" claude-bow.js`'s
  match inside the `set` usage string confirms the pointer exists.
- **AC-10. `amend`'s own usage/help text and a header comment near `cmdAmend` name the
  mandatory `--reason` requirement and point to `redact` as the sibling command for GR#22
  content specifically** (an operator trying to remove forbidden-title text via `amend`
  should be told to use `redact` instead, since `amend` quotes old/new text in its audit
  trail — the wrong tool for that specific case). Check: `grep -n "redact" claude-bow.js`
  finds a match inside `amend`'s usage/help text or header comment, not only inside
  `redact`'s own section.
- **AC-11. `amend`'s `--to`/`--to-file` value is checked against the target column's
  actual length limit before writing (mirroring `redact`'s `BOW_COLUMN_MAX_LEN`/
  `REDACT_FIELD_MAX_LEN` pre-write check, `claude-bow.js:2689-2691, 2825-2837`)** — for
  `title` (VARCHAR(255)), an amendment that would overflow the column is refused with no
  partial write, stating the violation is still present because the write was never
  attempted (same phrasing discipline as `redact`'s existing overflow message). `desc`
  and comment `body` are TEXT/unbounded, matching `redact`'s existing no-limit-needed
  reasoning for those columns. Check: a passing test attempts `amend <CODE> --field title
  --to <a 300-char string> --reason x` and asserts non-zero exit, the specific overflow
  message, and the title column unchanged (query directly).
- **AC-12 (advisory, not gate-failing). `amend` warns — non-fatally — if `--to`/
  `--to-file` content matches a GR#22 forbidden pattern (reusing
  `loadCodenameGuardPatterns()`, `claude-bow.js:2693-2700`), suggesting `redact` instead.**
  Does not block the write (an operator amending unrelated prose that happens to legitimately
  discuss the guard's own pattern set in the abstract must not be blocked), mirroring
  BUG-090/AC-6's advisory-not-gate posture for the identical reason: a heuristic
  content-match warning that blocks is a process own-goal. Check: a passing test supplies
  `--to` text containing a GR#22 pattern and asserts a stderr warning plus a successful
  write (non-fatal); a second test with clean text asserts no warning.

## Out of scope

- Bulk/pattern-based amendment across multiple items or comments in one invocation — one
  target (item field, or single comment id) per `amend` call, matching `redact`'s existing
  single-target shape.
- Amending `status`/`priority`/`mkey`/`seq`/`sprint`/deps/refs — explicitly excluded per
  AC-2, Aaron's own constraint against a second, weaker write route around their existing
  validated commands.
- Retiring or deprecating `set --desc`/`set --desc-file` — BUG-017 shipped this session and
  stays; AC-9 keeps it working, AC-10 only adds a documentation pointer.
- Any change to `redact`'s own GR#22-suppression behaviour — AC-8 requires the two commands
  share an engine, not that `redact` gain `amend`'s old/new quoting (that would reintroduce
  the exact leak class BUG-061 exists to close).

## Escalations

- **AC-8's "shared engine" requirement may require a non-trivial refactor of the existing,
  already-shipped `cmdRedact`** (extracting its apply-plus-audit logic into a helper both
  commands call) rather than `amend` being purely additive new code. Flagging since this
  touches code that shipped and was Destructive-verdicted under BUG-061 — the junior
  should not consider `cmdRedact`'s existing behavioural tests (GR#22 redaction shape,
  column-overflow refusal) permitted to change, only its internal structure. If the
  refactor risk is judged too high relative to FEAT-044's P2 priority, Bill may choose to
  accept two independently-implemented commands with a shared *test* (asserting both audit
  comments follow the same structural shape) as a lighter-weight substitute for AC-8 — that
  call is Bill's, not pre-empted here.
