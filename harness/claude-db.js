// Module key: tool.db (see code.json; GUID 4c12a143-9345-4ae9-990d-ac16ee3e2baa)
// Spec ref: M0-ENG §4 (BOW)

/**
 * claude-db.js — shared metro MariaDB connection helper (BUG-203, GR#3).
 *
 * The six non-test `claude-*.js` DB scripts used to each hand-roll their own
 * `mysql2/promise` `createConnection` block, in two failure-semantics classes:
 *
 *   - `connectCLI(label)` — interactive CLIs (claude-bow.js, claude-sync.js).
 *       On connection failure it prints a friendly error and `process.exit(1)`
 *       (fail-stop): the CLI cannot proceed without a database, so the user
 *       should be told and the process stopped.
 *
 *   - `connect(opts)` — PreToolUse/PostToolUse guards (claude-bow-ref-check.js,
 *       claude-destructive-guard.js, claude-dispatch-guard.js,
 *       claude-bow-autoref.js). On connection failure it THROWS and lets the
 *       caller decide: guards either fail-open (allow the action, stderr note)
 *       or fail-closed (deny the action) according to their own mandate. The
 *       guards pass `connectTimeout: 4000` so a dead DB cannot hang a commit.
 *
 * Connection parameters come from the METRO_DB_HOST/PORT/USER/PASSWORD/NAME
 * env vars (defaults: 127.0.0.1:3306, root, '', metro), read at CALL TIME so a
 * test that points METRO_DB_NAME at a scratch database before invoking a
 * caller sees the right target (claude-bow.test.js's AC-12 pattern).
 *
 * BUG-1119 safety net: this is the METROPOLIS harness and only ever talks to
 * the `metro` database. Other projects' launchers set METRO_DB_NAME to their
 * own database on purpose; running claude-bow.js from such a window used to
 * silently create Metropolis tables in that database. Now, at connect time:
 *   - METRO_DB_NAME unset or empty      -> `metro` (default, unchanged)
 *   - METRO_DB_NAME === 'metro'         -> ok
 *   - anything else                     -> REFUSED before any connection or DDL
 *   - sole exception (tests): METRO_DB_ALLOW_SCRATCH=1 AND the name starts
 *     with `metro_test`. The flag alone, or the name alone, never opens it.
 * An explicit `database` in the `overrides` argument is the calling code's own
 * choice and is not checked here. (The shared-kit variant also refuses when
 * the variable is UNSET; not adopted: hooks and CLIs run from the Metropolis
 * launcher and from tests with it unset, and the default is safe.)
 */

'use strict';

const PRODUCTION_DB = 'metro';
const SCRATCH_PREFIX = 'metro_test';

/** Error thrown when METRO_DB_NAME names a database this harness must not touch. */
class WrongDatabaseError extends Error {
  constructor(found) {
    super(
      `refusing to connect: METRO_DB_NAME is '${found}', but this is the Metropolis harness and only connects to ` +
      `'${PRODUCTION_DB}'. This window was probably launched for another project. Launch with the \`metro\` command ` +
      `(or unset METRO_DB_NAME) to use the Metropolis Book of Work.`
    );
    this.name = 'WrongDatabaseError';
    this.code = 'METRO_WRONG_DB';
    this.databaseFound = found;
  }
}

// Throws WrongDatabaseError unless the env names `metro`, is unset, or is a
// metro_test* scratch database explicitly allowed by METRO_DB_ALLOW_SCRATCH=1.
function assertDatabaseAllowed(env = process.env) {
  const name = env.METRO_DB_NAME;
  if (!name || name === PRODUCTION_DB) return;
  if (env.METRO_DB_ALLOW_SCRATCH === '1' && name.startsWith(SCRATCH_PREFIX)) return;
  throw new WrongDatabaseError(name);
}

function connectionOptions(overrides = {}) {
  return {
    host: process.env.METRO_DB_HOST || '127.0.0.1',
    port: Number(process.env.METRO_DB_PORT || 3306),
    user: process.env.METRO_DB_USER || 'root',
    password: process.env.METRO_DB_PASSWORD || '',
    database: process.env.METRO_DB_NAME || 'metro',
    dateStrings: true,  // BUG-264: return TIMESTAMP/DATETIME fields as strings to avoid tz skew
    ...overrides,
  };
}

// Throw-on-failure flavour. `mysql2/promise` is required lazily INSIDE the
// function (not at module load) and `.createConnection` is looked up at call
// time, so a caller that monkey-patches `require('mysql2/promise')
// .createConnection` for a test (claude-dispatch-guard.test.js's AC-8) still
// sees its patch — Node caches the module, so both sides share one object.
function connect(overrides = {}) {
  // BUG-1119: refuse BEFORE requiring mysql2 or opening any connection.
  if (!overrides.database) {
    try { assertDatabaseAllowed(); } catch (err) { return Promise.reject(err); }
  }
  const mysql = require('mysql2/promise');
  return mysql.createConnection(connectionOptions(overrides));
}

async function connectCLI(label, overrides = {}) {
  try {
    return await connect(overrides);
  } catch (err) {
    if (err && err.code === 'METRO_WRONG_DB') {
      console.error(`${label}: ${err.message}`);
      process.exit(1);
    }
    console.error(`${label}: cannot connect to metro MariaDB: ${err.message}`);
    console.error('Ensure the MariaDB service is running (Get-Service MariaDB) and the metro database exists.');
    process.exit(1);
  }
}

module.exports = { connect, connectCLI, connectionOptions, assertDatabaseAllowed, WrongDatabaseError };
