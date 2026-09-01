// Module key: tool.gitprepushgate
// Spec ref: GR#28 (Verify Against CI, Not Just Locally); BUG-340 destructive round r1
//
// ATTACK REGRESSIONS — written by the INDEPENDENT destructive attacker for
// BUG-340 (round r1, Opus, 2026-09-01), NOT by the author of
// githooks/pre-push. Both findings were reproduced LIVE against the real
// Metropolis working tree. They assert the CORRECT behaviour, so they are
// RED until the holes are closed — that is deliberate.
//
//   B1  runGofmtCheck() spawns `gofmt -l <every tracked .go file>`. This
//       repo tracks 1568 .go files, ~59,770 bytes of argv, against Windows'
//       32,767-byte CreateProcess limit. Running the real hook against the
//       real repo produced:
//         "gofmt -l" failed: gofmt could not run: spawnSync gofmt ENAMETOOLONG
//       i.e. the gate REJECTS EVERY push to main on the project's own
//       documented-Windows environment, and the only escape is
//       CLAUDE_DISABLE_PREPUSH_GATE=1, which disables the whole gate. CI
//       itself runs `gofmt -l .` (a directory, no argv blowup) — the fix is
//       to do the same, or to batch/chunk the file list.
//   B2  parsePushedRefs() silently DROPS any stdin line that is not exactly
//       four whitespace-separated fields, and touchesMainPush() then reads
//       "nothing recognised" as "does not touch main" => ALLOW. That is a
//       fail-OPEN in a file whose own header declares it fail-closed, and it
//       contradicts this repo's standing rule that a gate which cannot
//       evaluate must not report success.

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');

const pp = require('./pre-push');

const SHA = 'a'.repeat(40);
const ZERO = '0'.repeat(40);

test('BUG-340 r1 B1: the gofmt gate must survive a repo-sized .go file list (Windows argv limit)', () => {
  // 1568 paths of realistic length — the live count of tracked .go files in
  // this repo at the time of round r1.
  const files = Array.from({ length: 1568 }, (_, i) => `internal/engine/subsystem${i}/some_reasonably_named_file${i}.go`);
  const argvBytes = files.join(' ').length;
  assert.ok(argvBytes > 32767, `fixture must exceed the Windows argv limit to be a real test (got ${argvBytes})`);

  const result = pp.runGofmtCheck(process.cwd(), files);
  assert.equal(
    /ENAMETOOLONG|E2BIG|could not run/.test(result.output || ''), false,
    `the pre-push gate must not brick every push to main on a repo of this size. Got: ${result.output}. ` +
      'CI runs `gofmt -l .` (a directory) — do the same, or chunk the file list.'
  );
});

test('BUG-340 r1 B2: an UNPARSEABLE pre-push stdin line must fail closed, not be silently dropped into "allow"', () => {
  // A five-field line naming refs/heads/main. parsePushedRefs() drops it, so
  // touchesMainPush() reports false and checkPush() returns { ok:true,
  // skipped:true } — a push to main that ran zero gates.
  const malformed = `refs/heads/main ${SHA} refs/heads/main ${ZERO} unexpected-extra-field`;
  const runners = {
    gofmt: () => ({ ok: true, output: '' }),
    build: () => ({ ok: true, output: '' }),
    vet: () => ({ ok: true, output: '' }),
    listGoFiles: () => [],
  };
  const result = pp.checkPush(malformed, process.cwd(), runners);
  assert.equal(
    result.skipped, undefined,
    'a line this hook cannot parse must not be reported as "does not touch main"; ' +
      'a gate that cannot evaluate must not report success'
  );
});

test('BUG-340 r1 B2 (control, PROVE-CAN-FAIL): a well-formed main line IS gated', () => {
  const wellFormed = `refs/heads/main ${SHA} refs/heads/main ${ZERO}`;
  let gofmtRan = false;
  const runners = {
    gofmt: () => { gofmtRan = true; return { ok: true, output: '' }; },
    build: () => ({ ok: true, output: '' }),
    vet: () => ({ ok: true, output: '' }),
    listGoFiles: () => [],
  };
  const result = pp.checkPush(wellFormed, process.cwd(), runners);
  assert.equal(result.ok, true);
  assert.equal(result.skipped, undefined);
  assert.equal(gofmtRan, true, 'the control case must actually reach the gates');
});
