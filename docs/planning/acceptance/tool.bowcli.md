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
