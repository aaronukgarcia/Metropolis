/**
 * claude-sync-permitstate.test.js — closes the ASM-734 gap for tool.sync
 * (FEAT-107): claude-sync.test.js is scoped entirely to FEAT-069 message
 * delivery and FEAT-070 standing-loop regressions and requires a live scratch
 * MariaDB; the CORE permit state machine (slot state transitions, retired/
 * parked slot classification, checkin-only loop detection) had NO dedicated,
 * DB-free suite, per docs/planning/acceptance/tool.sync.md AC-25.
 *
 * This suite exercises ONLY the pure, already-exported logic that needs no
 * database, no subprocess, and no wall clock: `slotState`, `isRetired`,
 * `isParked`, `isUnusable`, `retiredMessage`, `parkedMessage`,
 * `unusableMessage`, `liveNames`, `isCheckinOnly`, and the `NAMES`/`RETIRED`/
 * `PARKED` fixtures themselves — all fixture-driven with an explicit `now`,
 * never `Date.now()`. Everything requiring a live `metro` connection (TTL
 * write-back on checkin, RESERVE-window persistence, force-evict, checkout,
 * gc, wake-recovery re-acquire) is EXPLICITLY OUT OF SCOPE here — see the
 * "Known gap" note at the bottom, which is the honest finding this task asked
 * for rather than a fragile DB-backed integration test.
 *
 * Covers docs/planning/acceptance/tool.sync.md:
 *   AC-5  the three-state slotState(row, now) mapping (ACTIVE/RESERVED/FREE).
 *   AC-6  reboot (boot_id mismatch) voids a permit AND its reservation.
 *   (retired/parked classification, referenced throughout section C/D as the
 *    slot-name validation surface `checkin --name` builds on)
 *
 * Run: node --test claude-sync-permitstate.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const {
  NAMES, RETIRED, PARKED,
  isRetired, isParked, isUnusable,
  retiredMessage, parkedMessage, unusableMessage,
  liveNames, slotState, isCheckinOnly,
  BOOT_ID,
} = require('./claude-sync.js');

// Contract constants from docs/planning/acceptance/tool.sync.md ("What the
// permit contract is") — restated here, not re-derived from the module
// (TTL_MS/RESERVE_MS are internal, unexported constants), so a change to
// either the code OR the doc without updating the other shows up as a test
// failure rather than silently drifting apart.
const TTL_MS = 5 * 60 * 1000;
const RESERVE_MS = 30 * 60 * 1000;
// The real BOOT_ID this process's claude-sync.js module computed at require
// time (now exported for exactly this purpose — see the export's own
// comment). Matching rows exercise ACTIVE/RESERVED/FREE; a literal mismatched
// string exercises the reboot-invalidation branch (AC-6).
const THIS_BOOT = BOOT_ID;

function row(overrides = {}) {
  return {
    session_id: 'sess-1',
    released: 0,
    boot_id: THIS_BOOT,
    expires_ms: 0,
    ...overrides,
  };
}

// ── AC-5: the three-state slotState mapping ─────────────────────────────────

test('AC-5: slotState is ACTIVE when expires_ms is in the future and boot matches', () => {
  const now = 1_000_000;
  const r = row({ expires_ms: now + TTL_MS });
  assert.equal(slotState(r, now), 'ACTIVE');
});

test('AC-5: slotState is RESERVED when expired less than RESERVE_MS ago, boot matches', () => {
  const now = 1_000_000;
  const r = row({ expires_ms: now - 1000 }); // expired 1s ago, well under 30min
  assert.equal(slotState(r, now), 'RESERVED');
});

test('AC-5: slotState is FREE once the reservation lapses (expired >= RESERVE_MS ago)', () => {
  const now = 1_000_000;
  const r = row({ expires_ms: now - RESERVE_MS }); // exactly at the boundary -> lapsed
  assert.equal(slotState(r, now), 'FREE');
});

test('AC-5: the RESERVED/FREE boundary is exclusive at exactly RESERVE_MS (real prove-can-fail boundary)', () => {
  const now = 1_000_000;
  const justInside = row({ expires_ms: now - (RESERVE_MS - 1) });
  const justOutside = row({ expires_ms: now - RESERVE_MS });
  assert.equal(slotState(justInside, now), 'RESERVED', 'one ms inside the window must still be RESERVED');
  assert.equal(slotState(justOutside, now), 'FREE', 'exactly at RESERVE_MS must have lapsed to FREE');
});

test('AC-5: slotState is FREE when released, regardless of expires_ms', () => {
  const now = 1_000_000;
  const r = row({ released: 1, expires_ms: now + TTL_MS }); // would be ACTIVE if not released
  assert.equal(slotState(r, now), 'FREE');
});

test('AC-5: slotState is FREE when there is no session_id at all (never held)', () => {
  const now = 1_000_000;
  const r = row({ session_id: null, expires_ms: now + TTL_MS });
  assert.equal(slotState(r, now), 'FREE');
});

// ── AC-6: reboot voids permits AND reservations ─────────────────────────────

test('AC-6: a boot_id mismatch is FREE even with an in-the-future expires_ms (would-be ACTIVE)', () => {
  const now = 1_000_000;
  const r = row({ boot_id: 'dead-boot', expires_ms: now + TTL_MS });
  assert.equal(slotState(r, now), 'FREE');
});

test('AC-6: a boot_id mismatch is FREE even inside the reservation window (would-be RESERVED)', () => {
  const now = 1_000_000;
  const r = row({ boot_id: 'dead-boot', expires_ms: now - 1000 });
  assert.equal(slotState(r, now), 'FREE', 'a reservation must never survive a reboot');
});

// ── Retired / parked / unusable classification ───────────────────────────────

test('NAMES / RETIRED / PARKED fixtures are disjoint and non-empty', () => {
  assert.ok(NAMES.length > 0);
  for (const r of RETIRED) assert.ok(!NAMES.includes(r), `${r} is RETIRED and must not also be a live NAMES entry`);
  for (const p of PARKED) assert.ok(NAMES.includes(p), `${p} (PARKED) must still be a seeded NAMES entry, unlike RETIRED`);
});

test('isRetired matches RETIRED names case-insensitively, and rejects live/parked names', () => {
  for (const r of RETIRED) {
    assert.equal(isRetired(r), true);
    assert.equal(isRetired(r.toUpperCase()), true);
    assert.equal(isRetired(r.toLowerCase()), true);
  }
  for (const n of NAMES.filter(n => !RETIRED.includes(n))) {
    assert.equal(isRetired(n), false, `${n} is a live/parked slot, not retired`);
  }
});

test('isParked matches PARKED names case-insensitively, and rejects live/retired names', () => {
  for (const p of PARKED) {
    assert.equal(isParked(p), true);
    assert.equal(isParked(p.toUpperCase()), true);
  }
  for (const n of NAMES.filter(n => !PARKED.includes(n))) {
    assert.equal(isParked(n), false);
  }
});

test('isUnusable is true for retired OR parked, false for any live slot', () => {
  for (const r of RETIRED) assert.equal(isUnusable(r), true);
  for (const p of PARKED) assert.equal(isUnusable(p), true);
  for (const n of NAMES.filter(n => !RETIRED.includes(n) && !PARKED.includes(n))) {
    assert.equal(isUnusable(n), false);
  }
});

test('unusableMessage dispatches to the parked message for a parked name and the retired message otherwise', () => {
  for (const p of PARKED) {
    assert.equal(unusableMessage(p), parkedMessage(p));
    assert.match(unusableMessage(p), /parked/i);
  }
  for (const r of RETIRED) {
    assert.equal(unusableMessage(r), retiredMessage(r));
    assert.match(unusableMessage(r), /retired/i);
  }
});

test('retiredMessage and parkedMessage each name the caller-supplied candidate and list the live slots', () => {
  if (RETIRED.length) {
    const msg = retiredMessage(RETIRED[0]);
    assert.match(msg, new RegExp(RETIRED[0]));
    for (const live of liveNames()) assert.match(msg, new RegExp(live), `must list live slot ${live}`);
  }
  if (PARKED.length) {
    const msg = parkedMessage(PARKED[0]);
    assert.match(msg, new RegExp(PARKED[0]));
    for (const live of liveNames()) assert.match(msg, new RegExp(live), `must list live slot ${live}`);
  }
});

test('liveNames excludes every retired and parked slot and includes every other NAMES entry', () => {
  const live = liveNames();
  for (const r of RETIRED) assert.ok(!live.includes(r));
  for (const p of PARKED) assert.ok(!live.includes(p));
  for (const n of NAMES.filter(n => !RETIRED.includes(n) && !PARKED.includes(n))) {
    assert.ok(live.includes(n), `${n} is neither retired nor parked and must be listed as live`);
  }
});

// ── isCheckinOnly (BUG-356 silent-failure detection helper) ─────────────────

test('isCheckinOnly: true only when checkin is invoked and read is not', () => {
  assert.equal(isCheckinOnly('node claude-sync.js checkin --any'), true);
  assert.equal(isCheckinOnly('15m node claude-sync.js checkin --any && node claude-sync.js read'), false);
  assert.equal(isCheckinOnly('node claude-sync.js read'), false);
  assert.equal(isCheckinOnly('15m /oversight-sweep'), false, 'a skill invocation is not inspectable and must not be flagged');
});

test('isCheckinOnly avoids the documented false-positive/false-negative traps', () => {
  // "checkin-dashboard" mentions the word but is not the actual command.
  assert.equal(isCheckinOnly('node checkin-dashboard.js'), false);
  // A bare mention of "readme" must not be mistaken for the read command.
  assert.equal(isCheckinOnly('node claude-sync.js checkin --any # see readme.md'), true);
});

test('isCheckinOnly is false/safe for non-string or empty input', () => {
  assert.equal(isCheckinOnly(''), false);
  assert.equal(isCheckinOnly(null), false);
  assert.equal(isCheckinOnly(undefined), false);
});

// ── Known gap (honest finding, not papered over) ─────────────────────────────
//
// The following ASM-734/AC-25 contract items remain UNCOVERED by any suite in
// this repo and are NOT exercised here: TTL write-back on `checkin` (AC-1),
// the `renew --auto` 3.5-minute threshold decision as persisted to the DB
// (AC-2), `--force --human-ok` eviction (AC-7), `checkout` (AC-9), `gc`
// (AC-10), and wake-recovery's DB-driven re-acquire (AC-14). Each of those
// lives inside `cmdCheckin`/`cmdRenew`/`cmdCheckout`/`cmdGc`, which are async
// functions that open a real `mysql2` connection via `connectCLI` at the top
// of their body — there is no seam to inject a fake connection without either
// (a) a live scratch MariaDB (the FEAT-069-style fixture claude-sync.test.js
// already uses, which this task's brief asked us NOT to add another
// DB-dependent suite of), or (b) a refactor of claude-sync.js to accept an
// injected `db` handle, which is a real but non-trivial change out of scope
// for a test-only pass. Recorded here rather than covered with a fragile
// integration test — the ASM-734/AC-25 gap for the DB-touching commands is a
// genuine follow-up item, not resolved by this file.
