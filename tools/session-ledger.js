#!/usr/bin/env node
// forge:script.safe.session-ledger.js@1
// session-ledger.js - per-session ledger in <repo>/_sessions/, local only (SAFE-112/123, Aaron 2026-10-18: "keep _sessions local").
// Idea from thebpandey/session-detail (MIT), re-authored. The ledger is git-ignored through .git/info/exclude (never the
// tracked .gitignore) and still reaches the nightly SAFE archive (the projects/git streams do not exclude it).
//
//   path [--session <id>]      Ensure _sessions/ exists and is excluded, then print the ledger path for this session and
//                              the next checkpoint number. Session id: --session, else CLAUDE_CODE_SESSION_ID (the variable
//                              this harness sets; CLAUDE_SESSION_ID is read only as a fallback), else "unknown-<date>".
//   check <file>               Secret scan of a ledger before or after writing: gitleaks (dir mode) when installed, else a
//                              built-in pattern list. Exit 1 on a finding (values never printed).
//   auto [--trigger t]         PreCompact hook entry (stdin JSON: session_id, cwd, trigger). Appends a mechanical checkpoint:
//                              branch, HEAD, commits since the last checkpoint, working-tree status. Never blocks: exit 0.
//
// File: _sessions/<YYYY-MM-DD>-<sid8>.md. First line is a marker; each write is "## Checkpoint <n>" and covers only what
// the previous checkpoint does not (the boundary is the file's content, not the clock).
'use strict';
const fs = require('fs'), path = require('path'), { execFileSync } = require('child_process');

const argv = process.argv.slice(2), cmd = argv[0];
const opt = (k, d) => { const i = argv.indexOf(k); return i > -1 && i + 1 < argv.length ? argv[i + 1] : d; };
const git = (cwd, ...a) => { try { return execFileSync('git', a, { cwd, encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim(); } catch { return null; } };
const today = () => new Date().toISOString().slice(0, 10);
const stamp = () => new Date().toISOString().replace('T', ' ').slice(0, 16) + 'Z';

function repoRoot(cwd) { return git(cwd, 'rev-parse', '--show-toplevel'); }

function ensureExcluded(root) {
  const ex = git(root, 'rev-parse', '--git-path', 'info/exclude');
  if (!ex) return false;
  const file = path.isAbsolute(ex) ? ex : path.join(root, ex);
  const cur = fs.existsSync(file) ? fs.readFileSync(file, 'utf8') : '';
  if (!cur.split(/\r?\n/).includes('_sessions/')) {
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.appendFileSync(file, (cur && !cur.endsWith('\n') ? '\n' : '') + '_sessions/\n');
  }
  try { execFileSync('git', ['check-ignore', '-q', '_sessions/probe.md'], { cwd: root, stdio: 'ignore' }); return true; } catch { return false; }
}

function sessionId(given) {
  return given || process.env.CLAUDE_CODE_SESSION_ID || process.env.CLAUDE_SESSION_ID || `unknown-${today()}`;
}

function ledger(root, sid) {
  const dir = path.join(root, '_sessions');
  fs.mkdirSync(dir, { recursive: true });
  const sid8 = sid.replace(/[^A-Za-z0-9]/g, '').slice(0, 8);
  // One file per session: reuse an existing file whose marker carries this session id (a session can cross midnight).
  for (const f of fs.readdirSync(dir).filter(f => f.endsWith('.md'))) {
    const first = fs.readFileSync(path.join(dir, f), 'utf8').split(/\r?\n/, 1)[0];
    if (first.includes(`session=${sid} `)) return path.join(dir, f);
  }
  return path.join(dir, `${today()}-${sid8}.md`);
}

function checkpoints(file) {
  if (!fs.existsSync(file)) return { n: 0, lastSha: null };
  const text = fs.readFileSync(file, 'utf8');
  const n = (text.match(/^## Checkpoint \d+/gm) || []).length;
  const shas = [...text.matchAll(/^HEAD: ([0-9a-f]{40})/gm)];
  return { n, lastSha: shas.length ? shas[shas.length - 1][1] : null };
}

function header(sid, root) {
  return `<!-- session-ledger | session=${sid} | project=${path.basename(root)} | started=${stamp()} -->\n# Session ledger ${path.basename(root)} ${today()}\n\nLocal only (git-ignored via .git/info/exclude). Never write a secret value here: use [REDACTED: <kind>].\n`;
}

function cmdPath() {
  const root = repoRoot(process.cwd()); if (!root) { console.error('session-ledger: not in a git repo'); process.exit(2); }
  if (!ensureExcluded(root)) { console.error('session-ledger: could not exclude _sessions/ from git; not writing a ledger'); process.exit(1); }
  const sid = sessionId(opt('--session'));
  const file = ledger(root, sid);
  if (!fs.existsSync(file)) fs.writeFileSync(file, header(sid, root));
  const c = checkpoints(file);
  console.log(`ledger: ${file}`);
  console.log(`next: ## Checkpoint ${c.n + 1}${c.n ? ' (incremental: cover only what Checkpoint ' + c.n + ' does not)' : ' (full)'}`);
  if (sid.startsWith('unknown-')) console.log('warning: no session id in the environment; pass --session <id>');
}

const PATTERNS = [
  /gh[pousr]_[A-Za-z0-9]{36,}/, /github_pat_[A-Za-z0-9_]{40,}/, /sk-ant-[A-Za-z0-9_-]{20,}/, /sk-[A-Za-z0-9]{32,}/,
  /AKIA[0-9A-Z]{16}/, /xox[baprs]-[A-Za-z0-9-]{10,}/, /-----BEGIN [A-Z ]*PRIVATE KEY-----/,
  /AIza[0-9A-Za-z_-]{35}/, /(password|passwd|pwd|secret|token|api[_-]?key)\s*[:=]\s*["']?[^\s"'[]{8,}/i,
];
function findGitleaks() {
  const local = process.env.LOCALAPPDATA && path.join(process.env.LOCALAPPDATA, 'Microsoft', 'WinGet', 'Packages');
  try { return execFileSync(process.platform === 'win32' ? 'where' : 'which', ['gitleaks'], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).split(/\r?\n/)[0].trim(); } catch {}
  if (local && fs.existsSync(local)) for (const d of fs.readdirSync(local)) if (d.startsWith('Gitleaks.Gitleaks_')) { const p = path.join(local, d, 'gitleaks.exe'); if (fs.existsSync(p)) return p; }
  return null;
}
function cmdCheck() {
  const file = argv[1]; if (!file || !fs.existsSync(file)) { console.error('session-ledger: check <file>'); process.exit(2); }
  const gl = findGitleaks();
  if (gl) {
    try { execFileSync(gl, ['dir', '--no-banner', '--redact', '-v', '--no-color', '--log-level', 'error', file], { stdio: ['ignore', 'pipe', 'pipe'] }); console.log('check: CLEAN (gitleaks)'); return; }
    catch (e) { if (e.status === 1) { console.log('check: SECRET FOUND (gitleaks; values redacted) - replace it with [REDACTED: <kind>]'); process.stdout.write(String(e.stdout || '').split('\n').filter(l => /RuleID|Line|File/.test(l)).join('\n') + '\n'); process.exit(1); } }
  }
  const lines = fs.readFileSync(file, 'utf8').split(/\r?\n/); let hits = 0;
  lines.forEach((l, i) => PATTERNS.forEach(p => { if (p.test(l) && !/\[REDACTED/.test(l)) { hits++; console.log(`line ${i + 1}: matches ${String(p).slice(0, 30)}...`); } }));
  if (hits) { console.log('check: SECRET FOUND (pattern scan) - replace with [REDACTED: <kind>]'); process.exit(1); }
  console.log(`check: CLEAN (${gl ? 'gitleaks error, ' : ''}pattern scan)`);
}

function cmdAuto() {
  let input = ''; try { input = fs.readFileSync(0, 'utf8'); } catch {}
  let ev = {}; try { ev = JSON.parse(input || '{}'); } catch {}
  try {
    const root = repoRoot(ev.cwd || process.cwd()); if (!root || !ensureExcluded(root)) return;
    const sid = sessionId(ev.session_id);
    const file = ledger(root, sid);
    if (!fs.existsSync(file)) fs.writeFileSync(file, header(sid, root));
    const c = checkpoints(file);
    const head = git(root, 'rev-parse', 'HEAD');
    const range = c.lastSha && c.lastSha !== head ? `${c.lastSha}..HEAD` : null;
    const log = range ? git(root, 'log', '--oneline', range) : git(root, 'log', '--oneline', '--since=12 hours ago', '-n', '20');
    const st = git(root, 'status', '--short');
    const body = [
      `\n## Checkpoint ${c.n + 1} - automatic before context compaction (${ev.trigger || opt('--trigger', 'unknown')}) ${stamp()}`,
      `Branch: ${git(root, 'branch', '--show-current') || '(detached)'}`,
      `HEAD: ${head || 'none'}`,
      `Commits ${range ? 'since checkpoint ' + c.n : 'in the last 12 hours'}:`, ...(log ? log.split('\n').map(l => '- ' + l) : ['- none']),
      'Working tree:', ...(st ? st.split('\n').map(l => '    ' + l) : ['    clean']),
      'Decisions, evidence and next step: not captured mechanically; the next /update or /bye adds them as the following checkpoint.',
    ].join('\n') + '\n';
    fs.appendFileSync(file, body);
    process.stderr.write(`session-ledger: checkpoint ${c.n + 1} written to ${file}\n`);
  } catch (e) { process.stderr.write('session-ledger auto: ' + e.message + '\n'); }
}

if (cmd === 'path') cmdPath();
else if (cmd === 'check') cmdCheck();
else if (cmd === 'auto') { cmdAuto(); process.exit(0); }
else { console.log(fs.readFileSync(__filename, 'utf8').split('\n').filter(l => l.startsWith('//')).map(l => l.slice(3)).join('\n')); process.exit(cmd ? 2 : 0); }
