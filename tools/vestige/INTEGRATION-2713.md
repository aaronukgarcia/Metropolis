# tools/vestige/INTEGRATION-2713.md — patch for `claude-bow.js` (FEAT-2326609713)

**Why a dedicated file instead of appending to `INTEGRATION.md`:** that file is
scoped to FEAT-2326609712 (the destructive-lesson ingest) and opens with "Do
NOT apply this until BUG-340 r3's edits to `claude-bow.js`'s `destructive`
command path have landed" — a warning specific to that lane's collision
avoidance. This feature (FEAT-2326609713) patches a DIFFERENT part of
`claude-bow.js` (a brand-new `ruling` command, not `cmdDestructive`), so
mixing the two patches into one file would make it unclear which anchor
belongs to which BOW item once either lands. Same reasoning the sibling used
to justify its own file; this one gets its own for the same reason.

**Do NOT apply this until it is confirmed no other lane has an in-flight,
uncommitted edit to `claude-bow.js`** — check `git log -1 -- claude-bow.js`
against `origin/main` and, if on a shared checkout, `git status
claude-bow.js` for another lane's uncommitted change before touching the
file. This lane (FEAT-2326609713) deliberately did NOT touch `claude-bow.js`
itself, for the same reason FEAT-2326609712 didn't: two lanes editing the
same file concurrently is a guaranteed conflict, and the lead's PR-time
review is the natural checkpoint to apply a patch like this one.

## What ships in this lane (no `claude-bow.js` edits)

- `tools/vestige/ruling-ingest.js` — `buildRulingPayload(fields)`,
  `emitRulingInstruction(payload)`, `parseRulingInstruction(block)`,
  `extractKeywords(text)`, `deriveTopic(text)`.
- `tools/vestige/backfill-rulings.js` — the one-off, READ-ONLY backfill
  scanner over `bow_comments` (not part of the live `ruling` command path).
- `tools/vestige/ruling-ingest.test.js` + `tools/vestige/backfill-rulings.test.js`
  — node:test, discovered by root `node --test` automatically (no wiring
  needed — the root test runner globs `**/*.test.js`).
- `tools/vestige/backfill-rulings.review.json` — the reviewable backfill
  output (82 candidate rulings, 32 carrying a possible-supersession flag as
  of the 2026-09-01 run; regenerate before trusting it against a newer BOW).

No `data/errors.json` change was needed for this lane: unlike the sibling's
`requireLesson` (which gates a CLI flag, `--lesson`, and needed a registry
error for the reject-without-lesson case), `buildRulingPayload` throws a
plain `Error` on a missing/blank `code`/`text` the same way the sibling's
`buildLessonPayload` does — the registry-error path in `lesson-ingest.js` is
reserved for the CLI-flag-validation half of that feature, which this
feature does not (yet) own since the `ruling` command itself isn't wired.

## The patch itself (new command, mirrors `cmdDestructive`'s shape exactly)

### 1. A new `recordRuling` function (mirrors `recordDestructiveVerdict`)

Anchor: place directly above `cmdDestructive` (or directly below it — either
side of the existing `destructive`/`recordDestructiveVerdict` pair is fine,
they don't interact).

```js
async function recordRuling(db, ref, opts = {}) {
  const item = await findItem(db, ref);
  if (!item) throw new Error(`no BOW item matches "${ref}" (use a code like FEAT-040 or a GUID)`);

  const text = String(opts.text || '').trim();
  if (!text) throw new Error('--text (or --text-file) is required and must be non-empty');

  const date = new Date().toISOString().slice(0, 10);
  const author = currentAuthor();
  const body = `AARON RULING ${date}: ${text}`;
  // No validateLen() call here: bow_comments.body is a TEXT column and
  // cmdComment (the existing precedent this factors out of) does not
  // length-check it either — confirmed against claude-bow.js as of
  // 2026-09-01 (BOW_COLUMN_MAX_LEN has no comment_body entry).
  validateLen('author', author, BOW_COLUMN_MAX_LEN.comment_author,
    { mode: 'throw', context: `author for ruling on ${item.code}` });

  await db.query(
    'INSERT INTO bow_comments (item_guid, author, body, example_code, code_language) VALUES (?, ?, ?, NULL, NULL)',
    [item.guid, author, body]);

  return { item, date, text, author };
}
```

This is the UNCHANGED existing behaviour the BOW item's FIX section asks
for — "appends the ruling comment to the item (tagged AARON RULING <date>,
as today)" — expressed as a named, reusable function instead of inline
`cmdComment` shelling, so `cmdRuling` (below) can call it directly without a
round trip through argv parsing.

### 2. `cmdRuling` — the CLI entry point (mirrors `cmdDestructive`)

```js
async function cmdRuling(db) {
  const ref = positional[0];
  if (!ref) {
    console.error('Usage: node claude-bow.js ruling <code|mkey> [--text "..." | --text-file <path>] [--supersedes "<prior ruling topic/guid>"]');
    console.error('  BUG-090: if --text content contains a backtick, "$(...)", an embedded quote, or spans multiple lines, use --text-file <path> instead.');
    process.exit(1);
  }
  try {
    const text = resolveTextFlag('text'); // BUG-090 pattern: --text | --text-file, mirrors --note/--note-file
    const result = await recordRuling(db, ref, { text });
    console.log(`Recorded ruling on ${result.item.code} (${result.date}).`);

    const { buildRulingPayload, emitRulingInstruction } = require('./tools/vestige/ruling-ingest.js');
    console.log(emitRulingInstruction(buildRulingPayload({
      code: result.item.code,
      mkey: result.item.mkey || result.item.code,
      text: result.text,
      date: result.date,
      supersedes: flags.supersedes || undefined,
    })));
  } catch (err) {
    console.error(`claude-bow ruling: ${err.message}`);
    process.exit(1);
  }
}
```

`resolveTextFlag(fieldName)` (defined around line 685 as of 2026-09-01) is
already fully generic — it takes any field name and checks `flags[fieldName]`
/ `flags[fieldName + '-file']` directly, so `resolveTextFlag('text')` needs
NO new logic in that function. The only required change is adding `'text'`,
`'text-file'`, and `'supersedes'` to the `VALUE_FLAGS` array (around line
219, the list currently containing `'desc'`, `'desc-file'`, `'note'`,
`'note-file'`, ...) so the CLI's generic argv parser treats `--text`/
`--text-file`/`--supersedes` as taking a value instead of being parsed as a
bare boolean flag.

### 3. Dispatch wiring (one line in the command switch/if-chain)

Anchor: wherever `cmdDestructive`/`cmdVerdict`/`cmdComment` are dispatched
(the `if (cmd === 'destructive') ... else if (cmd === 'comment') ...` chain,
or an object-keyed dispatch table if the file uses one — inspect the exact
shape at patch time, this file uses a plain if/else chain as of 2026-09-01).
Add:

```js
else if (cmd === 'ruling') await cmdRuling(db);
```

### 4. Usage-string update (cosmetic)

Add `ruling <code> --text "..." [--supersedes "..."]` to whatever top-level
usage/help listing enumerates the subcommands (`destructive`, `verdict`,
`comment`, ... — same list `cmdDestructive`/`cmdComment` are already named
in).

That is the whole patch: one new `recordRuling` function (the existing
"append AARON RULING <date>: <text> as a normal bow_comments row" behaviour,
factored out so it's callable from a dedicated command instead of only via
generic `comment`), one new `cmdRuling` wrapper that also emits the ingest
instruction, one dispatch line, and two flag-list additions
(`text`/`text-file`) reusing the SAME `resolveTextFlag`/`VALUE_FLAGS`
machinery `note`/`desc` already use — no new parsing logic. Everything else
(payload shape, tag set, supersession fields) lives in
`tools/vestige/ruling-ingest.js`, already built, tested, and reviewable
independently of `claude-bow.js`.

## What the RECORDING SESSION does with the printed block

`emitRulingInstruction`'s output is a fixed, greppable block:

```
=== INGEST RULING TO VESTIGE (FEAT-2326609713) ===
{ ...JSON payload... }
=== END ===
```

The interactive Claude session that ran `node claude-bow.js ruling ...`
greps its own tool output for that header/footer pair, `JSON.parse`s the
body, and calls `mcp__vestige__smart_ingest(payload)`. When the payload
carries `supersedesHint`/`topic`/`supersedeInstruction` (because
`--supersedes` was passed), the session ALSO runs a `mcp__vestige__recall`
for `topic` first, and — if a matching prior decision node is found — either
updates its `validUntil` via `mcp__vestige__memory`'s update path (if the
tool exposes one; `ruling-ingest.js`'s header explains why this module does
not assume the answer) or deletes it and calls `smart_ingest` with
`forceCreate` for the replacement, per the 2026-08-02 storage policy. The new
ruling is ingested as a `decision` node either way. This mirrors the exact
house pattern the sibling FEAT-2326609712 already established: a hook/CLI
process only ever PROMPTS the session to call an MCP tool, never calls one
itself.

## `/loadup` step 4 and interview-skill reference (BOW item's part (3))

Add one line to `.claude/commands/loadup.md` (wherever it enumerates its
steps — the BOW item names "step 4") and to whichever interview-flow
skill(s) record an Aaron ruling today (grep `.claude/commands/*.md` for
"AARON RULING" — `/bye` and any dedicated interview skill are the likely
hits):

> When Aaron rules on an open question, record it with `node claude-bow.js
> ruling <code> --text "..."` (add `--supersedes "<prior ruling topic>"`
> when this ruling reverses/refines an earlier one) instead of a plain
> `comment` — this is what makes the ruling recallable and supersession-
> aware in Vestige (FEAT-2326609713). A plain `comment` still works but
> skips the Vestige ingest instruction.

## Backfill (BOW item's part (2))

`tools/vestige/backfill-rulings.js` has already been run READ-ONLY against
the live `metro` DB (2026-09-01): 82 candidate ruling comments extracted
from `bow_comments`, 32 carrying a possible-supersession flag, written to
`tools/vestige/backfill-rulings.review.json`. Nothing has been ingested —
the lead reviews the JSON (confirm/clear each `possible_supersedes` entry,
set `approved: true` per row), then a single ingest pass calls
`buildRulingPayload`/`emitRulingInstruction` per approved row (a small
driver script, not yet written, would loop the approved rows and print one
instruction block per row for the recording session to act on — left as a
follow-up once the review pass is done, since writing it before the review
happens would be premature).
