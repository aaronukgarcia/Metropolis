#!/usr/bin/env node
// Root shim (FEAT-2326609811 repo-root tidy) — the real CLI lives in
// harness/claude-bow.js. Kept at the repo root so the ~327 documented
// `node claude-bow.js ...` invocations (CLAUDE.md, the skills, the SessionStart
// hook, muscle memory) keep working unchanged after the tidy. Pure CLI re-exec:
// nothing `require()`s this file as a module (every module import of claude-bow
// is an intra-harness `require('./claude-bow.js')` that resolves to the real
// file), so a child-process delegation that preserves argv/stdio/exit is exact.
const { spawnSync } = require('node:child_process');
const path = require('node:path');
const real = path.join(__dirname, 'harness', 'claude-bow.js');
const r = spawnSync(process.execPath, [real, ...process.argv.slice(2)], { stdio: 'inherit' });
process.exit(r.status === null ? 1 : r.status);
