// Module key: tool.startup (see code.json; GUID 7220138c-1822-4cc1-9e5e-b17714e2f42b)
// Spec ref: M0-ENG §5 (hooks)

// claude-startup.js — SessionStart hook script
// Runs checkin, validates identity, writes identity file, outputs confirmation for Claude
//
// v2.2: reads the Claude window's session_id from the hook stdin JSON and passes it
// to claude-sync as CLAUDE_CODE_SESSION_ID, so the checkin is mapped to THIS window
// (per-window identity — no manual $env:CLAUDE_SESSION_ID needed). Fallback checkins
// use --any: the launcher sets CLAUDE_IDENTITY=Bill in every window, and without
// --any a "plain" checkin silently re-requests Bill and loops into the same rejection.
const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');
// FEAT-045 AC-14: the commit-msg hook's install/survival state must reach a
// human without them having to remember to check for it — wired into the
// unconditional session-start summary below, in emitSuccess().
const committhook = require('./claude-committhook-install.js');
// FEAT-038: the startup summary already reports git SYNC state (branch,
// ahead/behind, uncommitted count) via claude-sync.js's own checkin output
// (relayed verbatim below) -- it never reported WHO the repo is configured
// to commit as. BUG-036 sat undetected because nothing checked that. Reuses
// claude-author-identity.js's shared sanctioned-identity derivation (GR#3 --
// one derivation, already consumed by the commit-msg hook and the demoted
// PreToolUse guard) rather than a third, independent identity check.
const identity = require('./claude-author-identity.js');
// BUG-344 / BUG-354 D2: the slot roster is DERIVED from claude-sync.js's own
// NAMES constant (single source of truth) — this file once hand-listed
// ['bill','ben','bev'], which silently missed Bro (added 2026-08-20) and
// would have treated a Bro-assigned startup checkin as a failure. claude-sync
// only loads mysql2/claude-db at require time (no DB connection until a
// command runs), so this is cheap and stops the two files from drifting.
const { NAMES, isUnusable, readSessionKey } = require('./claude-sync.js');
const VALID_NAMES = NAMES.map(n => n.toLowerCase());
// Live (occupiable) slot names, capitalised — parked/retired excluded — for
// the all-full and fallback messaging, so Ben (PARKED) and Bob (RETIRED) never
// appear as occupiable slots.
const LIVE_NAMES = NAMES.filter(n => !isUnusable(n)).map(n => n.toLowerCase());

const projectRoot = __dirname;
const identityPath = path.join(projectRoot, '.claude', '.identity');

/** Claude window UUID — resolved from hook stdin JSON before main logic runs. */
let windowId = process.env.CLAUDE_CODE_SESSION_ID || '';

/** Run a checkin command, returning { output, error, stderr }.
 *  BUG-124: args is an argv array (e.g. ['claude-sync.js', 'checkin', '--name', requestedIdentity])
 *  passed to execFileSync with no shell, so no value reaching this function --
 *  including an operator-controlled CLAUDE_IDENTITY -- can break out into shell
 *  metacharacters ( &, |, ;, `, $(), etc.). Never rebuild this as a template
 *  string handed to execSync/exec — that reintroduces the injection. */
/** Read this window's per-window session secret (BUG-354 r4, r5 F3). The
 *  startup checkin must present it as an explicit `--session` so checkin
 *  authenticates by secret, never by the ambient env CLAUDE_CODE_SESSION_ID —
 *  which any process can set to another window's value. A fresh window with no
 *  key file yet omits the flag (its checkin is a plain acquire, which writes
 *  the file). Delegates to claude-sync's readSessionKey: per-user location
 *  (os.homedir()/.claude/session-keys) with one-time legacy migration. */
function readSessionSecret() {
  return readSessionKey(windowId);
}

function tryCheckin(args) {
  const secret = readSessionSecret();
  const fullArgs = secret ? [...args, '--session', secret] : args;
  try {
    const output = execFileSync('node', fullArgs, {
      cwd: projectRoot,
      encoding: 'utf-8',
      timeout: 15000,
      env: { ...process.env, CLAUDE_CODE_SESSION_ID: windowId },
    });
    return { output, error: null, stderr: '' };
  } catch (err) {
    const stderr = (err.stderr || '') + (err.stdout || '');
    return { output: null, error: err, stderr };
  }
}

/** Parse the assigned name from successful checkin output */
function parseName(output) {
  const match = (output || '').match(/YOU ARE:\s*(\w[\w-]*)/i);
  const name = match ? match[1].toLowerCase() : null;
  return (name && VALID_NAMES.includes(name)) ? name : null;
}

/** True if stderr indicates all slots are occupied */
function isAllFull(stderr) {
  return stderr.includes('all-full')
    || stderr.includes('ALL SLOTS FULL')
    || stderr.includes('ALL PERMITS OCCUPIED');
}

/** True if stderr indicates a specific named slot is occupied or reserved for
 *  another window's possibly-idle holder — either way, take a different slot. */
function isNameOccupied(stderr) {
  return stderr.includes('SLOT IS OCCUPIED') || stderr.includes('name-occupied')
    || stderr.includes('SLOT IS RESERVED') || stderr.includes('name-reserved');
}

/**
 * Parse the maximum TTL (ms) from an all-full rejection stderr.
 * Matches patterns like "expires in 2m 19s" or "expires in 0m 45s".
 * Returns null if no TTL data found.
 */
function parseMaxTTLMs(stderr) {
  const matches = [...stderr.matchAll(/expires in (\d+)m (\d+)s/g)];
  if (!matches.length) return null;
  return Math.max(...matches.map(m => (parseInt(m[1]) * 60 + parseInt(m[2])) * 1000));
}

/**
 * Format a TTL in ms as "Xm Ys".
 */
function fmtMs(ms) {
  const s = Math.ceil(ms / 1000);
  return `${Math.floor(s / 60)}m ${s % 60}s`;
}

/**
 * FEAT-038: checks whether the CURRENTLY CONFIGURED git identity (`git
 * config user.email`, local then global -- identity.configuredEmail()) is
 * SANCTIONED, in the sense that matters for a startup check: corroborated by
 * something other than itself (this repo's trunk history, or the operator's
 * CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES env var).
 *
 * Deliberately NOT `identity.deriveSanctioned().has(configuredEmail())` --
 * deriveSanctioned() always adds configuredEmail() to its own returned set
 * (see that module's header, source 1: "trusted unconditionally"), so that
 * membership test would be true BY CONSTRUCTION regardless of what the
 * config value actually is. That is the right behaviour for the commit-time
 * guard/hook (config is the one source immune to a history rewrite -- see
 * BUG-036 in claude-author-identity.js's header) but it is exactly WRONG for
 * this check, whose entire job is to ask "does anything OTHER than the
 * config value itself corroborate this config value?" -- the same question
 * BUG-036 needed asked and nothing was asking.
 *
 * Returns:
 *   { ok: true,  email }                  -- configured, and corroborated
 *   { ok: false, email: null }            -- no git identity configured at all
 *   { ok: false, email }                  -- configured, but NOT corroborated
 *   { ok: false, email: null, error: true, message } -- internal error (no
 *     repo, git not on PATH, etc.) -- reported, not swallowed silently: a
 *     human is reading this output live at session start, so an unknown
 *     identity state is surfaced the same as a genuinely bad one rather than
 *     going quiet.
 */
function checkGitIdentity() {
  try {
    const email = identity.configuredEmail();
    if (!email) return { ok: false, email: null };
    const key = email.trim().toLowerCase();
    const corroborated = identity.historyEmails().has(key) || identity.extraIdentities().has(key);
    return { ok: corroborated, email };
  } catch (err) {
    return { ok: false, email: null, error: true, message: (err && err.message) || 'unknown error' };
  }
}

/** Formats checkGitIdentity()'s result as one printable line -- a clear,
 * loud warning when the identity is missing/unverified/unsanctioned
 * (mirroring how the summary already surfaces "git NOT SYNCED"), or a plain
 * positive confirmation when it checks out. */
function gitIdentityLine() {
  const result = checkGitIdentity();
  if (result.error) {
    return `⚠️ GIT IDENTITY: could not be verified (${result.message}). Run 'git config user.email' manually and check it yourself.`;
  }
  if (!result.email) {
    return `⚠️ GIT IDENTITY: NOT CONFIGURED — no 'git config user.email' found (local or global). Set one before committing.`;
  }
  if (!result.ok) {
    return `⚠️ GIT IDENTITY: configured as "${result.email}", which this repo's trunk history and CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES do NOT corroborate. This may be a misconfigured identity (see BUG-036) — verify before committing anything.`;
  }
  return `GIT IDENTITY: ${result.email} — sanctioned (corroborated by trunk history or CLAUDE_AUTHOR_GUARD_EXTRA_IDENTITIES).`;
}

const SUMMARY_MARKER = '── METROPOLIS STARTUP SUMMARY ──';

// FEAT-070 (tool.looparm): must be byte-identical to claude-sync.js's own
// LOOP_MARKER constant — this file never invokes claude-sync directly for
// loop state, it only lifts the already-printed block out of checkin's
// stdout (same technique as SUMMARY_MARKER above), because this hook cannot
// invoke the Claude Code `/loop` slash command itself (that is an agent-level
// tool call, not a shell-reachable action) — it can only print a mandatory
// instruction for the agent to act on, same trust boundary as step 1 below.
const LOOP_MARKER = '── STANDING LOOP ──';

/** Prints the block of console output emitSuccess() shows on every
 *  successful checkin, EXCLUDING the identity-file write (split out so
 *  FEAT-045 AC-14's test can capture this exact output — including the
 *  commit-msg hook's install/survival state — against a throwaway repo,
 *  without touching this machine's real .claude/.identity file, which is
 *  shared, session-coordination state other concurrent agents rely on). */
function printSessionSummary(name, checkinOutput, committhookRepoRoot) {
  console.log(`IDENTITY: ${name}>`);
  console.log(`PREFIX EVERY RESPONSE with "${name}>". No exceptions.`);
  console.log(`HOOKS: ACTIVE.`);
  // BUG-354 r4: relay this window's server-issued session secret into the
  // startup block so the agent can present it as an explicit `--session` for
  // manual identity commands (checkin/checkout/renew) — those commands no
  // longer trust the ambient env CLAUDE_CODE_SESSION_ID. Absent when the
  // checkin output has no Session line (fresh/plain-terminal acquire).
  const sessionMatch = (checkinOutput || '').match(/^Session: (\S+)$/m);
  if (sessionMatch) {
    console.log(`SESSION SECRET (for manual claude-sync identity commands — checkin --name / checkout / renew): ${sessionMatch[1]}`);
  }
  // FEAT-045 AC-14: printed unconditionally on every successful checkin, not
  // behind a skill/slash-command a human has to remember to run. The third
  // arg is test-only (lets a test point this at a throwaway repo instead of
  // the real one) — production call sites never pass it, so this always
  // checks the real installed hook in normal operation.
  console.log(committhook.summaryLine(committhookRepoRoot || projectRoot));
  // BUG-340/BUG-336 (deliverable 3): same unconditional-visibility treatment
  // for the pre-push GR#28 floor gate — printed as its OWN line (never
  // folded into the commit-msg line above) so absent/stale is distinguishable
  // per-hook, same AC-13 "never collapsed" reasoning.
  console.log(committhook.summaryLine(committhookRepoRoot || projectRoot, 'pre-push'));

  // FEAT-070 (AC-6/AC-7/AC-8): split the standing-loop status block (if any)
  // out of raw checkin stdout BEFORE the SUMMARY_MARKER slice-to-end below —
  // otherwise it would get swept into that block instead of landing inside
  // the numbered MANDATORY STARTUP SEQUENCE list where AC-6 requires it.
  // Absent entirely (loopContent === null) when the identity has no
  // sync_loop_config row — AC-7 requires zero output change in that case.
  let remaining = checkinOutput || '';
  let loopContent = null;
  const loopIdx = remaining.indexOf(LOOP_MARKER);
  if (loopIdx !== -1) {
    loopContent = remaining.slice(loopIdx + LOOP_MARKER.length).trim();
    remaining = remaining.slice(0, loopIdx).trimEnd();
  }

  const idx = remaining.indexOf(SUMMARY_MARKER);
  if (idx !== -1) {
    console.log(``);
    console.log(remaining.slice(idx).trim());
  } else {
    console.log(``);
    console.log(`WARNING: checkin returned no startup summary (BOW/Vestige/git state unknown).`);
    console.log(`Run it manually as your first action: node claude-bow.js startup-summary`);
  }

  // FEAT-038: printed unconditionally, same place/shape as the committhook
  // status line above -- git SYNC state (branch/ahead-behind/dirty count)
  // was already relayed verbatim from checkin output; this is the git
  // IDENTITY companion check that was missing (BUG-036).
  console.log(``);
  console.log(gitIdentityLine());

  console.log(``);
  console.log(`MANDATORY STARTUP SEQUENCE — DO ALL OF THESE BEFORE YOUR FIRST RESPONSE:`);
  console.log(`1. Use the mcp__vestige__search tool NOW with query "who am I, identity, session startup" to load memory.`);
  console.log(`2. Read CLAUDE.md for full Golden Rules.`);
  console.log(`3. Run 'node claude-sync.js read' to check coordination state.`);
  console.log(`4. Your first response to the user must confirm: identity, hooks status, the BOW summary above (metro DB health), Vestige status (live search worked), git sync state, and git identity status.`);
  // FEAT-070 (AC-6): a fresh, non-stale standing /loop spec becomes step 5 of
  // the SAME mandatory numbered block — not a separate, skippable aside. The
  // exact spec text is relayed verbatim from claude-sync.js's own arm check
  // (claude-sync.js:printLoopArmStatus), never re-derived here.
  if (loopContent && loopContent.startsWith('MANDATORY: invoke')) {
    console.log(`5. ${loopContent}`);
  }
  console.log(`If the summary above shows git NOT SYNCED, a Vestige problem, or a GIT IDENTITY warning, surface that to the user immediately.`);
  console.log(`DO NOT skip step 1. Memory recall is not optional. If Vestige tools are unavailable, state that explicitly.`);

  // FEAT-070 (AC-8): a STALE standing loop is withheld from the mandatory
  // block above and instead reported here, immediately alongside it, with
  // identity/spec/age/resolve-commands (all sourced verbatim from
  // claude-sync.js — this hook never re-derives staleness itself).
  if (loopContent && loopContent.startsWith('STALE STANDING LOOP')) {
    console.log(``);
    console.log(loopContent);
  }
}

/** Emit the mandatory startup instructions for a successfully-claimed identity.
 *  checkinOutput is the raw claude-sync checkin stdout — it carries the startup
 *  summary block (BOW state from the metro DB, Vestige check, git sync check),
 *  which is relayed verbatim so it reaches Claude's context every session.
 *  Writes the real identity file (this machine's actual session-coordination
 *  state) and then prints the SAME summary printSessionSummary() prints —
 *  see that function for FEAT-045 AC-14's test-only committhookRepoRoot
 *  override. */
function emitSuccess(name, checkinOutput, committhookRepoRoot) {
  // BUG-354 D3: identity is keyed PER-WINDOW. Write the per-window marker
  // (.identity-<session_id> — the same key claude-sync's acquire writes and
  // the statusline / prefix hooks read first) instead of clobbering the shared
  // .identity, which is cross-window last-checkin-wins state (Bill's 15m loop
  // rewriting it told a Bev-holding window to answer as "bill>" — the live
  // incident). This also covers the pure-renew startup path, where checkin
  // renews an existing permit without calling acquire, so the per-window
  // marker is still ensured to exist. A plain-terminal startup (no window id)
  // has no per-window key and falls back to the shared file, same as
  // claude-sync's writeIdentityFiles.
  if (windowId) {
    fs.writeFileSync(path.join(projectRoot, '.claude', `.identity-${windowId}`), name.toLowerCase(), 'utf-8');
  } else {
    fs.writeFileSync(identityPath, name, 'utf-8');
  }
  printSessionSummary(name, checkinOutput, committhookRepoRoot);
}

/**
 * Emit deferred-checkin instructions when all slots are occupied but expiring shortly.
 * Claude should run checkin as its first action in the conversation.
 */
function emitDeferredCheckin(maxTTLMs, stderrSnippet) {
  const waitSec = Math.ceil(maxTTLMs / 1000) + 5;
  console.log(`CHECKIN DEFERRED — All permits occupied but expire within ${fmtMs(maxTTLMs)}.`);
  console.log(stderrSnippet.trim());
  console.log(``);
  console.log(`YOU HAVE NO IDENTITY YET. Do not prefix responses until checkin succeeds.`);
  console.log(``);
  console.log(`ACTION: As your VERY FIRST action, run this command (slots will have expired by then):`);
  console.log(`  node claude-sync.js checkin --any`);
  console.log(``);
  console.log(`IF that still fails (race condition), you may force-evict with user authorisation:`);
  console.log(`  node claude-sync.js checkin --name Bill --force --human-ok`);
  console.log(`  AUTHORIZATION: User saying "yes", "go ahead", "proceed", selecting an option, or any`);
  console.log(`  explicit confirmation in the conversation counts as human-ok authorization.`);
  console.log(`  You DO NOT need to ask the user to type the command themselves.`);
  console.log(``);
  console.log(`Tell the user: "All slots just expired — running checkin now..." then run it immediately.`);
}

/** Emit the hard-blocked message when all slots are full with long TTLs */
function emitAllFull() {
  console.log(`ERROR: ALL PERMIT SLOTS ARE FULL (${LIVE_NAMES.join(', ')} all occupied, TTLs > 3 min).`);
  console.log(`YOU HAVE NO IDENTITY. DO NOT PREFIX RESPONSES WITH ANY NAME.`);
  console.log(`TELL THE USER IMMEDIATELY: "All ${NAMES.length} Claude slots are occupied. I cannot check in."`);
  console.log(`Ask the user to run: node claude-sync.js read  — to see who is active.`);
  console.log(`Do NOT proceed with any work until you have a valid permit.`);
}

/** Emit the hard-blocked message when checkin fails for a non-recoverable technical reason */
function emitTechnicalFailure(errMsg) {
  console.log(`ERROR: claude-sync.js checkin failed with a technical error.`);
  console.log(`Error: ${errMsg}`);
  console.log(`YOU HAVE NO CONFIRMED IDENTITY.`);
  console.log(`DO NOT use any previous identity — it may be stale or wrong.`);
  console.log(`TELL THE USER: "Session checkin failed: ${errMsg}. Please run: node claude-sync.js checkin manually."`);
  const livePrefixes = LIVE_NAMES.length > 1
    ? `${LIVE_NAMES.slice(0, -1).join('>, ')}, or ${LIVE_NAMES[LIVE_NAMES.length - 1]}>`
    : `${LIVE_NAMES[0]}>`;
  console.log(`You MUST still prefix responses with ${livePrefixes} once you have checked in successfully.`);
  console.log(`Read CLAUDE.md for full Golden Rules.`);
}

/**
 * Handle an all-full result: if TTLs are short, emit deferred-checkin;
 * otherwise emit hard-blocked all-full.
 */
function handleAllFull(stderr) {
  const maxTTL = parseMaxTTLMs(stderr);
  if (maxTTL !== null && maxTTL <= 3 * 60 * 1000) {
    // All slots expire within 3 minutes — defer checkin to first conversation turn
    emitDeferredCheckin(maxTTL, stderr);
  } else {
    // Long TTLs — hard block
    emitAllFull();
  }
}

// ─── Main Logic ───────────────────────────────────────────────────────────────

function runStartup() {

const requestedIdentity = process.env.CLAUDE_IDENTITY || null;

if (requestedIdentity) {
  // Step 1: Try to claim the specifically-requested identity
  const first = tryCheckin(['claude-sync.js', 'checkin', '--name', requestedIdentity]);

  if (first.output && parseName(first.output)) {
    // Got the requested slot — perfect
    emitSuccess(parseName(first.output), first.output);

  } else if (isNameOccupied(first.stderr)) {
    // Requested slot is taken (live holder) or reserved (idle holder may return)
    // — fall back to the next genuinely free slot
    console.log(`WARNING: ${requestedIdentity} slot is OCCUPIED or RESERVED by another session.`);
    // Derived from LIVE_NAMES (not hand-listed) so this line is correct
    // regardless of which identity was requested, and never drifts when the
    // slot roster changes (this hardcoded-Bob/Ben text is exactly what went
    // stale when Bob was retired — 2026-08-19; parked Ben is excluded too,
    // BUG-354 D2).
    const otherNames = LIVE_NAMES.filter(n => n.toLowerCase() !== requestedIdentity.toLowerCase());
    console.log(`Falling back to next available slot (${otherNames.join(' or ')})...`);

    const second = tryCheckin(['claude-sync.js', 'checkin', '--any']);

    if (second.output && parseName(second.output)) {
      const assigned = parseName(second.output);
      console.log(`NOTE: You requested "${requestedIdentity}" but that slot was taken.`);
      console.log(`You have been assigned "${assigned}" instead.`);
      emitSuccess(assigned, second.output);

    } else if (isAllFull(second.stderr)) {
      handleAllFull(second.stderr);

    } else {
      emitTechnicalFailure((second.error && second.error.message) || 'Unknown error on fallback checkin');
    }

  } else if (first.stderr && first.stderr.includes('FORCE-EVICT BLOCKED')) {
    console.log(`ERROR: Force-evict was blocked. Human authorisation required.`);
    console.log(`DO NOT USE "${requestedIdentity}>" prefix — you do not hold that permit.`);
    console.log(`TELL THE USER: "${requestedIdentity} slot is occupied and force-evict requires human authorisation."`);
    console.log(`The user must confirm ("yes", "go ahead", etc.) and then you may run:`);
    console.log(`  node claude-sync.js checkin --name ${requestedIdentity} --force --human-ok`);

  } else if (isAllFull(first.stderr)) {
    handleAllFull(first.stderr);

  } else {
    emitTechnicalFailure((first.error && first.error.message) || 'Unknown error');
  }

} else {
  // No identity preference — first-come, first-served
  const result = tryCheckin(['claude-sync.js', 'checkin']);

  if (result.output && parseName(result.output)) {
    emitSuccess(parseName(result.output), result.output);

  } else if (isAllFull(result.stderr)) {
    handleAllFull(result.stderr);

  } else {
    emitTechnicalFailure((result.error && result.error.message) || 'Unknown error');
  }
}

}

// ─── Entry: resolve this window's ID from the hook stdin JSON, then run ───────
//
// FEAT-045 AC-14: gated behind require.main === module so this file can be
// require()'d by claude-committhook-install.test.js (to exercise
// emitSuccess()'s captured output directly, against a throwaway repo) with
// zero production side effects — requiring it no longer starts a real
// checkin. Running it as a script (`node claude-startup.js`, or as the
// actual SessionStart hook) is completely unchanged.

if (require.main === module) {
  if (process.stdin.isTTY) {
    runStartup();  // manual run — no hook payload
  } else {
    let input = '';
    let started = false;
    const start = () => { if (!started) { started = true; runStartup(); } };
    process.stdin.setEncoding('utf-8');
    process.stdin.on('data', chunk => { input += chunk; });
    process.stdin.on('end', () => {
      try {
        const data = JSON.parse(input);
        if (data.session_id) windowId = data.session_id;
      } catch { /* env fallback stands */ }
      start();
    });
    // Never let a stuck pipe block the session from starting
    setTimeout(start, 3000).unref();
  }
} else {
  module.exports = { emitSuccess, emitAllFull, emitTechnicalFailure, printSessionSummary, projectRoot, VALID_NAMES, LIVE_NAMES, checkGitIdentity, gitIdentityLine, parseName };
}
