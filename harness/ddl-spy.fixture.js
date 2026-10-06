/**
 * ddl-spy.fixture.js — TEST FIXTURE, not production tooling.
 *
 * Preloaded into a spawned CLI subprocess with `node --require
 * ./ddl-spy.fixture.js claude-bow.js <command>` so that the parent test can
 * count, exactly and only, the DDL statements THAT SUBPROCESS itself issued.
 *
 * WHY THIS EXISTS (BUG-320, P1)
 * ----------------------------
 * BUG-115's and BUG-170's DDL tests originally measured MariaDB's
 * `SHOW GLOBAL STATUS` counters (Com_alter_table / Com_create_table) before
 * and after spawning the CLI, and asserted the delta. Those counters are
 * GLOBAL — instance-wide, incremented by every connection from every process
 * on the server. `node --test` runs test FILES concurrently (default
 * concurrency = availableParallelism() - 1, and CI passes no
 * --test-concurrency), and roughly a dozen sibling test files
 * (claude-sync.test.js, claude-bow-columns.test.js, claude-dispatch-log.
 * test.js, claude-destructive-guard.test.js, ...) spawn claude-sync.js /
 * claude-bow.js write commands that run their own ensureSchema DDL against
 * the SAME MariaDB instance. Any such statement landing inside the
 * before/after window inflated the delta and failed an unrelated assertion —
 * the 1-in-N red gate reported on PRs #85 and #86, neither of which touched
 * any JavaScript.
 *
 * The fix is to stop inferring a per-process fact from a shared global
 * counter. This spy wraps the subprocess's OWN mysql2 connection objects, so
 * the count is causally scoped to that one invocation and is structurally
 * incapable of seeing another process's traffic — no retry, no loosened
 * assertion, no test reordering, and the "exactly zero DDL" property stays
 * genuinely asserted.
 *
 * MECHANISM
 * ---------
 * claude-db.js deliberately requires `mysql2/promise` lazily and looks up
 * `.createConnection` at call time (see its header comment), precisely so a
 * test can monkey-patch it. `--require` runs this file before the CLI's main
 * module, so the patch is installed on the shared require cache entry before
 * the first connection is created.
 *
 * Counts are written to the JSON file named by METRO_DDL_SPY_OUT after every
 * statement (not only at exit), so the parent still gets a reading even if
 * the subprocess dies abnormally. If METRO_DDL_SPY_OUT is unset this file is
 * an inert no-op.
 */

'use strict';

const fs = require('fs');

const OUT = process.env.METRO_DDL_SPY_OUT;

if (OUT) {
  const counts = { alterTable: 0, createTable: 0, statements: [] };

  // Written eagerly (before any query) so the parent can distinguish
  // "subprocess ran and issued zero DDL" from "the spy never loaded at all,
  // so this measurement is worthless". A gate that cannot evaluate must not
  // report success.
  const flush = () => {
    try { fs.writeFileSync(OUT, JSON.stringify(counts)); } catch { /* best effort */ }
  };
  flush();

  /** Strip leading whitespace and leading SQL comments so the classifier sees
   * the first real keyword, however the statement was formatted. */
  function firstKeywords(sql) {
    let s = String(sql);
    for (;;) {
      const before = s;
      s = s.replace(/^\s+/, '').replace(/^\/\*[\s\S]*?\*\//, '').replace(/^--[^\n]*\n?/, '').replace(/^#[^\n]*\n?/, '');
      if (s === before) break;
    }
    return s;
  }

  /** Classify exactly the way MariaDB's Com_alter_table / Com_create_table
   * counters do — by the statement's leading verb+object — so this reading is
   * directly comparable to the measurement it replaces. */
  function record(sql) {
    const text = firstKeywords(typeof sql === 'string' ? sql : (sql && sql.sql) || '');
    if (/^ALTER\s+TABLE\b/i.test(text)) { counts.alterTable++; counts.statements.push(text.slice(0, 120)); }
    else if (/^CREATE\s+TABLE\b/i.test(text)) { counts.createTable++; counts.statements.push(text.slice(0, 120)); }
    else return;
    flush();
  }

  function instrument(conn) {
    if (!conn || conn.__ddlSpyInstalled) return conn;
    for (const method of ['query', 'execute']) {
      if (typeof conn[method] !== 'function') continue;
      const original = conn[method].bind(conn);
      conn[method] = function (sql, ...rest) {
        record(sql);
        return original(sql, ...rest);
      };
    }
    Object.defineProperty(conn, '__ddlSpyInstalled', { value: true, enumerable: false });
    return conn;
  }

  const mysql = require('mysql2/promise');

  const originalCreateConnection = mysql.createConnection;
  mysql.createConnection = function (...args) {
    const result = originalCreateConnection.apply(this, args);
    return (result && typeof result.then === 'function')
      ? result.then(instrument)
      : instrument(result);
  };

  if (typeof mysql.createPool === 'function') {
    const originalCreatePool = mysql.createPool;
    mysql.createPool = function (...args) { return instrument(originalCreatePool.apply(this, args)); };
  }

  process.on('exit', flush);
}
