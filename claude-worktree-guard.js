/**
 * PreToolUse hook — working-tree protection guard (GR#24 "No Code Left Behind",
 * BUG-215). BOW mkey: tool.worktreeguard.
 *
 * WHY THIS EXISTS — the evidence, not the principle.
 *
 * On 2026-08-13 a Destructive agent, part-way through a "prove the test can
 * fail" mutation cycle, ran `git checkout -- claude-destructive-guard.js` to
 * "restore" the file. `git checkout --` reverts to HEAD, not to the
 * uncommitted pre-mutation state — so it silently destroyed 211 lines of
 * uncommitted FEAT-077 work that had never been staged and was therefore not
 * recoverable from any git object. dev-team-process v1.5.1 already BANNED this
 * class of command for everyone but the lead, but that ban lived in prose the
 * subagents did not reliably read. This hook turns "remember not to" into "the
 * tool refuses" — the same move claude-codename-guard.js made for GR#22 and
 * claude-destructive-guard.js made for GR#23.
 *
 * WHAT IT BLOCKS. Git commands that DISCARD uncommitted working-tree content:
 *   - `git checkout -- <path>` / `git checkout .` / `git checkout -f ...` /
 *     `git checkout <branch> -- <path>` (any checkout that names paths or
 *     forces) — discards working-tree changes to those paths.
 *   - `git restore <path>` (NOT `git restore --staged <path>`, which only
 *     unstages and is safe) — restores working-tree content from HEAD/index.
 *   - `git reset --hard` / `git reset --keep` — throws away working-tree state.
 *   - `git clean -f` / `-d` / `-x` (any real clean; `-n`/`--dry-run` is safe) —
 *     deletes untracked files, the exact shape that eats a not-yet-added file.
 *   - `git stash` / `git stash push` / `git stash save` — sweeps the WHOLE
 *     working tree into a stash (v1.5.1's specific ban); with many agents live
 *     it takes every other agent's uncommitted work, and `pop` is a merge, not
 *     an undo.
 *
 * WHAT IT ALLOWS (must not fire on ordinary work — a guard that blocks safe
 * everyday commands gets switched off within a day, SEC-026's pattern):
 *   - `git checkout <branch>` / `git switch <branch>` / `git checkout -b ...`
 *     (branch switches and creation — no working-tree content is discarded;
 *     git refuses a switch that would clobber uncommitted changes anyway).
 *   - `git reset` (soft/mixed/default — moves HEAD/index, leaves the working
 *     tree untouched).
 *   - `git restore --staged <path>` (unstage only).
 *   - `git clean -n` / `--dry-run` (shows, deletes nothing).
 *   - `git stash list` / `show` / `pop` / `apply` (read or restore, not discard).
 *   - every non-git command, and every other git subcommand.
 *
 * FAIL-OPEN, like claude-dispatch-guard.js and UNLIKE the security guards.
 * This is a SAFETY NET against an accidental keystroke, not a security
 * boundary — nobody is adversarially trying to lose their own code. Its own
 * bug (a parse it cannot handle, an unexpected throw) must never stop the team
 * working, so any internal error ALLOWS and says so on stderr. A missed exotic
 * invocation is no worse than the zero protection that existed before this
 * file; a false BLOCK of a legitimate command would be the more damaging
 * failure, so the recogniser deliberately errs toward allowing.
 *
 * Deliberate operator override (set in the environment that LAUNCHES the
 * session, before it starts — never visible to this hook if written inline in
 * the very command being judged, same reasoning as the sibling guards):
 * CLAUDE_ALLOW_WORKTREE_RESET=1. The lead uses `git stash`/`reset --hard`
 * legitimately during recovery; that is what the override is for.
 *
 * GR#3: the command-recognition machinery (wrapper/heredoc/chain unwrapping,
 * quote masking, the `-c key=val` / `-C dir` global-option run, git-alias
 * resolution) is REUSED from claude-author-guard.js — the one
 * Destructive-hardened git recogniser in this repo — never re-hand-rolled
 * here. This file adds only the verb set and the per-verb argument
 * classification.
 *
 * Receives JSON on stdin: { tool_name|tool: "Bash"|"PowerShell",
 *   tool_input: { command: "..." } }. (Reads tool_name with a `tool`
 *   fallback — BUG-205's lesson: real payloads carry tool_name.)
 * Denies via: { hookSpecificOutput: { hookEventName: "PreToolUse",
 *   permissionDecision: "deny", permissionDecisionReason: "..." } }
 */

'use strict';

const fs = require('fs');
const ag = require('./claude-author-guard.js');

// Git subcommands that can discard uncommitted working-tree content. The
// value is a classifier over the verb's argument string: true = this specific
// invocation discards work (DENY), false = this spelling is safe (ALLOW).
const DESTRUCTIVE_VERBS = new Set(['checkout', 'restore', 'reset', 'clean', 'stash']);

function readStdin() {
  try {
    return fs.readFileSync(0, 'utf8');
  } catch {
    return '';
  }
}

function allow() {
  // Silent allow: emit nothing, exit 0. (PreToolUse treats no output as allow.)
  process.exit(0);
}

function deny(verb, spelling, safeAlternative) {
  process.stdout.write(
    JSON.stringify({
      hookSpecificOutput: {
        hookEventName: 'PreToolUse',
        permissionDecision: 'deny',
        permissionDecisionReason:
          `🛑 WORKTREE GUARD (GR#24 "No Code Left Behind"): \`git ${verb} ${spelling}\` ` +
          `DISCARDS uncommitted working-tree changes — this is exactly how 211 lines of ` +
          `un-staged work were destroyed on 2026-08-13.\n\n` +
          `${safeAlternative}\n\n` +
          `If you genuinely mean to throw away working-tree changes (a lead recovery, say), ` +
          `set CLAUDE_ALLOW_WORKTREE_RESET=1 in the environment BEFORE the session starts — ` +
          `it is never read from the command itself.`,
      },
    })
  );
  process.exit(0);
}

/** Split a git verb's argument suffix into whitespace-separated raw tokens.
 *  Good enough for flag/pathspec classification — the security-sensitive
 *  parsing (wrapper/quote/heredoc/-c) already happened in the shared
 *  recogniser upstream; here we only distinguish flags from pathspecs. */
function argTokens(suffix) {
  return suffix
    .replace(/^[\s]+/, '')
    .split(/\s+/)
    .filter(Boolean);
}

// Source/text file extensions this project actually contains. A bare token
// ending in one reads as a pathspec, not a ref. Deliberately NOT "any dotted
// token": `git checkout v1.2` / `v1.2.3` (a tag or version branch) is a legit
// SAFE switch and must not be mistaken for a file. GR#15-ish: this is the
// repo's real file-type surface, not an open-ended "has a dot" guess. A file
// whose extension is not listed, and has no slash, is left to the safe side
// (a false negative here just means "no worse than today"; a false positive
// would wrongly block a real branch/tag switch).
const PATHY_EXTENSIONS = new Set([
  'go', 'js', 'ts', 'md', 'json', 'txt', 'yml', 'yaml', 'sh', 'ps1', 'bat',
  'mod', 'sum', 'lock', 'toml', 'css', 'html', 'py', 'rs',
]);

function looksLikePath(tok) {
  if (tok.startsWith('-')) return false;
  if (tok === '.' || tok === '*') return true;
  if (tok.includes('/') || tok.includes('\\')) return true;
  const dot = tok.lastIndexOf('.');
  if (dot <= 0 || dot === tok.length - 1) return false;
  return PATHY_EXTENSIONS.has(tok.slice(dot + 1).toLowerCase());
}

/** Is this a working-tree-discarding invocation of `verb`? */
function isDestructiveInvocation(verb, tokens) {
  switch (verb) {
    case 'checkout': {
      // -b/-B/--orphan create or switch branches — never discard content.
      if (tokens.some((t) => t === '-b' || t === '-B' || t === '--orphan')) return false;
      // `--` introduces pathspecs; -f/--force overwrites the working tree;
      // a bare `.` or any path-shaped bareword is a path checkout (discard).
      if (tokens.includes('--')) return true;
      if (tokens.some((t) => t === '-f' || t === '--force')) return true;
      if (tokens.some((t) => !t.startsWith('-') && looksLikePath(t))) return true;
      return false; // `git checkout <branch>` — a safe switch
    }
    case 'restore': {
      // --staged (only) just unstages; --worktree or the default restores the
      // working tree and discards changes.
      const staged = tokens.some((t) => t === '--staged' || t === '-S');
      const worktree = tokens.some((t) => t === '--worktree' || t === '-W');
      if (staged && !worktree) return false; // unstage-only is safe
      return true; // default target is the working tree
    }
    case 'reset':
      return tokens.some((t) => t === '--hard' || t === '--keep' || t === '--merge');
    case 'clean': {
      const dryRun = tokens.some((t) => t === '-n' || t === '--dry-run');
      if (dryRun) return false;
      // A real clean needs -f (or a combined short flag containing f, e.g. -fd).
      return tokens.some((t) => /^-[a-eg-z]*f[a-z]*$/i.test(t) || t === '--force');
    }
    case 'stash': {
      const sub = tokens.find((t) => !t.startsWith('-')) || 'push'; // bare `git stash` == push
      return sub === 'push' || sub === 'save';
    }
    default:
      return false;
  }
}

function safeAlternativeFor(verb) {
  switch (verb) {
    case 'checkout':
    case 'restore':
      return 'To undo a change safely: copy first (`cp file file.bak`), edit, and `mv file.bak file` to revert — a scratch copy never touches HEAD. To just switch branches, no pathspec/`-f`/`--` is needed.';
    case 'reset':
      return 'To move HEAD without losing work, use a soft/mixed reset (no `--hard`/`--keep`/`--merge`). To recover a specific file, use a scratch copy, not `--hard`.';
    case 'clean':
      return 'Run `git clean -n` (dry-run) first to see what would be deleted; add wanted files with `git add` before any real clean. An un-added new file is the easiest thing to lose.';
    case 'stash':
      return 'Do NOT stash in a shared multi-agent tree — it sweeps up every other session\'s uncommitted work. Commit your own work first, or use a scratch copy for a temporary "before" state.';
    default:
      return 'Commit or scratch-copy your work before any command that reverts to HEAD.';
  }
}

function main() {
  if (process.env.CLAUDE_ALLOW_WORKTREE_RESET === '1') allow();

  let payload;
  try {
    payload = JSON.parse(readStdin() || '{}');
  } catch {
    allow(); // unparsable hook input is not this safety net's call to make
    return;
  }

  const command = String((payload.tool_input || {}).command || '');
  if (!command.trim()) allow();

  let texts;
  try {
    texts = ag.gatherScanTexts(command, 0);
  } catch (err) {
    process.stderr.write(`worktree-guard: could not scan command, allowing — ${err.message}\n`);
    allow();
    return;
  }

  for (const text of texts) {
    // Find each git executable token (bare `git`, `git.exe`, a path to it, or
    // a quoted path) and parse the invocation from just after it, reusing the
    // shared recogniser's -c/-C/--git-dir handling and alias resolution.
    const re = /(?:^|[\s;&|(])((?:"[^"]*"|'[^']*'|[^\s;&|()]*[\\/])?git(?:\.exe|\.cmd)?)(?=\s)/gi;
    let m;
    while ((m = re.exec(text)) !== null) {
      let inv;
      try {
        inv = ag.parseGitInvocation(text, m.index + m[0].length);
      } catch {
        continue;
      }
      if (!inv) continue;
      let verb;
      try {
        verb = ag.resolveAlias(inv.verbWord, 0, new Set());
      } catch {
        verb = inv.verbWord;
      }
      verb = String(verb || '').toLowerCase();
      if (!DESTRUCTIVE_VERBS.has(verb)) continue;

      const suffix = text.slice(inv.verbEnd);
      // Stop the arg scan at the next command separator so a chained
      // `git checkout branch; rm x` doesn't fold the second command's tokens in.
      const sepIdx = suffix.search(/[;&|]|\)/);
      const argStr = sepIdx === -1 ? suffix : suffix.slice(0, sepIdx);
      const tokens = argTokens(argStr);

      if (isDestructiveInvocation(verb, tokens)) {
        deny(verb, argStr.trim() || tokens.join(' '), safeAlternativeFor(verb));
      }
    }
  }

  allow();
}

if (require.main === module) {
  try {
    main();
  } catch (err) {
    // Fail OPEN — a safety net must never brick the session with its own bug.
    process.stderr.write(`worktree-guard: internal error, allowing — ${err.message}\n`);
    process.exit(0);
  }
}

module.exports = {
  DESTRUCTIVE_VERBS,
  argTokens,
  looksLikePath,
  isDestructiveInvocation,
  safeAlternativeFor,
};
