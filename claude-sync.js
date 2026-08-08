/**
 * claude-sync.js — DHCP-Style Permit Coordination for Multiple Claude Code Sessions
 * Version: 2.2.0 — Per-window identity + idle-slot reservation + wake recovery
 *
 * v2.2.0: Fixes the 2026-07-13 identity-theft incident (window 2 stole live-but-idle
 *   Bill's name and both windows cross-identified via the shared session-key file).
 *   • WINDOW ID — every Claude Code window exposes CLAUDE_CODE_SESSION_ID (a stable
 *     per-window UUID) to hooks and shell commands. checkin now records it on the
 *     lease AND in .claude-session-map.json (windowId → {sessionId, name}), so each
 *     window resolves ITS OWN sync session automatically. The shared last-checkin-wins
 *     .claude-session-key file is now only a fallback for non-Claude terminals.
 *     $env:CLAUDE_SESSION_ID is no longer required for multi-window setups (still
 *     honoured, highest priority, for manual overrides).
 *   • RESERVED SLOTS — a lease that expired < 30 min ago under the SAME OS boot may
 *     belong to an idle-but-live window (the renewal hook only fires on tool use, so
 *     idle windows can't renew). Such a slot is "reserved": a DIFFERENT window cannot
 *     claim the name (checkin --name is rejected; auto-assign skips it) unless every
 *     slot is reserved/occupied. The owning window (windowId match) reclaims freely;
 *     a reboot voids all reservations; --force --human-ok overrides.
 *   • WAKE RECOVERY — renew --auto with no valid lease now re-acquires the window's
 *     previous name from the session map (its slot was reserved for it). If the name
 *     was genuinely taken, it acquires the next free slot and LOUDLY tells the agent
 *     its identity changed.
 *   • CLAUDE_SYNC_DOC env var overrides the Firestore doc path (isolated testing);
 *     local state files get a .test suffix so tests never pollute live sessions.
 *
 * v2.1.0: A `checkin --name X` can now take back slot X from a holder that is
 *   PROVABLY gone (crashed or rebooted on this machine) WITHOUT --human-ok.
 *   "If I was Bill and I crashed, I come back as Bill." Liveness is proven by:
 *     • bootId   — lease created under a different OS boot ⇒ rebooted ⇒ dead.
 *     • heartbeat — local .claude-heartbeat-<name>, touched on every renew/ping;
 *                   missing or > 75s old ⇒ holder not actively alive ⇒ dead.
 *   Same-machine only (hostname-scoped); a live, actively-renewing holder keeps a
 *   fresh heartbeat and is NEVER reclaimed — eviction of a LIVE peer still needs
 *   the human-only --force --human-ok gate. See assessHolderLiveness().
 *
 * Version: 2.0.0 — Complete rewrite with named lease/permit model
 *
 * ARCHITECTURE: Named permit pool (inspired by DHCP / AD RID Pool)
 *   ┌────────────────────────────────────────────────────────────────┐
 *   │  3 named permits: Bill (1st), Bob (2nd), Ben (3rd)            │
 *   │  Each permit has a 5-minute TTL                               │
 *   │  Without a valid permit, the instance CANNOT WORK (fail-safe) │
 *   │  ALL mutations use Firestore transactions — no race conditions │
 *   │  Auto-renewal via Claude Code UserPromptSubmit hook            │
 *   └────────────────────────────────────────────────────────────────┘
 *
 * Commands:
 *   checkin [--name Bill|Bob|Ben]         - Acquire first available permit (5-min TTL)
 *   checkin --name Bill --force          - Evict current Bill holder and claim the slot
 *   checkout [--session ID]              - Surrender your permit
 *   checkout --force [Name]              - Admin: force-clear one or all permits
 *   renew [--session ID]        - Extend permit by 5 min
 *   renew --auto [--session ID] - Hook mode: only renew if < 2.5 min remaining
 *   ping [--session ID]         - Renew permit + log heartbeat + watchdog report
 *   status [--session ID]       - Show my permit status + time remaining
 *   read                        - Display all permit holders and coordination state
 *   write "msg" [--session ID]  - Log activity (requires valid permit)
 *   claim /path [--session ID]  - Claim file ownership (requires valid permit)
 *   release /path [--sess ID]   - Release file (requires valid permit)
 *   register "desc"             - Register current branch
 *   gc                          - Clean up expired permits
 *   init                        - Initialise fresh state
 *
 * HOOK SETUP — add to .claude/settings.json in your project root:
 *   {
 *     "hooks": {
 *       "UserPromptSubmit": [{
 *         "matcher": ".*",
 *         "hooks": [{
 *           "type": "command",
 *           "command": "node E:/GoogleDrive/Papers/03-PrixSix/03.Current/claude-sync.js renew --auto"
 *         }]
 *       }]
 *     }
 *   }
 *
 * PER-TERMINAL SETUP — run once after checkin (enables per-instance auto-renewal):
 *   PowerShell:  $env:CLAUDE_SESSION_ID = "session-XXXXXXXXXX"
 *   CMD:         set CLAUDE_SESSION_ID=session-XXXXXXXXXX
 *   (checkin output shows the exact command with your session ID)
 *
 * Uses Firestore document: coordination/claude-state
 */

'use strict';

const admin = require('firebase-admin');
const { execSync } = require('child_process');
const path = require('path');
const fs = require('fs');
const os = require('os');

// ─────────────────────────────────────────────────────────────────────────────
// Configuration
// ─────────────────────────────────────────────────────────────────────────────

const PROJECT_ID = 'studio-6033436327-281b1';
const SERVICE_ACCOUNT_PATH = process.env.GOOGLE_APPLICATION_CREDENTIALS
  || path.join(__dirname, 'service-account.json');
// CLAUDE_SYNC_DOC overrides the doc path for isolated testing (never set it in real sessions)
const DOCUMENT_PATH = process.env.CLAUDE_SYNC_DOC || 'coordination/claude-state';
// Test runs also isolate every local state file so they can't corrupt live sessions
const FILE_SUFFIX = process.env.CLAUDE_SYNC_DOC ? '.test' : '';

const LEASE_TTL_MS = 5 * 60 * 1000;            // 5-minute permit TTL
// 3.5 min (was 2.5): with the 2-min hook check interval, 2.5 could skip the T+2m check
// (3 min remaining > 2.5) and not renew until T+4m — one idle minute from expiry.
const RENEWAL_THRESHOLD_MS = 3.5 * 60 * 1000;  // auto-renew if < 3.5 min remaining
const SLEEP_THRESHOLD_MS = 5 * 60 * 1000;       // peer "sleeping" if no renewal in 5 min
const DEEP_SLEEP_THRESHOLD_MS = 30 * 60 * 1000; // peer "deep sleeping" if > 30 min

// Stale-lease self-reclaim (DHCP-style): lets a same-name checkin take back a
// slot held by a holder that PROVABLY crashed or rebooted — without the
// human-only --force --human-ok gate (which still guards eviction of a LIVE peer).
const BOOT_TOLERANCE_MS = 60 * 1000;        // boot timestamps within 60s = same boot
const HEARTBEAT_STALE_MS = 75 * 1000;       // local heartbeat older than this = holder not actively alive

// A lease that expired this recently (same boot) may belong to an idle-but-live
// window — the renewal hook only fires on tool use, so idle windows can't renew.
// The slot stays RESERVED for its previous holder; other windows must use a
// different slot until the grace lapses (or the holder reclaims / machine reboots).
const EXPIRED_GRACE_MS = 30 * 60 * 1000;

const VALID_NAMES = ['Bill', 'Bob', 'Ben'];

/**
 * Convenience cache — written after checkin so setups WITHOUT a Claude window ID
 * (plain human terminals) work without passing --session. Instances on the same
 * machine SHARE this file; last checkin wins — which is why Claude Code windows
 * never resolve through it (they use the per-window session map instead).
 */
const SESSION_FILE = path.join(__dirname, `.claude-session-key${FILE_SUFFIX}`);

/**
 * Per-window session map: { "<CLAUDE_CODE_SESSION_ID>": { sessionId, name, updatedAt } }.
 * Written on every checkin from inside a Claude Code window; read by hooks so each
 * window renews ITS OWN permit. This is what prevents two windows from
 * cross-identifying via the shared convenience file (2026-07-13 incident).
 */
const SESSION_MAP_FILE = path.join(__dirname, `.claude-session-map${FILE_SUFFIX}.json`);

// ─────────────────────────────────────────────────────────────────────────────
// Firebase Init
// ─────────────────────────────────────────────────────────────────────────────

let db;
try {
  admin.initializeApp({
    credential: admin.credential.cert(SERVICE_ACCOUNT_PATH),
    projectId: PROJECT_ID
  });
  db = admin.firestore();
} catch (error) {
  console.error('Failed to initialise Firebase Admin:', error.message);
  console.error('Ensure service-account.json exists or GOOGLE_APPLICATION_CREDENTIALS is set.');
  process.exit(1);
}

// ─────────────────────────────────────────────────────────────────────────────
// Arg Parsing (done once at startup)
// ─────────────────────────────────────────────────────────────────────────────

const rawArgs = process.argv.slice(2);
const CMD = rawArgs[0] || '';
const FLAG_AUTO = rawArgs.includes('--auto');
const FLAG_FORCE = rawArgs.includes('--force');
/** --any: take the next free slot, ignoring the CLAUDE_IDENTITY env fallback. The
 *  launcher sets CLAUDE_IDENTITY=Bill in EVERY window, so a "plain" checkin from a
 *  second window silently became a --name Bill request — this flag is how fallback
 *  paths (e.g. claude-startup.js after a rejection) genuinely ask for "whatever's free". */
const FLAG_ANY = rawArgs.includes('--any');
/** Human authorisation gate — Claude instances are NEVER permitted to supply this flag autonomously */
const FLAG_HUMAN_OK = rawArgs.includes('--human-ok');

/** --session SESSION_ID — authoritative identity flag for multi-instance setups */
function parseExplicitSessionId() {
  const idx = rawArgs.indexOf('--session');
  return (idx !== -1 && rawArgs[idx + 1]) ? rawArgs[idx + 1] : null;
}

/**
 * --name Bill|Bob|Ben — request a specific permit name on checkin.
 * If the slot is occupied and --force is also passed, the holder is evicted first.
 * Falls back to CLAUDE_IDENTITY env var if --name flag is not provided
 * (allows prix6.bat to set the preferred identity without passing --name explicitly).
 */
function parseRequestedName() {
  const idx = rawArgs.indexOf('--name');
  if (idx !== -1 && rawArgs[idx + 1]) {
    const n = rawArgs[idx + 1];
    const matched = VALID_NAMES.find(v => v.toLowerCase() === n.toLowerCase());
    return matched || null;
  }
  if (FLAG_ANY) return null; // explicit "next free slot" — skip the env fallback
  // Fallback: respect CLAUDE_IDENTITY env var (set by prix6.bat launcher)
  if (process.env.CLAUDE_IDENTITY) {
    const matched = VALID_NAMES.find(v => v.toLowerCase() === process.env.CLAUDE_IDENTITY.toLowerCase());
    return matched || null;
  }
  return null;
}

/** Plain positional args after command, excluding flags and their values */
function getPlainArgs() {
  const result = [];
  for (let i = 1; i < rawArgs.length; i++) {
    if (rawArgs[i].startsWith('--')) {
      if (rawArgs[i] === '--session' || rawArgs[i] === '--name') i++; // skip value
      continue;
    }
    result.push(rawArgs[i]);
  }
  return result;
}

// ─────────────────────────────────────────────────────────────────────────────
// Session File Helpers
// ─────────────────────────────────────────────────────────────────────────────

function loadSessionId() {
  try {
    if (fs.existsSync(SESSION_FILE)) {
      const id = fs.readFileSync(SESSION_FILE, 'utf8').trim();
      return id || null;
    }
  } catch { /* ignore */ }
  return null;
}

function saveSessionId(sessionId) {
  try { fs.writeFileSync(SESSION_FILE, sessionId, 'utf8'); } catch { /* ignore */ }
}

/** Delete the convenience file — but only if it still points at the given session,
 *  so checking out never wipes a newer checkin made from another terminal. */
function deleteSessionId(onlyIfMatches) {
  try {
    if (!fs.existsSync(SESSION_FILE)) return;
    if (onlyIfMatches && fs.readFileSync(SESSION_FILE, 'utf8').trim() !== onlyIfMatches) return;
    fs.unlinkSync(SESSION_FILE);
  } catch { /* ignore */ }
}

// ─────────────────────────────────────────────────────────────────────────────
// Per-Window Identity (CLAUDE_CODE_SESSION_ID → sync session map)
// ─────────────────────────────────────────────────────────────────────────────

/**
 * The Claude Code window UUID. Present in the environment of every shell command
 * and hook spawned by a Claude Code session; absent in plain human terminals.
 * Hooks that read it from their stdin JSON pass it down via this same env var.
 */
function getWindowId() {
  const id = (process.env.CLAUDE_CODE_SESSION_ID || '').trim();
  return id || null;
}

function loadSessionMap() {
  try {
    if (fs.existsSync(SESSION_MAP_FILE)) {
      const parsed = JSON.parse(fs.readFileSync(SESSION_MAP_FILE, 'utf8'));
      if (parsed && typeof parsed === 'object') return parsed;
    }
  } catch { /* corrupt map = start fresh */ }
  return {};
}

function getSessionMapEntry(windowId) {
  if (!windowId) return null;
  const entry = loadSessionMap()[windowId];
  return (entry && entry.sessionId) ? entry : null;
}

function saveSessionMapEntry(windowId, sessionId, name) {
  if (!windowId) return;
  try {
    const map = loadSessionMap();
    map[windowId] = { sessionId, name, updatedAt: new Date().toISOString() };
    // Prune entries from long-gone windows (7 days)
    const cutoff = Date.now() - 7 * 24 * 60 * 60 * 1000;
    for (const [key, entry] of Object.entries(map)) {
      if (!entry || !entry.updatedAt || new Date(entry.updatedAt).getTime() < cutoff) {
        delete map[key];
      }
    }
    fs.writeFileSync(SESSION_MAP_FILE, JSON.stringify(map, null, 2), 'utf8');
  } catch { /* ignore */ }
}

function deleteSessionMapEntry(windowId) {
  if (!windowId) return;
  try {
    const map = loadSessionMap();
    if (windowId in map) {
      delete map[windowId];
      fs.writeFileSync(SESSION_MAP_FILE, JSON.stringify(map, null, 2), 'utf8');
    }
  } catch { /* ignore */ }
}

/**
 * Write the identity files the statusline reads: a per-window one (authoritative
 * in multi-window setups) plus the legacy shared one (fallback / single window).
 */
function writeIdentityFiles(name) {
  try {
    const dotClaude = path.join(__dirname, '.claude');
    if (!fs.existsSync(dotClaude)) fs.mkdirSync(dotClaude, { recursive: true });
    const lower = name.toLowerCase();
    fs.writeFileSync(path.join(dotClaude, `.identity${FILE_SUFFIX}`), lower, 'utf8');
    const windowId = getWindowId();
    if (windowId) {
      fs.writeFileSync(path.join(dotClaude, `.identity-${windowId}${FILE_SUFFIX}`), lower, 'utf8');
    }
  } catch { /* ignore */ }
}

function clearIdentityFileForWindow() {
  const windowId = getWindowId();
  if (!windowId) return;
  try {
    const f = path.join(__dirname, '.claude', `.identity-${windowId}${FILE_SUFFIX}`);
    if (fs.existsSync(f)) fs.unlinkSync(f);
  } catch { /* ignore */ }
}

// ─────────────────────────────────────────────────────────────────────────────
// Liveness Fingerprints — boot id + per-name heartbeat file
// ─────────────────────────────────────────────────────────────────────────────

/**
 * Coarse OS boot timestamp (ms). Two processes from the SAME boot agree within a
 * few seconds; a reboot shifts this by however long the machine was off. Used to
 * detect "this lease was created under a dead boot" so it can be reclaimed.
 */
function getBootId() {
  return Date.now() - Math.floor(os.uptime() * 1000);
}

/** Local heartbeat file for a permit name (same-machine liveness proof). */
function heartbeatFile(name) {
  return path.join(__dirname, `.claude-heartbeat-${name}${FILE_SUFFIX}`);
}

/** Refresh the heartbeat for a name — called on every renew/ping/checkin while alive. */
function touchHeartbeat(name) {
  if (!name) return;
  try { fs.writeFileSync(heartbeatFile(name), String(Date.now()), 'utf8'); } catch { /* ignore */ }
}

/** Age of a name's heartbeat in ms; Infinity if the file is missing. */
function heartbeatAgeMs(name) {
  if (!name) return Infinity;
  try {
    const st = fs.statSync(heartbeatFile(name));
    return Date.now() - st.mtimeMs;
  } catch { return Infinity; }
}

/** Remove a name's heartbeat on checkout / GC. */
function clearHeartbeat(name) {
  if (!name) return;
  try { if (fs.existsSync(heartbeatFile(name))) fs.unlinkSync(heartbeatFile(name)); } catch { /* ignore */ }
}

/**
 * Resolve my session ID — priority order:
 *   1. $CLAUDE_SESSION_ID env var (manual per-terminal override)
 *   2. --session SESSION_ID flag (passed explicitly to command)
 *   3. Session map entry for this Claude window (automatic, multi-window safe)
 *   4. .claude-session-key file — ONLY outside a Claude window. Inside one, a
 *      missing map entry must NOT fall through to the shared file: that file may
 *      hold ANOTHER window's key (the exact cross-identification bug this fixes).
 */
function getMySessionId() {
  if (process.env.CLAUDE_SESSION_ID) return process.env.CLAUDE_SESSION_ID.trim();
  const explicit = parseExplicitSessionId();
  if (explicit) return explicit;
  const windowId = getWindowId();
  if (windowId) {
    const entry = getSessionMapEntry(windowId);
    return entry ? entry.sessionId : null;
  }
  return loadSessionId();
}

// ─────────────────────────────────────────────────────────────────────────────
// Firestore Helpers
// ─────────────────────────────────────────────────────────────────────────────

function getDocRef() {
  return db.doc(DOCUMENT_PATH);
}

function emptyState() {
  return {
    leases: { Bill: null, Bob: null, Ben: null },
    claimedFiles: {},
    branches: {},
    activityLog: []
  };
}

/**
 * Parse Firestore document data into a clean state object.
 * Handles migration from v1 (sessions: {}) to v2 (leases: {}).
 */
function readState(doc) {
  if (!doc.exists) return emptyState();
  const data = doc.data();

  // Migration: v1 used sessions: {}, v2 uses leases: {}
  if (data.sessions && !data.leases) {
    console.warn('[MIGRATION] Old session format detected — starting with fresh lease state.');
    console.warn('[MIGRATION] Run: node claude-sync.js init   to clear old state.');
    return emptyState();
  }

  // Ensure leases object is complete
  const state = { ...emptyState(), ...data };
  if (!state.leases) state.leases = { Bill: null, Bob: null, Ben: null };
  for (const name of VALID_NAMES) {
    if (!(name in state.leases)) state.leases[name] = null;
  }
  return state;
}

function addLog(state, sessionId, name, branch, message) {
  state.activityLog = state.activityLog || [];
  state.activityLog.push({
    sessionId, name, branch, message,
    timestamp: new Date().toISOString()
  });
  // Keep only last 100 entries to stay well under Firestore 1MB limit
  if (state.activityLog.length > 100) {
    state.activityLog = state.activityLog.slice(-100);
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Lease / Permit Logic
// ─────────────────────────────────────────────────────────────────────────────

function isLeaseValid(lease) {
  if (!lease || !lease.expiresAt) return false;
  return new Date(lease.expiresAt) > new Date();
}

function getLeaseTimeRemaining(lease) {
  if (!lease || !lease.expiresAt) return 0;
  return Math.max(0, new Date(lease.expiresAt) - new Date());
}

/**
 * Find which name my sessionId holds (only counts if lease is still valid).
 * Returns { name, lease } or null.
 */
function getMyLease(leases, sessionId) {
  if (!sessionId) return null;
  for (const name of VALID_NAMES) {
    const lease = leases[name];
    if (lease && lease.sessionId === sessionId && isLeaseValid(lease)) {
      return { name, lease };
    }
  }
  return null;
}

/**
 * Decide whether an occupied (still-valid-by-TTL) lease belongs to a holder that
 * is PROVABLY gone — crashed or rebooted — and may therefore be reclaimed by a
 * same-name checkin WITHOUT human authorisation.
 *
 * Safety model:
 *   - Only same-machine holders are assessed. A different-host holder cannot be
 *     evaluated locally, so it is reported ALIVE (caller rejects → human-ok path).
 *   - Reboot: lease.bootId differs from the current boot beyond tolerance.
 *   - Same-boot crash: the local heartbeat for this name is missing or stale. A
 *     genuinely active instance touches its heartbeat on every hook fire, so a
 *     fresh heartbeat means "alive" and blocks reclaim.
 *
 * @returns {{ dead: boolean, reason: string }}
 */
function assessHolderLiveness(name, lease) {
  // Same Claude window = same session lineage — always reclaimable (e.g. a window
  // re-checking-in after its own checkout/expiry confusion).
  const myWindowId = getWindowId();
  if (lease.windowId && myWindowId && lease.windowId === myWindowId) {
    return { dead: true, reason: 'same-window' };
  }

  const sameHost = !lease.hostname || lease.hostname === os.hostname();
  if (!sameHost) return { dead: false, reason: `different-host (${lease.hostname})` };

  if (typeof lease.bootId === 'number'
      && Math.abs(getBootId() - lease.bootId) > BOOT_TOLERANCE_MS) {
    return { dead: true, reason: 'rebooted-since-grant' };
  }

  const age = heartbeatAgeMs(name);
  if (age > HEARTBEAT_STALE_MS) {
    return { dead: true, reason: age === Infinity ? 'no-heartbeat' : `stale-heartbeat-${Math.round(age / 1000)}s` };
  }

  return { dead: false, reason: 'alive' };
}

/**
 * Is this expired lease RESERVED for its previous (possibly idle-but-live) holder?
 * Reserved = expired < EXPIRED_GRACE_MS ago AND nothing proves the holder dead.
 * A reboot since grant proves death (voids the reservation); a stale heartbeat does
 * NOT — an idle window stops renewing AND stops heart-beating, which is exactly the
 * holder this reservation protects. Different-host leases can't be assessed locally,
 * so they stay reserved for the grace period too.
 */
function isLeaseReserved(lease) {
  if (!lease || !lease.expiresAt || isLeaseValid(lease)) return false;
  const sinceExpiry = Date.now() - new Date(lease.expiresAt).getTime();
  if (!(sinceExpiry >= 0 && sinceExpiry < EXPIRED_GRACE_MS)) return false;
  const sameHost = !lease.hostname || lease.hostname === os.hostname();
  if (sameHost && typeof lease.bootId === 'number'
      && Math.abs(getBootId() - lease.bootId) > BOOT_TOLERANCE_MS) {
    return false; // machine rebooted since grant — holder is provably gone
  }
  return true;
}

/** Reserved for someone OTHER than this window (the case that must block a claim). */
function isReservedForAnotherWindow(lease) {
  if (!isLeaseReserved(lease)) return false;
  const myWindowId = getWindowId();
  return !(lease.windowId && myWindowId && lease.windowId === myWindowId);
}

/**
 * Pick a slot for auto-assign checkin. Prefers genuinely free slots (never held,
 * or expired past grace / provably dead). Falls back to the longest-expired
 * reserved slot only when nothing else is free — better than refusing service.
 */
function getAvailableName(leases) {
  for (const name of VALID_NAMES) {
    const lease = leases[name];
    if (!lease || (!isLeaseValid(lease) && !isReservedForAnotherWindow(lease))) return name;
  }
  // Everything is occupied or reserved — take the reserved slot that expired longest ago
  let fallback = null;
  for (const name of VALID_NAMES) {
    const lease = leases[name];
    if (lease && !isLeaseValid(lease)) {
      if (!fallback || new Date(lease.expiresAt) < new Date(leases[fallback].expiresAt)) {
        fallback = name;
      }
    }
  }
  return fallback;
}

// ─────────────────────────────────────────────────────────────────────────────
// GC Helper (pure — mutates state in-place, no Firestore I/O)
// ─────────────────────────────────────────────────────────────────────────────

function gcExpiredLeases(state) {
  const cleared = [];
  const releasedFiles = [];

  for (const name of VALID_NAMES) {
    const lease = state.leases[name];
    if (!lease) continue;
    // Keep reserved leases: the holder may be idle, not gone, and the lease data
    // (windowId, expiresAt) IS the reservation. Cleared once grace lapses.
    if (isLeaseReserved(lease)) continue;
    if (!isLeaseValid(lease)) {
      cleared.push(`${name} (expired ${formatTime(lease.expiresAt)})`);

      // Release any files claimed by this session
      for (const [filePath, claim] of Object.entries(state.claimedFiles || {})) {
        if (claim.sessionId === lease.sessionId || claim.name === name) {
          releasedFiles.push(filePath);
          delete state.claimedFiles[filePath];
        }
      }
      state.leases[name] = null;
      clearHeartbeat(name);
    }
  }

  if (cleared.length > 0) {
    addLog(state, 'system', 'GC', 'system',
      `Auto-expired ${cleared.length} permit(s): ${cleared.join(', ')}`);
  }

  return { cleared, releasedFiles };
}

// ─────────────────────────────────────────────────────────────────────────────
// Watchdog (read-only, no Firestore mutations)
// ─────────────────────────────────────────────────────────────────────────────

function watchdogReport(leases, mySessionId) {
  const now = Date.now();
  const lines = [];

  for (const name of VALID_NAMES) {
    const lease = leases[name];
    if (!lease || !isLeaseValid(lease)) continue;
    if (lease.sessionId === mySessionId) continue; // that's me

    const lastSeen = lease.lastRenewedAt
      ? new Date(lease.lastRenewedAt).getTime()
      : new Date(lease.grantedAt).getTime();
    const inactiveMs = now - lastSeen;

    if (inactiveMs >= DEEP_SLEEP_THRESHOLD_MS) {
      lines.push(`⚠️  WATCHDOG: ${name} DEEP SLEEP ${Math.round(inactiveMs / 60000)}m (last renewal: ${formatTime(lease.lastRenewedAt || lease.grantedAt)}) on ${lease.branch || 'unknown'}`);
    } else if (inactiveMs >= SLEEP_THRESHOLD_MS) {
      lines.push(`WATCHDOG: ${name} sleeping ${Math.round(inactiveMs / 60000)}m (last renewal: ${formatTime(lease.lastRenewedAt || lease.grantedAt)}) on ${lease.branch || 'unknown'}`);
    }
  }

  return lines;
}

// ─────────────────────────────────────────────────────────────────────────────
// Utility
// ─────────────────────────────────────────────────────────────────────────────

function formatTime(isoString) {
  if (!isoString) return 'N/A';
  return new Date(isoString).toLocaleString('en-GB', {
    dateStyle: 'short', timeStyle: 'short'
  });
}

function formatMs(ms) {
  if (ms <= 0) return 'EXPIRED';
  const totalSec = Math.floor(ms / 1000);
  const min = Math.floor(totalSec / 60);
  const sec = totalSec % 60;
  return min > 0 ? `${min}m ${sec}s` : `${sec}s`;
}

function getCurrentBranch() {
  try {
    return execSync('git rev-parse --abbrev-ref HEAD', { encoding: 'utf8' }).trim();
  } catch {
    return 'unknown';
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// COMMAND: init — Initialise fresh state
// ─────────────────────────────────────────────────────────────────────────────

async function cmdInit() {
  const state = {
    ...emptyState(),
    initialisedAt: new Date().toISOString(),
    version: '2.0'
  };
  await getDocRef().set(state);
  console.log('Coordination state initialised (permit model v2.0).');
  console.log(`Document: ${DOCUMENT_PATH}`);
}

// ─────────────────────────────────────────────────────────────────────────────
// COMMAND: checkin — Acquire first available permit
// ─────────────────────────────────────────────────────────────────────────────

async function cmdCheckin() {
  const branch = getCurrentBranch();
  const requestedName = parseRequestedName(); // --name Bill|Bob|Ben (optional)
  let result = null;
  let gcResult = null;
  let reclaimInfo = null;

  await db.runTransaction(async (t) => {
    result = null;
    gcResult = null;
    reclaimInfo = null;

    const docRef = getDocRef();
    const doc = await t.get(docRef);
    const state = readState(doc);

    // Step 1: GC any expired leases to free up slots
    gcResult = gcExpiredLeases(state);

    // Step 2: Determine which name to claim
    let name;
    if (requestedName) {
      const existingLease = state.leases[requestedName];
      if (existingLease && !isLeaseValid(existingLease) && isReservedForAnotherWindow(existingLease)) {
        // Expired but RESERVED for a possibly-idle window that isn't us.
        if (FLAG_FORCE && FLAG_HUMAN_OK) {
          addLog(state, 'system', 'ADMIN', 'system',
            `Force-claimed reserved slot ${requestedName} from idle holder ${existingLease.sessionId} [human-authorised]`);
          state.leases[requestedName] = null;
          name = requestedName;
        } else {
          result = {
            ok: false,
            reason: 'name-reserved',
            requestedName,
            holder: existingLease,
            holders: Object.fromEntries(VALID_NAMES.map(n => [n, state.leases[n]]))
          };
          t.set(docRef, state);
          return;
        }
      } else if (existingLease && isLeaseValid(existingLease)) {
        if (FLAG_FORCE && FLAG_HUMAN_OK) {
          // --name Bill --force --human-ok: evict current holder and claim the slot
          // HUMAN AUTHORISATION REQUIRED: Claude instances must never supply --human-ok autonomously
          const evictedSession = existingLease.sessionId;
          // Release any files held by the evicted session
          for (const [filePath, claim] of Object.entries(state.claimedFiles || {})) {
            if (claim.sessionId === evictedSession || claim.name === requestedName) {
              delete state.claimedFiles[filePath];
            }
          }
          addLog(state, 'system', 'ADMIN', 'system',
            `Force-evicted ${requestedName} (${evictedSession}) to free slot for new checkin [human-authorised]`);
          state.leases[requestedName] = null;
          name = requestedName;
        } else if (FLAG_FORCE && !FLAG_HUMAN_OK) {
          // --force without --human-ok: Claude tried to self-authorise — block it
          console.error('');
          console.error('============================================================');
          console.error('  FORCE-EVICT BLOCKED — HUMAN AUTHORISATION REQUIRED');
          console.error('============================================================');
          console.error(`  Slot "${requestedName}" is occupied by session ${existingLease.sessionId}.`);
          console.error('');
          console.error('  Force-evicting another permit holder requires explicit human');
          console.error('  authorisation. Claude instances may NOT supply --human-ok');
          console.error('  autonomously. Only a human may run this command.');
          console.error('');
          console.error('  To authorise the eviction, YOU (the human) must run:');
          console.error(`    node claude-sync.js checkin --name ${requestedName} --force --human-ok`);
          console.error('============================================================');
          console.error('');
          process.exit(1);
        } else {
          // Slot occupied, --force not given. Before rejecting, check whether the
          // holder is PROVABLY gone (crashed / rebooted on this machine). If so,
          // self-reclaim the name — no human-ok needed (we are not evicting a live
          // peer, we are taking back a dead lease, DHCP-style). "If I was Bill and
          // I crashed, I come back as Bill."
          const liveness = assessHolderLiveness(requestedName, existingLease);
          if (liveness.dead) {
            const deadSession = existingLease.sessionId;
            for (const [filePath, claim] of Object.entries(state.claimedFiles || {})) {
              if (claim.sessionId === deadSession || claim.name === requestedName) {
                delete state.claimedFiles[filePath];
              }
            }
            addLog(state, 'system', 'RECLAIM', 'system',
              `Auto-reclaimed ${requestedName} from dead holder ${deadSession} (${liveness.reason})`);
            state.leases[requestedName] = null;
            name = requestedName;
            reclaimInfo = { reason: liveness.reason, deadSession };
          } else {
            // Holder is genuinely live (or an unassessable remote) — reject with guidance
            result = {
              ok: false,
              reason: 'name-occupied',
              requestedName,
              holder: existingLease,
              holders: Object.fromEntries(VALID_NAMES.map(n => [n, state.leases[n]]))
            };
            t.set(docRef, state);
            return;
          }
        }
      } else {
        // Slot is free (or expired) — claim it directly
        name = requestedName;
      }
    } else {
      // No name requested: first-come, first-served
      name = getAvailableName(state.leases);
    }

    if (!name) {
      // All 3 slots occupied — snapshot holders for rejection message
      result = {
        ok: false,
        reason: 'all-full',
        holders: Object.fromEntries(VALID_NAMES.map(n => [n, state.leases[n]]))
      };
      t.set(docRef, state); // save GC results even on rejection
      return;
    }

    // Step 3: Issue new permit
    const sessionId = `session-${Date.now()}`;
    const now = new Date();
    const grantedAt = now.toISOString();
    const expiresAt = new Date(now.getTime() + LEASE_TTL_MS).toISOString();

    state.leases[name] = {
      sessionId,
      grantedAt,
      expiresAt,
      lastRenewedAt: grantedAt,
      branch,
      name,                       // self-describing (used by liveness checks)
      hostname: os.hostname(),    // same-machine scoping for reclaim safety
      bootId: getBootId(),        // detects "lease from a dead boot" → reclaimable
      windowId: getWindowId(),    // Claude window UUID — reservation ownership + self-reclaim
      ownerPid: process.ppid || process.pid  // diagnostic: owning process
    };

    addLog(state, sessionId, name, branch, `${name} acquired permit on branch '${branch}'`);
    t.set(docRef, state);

    result = { ok: true, sessionId, name, branch, grantedAt, expiresAt };
  });

  // Report GC results
  if (gcResult && gcResult.cleared.length > 0) {
    console.log(`[GC] Auto-expired ${gcResult.cleared.length} permit(s): ${gcResult.cleared.join(', ')}`);
  }

  // Report self-reclaim of a crashed/rebooted holder
  if (reclaimInfo) {
    console.log(`[RECLAIM] Took back ${requestedName} from dead holder ${reclaimInfo.deadSession} (${reclaimInfo.reason}).`);
  }

  if (!result || !result.ok) {
    const reason = result?.reason || 'all-full';
    const holders = result?.holders || {};
    console.error('');
    console.error('='.repeat(60));

    if (reason === 'name-occupied') {
      const rn = result.requestedName;
      const hl = result.holder;
      console.error(`CHECKIN REJECTED — ${rn.toUpperCase()} SLOT IS OCCUPIED`);
      console.error('='.repeat(60));
      console.error(`  ${rn}: expires ${formatTime(hl.expiresAt)} (${formatMs(getLeaseTimeRemaining(hl))} remaining)`);
      console.error(`  Session: ${hl.sessionId}`);
      console.error('');
      console.error('Options:');
      console.error(`  1. Wait for the permit to expire (max 5 min)`);
      console.error(`  2. Force-evict (HUMAN ONLY): node claude-sync.js checkin --name ${rn} --force --human-ok`);
      console.error(`  3. Use next available slot: node claude-sync.js checkin --any`);
    } else if (reason === 'name-reserved') {
      const rn = result.requestedName;
      const hl = result.holder;
      const reservedUntil = new Date(new Date(hl.expiresAt).getTime() + EXPIRED_GRACE_MS);
      console.error(`CHECKIN REJECTED — ${rn.toUpperCase()} SLOT IS RESERVED`);
      console.error('='.repeat(60));
      console.error(`  ${rn}'s permit expired ${formatTime(hl.expiresAt)}, but its holder may just be`);
      console.error(`  IDLE, not gone (idle windows cannot renew). The slot is reserved for`);
      console.error(`  its return until ${formatTime(reservedUntil.toISOString())}.`);
      console.error(`  Last holder session: ${hl.sessionId}`);
      console.error('');
      console.error('Options:');
      console.error(`  1. Use next available slot: node claude-sync.js checkin --any   ← usually right`);
      console.error(`  2. Wait for the reservation to lapse`);
      console.error(`  3. Override (HUMAN ONLY, if the holder is truly gone):`);
      console.error(`     node claude-sync.js checkin --name ${rn} --force --human-ok`);
    } else {
      console.error('CHECKIN REJECTED — ALL PERMITS OCCUPIED');
      console.error('='.repeat(60));
      for (const n of VALID_NAMES) {
        const l = holders[n];
        if (l && isLeaseValid(l)) {
          const rem = getLeaseTimeRemaining(l);
          console.error(`  ${n}: expires in ${formatMs(rem)} (${formatTime(l.expiresAt)})`);
        }
      }
      console.error('');
      console.error('Options:');
      console.error('  1. Wait for a permit to expire (max 5 minutes)');
      console.error('  2. Ask the holder to run: node claude-sync.js checkout');
      console.error('  3. Admin force-clear: node claude-sync.js checkout --force [Name]');
    }

    console.error('='.repeat(60));
    process.exit(1);
  }

  const { sessionId, name, branch: checkinBranch, expiresAt } = result;

  // Record identity everywhere this window will look for it:
  //  - session map (per-window, authoritative inside Claude Code)
  //  - convenience file (non-Claude terminals / single instance)
  //  - identity files (statusline display)
  saveSessionMapEntry(getWindowId(), sessionId, name);
  saveSessionId(sessionId);
  writeIdentityFiles(name);

  // Establish liveness heartbeat immediately so the slot reads as alive at once
  touchHeartbeat(name);

  const line = '='.repeat(60);
  const dashes = '─'.repeat(60);
  console.log('');
  console.log(line);
  console.log(`  ✓  YOU ARE: ${name.toUpperCase()}`);
  console.log(line);
  console.log(`  Session ID : ${sessionId}`);
  console.log(`  Branch     : ${checkinBranch}`);
  console.log(`  Permit TTL : 5 minutes (auto-renews via hook)`);
  console.log(`  Expires    : ${formatTime(expiresAt)}`);
  console.log('');
  console.log(`  MANDATORY  : All responses MUST start with  ${name.toLowerCase()}>`);
  console.log('');
  console.log(dashes);
  console.log('  ⚡ PERMIT MANAGEMENT');
  console.log(dashes);
  if (getWindowId()) {
    console.log('  Multi-window identity is AUTOMATIC: this checkin is mapped to your');
    console.log(`  Claude window (${getWindowId().slice(0, 8)}…) — the renewal hook renews the`);
    console.log('  right permit without any env var. If your permit expires while idle,');
    console.log('  the next hook fire re-acquires your name automatically.');
  } else {
    console.log('  No Claude window detected (plain terminal). For multi-instance use,');
    console.log('  set the session env var so renewals target this instance:');
    console.log(`  PowerShell:  $env:CLAUDE_SESSION_ID = "${sessionId}"`);
    console.log(`  CMD:         set CLAUDE_SESSION_ID=${sessionId}`);
  }
  console.log('');
  console.log(dashes);
  console.log('  Quick reference:');
  console.log(dashes);
  console.log(`  Renew:    node claude-sync.js renew   --session ${sessionId}`);
  console.log(`  Ping:     node claude-sync.js ping    --session ${sessionId}`);
  console.log(`  Status:   node claude-sync.js status  --session ${sessionId}`);
  console.log(`  Checkout: node claude-sync.js checkout --session ${sessionId}`);
  console.log('');
  console.log('  🛑 GOLDEN RULES: Read golden-rules-reminder.md NOW.');
  console.log('  Location: C:\\Users\\aarongarcia\\.claude\\projects\\E--GoogleDrive-Tools-Memory-source\\memory\\golden-rules-reminder.md');
  console.log(line);
  console.log('');
}

// ─────────────────────────────────────────────────────────────────────────────
// COMMAND: renew — Extend permit by 5 minutes
// ─────────────────────────────────────────────────────────────────────────────

/**
 * Wake recovery: this window's permit expired while it was idle (idle windows
 * cannot renew — the hook only fires on tool use). Re-acquire its previous name;
 * the slot was reserved for it. If the name was genuinely taken (live holder or
 * lapsed reservation claimed by someone else), take the next free slot instead.
 * Returns { ok, name, sessionId, renamed } or { ok: false }.
 */
async function attemptReacquire(previousName) {
  const branch = getCurrentBranch();
  const myWindowId = getWindowId();
  let result = null;

  await db.runTransaction(async (t) => {
    result = null;
    const docRef = getDocRef();
    const doc = await t.get(docRef);
    const state = readState(doc);

    gcExpiredLeases(state); // clears dead/lapsed leases, keeps reservations

    let name = null;
    const lease = state.leases[previousName];
    if (!lease || !isLeaseValid(lease)) {
      // Free, or expired: our own reservation (windowId match) — or anyone's dead
      // lease — is claimable. Only a reservation for a DIFFERENT window blocks us.
      if (!(lease && isReservedForAnotherWindow(lease))) name = previousName;
    }
    if (!name) name = getAvailableName(state.leases);
    if (!name) {
      result = { ok: false };
      t.set(docRef, state);
      return;
    }

    const sessionId = `session-${Date.now()}`;
    const now = new Date();
    state.leases[name] = {
      sessionId,
      grantedAt: now.toISOString(),
      expiresAt: new Date(now.getTime() + LEASE_TTL_MS).toISOString(),
      lastRenewedAt: now.toISOString(),
      branch,
      name,
      hostname: os.hostname(),
      bootId: getBootId(),
      windowId: myWindowId,
      ownerPid: process.ppid || process.pid
    };
    addLog(state, sessionId, name, branch,
      name === previousName
        ? `${name} re-acquired permit after idle expiry`
        : `${name} acquired permit (was ${previousName}; old slot taken during idle)`);
    t.set(docRef, state);
    result = { ok: true, name, sessionId, renamed: name !== previousName };
  });

  if (result && result.ok) {
    saveSessionMapEntry(myWindowId, result.sessionId, result.name);
    saveSessionId(result.sessionId);
    writeIdentityFiles(result.name);
    touchHeartbeat(result.name);
  }
  return result || { ok: false };
}

async function cmdRenew() {
  const sessionId = getMySessionId();

  // Auto mode with no session — single-instance not checked in, skip silently
  if (!sessionId) {
    if (FLAG_AUTO) process.exit(0);
    console.error('No session ID found. Run "checkin" first, or pass --session SESSION_ID.');
    process.exit(1);
  }

  let result = null;

  await db.runTransaction(async (t) => {
    result = null;
    const docRef = getDocRef();
    const doc = await t.get(docRef);
    const state = readState(doc);
    const now = new Date();

    const myLease = getMyLease(state.leases, sessionId);
    if (!myLease) {
      result = { ok: false, reason: 'no-lease' };
      return;
    }

    const { name, lease } = myLease;
    const remaining = getLeaseTimeRemaining(lease);

    // Auto mode: skip renewal if permit still has plenty of time remaining
    if (FLAG_AUTO && remaining > RENEWAL_THRESHOLD_MS) {
      result = { ok: true, skipped: true, name, remaining, expiresAt: lease.expiresAt };
      return; // no write — avoids unnecessary Firestore churn
    }

    // Renew: extend from now (and refresh liveness fingerprints)
    const expiresAt = new Date(now.getTime() + LEASE_TTL_MS).toISOString();
    state.leases[name] = {
      ...lease,
      expiresAt,
      lastRenewedAt: now.toISOString(),
      name,
      hostname: os.hostname(),
      bootId: getBootId(),
      ownerPid: process.ppid || process.pid
    };

    if (!FLAG_AUTO) {
      // Only log explicit renewals to avoid flooding the log with hook renewals
      addLog(state, sessionId, name, lease.branch, `${name} renewed permit`);
    }

    t.set(docRef, state);
    result = { ok: true, skipped: false, name, expiresAt, branch: lease.branch };
  });

  if (!result || !result.ok) {
    const reason = result?.reason;
    if (FLAG_AUTO) {
      if (reason === 'no-lease') {
        // WAKE RECOVERY — permit expired while this window was idle. Its slot is
        // reserved for it (windowId match), so re-acquire the previous name; if
        // that name was truly taken, grab the next free slot and tell the agent.
        const windowId = getWindowId();
        const mapEntry = windowId ? getSessionMapEntry(windowId) : null;
        if (mapEntry && mapEntry.name) {
          const re = await attemptReacquire(mapEntry.name);
          if (re.ok && !re.renamed) {
            process.stdout.write(
              `[claude-sync] Permit expired while idle — automatically re-acquired ${re.name} `
              + `(new session ${re.sessionId}). Keep prefixing responses with ${re.name.toLowerCase()}>.\n`);
          } else if (re.ok && re.renamed) {
            process.stdout.write(
              `[claude-sync] ⚠️ IDENTITY CHANGED: your permit expired while idle and the name `
              + `"${mapEntry.name}" was taken by another instance. YOU ARE NOW ${re.name.toUpperCase()} `
              + `(session ${re.sessionId}). Prefix ALL further responses with ${re.name.toLowerCase()}> `
              + `and tell the user about the rename.\n`);
          } else {
            process.stderr.write(`[claude-sync] WARN: Permit expired and no slot available to re-acquire. Run checkin.\n`);
          }
        } else {
          process.stderr.write(`[claude-sync] WARN: No valid permit for session ${sessionId}. Run checkin.\n`);
        }
      }
      // Never block the hook
      process.exit(0);
    }
    if (reason === 'no-lease') {
      console.error(`No valid permit found for session ${sessionId}.`);
      console.error('Your permit may have expired. Run "checkin" to acquire a new one.');
    } else {
      console.error('Renewal failed:', reason);
    }
    process.exit(1);
  }

  // Always refresh the local heartbeat — even on the throttled "skip" path — so an
  // actively-working instance keeps proving liveness between Firestore renewals.
  // This is what stops another same-name checkin from reclaiming a live slot.
  if (result.name) touchHeartbeat(result.name);

  if (result.skipped) {
    // Auto mode, lease still fresh — exit silently (don't pollute logs)
    process.exit(0);
  }

  if (!FLAG_AUTO) {
    console.log(`${result.name.toLowerCase()}> Permit renewed — expires: ${formatTime(result.expiresAt)}`);
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// COMMAND: checkout — Surrender permit
// ─────────────────────────────────────────────────────────────────────────────

async function cmdCheckout() {
  const plainArgs = getPlainArgs();
  const targetName = plainArgs[0] || null;

  if (FLAG_FORCE) {
    // Admin: force-clear one named permit or all permits
    let namesToClear;
    if (targetName) {
      const matched = VALID_NAMES.find(n => n.toLowerCase() === targetName.toLowerCase());
      if (!matched) {
        console.error(`Unknown name "${targetName}". Valid names: ${VALID_NAMES.join(', ')}`);
        process.exit(1);
      }
      namesToClear = [matched];
    } else {
      namesToClear = [...VALID_NAMES];
    }

    let result = null;
    await db.runTransaction(async (t) => {
      const docRef = getDocRef();
      const doc = await t.get(docRef);
      const state = readState(doc);
      const cleared = [];

      for (const name of namesToClear) {
        if (state.leases[name]) {
          const lease = state.leases[name];
          cleared.push(name);
          // Release files owned by this session
          for (const [filePath, claim] of Object.entries(state.claimedFiles || {})) {
            if (claim.sessionId === lease.sessionId || claim.name === name) {
              delete state.claimedFiles[filePath];
            }
          }
          addLog(state, 'system', 'ADMIN', 'system', `Force-cleared ${name}'s permit`);
          state.leases[name] = null;
          clearHeartbeat(name);
        }
      }

      t.set(docRef, state);
      result = { cleared };
    });

    const count = result.cleared.length;
    if (count === 0) {
      console.log(`No active permits to force-clear${targetName ? ` for ${targetName}` : ''}.`);
    } else {
      console.log(`Force-cleared ${count} permit(s): ${result.cleared.join(', ')}`);
    }
    return;
  }

  // Normal checkout — find by session ID or named target
  const sessionId = getMySessionId();
  let result = null;

  await db.runTransaction(async (t) => {
    result = null;
    const docRef = getDocRef();
    const doc = await t.get(docRef);
    const state = readState(doc);
    const releasedFiles = [];

    let myName = null;
    let myLease = null;

    if (targetName) {
      // Named checkout (ends any named session)
      const n = VALID_NAMES.find(v => v.toLowerCase() === targetName.toLowerCase());
      if (n && state.leases[n]) {
        myName = n;
        myLease = state.leases[n];
      }
    } else if (sessionId) {
      const found = getMyLease(state.leases, sessionId);
      if (found) { myName = found.name; myLease = found.lease; }
    }

    if (!myName) {
      result = { ok: false };
      return;
    }

    // Release files owned by this session
    for (const [filePath, claim] of Object.entries(state.claimedFiles || {})) {
      if (claim.sessionId === myLease.sessionId || claim.name === myName) {
        releasedFiles.push(filePath);
        delete state.claimedFiles[filePath];
      }
    }

    addLog(state, myLease.sessionId, myName, myLease.branch,
      `${myName} checked out. Released ${releasedFiles.length} file(s).`);
    state.leases[myName] = null;
    clearHeartbeat(myName);
    t.set(docRef, state);
    result = { ok: true, name: myName, sessionId: myLease.sessionId, releasedFiles };
  });

  if (!result || !result.ok) {
    console.error('No active permit found to checkout.');
    if (!sessionId) {
      console.error('No session ID. Pass --session SESSION_ID or set $env:CLAUDE_SESSION_ID.');
    }
    console.error('Run: node claude-sync.js read   — to see current state');
    process.exit(1);
  }

  // Clean up local identity records — but only the ones that point at the
  // checked-out session, so checking out a NAMED peer never wipes our own.
  deleteSessionId(result.sessionId);
  const windowId = getWindowId();
  const mapEntry = windowId ? getSessionMapEntry(windowId) : null;
  if (mapEntry && mapEntry.sessionId === result.sessionId) {
    deleteSessionMapEntry(windowId);
    clearIdentityFileForWindow();
  }

  console.log('');
  console.log('='.repeat(60));
  console.log(`  ${result.name} CHECKED OUT`);
  console.log('='.repeat(60));
  if (result.releasedFiles.length > 0) {
    console.log(`  Released files: ${result.releasedFiles.join(', ')}`);
  }
  console.log('  Permit surrendered. Thanks for a good session!');
  console.log('='.repeat(60));
  console.log('');
}

// ─────────────────────────────────────────────────────────────────────────────
// COMMAND: ping — Renew permit + log heartbeat + watchdog report
// ─────────────────────────────────────────────────────────────────────────────

async function cmdPing() {
  const sessionId = getMySessionId();
  if (!sessionId) {
    console.error('No session ID. Run "checkin" first, or pass --session SESSION_ID.');
    process.exit(1);
  }

  let result = null;

  await db.runTransaction(async (t) => {
    result = null;
    const docRef = getDocRef();
    const doc = await t.get(docRef);
    const state = readState(doc);
    const now = new Date();

    const myLease = getMyLease(state.leases, sessionId);
    if (!myLease) {
      result = { ok: false };
      return;
    }

    const { name, lease } = myLease;
    const expiresAt = new Date(now.getTime() + LEASE_TTL_MS).toISOString();

    // Renew permit (and refresh liveness fingerprints)
    state.leases[name] = {
      ...lease,
      expiresAt,
      lastRenewedAt: now.toISOString(),
      name,
      hostname: os.hostname(),
      bootId: getBootId(),
      ownerPid: process.ppid || process.pid
    };

    addLog(state, sessionId, name, lease.branch, `${name} ping (permit renewed)`);

    // Capture watchdog lines for post-commit printing
    const wdLines = watchdogReport(state.leases, sessionId);

    t.set(docRef, state);
    result = { ok: true, name, expiresAt, branch: lease.branch, wdLines };
  });

  if (!result || !result.ok) {
    console.error('No valid permit found. Your permit may have expired. Run "checkin".');
    process.exit(1);
  }

  touchHeartbeat(result.name);

  console.log(`${result.name.toLowerCase()}> ping — permit renewed. Expires: ${formatTime(result.expiresAt)}`);
  for (const line of result.wdLines) {
    console.log(line);
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// COMMAND: status — Show my permit status + time remaining
// ─────────────────────────────────────────────────────────────────────────────

async function cmdStatus() {
  const sessionId = getMySessionId();
  const doc = await getDocRef().get();
  const state = readState(doc);

  console.log('');
  console.log('='.repeat(60));
  console.log('MY PERMIT STATUS');
  console.log('='.repeat(60));

  if (!sessionId) {
    console.log('  No session ID found.');
    console.log('  Run "checkin", or set $env:CLAUDE_SESSION_ID, or pass --session.');
  } else {
    console.log(`  Session ID : ${sessionId}`);

    const myLease = getMyLease(state.leases, sessionId);
    if (!myLease) {
      // Check if session exists but permit expired
      let found = false;
      for (const name of VALID_NAMES) {
        const lease = state.leases[name];
        if (lease && lease.sessionId === sessionId) {
          console.log(`  Name       : ${name} — PERMIT EXPIRED`);
          console.log(`  Expired    : ${formatTime(lease.expiresAt)}`);
          found = true;
          break;
        }
      }
      if (!found) console.log('  Status     : NOT FOUND — no permit held');
      console.log('');
      console.log('  → Run "checkin" to acquire a new permit.');
    } else {
      const { name, lease } = myLease;
      const remaining = getLeaseTimeRemaining(lease);
      console.log(`  Name       : ${name}`);
      console.log(`  Branch     : ${lease.branch}`);
      console.log(`  Granted    : ${formatTime(lease.grantedAt)}`);
      console.log(`  Last renew : ${formatTime(lease.lastRenewedAt)}`);
      console.log(`  Expires    : ${formatTime(lease.expiresAt)}`);
      console.log(`  Remaining  : ${formatMs(remaining)}`);
      if (remaining < RENEWAL_THRESHOLD_MS) {
        console.log('  ⚠️  WARNING  : Expiring soon — run "ping" or "renew" NOW!');
      } else {
        console.log('  ✓  Permit valid');
      }
    }
  }

  console.log('');
  console.log('  ALL PERMITS:');
  console.log('  ' + '─'.repeat(40));
  for (const name of VALID_NAMES) {
    const lease = state.leases[name];
    const marker = (lease && lease.sessionId === sessionId) ? ' ← YOU' : '';
    if (!lease) {
      console.log(`  ${name}: ◯ available`);
    } else if (!isLeaseValid(lease)) {
      if (isLeaseReserved(lease)) {
        const until = new Date(new Date(lease.expiresAt).getTime() + EXPIRED_GRACE_MS);
        console.log(`  ${name}: ◌ idle-reserved — holder may return (reserved until ${formatTime(until.toISOString())})`);
      } else {
        console.log(`  ${name}: ✗ EXPIRED — will clear on next checkin`);
      }
    } else {
      const rem = getLeaseTimeRemaining(lease);
      console.log(`  ${name}: ● ${formatMs(rem)} remaining on ${lease.branch}${marker}`);
    }
  }
  console.log('='.repeat(60));
  console.log('');
}

// ─────────────────────────────────────────────────────────────────────────────
// COMMAND: read — Display full coordination state
// ─────────────────────────────────────────────────────────────────────────────

async function cmdRead() {
  const sessionId = getMySessionId();
  const doc = await getDocRef().get();
  const state = readState(doc);

  console.log('');
  console.log('='.repeat(60));
  console.log('CLAUDE CODE COORDINATION STATE (permit model v2.0)');
  console.log('='.repeat(60));
  console.log(`Timestamp : ${new Date().toLocaleString('en-GB')}`);
  console.log('');

  console.log('PERMITS:');
  console.log('─'.repeat(40));
  for (const name of VALID_NAMES) {
    const lease = state.leases[name];
    const marker = (lease && lease.sessionId === sessionId) ? '  ← YOU' : '';
    if (!lease) {
      console.log(`  ${name}: ◯ available`);
    } else if (!isLeaseValid(lease)) {
      if (isLeaseReserved(lease)) {
        const until = new Date(new Date(lease.expiresAt).getTime() + EXPIRED_GRACE_MS);
        console.log(`  ${name}: ◌ idle-reserved (expired ${formatTime(lease.expiresAt)}, held for return until ${formatTime(until.toISOString())})`);
      } else {
        console.log(`  ${name}: ✗ EXPIRED (${formatTime(lease.expiresAt)})`);
      }
    } else {
      const rem = getLeaseTimeRemaining(lease);
      console.log(`  ${name}: ● active${marker}`);
      console.log(`    Session : ${lease.sessionId}`);
      console.log(`    Branch  : ${lease.branch}`);
      console.log(`    Granted : ${formatTime(lease.grantedAt)}`);
      console.log(`    Expires : ${formatTime(lease.expiresAt)} (${formatMs(rem)} remaining)`);
    }
  }
  console.log('');

  console.log('CLAIMED FILES (NO-TOUCH ZONES):');
  console.log('─'.repeat(40));
  const claimed = Object.entries(state.claimedFiles || {});
  if (claimed.length === 0) {
    console.log('  (none)');
  } else {
    for (const [filePath, claim] of claimed) {
      console.log(`  ${filePath}`);
      console.log(`    By: ${claim.name} — since ${formatTime(claim.claimedAt)}`);
    }
  }
  console.log('');

  console.log('REGISTERED BRANCHES:');
  console.log('─'.repeat(40));
  const branches = Object.entries(state.branches || {});
  if (branches.length === 0) {
    console.log('  (none)');
  } else {
    for (const [branchName, info] of branches) {
      console.log(`  ${branchName}: ${info.description || '(no description)'}`);
    }
  }
  console.log('');

  console.log('RECENT ACTIVITY (last 10):');
  console.log('─'.repeat(40));
  const log = (state.activityLog || []).slice(-10).reverse();
  if (log.length === 0) {
    console.log('  (none)');
  } else {
    for (const entry of log) {
      console.log(`  [${formatTime(entry.timestamp)}] ${entry.name}: ${entry.message}`);
    }
  }
  console.log('');
  console.log('='.repeat(60));
  console.log('');
}

// ─────────────────────────────────────────────────────────────────────────────
// COMMAND: write — Log activity (requires valid permit)
// ─────────────────────────────────────────────────────────────────────────────

async function cmdWrite(message) {
  if (!message) {
    console.error('Usage: node claude-sync.js write "message" [--session SESSION_ID]');
    process.exit(1);
  }

  const sessionId = getMySessionId();
  let result = null;

  await db.runTransaction(async (t) => {
    result = null;
    const docRef = getDocRef();
    const doc = await t.get(docRef);
    const state = readState(doc);

    const myLease = getMyLease(state.leases, sessionId);
    if (!myLease) {
      result = { ok: false };
      return;
    }

    const { name, lease } = myLease;
    addLog(state, sessionId, name, lease.branch, message);
    t.set(docRef, state);
    result = { ok: true, name };
  });

  if (!result || !result.ok) {
    console.error('No valid permit. Run "checkin" first.');
    process.exit(1);
  }

  console.log(`${result.name.toLowerCase()}> Logged: "${message}"`);
}

// ─────────────────────────────────────────────────────────────────────────────
// COMMAND: claim — Claim file ownership (requires valid permit)
// ─────────────────────────────────────────────────────────────────────────────

async function cmdClaim(filePath) {
  if (!filePath) {
    console.error('Usage: node claude-sync.js claim /path/to/file [--session SESSION_ID]');
    process.exit(1);
  }

  const sessionId = getMySessionId();
  let result = null;

  await db.runTransaction(async (t) => {
    result = null;
    const docRef = getDocRef();
    const doc = await t.get(docRef);
    const state = readState(doc);

    const myLease = getMyLease(state.leases, sessionId);
    if (!myLease) {
      result = { ok: false, reason: 'no-permit' };
      return;
    }

    const { name, lease } = myLease;
    state.claimedFiles = state.claimedFiles || {};
    const existing = state.claimedFiles[filePath];

    if (existing && existing.sessionId !== sessionId && existing.name !== name) {
      result = { ok: false, reason: 'already-claimed', existing };
      return;
    }

    state.claimedFiles[filePath] = {
      sessionId, name,
      claimedAt: new Date().toISOString(),
      branch: lease.branch
    };

    addLog(state, sessionId, name, lease.branch, `Claimed: ${filePath}`);
    t.set(docRef, state);
    result = { ok: true, name };
  });

  if (!result) { console.error('Transaction failed.'); process.exit(1); }

  if (result.reason === 'no-permit') {
    console.error('No valid permit. Run "checkin" first.');
    process.exit(1);
  }

  if (result.reason === 'already-claimed') {
    const ex = result.existing;
    console.error('');
    console.error('='.repeat(60));
    console.error(`ERROR: ${filePath} is already claimed by ${ex.name}!`);
    console.error(`Since: ${formatTime(ex.claimedAt)}`);
    console.error('This is a NO-TOUCH ZONE. Coordinate with the holder first.');
    console.error('='.repeat(60));
    process.exit(1);
  }

  console.log(`${result.name.toLowerCase()}> Claimed: ${filePath}`);
}

// ─────────────────────────────────────────────────────────────────────────────
// COMMAND: release — Release file ownership (requires valid permit)
// ─────────────────────────────────────────────────────────────────────────────

async function cmdRelease(filePath) {
  if (!filePath) {
    console.error('Usage: node claude-sync.js release /path/to/file [--session SESSION_ID]');
    process.exit(1);
  }

  const sessionId = getMySessionId();
  let result = null;

  await db.runTransaction(async (t) => {
    result = null;
    const docRef = getDocRef();
    const doc = await t.get(docRef);
    const state = readState(doc);

    const myLease = getMyLease(state.leases, sessionId);
    if (!myLease) {
      result = { ok: false, reason: 'no-permit' };
      return;
    }

    const { name, lease } = myLease;
    state.claimedFiles = state.claimedFiles || {};
    const existing = state.claimedFiles[filePath];

    if (!existing) {
      result = { ok: true, name, notFound: true };
      return;
    }

    if (existing.sessionId !== sessionId && existing.name !== name) {
      // Allow release but warn
      process.stderr.write(`WARNING: ${filePath} was claimed by ${existing.name}, releasing anyway.\n`);
    }

    delete state.claimedFiles[filePath];
    addLog(state, sessionId, name, lease.branch, `Released: ${filePath}`);
    t.set(docRef, state);
    result = { ok: true, name };
  });

  if (!result || result.reason === 'no-permit') {
    console.error('No valid permit. Run "checkin" first.');
    process.exit(1);
  }

  if (result.notFound) {
    console.log(`${result.name.toLowerCase()}> ${filePath} was not claimed.`);
    return;
  }

  console.log(`${result.name.toLowerCase()}> Released: ${filePath}`);
}

// ─────────────────────────────────────────────────────────────────────────────
// COMMAND: register — Register current branch
// ─────────────────────────────────────────────────────────────────────────────

async function cmdRegister(description) {
  if (!description) {
    console.error('Usage: node claude-sync.js register "description"');
    process.exit(1);
  }

  const branch = getCurrentBranch();
  const sessionId = getMySessionId();
  let result = null;

  await db.runTransaction(async (t) => {
    result = null;
    const docRef = getDocRef();
    const doc = await t.get(docRef);
    const state = readState(doc);

    const myLease = getMyLease(state.leases, sessionId);
    const name = myLease?.name || branch;

    state.branches = state.branches || {};
    state.branches[branch] = {
      description,
      registeredAt: new Date().toISOString()
    };

    addLog(state, sessionId || 'anonymous', name, branch, `Registered branch: ${description}`);
    t.set(docRef, state);
    result = { ok: true, name };
  });

  console.log(`${result.name.toLowerCase()}> Registered branch '${branch}': ${description}`);
}

// ─────────────────────────────────────────────────────────────────────────────
// COMMAND: gc — Clean up expired permits
// ─────────────────────────────────────────────────────────────────────────────

async function cmdGc() {
  let result = null;

  await db.runTransaction(async (t) => {
    result = null;
    const docRef = getDocRef();
    const doc = await t.get(docRef);
    const state = readState(doc);
    result = gcExpiredLeases(state);
    t.set(docRef, state);
  });

  if (result.cleared.length > 0) {
    console.log('');
    console.log('='.repeat(60));
    console.log('GARBAGE COLLECTION');
    console.log('='.repeat(60));
    console.log(`Expired ${result.cleared.length} permit(s):`);
    for (const d of result.cleared) console.log(`  - ${d}`);
    if (result.releasedFiles.length > 0) {
      console.log(`Released ${result.releasedFiles.length} file(s):`);
      for (const f of result.releasedFiles) console.log(`  - ${f}`);
    }
    console.log('='.repeat(60));
    console.log('');
  } else {
    console.log('No expired permits to clean up.');
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// COMMAND: watchdog — Standalone peer sleep report (backward compat alias)
// ─────────────────────────────────────────────────────────────────────────────

async function cmdWatchdog() {
  const sessionId = getMySessionId();
  const doc = await getDocRef().get();
  const state = readState(doc);
  const lines = watchdogReport(state.leases, sessionId);

  console.log(`Watchdog report at ${formatTime(new Date().toISOString())}`);
  if (lines.length === 0) {
    console.log('All permit holders active. No sleeping sessions detected.');
  } else {
    for (const line of lines) console.log(line);
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Main
// ─────────────────────────────────────────────────────────────────────────────

async function main() {
  const plainArgs = getPlainArgs();
  const param = plainArgs.join(' ');

  try {
    switch (CMD) {
      case 'init':      await cmdInit(); break;
      case 'checkin':   await cmdCheckin(); break;
      case 'checkout':  await cmdCheckout(); break;
      case 'renew':     await cmdRenew(); break;
      case 'ping':      await cmdPing(); break;
      case 'status':    await cmdStatus(); break;
      case 'read':      await cmdRead(); break;
      case 'write':     await cmdWrite(param); break;
      case 'claim':     await cmdClaim(param); break;
      case 'release':   await cmdRelease(param); break;
      case 'register':  await cmdRegister(param); break;
      case 'gc':        await cmdGc(); break;
      case 'watchdog':  await cmdWatchdog(); break;
      default:
        console.log('claude-sync.js v2.2 — DHCP-style permit coordination');
        console.log('');
        console.log('Model: Bill (1st) → Bob (2nd) → Ben (3rd). Max 3. 5-min TTL permits.');
        console.log('Multi-window identity is automatic inside Claude Code (per-window session map).');
        console.log('Recently-expired slots stay RESERVED 30 min for their (possibly idle) holder.');
        console.log('');
        console.log('Commands:');
        console.log('  checkin [--name Bill|Bob|Ben]         - Acquire permit (auto-assigns if no --name)');
        console.log('  checkin --any                         - Next free slot, ignore CLAUDE_IDENTITY env');
        console.log('  checkin --name Bill --force --human-ok - Evict current Bill and claim (HUMAN ONLY)');
        console.log('  checkout [--session ID]               - Surrender your permit');
        console.log('  checkout --force [Name]               - Admin: force-clear one or all permits');
        console.log('  renew [--session ID]          - Extend permit by 5 min');
        console.log('  renew --auto [--session ID]   - Hook mode: renew only if < 2.5 min left');
        console.log('  ping [--session ID]           - Renew + heartbeat + watchdog');
        console.log('  status [--session ID]         - My permit status + time remaining');
        console.log('  read                          - All permits + state');
        console.log('  write "msg" [--session ID]    - Log activity (requires permit)');
        console.log('  claim /path [--session ID]    - Claim file (requires permit)');
        console.log('  release /path [--session ID]  - Release file (requires permit)');
        console.log('  register "description"        - Register current branch');
        console.log('  gc                            - Clean up expired permits');
        console.log('  init                          - Initialise fresh state');
        console.log('');
        console.log('Hook (.claude/settings.json):');
        console.log('  "UserPromptSubmit": [{"hooks": [{"type": "command",');
        console.log('    "command": "node .../claude-sync.js renew --auto"}]}]');
        console.log('');
        console.log('Per-terminal env var (only needed OUTSIDE Claude Code windows):');
        console.log('  PowerShell: $env:CLAUDE_SESSION_ID = "session-XXXXXXXXXX"');
        break;
    }
  } catch (error) {
    console.error('Error:', error.message);
    if (process.env.DEBUG) console.error(error.stack);
    process.exit(1);
  }

  process.exit(0);
}

main();
