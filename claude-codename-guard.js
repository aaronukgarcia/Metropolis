/**
 * PreToolUse hook — codename guard (BOW mkey: tool.codenameguard).
 *
 * Enforces GOLDEN RULE #22 (Codename Discipline): the reference title this
 * project's design docs compare against is 'Blue', and only 'Blue'. Its real
 * name, its abbreviations and its numbered sequel form must never be written
 * into git — not in code, data, docs, plans, comments, commit messages, or
 * branch names.
 *
 * WHY MECHANICAL. The repo is intended to go public. A name written into git
 * is a disclosure that cannot be withdrawn afterwards: clones, caches and
 * indexers outlive any later edit, which is exactly why the existing
 * occurrences were removed by rewriting history rather than by editing the
 * working tree. A rule that depends on everyone remembering it, across a
 * dozen concurrent agents, will be broken — so it is checked instead.
 *
 * WHY THE PATTERNS ARE ASSEMBLED FROM FRAGMENTS BELOW, which looks like
 * obfuscation and is not: this file lives IN git. If it contained the
 * forbidden strings as plain literals in order to search for them, the guard
 * would be the single largest violation of the rule it enforces — and it
 * would flag itself on every commit. The same trap catches a well-meaning
 * comment explaining a rename ("renamed <real name> to 'Blue'"), which is why
 * the rule covers comments and commit messages and not just code. Fragments
 * are joined at runtime so no forbidden literal ever appears on disk.
 *
 * WHAT IT CHECKS, on `git commit` and `git push`:
 *   1. Staged content (git diff --cached), added lines only — an existing
 *      violation elsewhere in a file must not block an unrelated fix.
 *   2. The commit message, including -m arguments and heredoc bodies. This is
 *      the likeliest place to slip: the message describing the removal is the
 *      easiest thing to write the name into.
 *   3. The current branch name.
 *
 * Ambiguity is handled deliberately. The bare two-letter abbreviation is NOT
 * matched: it appears innocently in ordinary technical prose, and a guard that
 * fires on false positives gets disabled within a day — a failure mode this
 * project has now catalogued three times (SEC-026, and twice since). The
 * numbered forms ARE matched, since those are unambiguous.
 *
 * Fail-CLOSED, like claude-plan-guard.js and unlike claude-dispatch-guard.js.
 * The cost asymmetry decides it: a false block is a minor annoyance that a
 * human resolves in seconds, while a miss is permanent and public. If this
 * guard cannot do its job it must not pretend the commit is clean.
 *
 * Deliberate disable: CLAUDE_DISABLE_CODENAME_GUARD=1. Use it to commit a
 * genuine false positive, never to push a real one.
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 */

'use strict';

const { execSync } = require('child_process');
const fs = require('fs');

// Fragments, never joined in source. See the header for why.
const SKY = 'sky';
const LINES = 'lines';
const CITY = 'cit';
const IES = '(?:y|ies)';

// Built at runtime. Matches the two-word title with any separator, the
// single distinctive word on its own, and the numbered abbreviations.
const PATTERNS = [
  {
    re: new RegExp(`${CITY}${IES}[\\s:_-]*${SKY}${LINES}`, 'i'),
    what: 'the full reference title',
  },
  {
    re: new RegExp(`\\b${SKY}${LINES}\\b`, 'i'),
    what: 'the distinctive single word from the reference title',
  },
  {
    re: /\bCS ?[12]\b/,
    what: 'a numbered abbreviation of the reference title',
  },
];

function allow() {
  process.exit(0);
}

function deny(reason) {
  process.stdout.write(
    JSON.stringify({
      hookSpecificOutput: {
        hookEventName: 'PreToolUse',
        permissionDecision: 'deny',
        permissionDecisionReason: reason,
      },
    })
  );
  process.exit(0);
}

function scan(text, where, hits) {
  if (!text) return;
  const lines = String(text).split(/\r?\n/);
  for (const p of PATTERNS) {
    for (let i = 0; i < lines.length; i++) {
      if (!p.re.test(lines[i])) continue;
      hits.push(
        `${where}${lines.length > 1 ? ` (line ${i + 1})` : ''}: contains ${p.what}.`
      );
      break; // One report per pattern per location is enough to act on.
    }
  }
}

function main() {
  if (process.env.CLAUDE_DISABLE_CODENAME_GUARD === '1') allow();

  let payload;
  try {
    payload = JSON.parse(fs.readFileSync(0, 'utf8') || '{}');
  } catch {
    // Unparsable hook input is not evidence of a clean commit, but it is also
    // not this guard's call to make — the shell will fail on its own.
    allow();
  }

  const cmd = String((payload.tool_input || {}).command || '');
  if (!/\bgit\s+(commit|push)\b/.test(cmd)) allow();

  const hits = [];

  // 2 & 3 first: they need no subprocess and cover the likeliest slip.
  scan(cmd, 'the git command (message text or arguments)', hits);

  try {
    const branch = execSync('git rev-parse --abbrev-ref HEAD', {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    }).trim();
    scan(branch, `the branch name "${branch}"`, hits);
  } catch {
    /* detached HEAD or no commits yet — nothing to check */
  }

  // 1: staged ADDED lines only. Scanning whole files would block an unrelated
  // fix in a file that still carries an occurrence somewhere else.
  try {
    const diff = execSync('git diff --cached --unified=0', {
      encoding: 'utf8',
      maxBuffer: 64 * 1024 * 1024,
      stdio: ['ignore', 'pipe', 'ignore'],
    });
    const added = diff
      .split(/\r?\n/)
      .filter((l) => l.startsWith('+') && !l.startsWith('+++'))
      .join('\n');
    scan(added, 'staged content (added lines)', hits);
  } catch (err) {
    deny(
      `🛑 CODENAME GUARD (GR#22): could not read the staged diff to check it ` +
        `— ${err.message}\n\nFailing closed: an unchecked commit is not a clean ` +
        `one, and a name written into git cannot be withdrawn once the repo is ` +
        `public. Resolve the git error and retry.`
    );
  }

  if (hits.length) {
    deny(
      `🛑 CODENAME GUARD — GOLDEN RULE #22 violation (${hits.length}):\n\n` +
        hits.map((h) => `  - ${h}`).join('\n') +
        `\n\nThe reference title is 'Blue', and only 'Blue'. Its real name, its ` +
        `abbreviations and its numbered form must never enter git — code, data, ` +
        `docs, comments, commit messages or branch names.\n\n` +
        `Rewrite to say 'Blue' or "the reference title". Where a sentence only ` +
        `reads sensibly with the real name, rewrite the sentence: the reference ` +
        `is being renamed, not deleted, so keep the technical point.\n\n` +
        `Note the trap this guard exists to catch — do NOT write a commit ` +
        `message or comment EXPLAINING the rename that quotes the old name. ` +
        `The explanation would itself be the exposure.\n\n` +
        `Deliberate bypass (genuine false positive only): ` +
        `CLAUDE_DISABLE_CODENAME_GUARD=1`
    );
  }

  allow();
}

try {
  main();
} catch (err) {
  // Fail closed — see the header on cost asymmetry.
  deny(
    `🛑 CODENAME GUARD (GR#22) internal error: ${err.message}\n\n` +
      `Failing closed deliberately. Bypass only if you have checked by hand: ` +
      `CLAUDE_DISABLE_CODENAME_GUARD=1`
  );
}
