#!/usr/bin/env node
/**
 * tools/health/check.js — module key: tool.healthcheck (FEAT-027)
 *
 * Live health probe for the /health-check skill (Metropolis profile).
 * Everything this script reports is derived from a live source at run
 * time (GR#15) — nothing here is a hardcoded job name, expected count,
 * or "should be green" assumption:
 *
 *   - CI job names/steps          <- parsed from .github/workflows/ci.yml
 *   - -race flag presence         <- grepped from the same file
 *   - perf-CI job presence        <- grepped from the same file (absent
 *                                    is reported honestly, not faked)
 *   - latest run status per job   <- `gh run list` / `gh run view --json`
 *   - "did it finish, and is it
 *      for THIS commit"           <- run.status + run.headSha vs HEAD
 *   - BOW ready queue / P0s       <- `node claude-bow.js ready` / `list`
 *   - git sync state              <- `git status` / `git rev-list`
 *
 * This tool is read-only. It never mutates the repo, the BOW, or CI.
 * It prints a report and exits 1 if any FAILURE-class condition was
 * found, 2 if nothing failed but at least one check was genuinely
 * UNCONFIRMED (gh unavailable, no run for this branch/sha, or a run
 * still in progress), 0 otherwise. UNCONFIRMED is a DIFFERENT axis from
 * WARN: a determined-but-dirty state (e.g. uncommitted git changes) is
 * WARN, not UNCONFIRMED — collapsing the two trains the reader to skim
 * past UNCONFIRMED because it fires on every normal dirty-tree run
 * (SEC-026's "alarm that fires under normal conditions stops being an
 * alarm" pattern — this bounced FEAT-027 back once already, see
 * ASM-121-adjacent BOW history). Node script — cannot call MCP tools
 * (Vestige) itself; the health-check skill markdown covers that check
 * separately.
 *
 * Usage: node tools/health/check.js [--json]
 */

'use strict';

const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const REPO_ROOT = path.resolve(__dirname, '..', '..');
const CI_YML_PATH = path.join(REPO_ROOT, '.github', 'workflows', 'ci.yml');

let anyFailure = false;
let anyWarn = false;
let anyUnconfirmed = false;
const report = { sections: [] };

function section(name, lines, status) {
  // status: 'ok' | 'warn' | 'fail' | 'unconfirmed' | 'info'
  //
  // 'warn' and 'unconfirmed' are DIFFERENT AXES, not synonyms — this is
  // the exact distinction Bill bounced FEAT-027 back over:
  //   'warn'         — the check DETERMINED a state, and that state is
  //                    not clean (e.g. git has uncommitted files: we
  //                    know precisely what's dirty). A determined WARN
  //                    is not "unconfirmed" — collapsing the two makes
  //                    UNCONFIRMED fire on every normal dirty-tree run,
  //                    which trains the operator to skim past it
  //                    (SEC-026's pattern: an alarm that fires under
  //                    normal conditions stops being an alarm).
  //   'unconfirmed'  — the check could NOT reach a verdict at all:
  //                    gh CLI unavailable, no CI run exists for this
  //                    branch/sha, or the latest run hasn't finished.
  //                    Reserved for genuine "don't know" states only.
  report.sections.push({ name, status, lines });
  if (status === 'fail') anyFailure = true;
  if (status === 'warn') anyWarn = true;
  if (status === 'unconfirmed') anyUnconfirmed = true;
}

function run(cmd, args, opts = {}) {
  try {
    const out = execFileSync(cmd, args, {
      cwd: REPO_ROOT,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'pipe'],
      ...opts,
    });
    return { ok: true, out: out.trim() };
  } catch (e) {
    return { ok: false, out: (e.stdout || '').toString().trim(), err: (e.stderr || e.message || '').toString().trim() };
  }
}

// ---------------------------------------------------------------------
// 1. Parse ci.yml live — job names, -race presence, perf-CI presence,
//    determinism-gate presence. No job name is hardcoded; we derive the
//    job list from the file's own "jobs:" block.
// ---------------------------------------------------------------------
let ciYml = '';
let jobNames = [];
let determinismJobNames = [];
let perfJobNames = [];
let raceLinesByJob = {};

if (!fs.existsSync(CI_YML_PATH)) {
  section('CI workflow file', [`NOT FOUND at ${CI_YML_PATH} — cannot check any CI-derived gate.`], 'fail');
} else {
  ciYml = fs.readFileSync(CI_YML_PATH, 'utf8');
  const lines = ciYml.split(/\r?\n/);

  // Find the "jobs:" top-level key, then collect 2-space-indented keys
  // under it as job names, splitting the file into per-job blocks.
  const jobsIdx = lines.findIndex((l) => /^jobs:\s*$/.test(l));
  const jobBlocks = {}; // name -> array of lines
  if (jobsIdx !== -1) {
    let currentJob = null;
    for (let i = jobsIdx + 1; i < lines.length; i++) {
      const l = lines[i];
      const jobHeader = l.match(/^  ([A-Za-z0-9_-]+):\s*$/);
      if (jobHeader) {
        currentJob = jobHeader[1];
        jobBlocks[currentJob] = [];
        jobNames.push(currentJob);
        continue;
      }
      // A non-indented, non-comment line ends the jobs: block.
      if (currentJob && /^\S/.test(l) && l.trim() !== '') break;
      if (currentJob) jobBlocks[currentJob].push(l);
    }
  }

  for (const [name, blockLines] of Object.entries(jobBlocks)) {
    // Comment lines can precede the NEXT job's header (its explanatory
    // preamble) and get attributed to whichever job's block the parser
    // was in at the time — exclude comments from content matching so a
    // neighbouring job's doc-comment doesn't cause a false name match.
    const codeLines = blockLines.filter((l) => !/^\s*#/.test(l));
    const block = codeLines.join('\n');
    if (/determinism/i.test(name) || /determinism/i.test(block)) determinismJobNames.push(name);
    if (/\bperf\b/i.test(name) || /\bperf[\s-]?ci\b/i.test(block)) perfJobNames.push(name);
    const raceMatches = codeLines.filter((l) => /-race\b/.test(l));
    if (raceMatches.length) raceLinesByJob[name] = raceMatches.map((l) => l.trim());
  }

  section(
    'CI workflow structure (.github/workflows/ci.yml, live parse)',
    [
      `Jobs found: ${jobNames.length ? jobNames.join(', ') : '(none — parse failed, check file structure)'}`,
    ],
    jobNames.length ? 'info' : 'fail'
  );

  // --- Determinism gate presence (GR#21) ---
  if (determinismJobNames.length === 0) {
    section(
      'Determinism gate presence (GR#21)',
      ['NO job in ci.yml matches "determinism" by name or step content.', 'This is P0-class: GR#21 requires a determinism gate on every merge.'],
      'fail'
    );
  } else {
    section('Determinism gate presence (GR#21)', [`Present: job(s) ${determinismJobNames.join(', ')}`], 'ok');
  }

  // --- -race flag presence (SEC-028) ---
  const raceReportLines = [];
  const testJobs = jobNames.filter((n) => /build-test|test-vet|^test$/i.test(n) || determinismJobNames.includes(n));
  // Fall back to reporting -race presence across ALL jobs that contain "go test"
  const goTestJobs = jobNames.filter((n) => /go test/i.test(jobBlocks[n].join('\n')));
  const jobsToCheck = Array.from(new Set([...testJobs, ...goTestJobs]));
  let raceMissing = [];
  if (jobsToCheck.length === 0) {
    raceReportLines.push('No job with "go test" found — cannot verify -race presence.');
  } else {
    for (const j of jobsToCheck) {
      if (raceLinesByJob[j]) {
        raceReportLines.push(`${j}: -race present (${raceLinesByJob[j].join(' | ')})`);
      } else {
        raceReportLines.push(`${j}: go test present but NO -race flag found`);
        raceMissing.push(j);
      }
    }
  }
  section(
    '-race flag presence (SEC-028 — missing for entire project history until 2026-08-09)',
    raceReportLines,
    raceMissing.length ? 'fail' : 'ok'
  );

  // --- perf-CI presence ---
  if (perfJobNames.length === 0) {
    section(
      'perf-CI job presence',
      [
        'NO job in ci.yml matches "perf" by name or content.',
        'Per docs/planning/sprint-plan-v1.md (H-SYNTH / harness.synth, Sprint 2) this is EXPECTED until',
        'harness.synth lands and wires its perf-CI job in — absence today is not itself a failure,',
        'but this check will start flagging FAIL once harness.synth is BOW-marked done and the job is',
        'still missing. Re-run `node claude-bow.js show MOD-016` (or the harness.synth code) to see',
        'whether that module has landed yet.',
      ],
      'info'
    );
  } else {
    section('perf-CI job presence', [`Present: job(s) ${perfJobNames.join(', ')}`], 'ok');
  }
}

// ---------------------------------------------------------------------
// 2. Git sync state
// ---------------------------------------------------------------------
const branchRes = run('git', ['branch', '--show-current']);
const branch = branchRes.ok ? branchRes.out : '(unknown)';
const headRes = run('git', ['rev-parse', 'HEAD']);
const headSha = headRes.ok ? headRes.out : null;
const statusRes = run('git', ['status', '--short']);
const dirtyLines = statusRes.ok ? statusRes.out.split(/\r?\n/).filter(Boolean) : [];

let aheadBehindLine = '(no upstream tracking info)';
const upstreamRes = run('git', ['rev-parse', '--abbrev-ref', '--symbolic-full-name', '@{u}']);
if (upstreamRes.ok) {
  const upstream = upstreamRes.out;
  const counts = run('git', ['rev-list', '--left-right', '--count', `${upstream}...HEAD`]);
  if (counts.ok) {
    const [behind, ahead] = counts.out.split(/\s+/);
    aheadBehindLine = `${upstream}: ahead ${ahead}, behind ${behind}`;
  }
}

section(
  'Git sync state',
  [
    `Branch: ${branch}`,
    `HEAD: ${headSha || '(unknown)'}`,
    aheadBehindLine,
    dirtyLines.length ? `Uncommitted: ${dirtyLines.length} file(s):` : 'Uncommitted: clean',
    ...dirtyLines.map((l) => '  ' + l),
  ],
  dirtyLines.length ? 'warn' : 'ok'
);

// ---------------------------------------------------------------------
// 3. Latest CI run for this branch — did it complete, is it for HEAD?
// ---------------------------------------------------------------------
const ghVersion = run('gh', ['--version']);
if (!ghVersion.ok) {
  section('CI run status (gh CLI)', ['gh CLI not found/working — cannot check CI run status live. UNCONFIRMED, not a failure of CI itself.'], 'unconfirmed');
} else {
  const runListRes = run('gh', [
    'run', 'list',
    '--branch', branch,
    '--limit', '1',
    '--json', 'databaseId,headSha,status,conclusion,workflowName,createdAt,event,url',
  ]);
  if (!runListRes.ok) {
    section('CI run status (gh CLI)', [`gh run list failed: ${runListRes.err}`], 'unconfirmed');
  } else {
    let runs = [];
    try { runs = JSON.parse(runListRes.out); } catch (e) { /* fallthrough */ }
    if (!runs.length) {
      section('CI run status', [`No CI runs found for branch '${branch}' — cannot confirm pass/fail, nothing to read.`], 'unconfirmed');
    } else {
      const latest = runs[0];
      const lines = [
        `Latest run: #${latest.databaseId} (${latest.workflowName}, ${latest.event}) — ${latest.createdAt}`,
        `Run status: ${latest.status} / conclusion: ${latest.conclusion || '(none yet)'}`,
        `Run headSha: ${latest.headSha}`,
        `Local HEAD:  ${headSha}`,
      ];
      let status = 'ok';

      if (latest.status !== 'completed') {
        lines.push('? RUN HAS NOT FINISHED — genuinely unconfirmed, not a determined pass or fail. Re-check before relying on it.');
        status = 'unconfirmed';
      } else if (headSha && latest.headSha !== headSha) {
        lines.push('? STALE — the latest run is NOT for the current HEAD commit. Genuinely unconfirmed for what is checked out now.');
        status = 'unconfirmed';
      } else if (latest.conclusion !== 'success') {
        lines.push(`❌ Latest completed run for current HEAD did NOT succeed (conclusion: ${latest.conclusion}).`);
        status = 'fail';
      } else {
        lines.push('✅ Latest run completed successfully and matches current HEAD.');
      }

      section('CI run status (overall)', lines, status);

      // Per-job breakdown, so determinism-gate specifically is unmissable.
      if (latest.status === 'completed') {
        const jobsRes = run('gh', ['run', 'view', String(latest.databaseId), '--json', 'jobs']);
        if (jobsRes.ok) {
          let jobs = [];
          try { jobs = JSON.parse(jobsRes.out).jobs; } catch (e) { /* ignore */ }
          const jobLines = jobs.map((j) => `${j.name}: ${j.status} / ${j.conclusion}`);
          const failedJobs = jobs.filter((j) => j.conclusion && j.conclusion !== 'success');
          const isStaleRun = headSha && latest.headSha !== headSha;

          // Determinism gate: unmissable per-job line (GR#21).
          const detJob = jobs.find((j) => determinismJobNames.some((n) => j.name.toLowerCase().includes(n.toLowerCase())) || /determinism/i.test(j.name));
          if (detJob) {
            const detOk = detJob.conclusion === 'success';
            section(
              'DETERMINISM GATE (GR#21 — red here is auto-P0, blocks all merges)',
              [
                `Job: ${detJob.name}`,
                `Status/conclusion: ${detJob.status} / ${detJob.conclusion}`,
                isStaleRun ? 'NOTE: this result is from a run NOT matching current HEAD — genuinely unconfirmed for the current commit.' : `Confirmed for current HEAD (${headSha}).`,
              ],
              isStaleRun ? 'unconfirmed' : (detOk ? 'ok' : 'fail')
            );
          } else {
            section('DETERMINISM GATE (GR#21)', ['No job in the latest run matches the determinism gate found in ci.yml — cannot confirm pass/fail.'], 'unconfirmed');
          }

          section(
            'CI run — per-job breakdown',
            jobLines.length ? jobLines : ['(gh run view returned no jobs)'],
            failedJobs.length ? 'fail' : 'ok'
          );
        } else {
          section('CI run — per-job breakdown', [`gh run view --json jobs failed: ${jobsRes.err}`], 'unconfirmed');
        }
      }
    }
  }
}

// ---------------------------------------------------------------------
// 4. BOW ready queue + P0s (open, no blockers) — via claude-bow.js
// ---------------------------------------------------------------------
const bowSummary = run('node', ['claude-bow.js'], { cwd: REPO_ROOT });
if (!bowSummary.ok) {
  section('BOW status', [`claude-bow.js summary failed: ${bowSummary.err}`], 'fail');
} else {
  section('BOW status (summary)', bowSummary.out.split(/\r?\n/), 'info');
}

const bowReady = run('node', ['claude-bow.js', 'ready'], { cwd: REPO_ROOT });
if (bowReady.ok) {
  const readyLines = bowReady.out.split(/\r?\n/);
  // Surface P0 module/feature/bug/interface items in full (the ones that
  // matter for "is the ready queue starved of top-priority work"); fold
  // the long tail of ASM/SEC/lower-priority items into a count derived
  // from the tool's own trailer line, not a hardcoded expectation.
  // Match the structured "<code>   P0 [STATUS" column only — not any
  // line whose free-text description happens to mention "P0" in prose
  // (e.g. an ASM item saying "...not P0/P1 because...").
  const p0Lines = readyLines.filter((l) => /\sP0\s+\[/.test(l));
  const trailerMatch = bowReady.out.match(/(\d+)\s+item\(s\)\s+ready/i);
  const totalReady = trailerMatch ? trailerMatch[1] : '(unknown — trailer line not found)';
  section(
    `BOW ready queue (no open dependencies) — ${p0Lines.length} P0 item(s), ${totalReady} total ready`,
    [
      p0Lines.length ? 'P0 ready items:' : 'No P0 items in the ready queue.',
      ...p0Lines,
      '(Full list, including ASM/SEC/lower-priority items: node claude-bow.js ready)',
    ],
    p0Lines.length === 0 ? 'ok' : 'info'
  );
} else {
  section('BOW ready queue', [`claude-bow.js ready failed: ${bowReady.err}`], 'fail');
}

const bowBlocked = run('node', ['claude-bow.js', 'list', '--status', 'blocked'], { cwd: REPO_ROOT });
if (bowBlocked.ok) {
  section('BOW blocked items', bowBlocked.out.split(/\r?\n/), /clean/i.test(bowBlocked.out) ? 'ok' : 'warn');
}

// ---------------------------------------------------------------------
// Final report
// ---------------------------------------------------------------------
if (process.argv.includes('--json')) {
  console.log(JSON.stringify({ anyFailure, anyWarn, anyUnconfirmed, sections: report.sections }, null, 2));
} else {
  console.log('='.repeat(72));
  console.log('METROPOLIS HEALTH CHECK (tool.healthcheck, FEAT-027) — live probe');
  console.log('='.repeat(72));
  for (const s of report.sections) {
    const tag = { ok: '[OK]  ', warn: '[WARN]', fail: '[FAIL]', unconfirmed: '[UNCONF]', info: '[INFO]' }[s.status] || '[??]  ';
    console.log(`\n${tag} ${s.name}`);
    for (const l of s.lines) console.log('       ' + l);
  }
  console.log('\n' + '='.repeat(72));
  console.log(
    anyFailure
      ? 'OVERALL: FAIL — at least one FAILURE-class check above needs attention.'
      : anyUnconfirmed
      ? 'OVERALL: UNCONFIRMED — at least one check could not reach a verdict at all (see [UNCONF] lines) — this is distinct from a determined WARN below.'
      : anyWarn
      ? 'OVERALL: ATTENTION NEEDED — every check reached a verdict; at least one determined state is not clean (see [WARN] lines).'
      : 'OVERALL: OK — no FAIL/WARN/UNCONFIRMED conditions found by this probe.'
  );
}

process.exitCode = anyFailure ? 1 : anyUnconfirmed ? 2 : 0;
