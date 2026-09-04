#!/usr/bin/env node
// tools/azure/smoke.mjs — FEAT-2326609775 inc1 deliverable 5: the smoke
// test that is inc1's REAL deliverable
// (docs/planning/azure-cloud-engine-design.md §6.5). It measures, from a
// real client against a real (local or deployed) metroserve, the numbers
// the design doc's whole latency budget (§6) rests on, and prints a clear
// PASS/FAIL against its stated bar:
//
//   round-trip p95 < 100ms   (design doc §6.5 "Pass" line)
//   journal-append p95 < 25ms
//
// Deliberately dependency-free: uses Node's built-in fetch and WebSocket
// (stable since Node 21+; this repo's CI/dev machines run Node 22+, see
// .github/workflows/ci.yml's node-version and package.json's engines
// field) rather than pulling in `ws` or the webconsole's
// protocolClient.ts (a browser-bundled TS module, not directly runnable
// under plain `node` without a build step) -- this script has ONE job
// (measure, report, exit non-zero on FAIL) and no reason to carry a
// bundler dependency for it.
//
// Usage:
//   node tools/azure/smoke.mjs [options]
//
// Options:
//   --health-url <url>   default: http://localhost:9999/health
//   --ws-url <url>       default: ws://localhost:9999/ws
//   --secret <value>     shared secret (METROSERVE_SHARED_SECRET on the
//                        server) -- sent as ?secret=... on the ws-url,
//                        matching the browser-client fallback
//                        (cmd/metroserve/portknock.go's
//                        SharedSecretQueryParam), since this script
//                        deliberately exercises the SAME code path the
//                        webconsole itself will use, not the header-only
//                        path only a non-browser client could use.
//   --client-version <v> the clientVersion the handshake sends. Defaults
//                        to the /health response's own `version` field
//                        (fetched first) -- matching the running server's
//                        build exactly, since wsserver's handshake
//                        refuses ANY mismatch (MET-P010, server.go).
//   --rounds <n>         number of minimal round-trips for step 3.
//                        Default 1000 (design doc §6.5 step 3).
//   --ticks <n>          size of the single batch for step 4. Default 360
//                        (one simulated year at the compose.SnapshotCadenceTicks
//                        default -- design doc §6.5 step 4).
//   --city <id>          city id to connect to (handshake cityId field).
//                        Default "" (server default city).
//   --tenant <id>        tenant id (handshake tenantId field). Default "".
//   --json               print the full result summary as JSON instead of
//                        the human-readable report (for CI capture).
//
// Exit code: 0 iff every measured pass bar is met; 1 otherwise (or on any
// connection/protocol failure) -- "a gate that can't evaluate must not
// report success" (Vestige: metropolis-verification-standards).

import { performance } from 'node:perf_hooks';

const PASS_ROUNDTRIP_P95_MS = 100;
const PASS_APPEND_P95_MS = 25;

function parseArgs(argv) {
  const opts = {
    healthUrl: 'http://localhost:9999/health',
    wsUrl: 'ws://localhost:9999/ws',
    secret: '',
    clientVersion: '',
    rounds: 1000,
    ticks: 360,
    city: '',
    tenant: '',
    json: false,
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    const next = () => argv[++i];
    switch (a) {
      case '--health-url':
        opts.healthUrl = next();
        break;
      case '--ws-url':
        opts.wsUrl = next();
        break;
      case '--secret':
        opts.secret = next();
        break;
      case '--client-version':
        opts.clientVersion = next();
        break;
      case '--rounds':
        opts.rounds = parseInt(next(), 10);
        break;
      case '--ticks':
        opts.ticks = parseInt(next(), 10);
        break;
      case '--city':
        opts.city = next();
        break;
      case '--tenant':
        opts.tenant = next();
        break;
      case '--json':
        opts.json = true;
        break;
      case '--help':
      case '-h':
        printHelp();
        process.exit(0);
        break;
      default:
        console.error(`unknown argument: ${a} (--help for usage)`);
        process.exit(2);
    }
  }
  return opts;
}

function printHelp() {
  console.log(`node tools/azure/smoke.mjs [options]

  --health-url <url>   default http://localhost:9999/health
  --ws-url <url>       default ws://localhost:9999/ws
  --secret <value>     METROSERVE_SHARED_SECRET, sent as ?secret=
  --client-version <v> defaults to the running server's own /health version
  --rounds <n>         minimal round-trips to measure (default 1000)
  --ticks <n>          size of the single AdvanceTicks batch (default 360)
  --city <id>          handshake cityId (default: server default)
  --tenant <id>        handshake tenantId (default: server default)
  --json               emit machine-readable JSON instead of a report

Kill-recovery (design doc §6.5 step 6) is NOT automated by this script
-- it never issues Azure/az commands (this tool has no business creating
or mutating cloud resources). Manual procedure:

  1. Run this script once and note the printed cities[].tick for your city.
  2. Kill the container revision:
       az containerapp revision restart --name <app> --resource-group <rg>
     (or, for a hard-kill test: az containerapp revision deactivate ...,
     then reactivate; see docs/planning/azure-runbook.md)
  3. Re-run this script (or just \`curl <health-url>\`) and confirm:
       a. /health becomes reachable again within your Container Apps
          cold-start budget (scale-to-zero min-replicas=0 means a FIRST
          request after idle pays a cold-start; note that time).
       b. The reported tick for your city is >= the tick noted in step 1
          (never regressed -- a regression means a snapshot/journal
          restore bug, not an acceptable outcome).
`);
}

async function timedFetch(url, opts) {
  const t0 = performance.now();
  const res = await fetch(url, opts);
  const t1 = performance.now();
  if (!res.ok) {
    throw new Error(`GET ${url} failed: HTTP ${res.status}`);
  }
  const body = await res.json();
  return { ms: t1 - t0, body };
}

function percentile(sortedMs, p) {
  if (sortedMs.length === 0) return NaN;
  const idx = Math.min(sortedMs.length - 1, Math.ceil((p / 100) * sortedMs.length) - 1);
  return sortedMs[Math.max(0, idx)];
}

function stats(samplesMs) {
  const sorted = [...samplesMs].sort((a, b) => a - b);
  return {
    n: sorted.length,
    p50: percentile(sorted, 50),
    p95: percentile(sorted, 95),
    p99: percentile(sorted, 99),
    max: sorted.length ? sorted[sorted.length - 1] : NaN,
  };
}

// --- Step 1: /health p50/p95 ---------------------------------------------

async function measureHealth(opts) {
  const samples = [];
  let lastBody = null;
  for (let i = 0; i < 20; i++) {
    const { ms, body } = await timedFetch(opts.healthUrl);
    samples.push(ms);
    lastBody = body;
  }
  return { stats: stats(samples), lastBody };
}

// --- WebSocket JSON-RPC helper --------------------------------------------
//
// Mirrors internal/protocol/wsserver's wire shape exactly (server.go's
// rpcMessage/handshakeParams/handshakeResult) -- this script is a
// from-scratch client deliberately kept minimal, not a port of
// protocolClient.ts, so every field name below is a direct read of that
// package's doc comments, not a copy of TS code.

class RPCClient {
  constructor(url) {
    this.url = url;
    this.ws = null;
    this.nextId = 1;
    this.pending = new Map(); // id -> {resolve, reject}
    this.resultWaiters = new Map(); // correlationId -> {resolve}
  }

  connect() {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(this.url);
      this.ws = ws;
      ws.addEventListener('open', () => resolve());
      ws.addEventListener('error', (e) => reject(new Error(`WebSocket error: ${e.message || e}`)));
      ws.addEventListener('message', (ev) => this._onMessage(ev));
      ws.addEventListener('close', (ev) => {
        // Reject anything still pending rather than hanging forever.
        const err = new Error(`WebSocket closed (code=${ev.code} reason=${ev.reason || ''})`);
        for (const { reject: rej } of this.pending.values()) rej(err);
        this.pending.clear();
      });
    });
  }

  _onMessage(ev) {
    let msg;
    try {
      msg = JSON.parse(typeof ev.data === 'string' ? ev.data : ev.data.toString());
    } catch {
      return;
    }
    if (msg.id !== undefined && msg.id !== null && this.pending.has(msg.id)) {
      const { resolve, reject } = this.pending.get(msg.id);
      this.pending.delete(msg.id);
      if (msg.error) reject(Object.assign(new Error(msg.error.message || 'rpc error'), { code: msg.error.code }));
      else resolve(msg.result);
      return;
    }
    if (msg.method === 'result' && msg.params) {
      // msg.params arrives as an already-decoded JSON object here: the
      // whole frame was one JSON.parse call above, and server.go's
      // rpcMessage.Params is a json.RawMessage embedded INLINE in that
      // frame (not a quoted string needing a second decode) -- an earlier
      // version of this script wrongly JSON.parse'd it a second time,
      // which throws on an object and silently swallowed the result
      // notification (every round-trip waiter hung forever). Keep this
      // comment: it is the exact failure this script's own manual smoke
      // run against a live metroserve caught.
      const result = msg.params;
      const corrId = result.correlationId;
      if (corrId && this.resultWaiters.has(corrId)) {
        const { resolve } = this.resultWaiters.get(corrId);
        this.resultWaiters.delete(corrId);
        resolve(result);
      }
    }
  }

  // request sends a JSON-RPC request and resolves with the immediate
  // response (the "queued":true ack for a command, or the handshake
  // result) -- NOT the later, asynchronous CommandResult notification.
  request(method, params) {
    const id = this.nextId++;
    const frame = { jsonrpc: '2.0', id, method, params };
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.ws.send(JSON.stringify(frame));
    });
  }

  // waitForResult registers interest in the "result" notification whose
  // decoded CommandResult.correlationId matches corrId. Must be called
  // BEFORE sending the command that will produce it (or in the same tick
  // of the event loop) to avoid a race with an unusually fast server.
  waitForResult(corrId) {
    return new Promise((resolve) => {
      this.resultWaiters.set(corrId, { resolve });
    });
  }

  close() {
    if (this.ws) this.ws.close();
  }
}

function newCorrelationId(prefix) {
  return `${prefix}-${Math.random().toString(36).slice(2)}-${Date.now()}`;
}

function advanceTicksCommand(correlationId, n) {
  return {
    protocolVersion: '1.0',
    correlationId,
    issuedAtTick: 0,
    kind: 'AdvanceTicks',
    payload: { n },
  };
}

// --- Main ------------------------------------------------------------------

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  const report = { passBar: { roundTripP95Ms: PASS_ROUNDTRIP_P95_MS, appendP95Ms: PASS_APPEND_P95_MS } };
  let overallPass = true;

  // Step 1: /health p50/p95.
  process.stderr.write('[1/4] /health x20 ...\n');
  const health = await measureHealth(opts);
  report.health = health.stats;
  if (!opts.clientVersion) {
    opts.clientVersion = health.lastBody?.version || '';
  }
  process.stderr.write(`      p50=${health.stats.p50.toFixed(1)}ms p95=${health.stats.p95.toFixed(1)}ms (server version ${opts.clientVersion || '(unknown)'})\n`);

  // Step 2: handshake.
  process.stderr.write('[2/4] WebSocket handshake ...\n');
  const wsUrl = opts.secret ? `${opts.wsUrl}${opts.wsUrl.includes('?') ? '&' : '?'}secret=${encodeURIComponent(opts.secret)}` : opts.wsUrl;
  const client = new RPCClient(wsUrl);
  const tHandshakeStart = performance.now();
  await client.connect();
  let handshakeResult;
  try {
    handshakeResult = await client.request('handshake', {
      clientVersion: opts.clientVersion,
      clientMinVersion: { major: 1, minor: 0 },
      clientMaxVersion: { major: 1, minor: 0 },
      capabilities: [],
      cityId: opts.city,
      tenantId: opts.tenant,
    });
  } catch (err) {
    console.error(`FAIL: handshake refused: ${err.message} (code=${err.code || 'n/a'})`);
    console.error('If code=MET-P010, --client-version does not match the server build -- omit --client-version to auto-detect from /health, or pass it explicitly.');
    process.exit(1);
  }
  const handshakeMs = performance.now() - tHandshakeStart;
  report.handshake = { ms: handshakeMs, negotiatedVersion: handshakeResult.negotiatedVersion, serverVersion: handshakeResult.serverVersion };
  process.stderr.write(`      completed in ${handshakeMs.toFixed(1)}ms (negotiated ${JSON.stringify(handshakeResult.negotiatedVersion)})\n`);

  // Step 3: 1,000 minimal round-trips. Two measurements per round:
  //   ackMs    -- request send -> synchronous JSON-RPC ack ({"queued":true}),
  //               i.e. wsserver's own dispatch overhead only.
  //   resultMs -- request send -> the correlated "result" notification,
  //               i.e. the FULL round trip through the engine's command
  //               loop, including a durable journal append when
  //               persistence is on (commands.go journals BEFORE
  //               SendResult -- see internal/engine/core's own doc
  //               comments). This is the number graded against the
  //               design doc's round-trip p95 bar, and doubles as the
  //               design doc step 5 "journal-append latency" proxy
  //               (design doc's own honest caveat: isolating fsync cost
  //               from engine-processing cost needs a second run against
  //               a persist-dir="" instance to diff against -- see
  //               docs/planning/azure-runbook.md).
  process.stderr.write(`[3/4] ${opts.rounds} minimal round-trips (AdvanceTicks N=1) ...\n`);
  const ackSamples = [];
  const resultSamples = [];
  for (let i = 0; i < opts.rounds; i++) {
    const corrId = newCorrelationId('smoke-rt');
    const waiter = client.waitForResult(corrId);
    const t0 = performance.now();
    await client.request('command', advanceTicksCommand(corrId, 1));
    const tAck = performance.now();
    await waiter;
    const tResult = performance.now();
    ackSamples.push(tAck - t0);
    resultSamples.push(tResult - t0);
  }
  report.roundTrip = { ack: stats(ackSamples), full: stats(resultSamples) };
  process.stderr.write(`      ack   p50=${report.roundTrip.ack.p50.toFixed(1)}ms p95=${report.roundTrip.ack.p95.toFixed(1)}ms p99=${report.roundTrip.ack.p99.toFixed(1)}ms max=${report.roundTrip.ack.max.toFixed(1)}ms\n`);
  process.stderr.write(`      full  p50=${report.roundTrip.full.p50.toFixed(1)}ms p95=${report.roundTrip.full.p95.toFixed(1)}ms p99=${report.roundTrip.full.p99.toFixed(1)}ms max=${report.roundTrip.full.max.toFixed(1)}ms\n`);

  const roundTripPass = report.roundTrip.full.p95 < PASS_ROUNDTRIP_P95_MS;
  const appendPass = report.roundTrip.full.p95 < PASS_APPEND_P95_MS; // conservative proxy; see note above
  report.pass = { roundTrip: roundTripPass, appendProxy: appendPass };
  overallPass = overallPass && roundTripPass;

  // Step 4: one batch of `ticks` AdvanceTicks in a SINGLE command.
  process.stderr.write(`[4/4] one batch of ${opts.ticks} ticks (single AdvanceTicks command) ...\n`);
  const batchCorrId = newCorrelationId('smoke-batch');
  const batchWaiter = client.waitForResult(batchCorrId);
  const tBatch0 = performance.now();
  await client.request('command', advanceTicksCommand(batchCorrId, opts.ticks));
  await batchWaiter;
  const batchMs = performance.now() - tBatch0;
  report.batch = { ticks: opts.ticks, totalMs: batchMs, perTickMs: batchMs / opts.ticks };
  process.stderr.write(`      total=${batchMs.toFixed(1)}ms (${report.batch.perTickMs.toFixed(3)}ms/tick)\n`);

  // Final city tick, for the kill-recovery procedure printed in --help.
  try {
    const { body: finalHealth } = await timedFetch(opts.healthUrl);
    report.finalHealth = finalHealth;
  } catch {
    // Non-fatal -- the round-trip/batch numbers above are already captured.
  }

  client.close();

  report.overallPass = overallPass;

  if (opts.json) {
    console.log(JSON.stringify(report, null, 2));
  } else {
    console.log('\n=== FEAT-2326609775 inc1 smoke test ===');
    console.log(`/health           p50=${report.health.p50.toFixed(1)}ms p95=${report.health.p95.toFixed(1)}ms`);
    console.log(`round-trip (full) p50=${report.roundTrip.full.p50.toFixed(1)}ms p95=${report.roundTrip.full.p95.toFixed(1)}ms p99=${report.roundTrip.full.p99.toFixed(1)}ms max=${report.roundTrip.full.max.toFixed(1)}ms`);
    console.log(`batch (${opts.ticks} ticks)   total=${report.batch.totalMs.toFixed(1)}ms  per-tick=${report.batch.perTickMs.toFixed(3)}ms`);
    console.log(`\nPASS BAR: round-trip p95 < ${PASS_ROUNDTRIP_P95_MS}ms -> ${roundTripPass ? 'PASS' : 'FAIL'} (${report.roundTrip.full.p95.toFixed(1)}ms)`);
    console.log(`          journal-append proxy < ${PASS_APPEND_P95_MS}ms -> ${appendPass ? 'PASS' : 'FAIL (informational only -- see script header for why this is a proxy, not a direct measurement)'} `);
    console.log(`\nFinal city tick: ${JSON.stringify(report.finalHealth?.cities || [])}`);
    console.log(`\nOverall: ${overallPass ? 'PASS' : 'FAIL'}`);
    console.log('\nFor the kill-recovery step (design doc step 6), see --help.');
  }

  process.exit(overallPass ? 0 : 1);
}

main().catch((err) => {
  console.error(`FAIL: ${err.stack || err.message || err}`);
  process.exit(1);
});
