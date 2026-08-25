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
 */

const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');

const CHECK_INTERVAL_MS = 2 * 60 * 1000;  // Check every 2 minutes
const SYNC_SCRIPT = path.join(__dirname, 'claude-sync.js');

// Ensure .claude directory exists
const dotClaudeDir = path.join(__dirname, '.claude');
if (!fs.existsSync(dotClaudeDir)) {
  fs.mkdirSync(dotClaudeDir, { recursive: true });
}

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
  const pingFile = path.join(dotClaudeDir, windowId ? `.last-ping-${windowId}` : '.last-ping');

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

  // BUG-354 r4: pass the permit's server-issued session secret explicitly so
  // renew authenticates by secret, never by the ambient env var (which any
  // process can set to another window's value — Warden round 3). The secret
  // lives in the per-window key file acquire() writes at checkin; a fresh
  // window with no key file yet omits it (renew is then a clean no-op).
  let sessionSecret = '';
  if (windowId) {
    try {
      sessionSecret = fs.readFileSync(path.join(dotClaudeDir, `.session-key-${windowId}`), 'utf8').trim();
    } catch { /* fresh window — no secret yet */ }
  }
  const renewArgs = ['renew', '--auto'];
  if (sessionSecret) renewArgs.push('--session', sessionSecret);
  try {
    const output = execFileSync('node', [SYNC_SCRIPT, ...renewArgs], {
      encoding: 'utf8',
      timeout: 15000,
      cwd: __dirname,
      env: { ...process.env, CLAUDE_CODE_SESSION_ID: windowId }
    });
    if (output.trim()) {
      // Wake-recovery notices (re-acquired name / IDENTITY CHANGED) surface here
      process.stdout.write(output);
    }
  } catch {
    // Network error or timeout — .last-ping already updated, avoid retry spam
  }
  process.exit(0);
});
