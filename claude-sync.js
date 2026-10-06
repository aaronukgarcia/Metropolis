#!/usr/bin/env node
// Root shim (FEAT-2326609811 repo-root tidy) — the real CLI lives in
// harness/claude-sync.js. Kept at the repo root so the ~47 documented
// `node claude-sync.js ...` invocations (CLAUDE.md, skills, the SessionStart
// hook via claude-startup.js, muscle memory) keep working unchanged after the
// tidy. Pure CLI re-exec: every module import of claude-sync is an intra-harness
// `require('./claude-sync.js')` that resolves to the real file, so nothing
// requires this shim as a module and a child-process delegation is exact.
const { spawnSync } = require('node:child_process');
const path = require('node:path');
const real = path.join(__dirname, 'harness', 'claude-sync.js');
const r = spawnSync(process.execPath, [real, ...process.argv.slice(2)], { stdio: 'inherit' });
process.exit(r.status === null ? 1 : r.status);
