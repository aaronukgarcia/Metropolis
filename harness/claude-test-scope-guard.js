#!/usr/bin/env node
// Spec ref: agent test-verification contract (docs/planning/agent-test-verification-contract.md).
// Module key: reserved for tooling (root tooling exempt per CLAUDE.md GR#2); no code.json GUID.
//
// PreToolUse (Bash) COST/HYGIENE guard — "test scope" guard.
//
// WHY THIS EXISTS (2026-09-02, Aaron: "such a waste ... improve the harness,
// better monitoring, better specification for the testing. you're better than
// this"):
//
// A dispatched build agent ran the FULL webconsole suite — `npm test`
// (= `node --test "test/*.test.mjs" && tsx --test <14 files>`) — which in this
// environment emits 60k+ lines with the default reporter, gets KILLED by the
// harness for flooding, and is RE-RUN by the agent. That unbounded
// kill-and-retry loop burned ~1h37m and ~309k tokens and added ZERO
// verification value over (a) the targeted suites the change touches and
// (b) CI's node-test job, which runs the full glob properly.
//
// This guard makes the runaway command REFUSE to launch and redirects to the
// bounded, concise runner (`node tools/test/scoped.mjs`), which has a hard
// timeout and the `dot` reporter so it can neither flood nor hang-and-retry.
//
// POLICY (deliberately narrow — block ONLY the known flooding signatures, let
// every scoped run through):
//   BLOCKED (exit 2):
//     * `npm test` / `npm run test`  (any cwd) — the exact flood.
//     * `node --test` with a GLOB target (test/*, *.test, **) or with NO
//       file/path argument at all (bare whole-tree discovery).
//     * `tsx --test` with a glob target, or with more than MAX_NAMED files.
//   ALLOWED (exit 0):
//     * `node tools/test/scoped.mjs ...`   (the sanctioned bounded runner)
//     * `node --test <named files>` / `tsx --test <named files>` up to
//       MAX_NAMED explicit files, no glob — the normal targeted path.
//     * ANYTHING when CLAUDE_ALLOW_FULL_TEST=1 is set in the environment
//       (the escape hatch for a deliberate, supervised full run, e.g. a lead
//       reproducing CI locally — set it BEFORE the command, never inline).
//
// FAIL-OPEN. This is a cost/hygiene guard, not a security control: on any
// parse ambiguity or its own error it ALLOWS (exit 0). A missed detection
// costs at worst the pre-guard status quo; a false block costs an annoying
// redirect. That asymmetry is why a plain regex over the raw command is fine
// here (contrast the fail-CLOSED security guards, where "a regex is not a
// shell parser" forbids exactly this shortcut).

'use strict';

const MAX_NAMED = 12; // more than a full estate's touched files ⇒ treat as a flood

function readStdin() {
  try {
    const fs = require('fs');
    return fs.readFileSync(0, 'utf8');
  } catch {
    return '';
  }
}

// Extract the shell command string from the PreToolUse hook payload.
function extractCommand(raw) {
  if (!raw) return '';
  try {
    const obj = JSON.parse(raw);
    const c = obj && obj.tool_input && obj.tool_input.command;
    return typeof c === 'string' ? c : '';
  } catch {
    return '';
  }
}

// Does the command invoke the sanctioned bounded runner? If so, always allow —
// even if a later part of a compound command would otherwise match.
function usesScopedRunner(cmd) {
  return /tools[\/\\]test[\/\\]scoped\.mjs\b/.test(cmd);
}

// Split into pipeline/statement segments so we judge each `--test` invocation
// on ITS OWN arguments, not the whole compound string.
function segments(cmd) {
  return cmd.split(/\|\||&&|;|\n|\|/).map((s) => s.trim()).filter(Boolean);
}

const GLOB_RE = /[*?]|\*\*|\.test['"]?\s*$/; // a glob-y target
// Count bareword file-ish targets after `--test` in one segment.
function testTargetsInfo(seg) {
  // everything after the first --test token
  const m = seg.match(/--test\b(.*)$/s);
  if (!m) return null;
  const tail = m[1].trim();
  // strip known flags (--test-reporter=..., --test-name-pattern=..., etc.)
  const withoutFlags = tail.replace(/--[a-zA-Z0-9-]+(=[^\s]+)?/g, ' ').trim();
  const tokens = withoutFlags.length ? withoutFlags.split(/\s+/).filter(Boolean) : [];
  const hasGlob = tokens.some((t) => GLOB_RE.test(t)) || /"[^"]*[*?][^"]*"|'[^']*[*?][^']*'/.test(tail);
  return { count: tokens.length, hasGlob };
}

function decide(cmd) {
  if (!cmd) return { block: false };
  if (process.env.CLAUDE_ALLOW_FULL_TEST === '1') return { block: false };
  if (usesScopedRunner(cmd)) return { block: false };

  for (const seg of segments(cmd)) {
    // `npm test` / `npm run test`
    if (/\bnpm\s+(run\s+)?test\b/.test(seg)) {
      return { block: true, why: '`npm test` runs the entire webconsole suite (node --test "test/*.test.mjs" && tsx --test <14 files>) — 60k+ lines that flood and get killed.' };
    }
    // node/tsx --test flooding forms
    if (/\b(node|tsx|npx\s+tsx)\b[^|;&]*--test\b/.test(seg)) {
      const info = testTargetsInfo(seg);
      if (info) {
        if (info.hasGlob) {
          return { block: true, why: 'a `--test` run with a glob target (e.g. test/*.test.mjs) discovers the whole suite and floods the harness.' };
        }
        if (info.count === 0) {
          return { block: true, why: 'a bare `--test` with no file argument discovers and runs every test in the tree — the whole-suite flood.' };
        }
        if (info.count > MAX_NAMED) {
          return { block: true, why: `a \`--test\` run naming ${info.count} files (> ${MAX_NAMED}) is effectively a full-suite run — scope it to the files your change touched.` };
        }
      }
    }
  }
  return { block: false };
}

function main() {
  const cmd = extractCommand(readStdin());
  let verdict;
  try {
    verdict = decide(cmd);
  } catch {
    // fail-open on any guard error
    process.exit(0);
  }
  if (verdict && verdict.block) {
    process.stderr.write(
      '\n🛑 TEST-SCOPE GUARD (2026-09-02, Aaron "improve the harness / better testing spec"): this run floods the harness and wastes time+tokens.\n' +
      `Reason: ${verdict.why}\n\n` +
      'Verification contract (docs/planning/agent-test-verification-contract.md):\n' +
      '  1. Run ONLY the test files your change touches, via the bounded runner:\n' +
      '       node tools/test/scoped.mjs <file> [<file> ...]\n' +
      '     (hard timeout + concise `dot` reporter — it cannot flood or hang-and-retry)\n' +
      '  2. Typecheck with:  npx tsc --noEmit   (in webconsole/)\n' +
      '  3. Do NOT run the whole suite locally — CI\'s node-test job certifies the full glob.\n' +
      '     If you truly must (a supervised lead run), use the bounded full set:\n' +
      '       node tools/test/scoped.mjs --webconsole-ci\n' +
      '     or set CLAUDE_ALLOW_FULL_TEST=1 BEFORE the command to bypass this guard.\n'
    );
    process.exit(2);
  }
  process.exit(0);
}

// Only run when invoked directly (so the .test.js can import decide()).
if (require.main === module) {
  main();
}

module.exports = { decide, extractCommand, usesScopedRunner, testTargetsInfo, segments, MAX_NAMED };
