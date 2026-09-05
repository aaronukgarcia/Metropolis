// Module key: tool.pingcheck (see code.json; GUID dd3cef45-baf0-4a24-ae8e-67c4c84add1a)
// Spec ref: M0-ENG §5 (hooks)

/**
 * claude-ping-check.js - PostToolUse hook for DHCP-style permit auto-renewal
 *
 * Runs after every tool use. Checks if 2 minutes have passed since the last
 * renewal and, if so, calls `node claude-sync.js renew --auto` (which renews
 * only when < 3.5 min remaining on the 5-min TTL permit, and re-acquires the
 * window's previous name if the permit expired while idle — wake recovery).
 *
 * This implements the DHCP "renewal at T/2" pattern:
 *   - Permit TTL = 5 min
 *   - Hook interval = 2 min (checked on every tool use)
 *   - renew --auto threshold = 3.5 min remaining
 *   - Result: permit renews at the first check after 1.5 min elapsed
 *
 * PER-WINDOW IDENTITY (v2.2): every Claude Code hook receives its window's
 * session_id in the stdin JSON. We pass it to claude-sync via the
 * CLAUDE_CODE_SESSION_ID env var, so claude-sync resolves THIS window's permit
 * from the session map — two windows can no longer cross-renew each other via
 * the shared .claude-session-key file. The renewal throttle file is also
 * per-window, so one window's throttle can't starve another's renewals
 * (that starvation is how Bill's permit silently expired on 2026-07-13).
 *
 * Fast path (< 2 min since last check): single file read. No Firestore hit.
 *
 * BUG-343 (silent-failure, GR#1 aggressive error trapping + GR#17 silent-failure
 * detection): wake recovery in claude-sync `renew --auto` fails LOUD — it writes
 * a `console.error` explanation and `process.exit(1)` — when the window's previous
 * slot is retired/parked or has been taken by another window (the live path after
 * every laptop sleep/resume). `execSync` throws on that nonzero exit. This hook
 * USED TO swallow that throw in an empty `catch {}`, so the session silently lost
 * its permit with no notice of any kind. It now SURFACES the failure: the failed
 * command's own explanation plus a "WAKE RECOVERY FAILED — permit LOST" banner is
 * written to process.stdout (the channel this hook relays to the session — see the
 * success path below, which surfaces wake-recovery notices the same way) and to
 * stderr. The hook still stays fail-open for the tool call (exit 0) so a coord
 * outage never blocks the user's work.
 */

const fs = require('fs');
const path = require('path');
const { execSync } = require('child_process');

const CHECK_INTERVAL_MS = 2 * 60 * 1000;  // Check every 2 minutes
// The claude-sync script and the .claude throttle dir are overridable via env so
// the regression test (claude-ping-check.test.js) can drive the failure path
// end-to-end against a stub that exits nonzero, without a live metro DB.
const SYNC_SCRIPT = process.env.CLAUDE_PING_SYNC_SCRIPT || path.join(__dirname, 'claude-sync.js');

/**
 * BUG-486: read this window's own server-issued session secret straight from
 * its per-user session-key file, so it can be presented as an explicit
 * `--session` flag on the `renew --auto` call below. THIS is the actual fix
 * for BUG-486 (root cause: cmdRenew in claude-sync.js resolves the caller's
 * permit ONLY via a presented --session secret — never via ambient
 * CLAUDE_CODE_SESSION_ID env — and this hook used to call `renew --auto` with
 * env only, so it silently renewed nothing, EVER).
 *
 * The fix deliberately does NOT live in claude-sync.js's cmdRenew: making
 * cmdRenew trust WINDOW_ID + key-file possession on its own would reopen the
 * exact hole BUG-354 r4/r6 closed (claude-sync.test.js's 'BUG-354 r4 W-3' and
 * 'BUG-354 r5 F4' both model an attacker who merely knows/guesses a victim's
 * window id and sets it in env with no secret of their own — on the same
 * machine the key file is addressed purely by that guessable id string, so
 * "possession" of it proves nothing once the id leaks). This hook, in
 * contrast, genuinely IS the window it is renewing for — it received this
 * exact window's session_id from Claude Code's own hook stdin payload two
 * lines below, not from a value some other process merely claimed — so
 * reading and presenting ITS OWN key file is the same possession proof an
 * operator or the test suite's own renewAuto() helper already uses manually;
 * it changes nothing about cmdRenew's trust boundary, only who is allowed to
 * self-serve calling it.
 *
 * Best-effort/fail-open by design (matches every other read in this file):
 * a missing key file (fresh window, pre-first-checkin) just means no
 * --session flag is added and the call behaves exactly as before (falls
 * through to claude-sync's own "nothing to renew" notice) — never a reason
 * to crash the hook.
 *
 * GR#3 (Single Source of Truth): the key-file path formula (per-user
 * os.homedir()/.claude/session-keys/.session-key-<windowId>[-<dbTag>]) is
 * NOT reimplemented here — it is required straight from claude-sync.js's own
 * `readSessionKey` export, the exact function acquire()/ensureSessionKey()
 * use to read/write that same file, so the two can never drift apart. This
 * intentionally requires the REAL claude-sync.js next to this file, NOT the
 * overridable `SYNC_SCRIPT` (which claude-ping-check.test.js points at a
 * bare stub for its execSync-failure tests — that stub has no such export
 * and would throw at require-time if used here).
 */
function readOwnSessionSecret(windowId) {
  if (!windowId) return '';
  try {
    const { readSessionKey } = require(path.join(__dirname, 'claude-sync.js'));
    return readSessionKey(windowId);
  } catch {
    return ''; // claude-sync.js missing/broken or no key file yet — never crash the hook over it
  }
}
const DOT_CLAUDE_DIR = process.env.CLAUDE_PING_DIR || path.join(__dirname, '.claude');

/** Read the hook's stdin JSON (Claude Code always provides it and closes the pipe). */
function readStdin(cb) {
  if (process.stdin.isTTY) return cb('');  // manual run — no hook payload
  let input = '';
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', chunk => { input += chunk; });
  process.stdin.on('end', () => cb(input));
  // Safety net: never hang the agent if stdin misbehaves
  setTimeout(() => cb(input), 2000).unref();
}

/**
 * BUG-343: build the warning text for a `renew --auto` failure so the hook can
 * SURFACE it instead of swallowing it. Always returns a non-empty, multi-line
 * string. Two failure shapes are distinguished:
 *
 *   - Wake-recovery REFUSAL — claude-sync reached a verdict and exited nonzero
 *     (previous slot retired/parked, or held by another window after a wake).
 *     The permit is GONE and the session must be told, loudly, carrying
 *     claude-sync's own explanation and the recovery command.
 *   - Non-verdict failure — a spawn error / timeout with no numeric exit status.
 *     No permit decision was reached, but it still must not vanish in silence.
 *
 * @param {any} err  the error thrown by execSync (has .status/.stderr/.stdout/.message)
 * @param {string} windowId  this window's session id (may be '')
 * @returns {string} the warning to surface
 */
function describeRenewFailure(err, windowId) {
  const sess = windowId ? ` (session ${windowId})` : '';
  const stderrText = err && err.stderr != null ? String(err.stderr).trim() : '';
  const stdoutText = err && err.stdout != null ? String(err.stdout).trim() : '';
  const detail = [stderrText, stdoutText].filter(Boolean).join('\n');
  const status = err && typeof err.status === 'number' ? err.status : null;
  if (status !== null && status !== 0) {
    // A verdict was reached and it refused: the permit for this window is lost.
    return `[claude-ping-check] WAKE RECOVERY FAILED — permit LOST for this window${sess}.\n` +
      (detail ? detail + '\n' : '') +
      `[claude-ping-check] Re-checkin explicitly: node claude-sync.js checkin --name <live slot>`;
  }
  // No exit verdict — spawn failure, timeout, or similar. Still surface it (GR#1).
  const msg = err && err.message ? String(err.message).trim() : 'unknown error';
  return `[claude-ping-check] permit renew check could not complete${sess}: ${msg}` +
    (detail ? `\n${detail}` : '');
}

function main() {
  // Ensure .claude directory exists
  if (!fs.existsSync(DOT_CLAUDE_DIR)) {
    fs.mkdirSync(DOT_CLAUDE_DIR, { recursive: true });
  }

  let done = false;
  readStdin((input) => {
    if (done) return;
    done = true;

    // Window ID: prefer the hook payload, fall back to inherited env
    let windowId = process.env.CLAUDE_CODE_SESSION_ID || '';
    try {
      const data = JSON.parse(input);
      if (data.session_id) windowId = data.session_id;
    } catch { /* no/invalid payload — env fallback stands */ }

    // Per-window throttle file: a shared one would let window A's ping suppress
    // window B's renewals entirely.
    const pingFile = path.join(DOT_CLAUDE_DIR, windowId ? `.last-ping-${windowId}` : '.last-ping');

    let lastPingMs = 0;
    try {
      lastPingMs = parseInt(fs.readFileSync(pingFile, 'utf8').trim(), 10) || 0;
    } catch { /* first run */ }

    const nowMs = Date.now();
    if (nowMs - lastPingMs < CHECK_INTERVAL_MS) {
      process.exit(0);  // Fast path: not time yet
    }

    // Update timestamp FIRST to avoid retry spam on network errors
    fs.writeFileSync(pingFile, String(nowMs), 'utf8');

    // BUG-486: present this window's own server-issued session secret
    // explicitly, exactly as an operator/the test suite's renewAuto() helper
    // would — claude-sync's cmdRenew resolves permits ONLY via a presented
    // --session secret, never via ambient env, so without this the renew
    // call below has always silently resolved nothing (see
    // readOwnSessionSecret's doc comment above for why this fix lives here
    // and not by loosening claude-sync's trust model). Validate the UUID
    // shape before it ever reaches a shell-interpolated command string —
    // crypto.randomUUID() (the only thing that ever writes this file) always
    // produces this shape, so a mismatch means a corrupted/tampered file and
    // the flag is simply omitted rather than trusted.
    const ownSecret = readOwnSessionSecret(windowId);
    const sessionArg = /^[0-9a-f-]{36}$/i.test(ownSecret) ? ` --session "${ownSecret}"` : '';

    try {
      const output = execSync(`node "${SYNC_SCRIPT}" renew --auto${sessionArg}`, {
        encoding: 'utf8',
        timeout: 15000,
        cwd: __dirname,
        stdio: ['ignore', 'pipe', 'pipe'],
        env: { ...process.env, CLAUDE_CODE_SESSION_ID: windowId }
      });
      if (output.trim()) {
        // Wake-recovery notices (re-acquired name / IDENTITY CHANGED) surface here
        process.stdout.write(output);
      }
    } catch (err) {
      // BUG-343: do NOT swallow. Wake recovery fails LOUD (console.error + exit 1)
      // when the previous slot is unavailable after a wake; execSync then throws on
      // the nonzero exit. An empty catch here silently loses the permit — a GR#1
      // (aggressive error trapping) + GR#17 (silent-failure detection) violation.
      // Surface it on stdout (the channel this hook relays to the session, exactly
      // as the success path above does) AND stderr, so the session SEES
      // "WAKE RECOVERY FAILED — permit LOST" instead of nothing. The hook still
      // stays fail-open for the tool call: exit 0, and .last-ping is already
      // updated so this never retry-spams.
      const warning = describeRenewFailure(err, windowId);
      process.stdout.write(warning + '\n');
      process.stderr.write(warning + '\n');
    }
    process.exit(0);
  });
}

if (require.main === module) {
  main();
}

module.exports = { describeRenewFailure, readOwnSessionSecret };
