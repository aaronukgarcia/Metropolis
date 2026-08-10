/**
 * PreToolUse hook — commit-identity guard (BOW mkey: tool.authorguard).
 *
 * Spec: BUG-035; GR#2 (Version/Identity Discipline); GR#15 (Validators
 * Derive From Data — no hardcoded expected values).
 *
 * WHY THIS EXISTS — the evidence, not the principle.
 *
 * On 2026-08-10 an agent verifying git behaviour made a local test commit
 * authored as `test <test@test.com>` and left it on local main — the
 * cleanup checklist covered the worktree, the temp git config and the refs
 * it had consciously created, but not the COMMIT, which is a side effect of
 * the ACTION rather than of the setup (BUG-035). It never reached origin and
 * the content was verified identical to what the PR already carried, so
 * nothing was lost. But this repository went PUBLIC the same day, and its
 * history was rewritten hours earlier specifically to control which
 * identity appears in it. A fabricated author reaching origin would have
 * undone part of that, permanently, on a repo anyone can read and clone.
 * That is the exposure this guard exists to prevent — mechanically, the
 * same way claude-codename-guard.js turned "remember not to write the name"
 * into "the tool checks."
 *
 * WHAT COUNTS AS "SANCTIONED" — DERIVED, NOT HARDCODED (GR#15).
 *
 * The sanctioned set is a set of email addresses (case-insensitive; see the
 * matching note below), built at runtime as the UNION of two sources, plus
 * one explicit escape hatch:
 *
 *   1. THE CURRENTLY CONFIGURED GIT IDENTITY on this machine/repo —
 *      `git config user.email` (local .git/config takes precedence over
 *      global ~/.gitconfig automatically; that is git's own resolution
 *      order and this guard does not second-guess it). This is trusted
 *      unconditionally, with no threshold, because it is operator-set data
 *      that lives OUTSIDE the git command text this guard is evaluating —
 *      a Bash/PowerShell command an agent submits for approval cannot rewrite
 *      it (see claude-secret-guard.js's header for the same reasoning about
 *      why inline env vars in the proposed command never reach this
 *      process). It is also the reason this guard cannot simply "read the
 *      dominant identity out of git history" and stop there: on THIS repo,
 *      history is dominated 106-to-1 by a GitHub noreply address applied
 *      during the public-repo history rewrite, but the actual configured
 *      git identity — and the one every future ordinary `git commit` on
 *      this machine will actually produce — is a different, real address.
 *      A frequency-only derivation would have sanctioned the wrong one and
 *      then blocked every normal commit made afterwards. Config is the
 *      ground truth for "what does an unmodified `git commit` produce here."
 *
 *   2. EMAILS SEEN REPEATEDLY IN THE TRUNK BRANCH'S OWN HISTORY (`main` if
 *      it exists locally, else `master`, else the current branch) — as
 *      author OR committer, counted separately, over HISTORY_THRESHOLD
 *      (3) commits. The threshold exists so that a single (or a small
 *      handful of) fabricated commit(s) reaching the trunk — the exact
 *      failure mode this guard exists to prevent — can never quietly
 *      grandfather itself into the allowlist by having existed once. Under
 *      normal operation this is moot: this guard runs on every `git commit`
 *      going forward, so a fabricated identity is refused before it is ever
 *      created, and can never reach history to be counted at all. The
 *      threshold only matters if the guard was bypassed (see
 *      CLAUDE_DISABLE_AUTHOR_GUARD below) — it caps how much damage a single
 *      bypassed commit can do to future trust.
 *
 *   3. EXTENSION FOR A LEGITIMATE SECOND CONTRIBUTOR — a list, not a single
 *      slot, and not a code edit: the env var CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES,
 *      a comma- or semicolon-separated list of email addresses (bare, or
 *      "Name <email>" — only the email is read), set in the environment of
 *      the harness process itself (the same operator-only escape hatch
 *      claude-secret-guard.js documents for CLAUDE_DISABLE_SECRET_GUARD —
 *      never inside a proposed command string, which this process never
 *      sees). This is the "documented way to extend it" the brief asked
 *      for: add the new contributor's email to that env var (e.g. in
 *      metro.bat, or the shell profile that launches Claude Code) and their
 *      FIRST commit is already sanctioned — no waiting for history to
 *      accumulate, and no editing this file. Once their commits land on
 *      trunk and clear HISTORY_THRESHOLD, source (2) covers them even if
 *      the env var is later removed.
 *
 * MATCHING IS BY EMAIL ONLY, not name+email. Real data from this repo's own
 * history forced this call: the current HEAD commit at design time carries
 * author name "aaron garcia" and committer name "Aaron Garcia" — same
 * person, same email, different casing — which a name-inclusive match would
 * have flagged as a mismatch. Email is also the field that actually drives
 * public attribution (GitHub blame, commit search), which is the concrete
 * harm BUG-035 is about. Logged as ASM-author-guard-email-only.
 *
 * WHAT IT CHECKS, on `git commit` only (not `git rebase` — see below):
 *
 *   - The AUTHOR that commit would carry: from an explicit `--author=...`
 *     flag, or GIT_AUTHOR_NAME/GIT_AUTHOR_EMAIL set INLINE IN THE SAME
 *     COMMAND STRING (prefix form, `export`, or PowerShell `$env:...=`),
 *     which is exactly how BUG-035's stray commit got its identity. If
 *     neither is present, the author comes from git config same as the
 *     committer (checked once). `--amend` WITHOUT an explicit override and
 *     without `--reset-author` inherits the original commit's author
 *     unchanged — that author was already vetted when IT was created, so
 *     this guard does not re-check it (see the amend note below).
 *   - The COMMITTER that commit would carry: from GIT_COMMITTER_NAME/
 *     GIT_COMMITTER_EMAIL set the same way, else git config. There is no
 *     `--committer` flag on `git commit` — env vars or config are the only
 *     two paths, and both are checked. Committer identity is ALWAYS
 *     re-evaluated, including on `--amend` (git resets the committer to the
 *     current identity on every amend, by design — only the author can be
 *     inherited).
 *
 * `git rebase` is not intercepted at all: this guard only fires when the
 * command text contains a `git commit` invocation (mirroring
 * claude-secret-guard.js's GIT_COMMIT_RE), and `git rebase` replays commits
 * through internal plumbing, not through the `git commit` porcelain command
 * — so a rebase that preserves already-vetted authors is naturally
 * unaffected, with nothing special to special-case. Logged as
 * ASM-author-guard-rebase-scope.
 *
 * NOT HANDLED (out of scope for this version, logged as
 * ASM-author-guard-reuse-flags): `git commit -C <commit>` / `-c <commit>`,
 * which reuse an arbitrary OTHER commit's author. Not raised by BUG-035 or
 * the brief; revisit if it becomes a real path.
 *
 * FAIL-CLOSED, like claude-codename-guard.js and unlike
 * claude-dispatch-guard.js: the cost is asymmetric — a false block costs a
 * human seconds, a fabricated identity reaching public history is
 * permanent. An unparsable `--author` value (no recognisable `<email>`) is
 * treated as unverifiable and denied, not waved through. Any internal error
 * (git invocation failure, etc.) denies.
 *
 * Deliberate disable (genuine false positive / emergency only, never a
 * per-command agent bypass — see the reasoning in claude-secret-guard.js's
 * header for why a command-inline setting could never reach this process
 * anyway): CLAUDE_DISABLE_AUTHOR_GUARD=1, set in the harness process's own
 * environment before the session starts.
 *
 * Receives JSON on stdin: { tool: "Bash", tool_input: { command: "..." } }
 * Denies via: { hookSpecificOutput: { hookEventName: "PreToolUse",
 *               permissionDecision: "deny", permissionDecisionReason: "..." } }
 * (same convention as claude-codename-guard.js / claude-secret-guard.js)
 */

'use strict';

const fs = require('fs');
const { execFileSync } = require('child_process');

// Same shell-command-boundary-anchored shape already proven in
// claude-secret-guard.js / claude-version-guard.js / claude-plan-guard.js:
// the phrase must sit at the start of the command or right after a shell
// separator, so a quoted mention (e.g. inside a commit message being typed)
// never matches, but every real invocation still does. Extended (over the
// secret-guard original) with an optional run of inline env-var assignments
// between the boundary and "git" — `GIT_AUTHOR_NAME=x GIT_AUTHOR_EMAIL=y git
// commit` is exactly BUG-035's shape, and without this the whole command
// would simply fail to match (no boundary character directly precedes
// "git"), which would silently allow it rather than deny it.
const GIT_COMMIT_RE =
  /(?:^|[;&|(\n])(?:\s*(?:export\s+)?[A-Za-z_][A-Za-z0-9_]*=(?:"[^"]*"|'[^']*'|\S+))*\s*git\s+(?:-C\s+\S+\s+)?commit\b/i;

const ENV_VAR_NAMES = [
  'GIT_AUTHOR_NAME',
  'GIT_AUTHOR_EMAIL',
  'GIT_COMMITTER_NAME',
  'GIT_COMMITTER_EMAIL',
];

// See header: a single (or few) fabricated commit(s) reaching trunk must
// not silently earn a permanent place in the allowlist.
const HISTORY_THRESHOLD = 3;

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

function readStdin() {
  try {
    return fs.readFileSync(0, 'utf8');
  } catch {
    return '';
  }
}

function git(args) {
  return execFileSync('git', args, {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'ignore'],
  }).trim();
}

/**
 * Locate the `git commit` invocation inside a full command string. Returns
 * { start, end } (character offsets) or null if there isn't one. `start` is
 * where the match begins (used to bound the env-var-override search to text
 * that precedes the invocation); `end` is just past the word "commit" (used
 * to bound the flag search to text that follows it).
 */
function findCommitInvocation(cmd) {
  const m = GIT_COMMIT_RE.exec(cmd);
  if (!m) return null;
  return { start: m.index, end: m.index + m[0].length };
}

/**
 * Pull GIT_AUTHOR_ and GIT_COMMITTER_ overrides out of the text that precedes
 * a `git commit` invocation — the only place they can apply (inline prefix,
 * `export VAR=...;`, or PowerShell `$env:VAR = '...'`). Bash tool state does
 * not persist between separate tool calls (see the harness docs), so an
 * override set in an EARLIER command has no effect here; only the same
 * command string counts, which is also exactly how BUG-035 happened.
 */
function extractEnvOverrides(prefix) {
  const out = {};
  for (const name of ENV_VAR_NAMES) {
    // POSIX: NAME=value / NAME="value" / NAME='value', optionally after
    // `export `. PowerShell: $env:NAME = 'value' / "value".
    const posix = new RegExp(
      `\\b${name}\\s*=\\s*(?:"([^"]*)"|'([^']*)'|(\\S+))`,
      'g'
    );
    const pwsh = new RegExp(
      `\\$env:${name}\\s*=\\s*(?:"([^"]*)"|'([^']*)'|(\\S+))`,
      'gi'
    );
    let last = null;
    for (const re of [posix, pwsh]) {
      let m;
      re.lastIndex = 0;
      while ((m = re.exec(prefix)) !== null) {
        last = m[1] ?? m[2] ?? m[3] ?? '';
      }
    }
    if (last !== null) out[name] = last;
  }
  return out;
}

/**
 * Pull `--author=<value>` / `--author <value>` out of the text that follows
 * "commit". Returns:
 *   - null              — no --author flag present
 *   - { raw, email }     — flag present, email extracted from "<...>"
 *   - { raw, email: null } — flag present but no "<email>" could be found
 *     (unverifiable — the caller must fail closed on this).
 */
function extractAuthorFlag(suffix) {
  const re = /--author[=\s]+(?:"([^"]*)"|'([^']*)'|(\S+))/;
  const m = re.exec(suffix);
  if (!m) return null;
  const raw = m[1] ?? m[2] ?? m[3] ?? '';
  const emailMatch = /<([^<>]+)>/.exec(raw);
  return { raw, email: emailMatch ? emailMatch[1] : null };
}

function hasFlag(suffix, flag) {
  return new RegExp(`(?:^|\\s)${flag}(?:\\s|$)`).test(suffix);
}

/** The email git config would actually use right now (local, then global). */
function configuredEmail() {
  try {
    const e = git(['config', 'user.email']);
    return e || null;
  } catch {
    return null;
  }
}

/** Trunk branch to derive history from: main, then master, then current HEAD. */
function trunkBranch() {
  for (const name of ['main', 'master']) {
    try {
      git(['show-ref', '--verify', '--quiet', `refs/heads/${name}`]);
      return name;
    } catch {
      /* try next */
    }
  }
  try {
    return git(['rev-parse', '--abbrev-ref', 'HEAD']);
  } catch {
    return null; // unborn HEAD — no commits yet, nothing to derive
  }
}

/** Emails appearing >= HISTORY_THRESHOLD times (author or committer, counted
 * together) in the trunk branch's history. Empty set on a brand-new repo. */
function historyEmails() {
  const branch = trunkBranch();
  if (!branch) return new Set();
  let raw;
  try {
    raw = git(['log', branch, '--format=%ae%n%ce']);
  } catch {
    return new Set(); // no commits yet
  }
  const counts = new Map();
  for (const line of raw.split(/\r?\n/)) {
    const key = line.trim().toLowerCase();
    if (!key) continue;
    counts.set(key, (counts.get(key) || 0) + 1);
  }
  const out = new Set();
  for (const [email, count] of counts) {
    if (count >= HISTORY_THRESHOLD) out.add(email);
  }
  return out;
}

/** Operator-set extension list. See header — never read from the command
 * text being evaluated, only from this process's own environment. */
function extraIdentities() {
  const raw = process.env.CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES || '';
  const out = new Set();
  for (const entry of raw.split(/[;,]/)) {
    const trimmed = entry.trim();
    if (!trimmed) continue;
    const emailMatch = /<([^<>]+)>/.exec(trimmed);
    const email = (emailMatch ? emailMatch[1] : trimmed).trim().toLowerCase();
    if (email) out.add(email);
  }
  return out;
}

/** Build the full sanctioned set: config identity (unconditional) UNION
 * history (thresholded) UNION the operator extension list. */
function deriveSanctioned() {
  const set = new Set();
  const cfg = configuredEmail();
  if (cfg) set.add(cfg.trim().toLowerCase());
  for (const e of historyEmails()) set.add(e);
  for (const e of extraIdentities()) set.add(e);
  return set;
}

function main() {
  if (process.env.CLAUDE_DISABLE_AUTHOR_GUARD === '1') allow();

  let payload;
  try {
    payload = JSON.parse(readStdin() || '{}');
  } catch {
    allow(); // unparsable hook input is not this guard's call to make
  }

  const cmd = String((payload.tool_input || {}).command || '');
  const invocation = findCommitInvocation(cmd);
  if (!invocation) allow(); // not a `git commit` — includes `git rebase`, see header

  // Scanned up to (and including) the matched "commit" text rather than
  // just up to invocation.start: the match itself now swallows any inline
  // env-var assignments that precede "git" (see GIT_COMMIT_RE), and there
  // is no VAR=value-shaped text inside "git commit" itself to false-match.
  const prefix = cmd.slice(0, invocation.end);
  const suffix = cmd.slice(invocation.end);

  const isAmend = hasFlag(suffix, '--amend');
  const hasResetAuthor = hasFlag(suffix, '--reset-author');

  const envOverrides = extractEnvOverrides(prefix);
  const authorFlag = extractAuthorFlag(suffix);

  const sanctioned = deriveSanctioned();
  if (sanctioned.size === 0) {
    deny(
      '🛑 AUTHOR GUARD: could not derive ANY sanctioned identity (no git ' +
        'config user.email, no qualifying history, no ' +
        'CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES). Failing closed — set ' +
        '`git config user.email` and retry.'
    );
  }

  const problems = [];

  function checkEmail(email, field) {
    if (!email) {
      problems.push(
        `${field} override was present but no "<email>" could be parsed ` +
          `from it — unverifiable, and this guard fails closed on anything ` +
          `it cannot verify. Use the "Name <email>" form explicitly.`
      );
      return;
    }
    if (!sanctioned.has(email.trim().toLowerCase())) {
      problems.push(
        `${field} "${email}" is not a sanctioned identity for this repo.`
      );
    }
  }

  // --- COMMITTER: env override, else config. Always checked, including on
  // --amend (git always resets the committer on amend). ---
  if (envOverrides.GIT_COMMITTER_EMAIL !== undefined) {
    checkEmail(envOverrides.GIT_COMMITTER_EMAIL, 'GIT_COMMITTER_EMAIL');
  } else {
    checkEmail(configuredEmail(), 'the committer (from git config user.email)');
  }

  // --- AUTHOR: explicit override (flag or env) always checked. Absent an
  // override, --amend without --reset-author inherits the prior commit's
  // (already-vetted) author and is NOT re-checked; every other case falls
  // back to config, same as an ordinary commit. ---
  const authorOverridden =
    authorFlag !== null || envOverrides.GIT_AUTHOR_EMAIL !== undefined;

  if (authorFlag !== null) {
    checkEmail(authorFlag.email, '--author');
  } else if (envOverrides.GIT_AUTHOR_EMAIL !== undefined) {
    checkEmail(envOverrides.GIT_AUTHOR_EMAIL, 'GIT_AUTHOR_EMAIL');
  } else if (!isAmend || hasResetAuthor) {
    checkEmail(configuredEmail(), 'the author (from git config user.email)');
  }
  // else: amend, no override, no --reset-author — author is inherited from
  // HEAD and was already checked when that commit was created. Not
  // re-checked here (see header note on amend).
  void authorOverridden; // (kept named for readability of the branch above)

  if (problems.length) {
    deny(
      `🛑 AUTHOR GUARD (BUG-035) — commit identity not sanctioned ` +
        `(${problems.length}):\n\n` +
        problems.map((p) => `  - ${p}`).join('\n') +
        `\n\nSanctioned identities for this repo (derived from git config ` +
        `+ trunk history + CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES):\n` +
        [...sanctioned].map((e) => `  - ${e}`).join('\n') +
        `\n\nA fabricated or unexpected identity reaching this repo's ` +
        `history cannot be withdrawn once pushed — this repo is public. ` +
        `If this is a genuine new contributor, add their email to ` +
        `CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES in the environment that ` +
        `launches Claude Code (see this file's header) — do not edit this ` +
        `guard.\n\nDeliberate bypass (genuine false positive only): ` +
        `CLAUDE_DISABLE_AUTHOR_GUARD=1`
    );
  }

  allow();
}

if (require.main === module) {
  try {
    main();
  } catch (err) {
    // Fail closed — see header on cost asymmetry.
    deny(
      `🛑 AUTHOR GUARD (BUG-035) internal error: ${err.message}\n\n` +
        `Failing closed deliberately. Bypass only if you have checked by ` +
        `hand: CLAUDE_DISABLE_AUTHOR_GUARD=1`
    );
  }
} else {
  module.exports = {
    GIT_COMMIT_RE,
    findCommitInvocation,
    extractEnvOverrides,
    extractAuthorFlag,
    hasFlag,
    configuredEmail,
    trunkBranch,
    historyEmails,
    extraIdentities,
    deriveSanctioned,
    HISTORY_THRESHOLD,
  };
}
