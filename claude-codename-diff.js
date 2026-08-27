// Module key: tool.codenamediff (see code.json; GUID 147cceb9-c50e-4719-9f2e-df80e4b8c806)
// Spec ref: GR#22

/**
 * claude-codename-diff.js — shared unified-diff line classifier (BUG-182).
 *
 * Classifies each line of `git diff --cached --unified=0` output as HEADER
 * (the per-file header region: `diff --git` / `index` / mode / `---` /
 * `+++` / rename / copy lines) or CONTENT (inside a `@@ ... @@` hunk body) by
 * POSITION, never by matching the header lines' own text.
 *
 * THIS IS THE FIX FOR BUG-182. Both claude-codename-content-scan.js and
 * claude-codename-guard.js used to derive "added lines" independently with
 * the identical filter `l.startsWith('+') && !l.startsWith('+++')` — the
 * second half meant to exclude the one-per-file '+++ b/<path>' header line by
 * guessing from its text shape. But a genuine added CONTENT line whose own
 * text happens to start with two literal '+' characters produces the exact
 * same 'starts with +++' textual shape: git's own single '+' added-line
 * marker, followed by payload beginning '++', is textually indistinguishable
 * from the header line. That filter silently dropped such a line before it
 * ever reached the pattern scanner — a live, reproduced bypass of GR#22.
 *
 * A real unified diff has exactly one header region per file section —
 * everything from its 'diff --git' line up to (not including) the first
 * '@@' hunk marker — and every line at or after that '@@' marker is
 * unambiguously hunk-body content until the next 'diff --git' line starts a
 * new file section. Tracking that with a boolean (`inHunk`) is immune to
 * what the content itself says, so a hunk-body line's own first two
 * characters can never be misclassified as a header, regardless of content.
 *
 * Single source of truth (GR#3): both consumers require() this module and
 * call splitDiffSections() rather than each re-deriving the same
 * header-vs-content split — the exact duplication that let BUG-182 exist as
 * one bug with two independent occurrences instead of one.
 */

'use strict';

const HUNK_RE = /^@@ /;
const FILE_START_RE = /^diff --git /;
const PATH_HEADER_RE = /^(\+\+\+ |--- |rename to |rename from |copy to |copy from )/;

// BUG-185 defense-in-depth: strips a single leading ANSI CSI (Control
// Sequence Introducer) escape sequence — ESC '[' <params> <final-byte> — from
// the START of a line before it is tested against the header/hunk regexes
// above. The primary fix is invoking `git diff` with `--no-color` at both
// call sites (claude-codename-content-scan.js, claude-codename-guard.js), so
// this should never fire in practice; it exists only in case some future
// call site invokes git without that flag. Deliberately NOT a general ANSI
// parser (no OSC/DCS handling, no mid-line stripping) — a real diff line's
// meaningful classification only ever depends on what appears at its true
// start, so stripping one leading CSI sequence is sufficient and keeps the
// logic simple enough to reason about and test exhaustively.
const LEADING_CSI_RE = /^\x1b\[[0-9;]*[A-Za-z]/;

function stripLeadingAnsi(line) {
  return line.replace(LEADING_CSI_RE, '');
}

/** Splits raw `git diff --cached --unified=0` output into:
 *   - addedLines: the content of every genuine added line (hunk-body lines
 *     starting with a single '+' marker), marker stripped, joined by '\n'.
 *   - pathHeaderLines: the per-file header lines identifying new, renamed or
 *     copied paths (BUG-137), path text extracted, joined by '\n'.
 * Both are produced from the SAME single pass, classifying each line as
 * header-region or hunk-body strictly by position (`inHunk`), never by
 * re-testing a line's own text against the header lines' shape.
 *
 * BUG-416: also returns `sections` (per-file breakdown):
 *   - sections: array of { filePath, addedLines (array, not joined) }
 * This allows the guard to apply file-specific logic (e.g. skip integrity-hash
 * lines only in lockfiles, not in arbitrary source files). */
function splitDiffSections(diffText) {
  const lines = String(diffText).split(/\r?\n/);
  const added = [];
  const pathHeaders = [];
  const sections = [];
  let inHunk = false;
  let currentFilePath = null;
  let currentSectionAdded = [];

  for (const line of lines) {
    // BUG-185 defense-in-depth: classification below is tested against a
    // copy with one leading ANSI CSI sequence stripped, so a forced-color
    // diff (color.ui/color.diff=always in ANY applicable git config) can't
    // hide a 'diff --git '/'@@ ' marker from these regexes even if some
    // future call site invokes git without --no-color. The primary fix is
    // --no-color at both call sites (git never emits color in that case, so
    // this strip is a no-op there); content extraction below still uses the
    // ORIGINAL line, since stripping is for position classification only.
    const test = stripLeadingAnsi(line);
    if (FILE_START_RE.test(test)) {
      // A new file section always starts back in its own header region,
      // even though the previous file's last hunk left inHunk === true.
      // BUG-416: push the previous section if it had content.
      if (currentFilePath && currentSectionAdded.length > 0) {
        sections.push({
          filePath: currentFilePath,
          addedLines: currentSectionAdded,
        });
      }
      inHunk = false;
      // Extract the file path from 'diff --git a/... b/...' marker.
      // Format: "diff --git a/path b/path" — extract b-path (the new version).
      // Simple approach: match the ' b/' prefix and everything after it.
      // (The git diff format always has " b/" before the b-path, making this
      // match unambiguous even with complex path names.)
      const fileStartMatch = test.match(/ b\/(.+)$/);
      currentFilePath = fileStartMatch ? fileStartMatch[1] : null;
      currentSectionAdded = [];
      continue;
    }
    if (HUNK_RE.test(test)) {
      // '@@ -a,b +c,d @@' marker: everything after this, up to the next
      // 'diff --git' line, is unambiguous hunk-body content.
      inHunk = true;
      continue;
    }
    if (inHunk) {
      // Hunk body: a line starting with a single '+' is genuine added
      // content, whatever its own text looks like — a header line can never
      // structurally appear here.
      if (test.startsWith('+')) {
        const content = test.slice(1);
        added.push(content);
        currentSectionAdded.push(content);
      }
    } else {
      // Still in the file-header region: this is where the real
      // '+++ b/<path>' / '--- a/<path>' / rename/copy lines actually live.
      if (PATH_HEADER_RE.test(test)) pathHeaders.push(test.replace(PATH_HEADER_RE, ''));
    }
  }

  // BUG-416: push the final section if it had content.
  if (currentFilePath && currentSectionAdded.length > 0) {
    sections.push({
      filePath: currentFilePath,
      addedLines: currentSectionAdded,
    });
  }

  return {
    addedLines: added.join('\n'),
    pathHeaderLines: pathHeaders.join('\n'),
    sections, // BUG-416: per-file breakdown for file-specific filtering
  };
}

module.exports = { splitDiffSections };
