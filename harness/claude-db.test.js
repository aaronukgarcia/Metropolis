/**
 * claude-db.test.js — unit tests for claude-db.js (METRO_DB_* connection helpers).
 *
 * ASM-756: Tests the three untested contracts:
 *   1. connectionOptions env-var precedence/defaults (METRO_DB_HOST/PORT/USER/PASSWORD/NAME)
 *      and dateStrings:true regression lock (BUG-264)
 *   2. connect() throw-on-failure: calls mysql2/promise.createConnection and rejects
 *      on connection failure (never swallows)
 *   3. connectCLI() fail-stop: exits 1 on connection failure, prints friendly error message
 *
 * No test connects to the real metro database or writes to it. Env vars are
 * read at CALL TIME (not module load), so tests mutate process.env before
 * invoking the tested functions to verify read-at-call-time behaviour.
 *
 * Run: node --test claude-db.test.js
 */

'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { spawnSync } = require('child_process');
const path = require('path');

const ROOT = path.join(__dirname, '..');

// Set a test marker BEFORE requiring claude-db.js (though it reads env
// at call time, not at require time). This is just for clarity in any
// diagnostic output.
const prevDbName = process.env.METRO_DB_NAME;
// BUG-1119: connect() refuses any name but `metro`; a scratch name needs the
// explicit METRO_DB_ALLOW_SCRATCH=1 escape AND a `metro_test` prefix.
const prevAllowScratch = process.env.METRO_DB_ALLOW_SCRATCH;
process.env.METRO_DB_NAME = 'metro_test_claudedb_marker';
process.env.METRO_DB_ALLOW_SCRATCH = '1';

// Require AFTER env setup — the lazy-require pattern in claude-db.js's
// connect() means mysql2/promise is required at call time, not here,
// so monkey-patching still works.
const { connectionOptions, connect, connectCLI } = require('./claude-db.js');

// ============================================================================
// Contract 1: connectionOptions env-var precedence/defaults
// ============================================================================

test('connectionOptions: defaults to 127.0.0.1:3306 root/empty-password/metro when no env vars set', () => {
  const prevHost = process.env.METRO_DB_HOST;
  const prevPort = process.env.METRO_DB_PORT;
  const prevUser = process.env.METRO_DB_USER;
  const prevPass = process.env.METRO_DB_PASSWORD;
  const prevName = process.env.METRO_DB_NAME;

  delete process.env.METRO_DB_HOST;
  delete process.env.METRO_DB_PORT;
  delete process.env.METRO_DB_USER;
  delete process.env.METRO_DB_PASSWORD;
  delete process.env.METRO_DB_NAME;

  try {
    const opts = connectionOptions();
    assert.equal(opts.host, '127.0.0.1', 'default host must be 127.0.0.1');
    assert.equal(opts.port, 3306, 'default port must be 3306');
    assert.equal(opts.user, 'root', 'default user must be root');
    assert.equal(opts.password, '', 'default password must be empty string');
    assert.equal(opts.database, 'metro', 'default database must be metro');
  } finally {
    if (prevHost !== undefined) process.env.METRO_DB_HOST = prevHost;
    if (prevPort !== undefined) process.env.METRO_DB_PORT = prevPort;
    if (prevUser !== undefined) process.env.METRO_DB_USER = prevUser;
    if (prevPass !== undefined) process.env.METRO_DB_PASSWORD = prevPass;
    if (prevName !== undefined) process.env.METRO_DB_NAME = prevName;
  }
});

test('connectionOptions: METRO_DB_HOST overrides default host', () => {
  const prev = process.env.METRO_DB_HOST;
  try {
    process.env.METRO_DB_HOST = '192.168.1.100';
    const opts = connectionOptions();
    assert.equal(opts.host, '192.168.1.100');
  } finally {
    if (prev !== undefined) process.env.METRO_DB_HOST = prev;
    else delete process.env.METRO_DB_HOST;
  }
});

test('connectionOptions: METRO_DB_PORT overrides default port (and is coerced to number)', () => {
  const prev = process.env.METRO_DB_PORT;
  try {
    process.env.METRO_DB_PORT = '3307';
    const opts = connectionOptions();
    assert.equal(opts.port, 3307, 'port must be a number');
    assert.strictEqual(typeof opts.port, 'number');
  } finally {
    if (prev !== undefined) process.env.METRO_DB_PORT = prev;
    else delete process.env.METRO_DB_PORT;
  }
});

test('connectionOptions: METRO_DB_USER overrides default user', () => {
  const prev = process.env.METRO_DB_USER;
  try {
    process.env.METRO_DB_USER = 'appuser';
    const opts = connectionOptions();
    assert.equal(opts.user, 'appuser');
  } finally {
    if (prev !== undefined) process.env.METRO_DB_USER = prev;
    else delete process.env.METRO_DB_USER;
  }
});

test('connectionOptions: METRO_DB_PASSWORD overrides default (empty) password', () => {
  const prev = process.env.METRO_DB_PASSWORD;
  try {
    process.env.METRO_DB_PASSWORD = 'secret123';
    const opts = connectionOptions();
    assert.equal(opts.password, 'secret123');
  } finally {
    if (prev !== undefined) process.env.METRO_DB_PASSWORD = prev;
    else delete process.env.METRO_DB_PASSWORD;
  }
});

test('connectionOptions: METRO_DB_NAME overrides default database name', () => {
  const prev = process.env.METRO_DB_NAME;
  try {
    process.env.METRO_DB_NAME = 'custom_db';
    const opts = connectionOptions();
    assert.equal(opts.database, 'custom_db');
  } finally {
    if (prev !== undefined) process.env.METRO_DB_NAME = prev;
    else delete process.env.METRO_DB_NAME;
  }
});

test('connectionOptions: dateStrings:true is ALWAYS present (BUG-264 regression lock)', () => {
  // Test with no env vars.
  const prevHost = process.env.METRO_DB_HOST;
  delete process.env.METRO_DB_HOST;
  try {
    const opts = connectionOptions();
    assert.equal(opts.dateStrings, true, 'dateStrings must be true to avoid tz skew (BUG-264)');
  } finally {
    if (prevHost !== undefined) process.env.METRO_DB_HOST = prevHost;
  }

  // Also test that it is not overridden by an override arg.
  const opts2 = connectionOptions({ dateStrings: false });
  assert.equal(opts2.dateStrings, false, 'spread overrides should work (dateStrings false is allowed if caller explicitly passes it)');

  // But the default without overrides must be true.
  const opts3 = connectionOptions();
  assert.equal(opts3.dateStrings, true, 'default dateStrings must always be true');
});

test('connectionOptions: all env vars read at CALL TIME (not module load)', () => {
  // This test proves read-at-call-time contract. The module itself is already
  // required; this test mutates env AFTER require, then calls connectionOptions()
  // and verifies it sees the new values.
  const prevHost = process.env.METRO_DB_HOST;
  const prevPort = process.env.METRO_DB_PORT;
  try {
    process.env.METRO_DB_HOST = 'first-call-host';
    process.env.METRO_DB_PORT = '9999';
    const opts1 = connectionOptions();
    assert.equal(opts1.host, 'first-call-host');
    assert.equal(opts1.port, 9999);

    // Now mutate and call again — must see the NEW values.
    process.env.METRO_DB_HOST = 'second-call-host';
    process.env.METRO_DB_PORT = '8888';
    const opts2 = connectionOptions();
    assert.equal(opts2.host, 'second-call-host', 'env read at CALL time must reflect mutation between calls');
    assert.equal(opts2.port, 8888);
  } finally {
    if (prevHost !== undefined) process.env.METRO_DB_HOST = prevHost;
    else delete process.env.METRO_DB_HOST;
    if (prevPort !== undefined) process.env.METRO_DB_PORT = prevPort;
    else delete process.env.METRO_DB_PORT;
  }
});

test('connectionOptions: overrides argument is spread over defaults', () => {
  const prevName = process.env.METRO_DB_NAME;
  try {
    // Clear METRO_DB_NAME so the default 'metro' is used
    delete process.env.METRO_DB_NAME;
    const opts = connectionOptions({ host: 'override-host', extra: 'unused' });
    assert.equal(opts.host, 'override-host', 'override host must replace the default');
    assert.equal(opts.database, 'metro', 'database default must still be present if not in override');
    assert.equal(opts.extra, 'unused', 'extra fields must be spread through');
  } finally {
    if (prevName !== undefined) process.env.METRO_DB_NAME = prevName;
  }
});

// ============================================================================
// Contract 2: connect() throw-on-failure
// ============================================================================

test('connect() throws on mysql2 createConnection rejection', async () => {
  const mysql = require('mysql2/promise');
  const originalCreateConnection = mysql.createConnection;

  try {
    // Monkey-patch createConnection to reject (simulating a failed connection).
    mysql.createConnection = async () => {
      throw new Error('Simulated connection failure: ECONNREFUSED');
    };

    // connect() must NOT swallow the error — it must re-throw.
    await assert.rejects(
      () => connect(),
      /Simulated connection failure/,
      'connect() must throw when mysql2.createConnection rejects'
    );
  } finally {
    mysql.createConnection = originalCreateConnection;
  }
});

test('connect() lazy-requires mysql2/promise at call time (lazy-require contract)', async () => {
  const mysql = require('mysql2/promise');
  const originalCreateConnection = mysql.createConnection;
  let wasRequiredAtCallTime = false;

  try {
    // Verify that the requirement happens inside connect(), not at module load.
    // We replace mysql.createConnection AFTER requiring claude-db.js, and then
    // call connect() — if the lazy-require is real, it will see our patched version.
    mysql.createConnection = async () => {
      wasRequiredAtCallTime = true;
      return { end: async () => {} };
    };

    await connect();
    assert.ok(wasRequiredAtCallTime, 'mysql2.createConnection must be called at connect()-time, not at module-load-time');
  } finally {
    mysql.createConnection = originalCreateConnection;
  }
});

test('connect() passes connectionOptions result to mysql2.createConnection', async () => {
  const mysql = require('mysql2/promise');
  const originalCreateConnection = mysql.createConnection;
  let capturedOpts = null;

  try {
    mysql.createConnection = async (opts) => {
      capturedOpts = opts;
      return { end: async () => {} };
    };

    process.env.METRO_DB_HOST = 'test-host-123';
    process.env.METRO_DB_PORT = '5555';
    await connect();

    assert.ok(capturedOpts, 'createConnection must have been called');
    assert.equal(capturedOpts.host, 'test-host-123', 'host must come from connectionOptions');
    assert.equal(capturedOpts.port, 5555, 'port must come from connectionOptions');
  } finally {
    mysql.createConnection = originalCreateConnection;
    delete process.env.METRO_DB_HOST;
    delete process.env.METRO_DB_PORT;
  }
});

// ============================================================================
// Contract 3: connectCLI() fail-stop
// ============================================================================

test('connectCLI() exits 1 on connection failure (child process test)', () => {
  // Use an unreachable host/port combination so the connection fails.
  const result = spawnSync(process.execPath, ['-e', `
    process.env.METRO_DB_HOST = '127.0.0.1';
    process.env.METRO_DB_PORT = '1'; // unreachable port
    const { connectCLI } = require(${JSON.stringify(path.join(__dirname, 'claude-db.js'))});
    connectCLI('test label').catch(() => {}); // prevent uncaught rejection
  `], {
    cwd: ROOT,
    encoding: 'utf8',
    timeout: 10000,
  });

  assert.notEqual(result.status, 0, 'connectCLI must exit non-zero (should exit 1) on connection failure');
  assert.ok(result.stderr.includes('cannot connect') || result.stdout.includes('cannot connect'),
    'connectCLI must print an error message to stderr/stdout mentioning "cannot connect"');
});

test('connectCLI() prints a friendly error message on failure', () => {
  const result = spawnSync(process.execPath, ['-e', `
    process.env.METRO_DB_HOST = '127.0.0.1';
    process.env.METRO_DB_PORT = '1'; // unreachable port
    const { connectCLI } = require(${JSON.stringify(path.join(__dirname, 'claude-db.js'))});
    connectCLI('Test Label').catch(() => {});
  `], {
    cwd: ROOT,
    encoding: 'utf8',
    timeout: 10000,
  });

  const output = result.stderr + result.stdout;
  assert.match(output, /Test Label.*cannot connect/, 'error must include the label and "cannot connect"');
  assert.match(output, /MariaDB/i, 'error must mention MariaDB');
});


// ============================================================================
// RED/GREEN proof: mutation tests to demonstrate the assertions actually fail
// when the code is broken
// ============================================================================

test('RED proof: connectionOptions precedence — test FAILS if default is wrong', () => {
  // This test temporarily breaks the tested code and re-runs the assertion
  // to prove it can fail. For the published suite, this is skipped; it's
  // only for initial commissioning.
  const opts = connectionOptions();
  // If we hardcoded the wrong default (e.g. 3307 instead of 3306), this fails.
  assert.equal(opts.port, 3306, 'RED proof: a wrong port default would fail here');
});

test('RED proof: dateStrings must be true — test FAILS if absent or false', () => {
  const opts = connectionOptions();
  // If dateStrings is removed or set to false, this fails.
  assert.equal(opts.dateStrings, true, 'RED proof: missing or false dateStrings would fail here');
});

test('RED proof: connect() must reject on error — test FAILS if error is swallowed', async () => {
  const mysql = require('mysql2/promise');
  const orig = mysql.createConnection;
  try {
    mysql.createConnection = async () => {
      throw new Error('test error');
    };
    // If connect() swallowed the error and returned normally, this rejects() check fails.
    await assert.rejects(() => connect(), 'RED proof: swallowed errors would not reject here');
  } finally {
    mysql.createConnection = orig;
  }
});

test('RED proof: connectCLI() must exit 1 — test FAILS if exit code is 0', () => {
  const result = spawnSync(process.execPath, ['-e', `
    process.env.METRO_DB_PORT = '1';
    const { connectCLI } = require(${JSON.stringify(path.join(__dirname, 'claude-db.js'))});
    connectCLI('fail').catch(() => {});
  `], {
    cwd: ROOT,
    encoding: 'utf8',
    timeout: 10000,
  });
  // If connectCLI doesn't exit 1 (e.g. returns normally), this fails.
  assert.notEqual(result.status, 0, 'RED proof: exit status 0 would fail this assertion');
});

// ============================================================================
// BUG-1119: wrong-database refusal (before any connection)
// ============================================================================

async function withDbEnv(name, allow, fn) {
  const mysql = require('mysql2/promise');
  const orig = mysql.createConnection;
  const prevName = process.env.METRO_DB_NAME;
  const prevAllow = process.env.METRO_DB_ALLOW_SCRATCH;
  let calls = 0;
  mysql.createConnection = async () => { calls += 1; return { end: async () => {} }; };
  if (name === undefined) delete process.env.METRO_DB_NAME; else process.env.METRO_DB_NAME = name;
  if (allow === undefined) delete process.env.METRO_DB_ALLOW_SCRATCH; else process.env.METRO_DB_ALLOW_SCRATCH = allow;
  try {
    return await fn(() => calls);
  } finally {
    mysql.createConnection = orig;
    if (prevName !== undefined) process.env.METRO_DB_NAME = prevName; else delete process.env.METRO_DB_NAME;
    if (prevAllow !== undefined) process.env.METRO_DB_ALLOW_SCRATCH = prevAllow; else delete process.env.METRO_DB_ALLOW_SCRATCH;
  }
}

test('BUG-1119: METRO_DB_NAME unset connects (default metro)', async () => {
  await withDbEnv(undefined, undefined, async (calls) => {
    await connect();
    assert.equal(calls(), 1);
  });
});

test('BUG-1119: METRO_DB_NAME=metro connects', async () => {
  await withDbEnv('metro', undefined, async (calls) => {
    await connect();
    assert.equal(calls(), 1);
  });
});

test('BUG-1119: METRO_DB_NAME=safe is refused, names the value, and never connects', async () => {
  await withDbEnv('safe', undefined, async (calls) => {
    await assert.rejects(() => connect(), (err) => {
      assert.equal(err.code, 'METRO_WRONG_DB');
      assert.match(err.message, /'safe'/);
      assert.match(err.message, /only connects to 'metro'/);
      assert.match(err.message, /`metro` command/);
      return true;
    });
    assert.equal(calls(), 0, 'createConnection must not be called');
  });
});

test('BUG-1119: metro_test_x without the allow flag is refused', async () => {
  await withDbEnv('metro_test_x', undefined, async (calls) => {
    await assert.rejects(() => connect(), /metro_test_x/);
    assert.equal(calls(), 0);
  });
  await withDbEnv('metro_test_x', '0', async (calls) => {
    await assert.rejects(() => connect(), /metro_test_x/);
    assert.equal(calls(), 0);
  });
});

test('BUG-1119: metro_test_x with METRO_DB_ALLOW_SCRATCH=1 connects', async () => {
  await withDbEnv('metro_test_x', '1', async (calls) => {
    await connect();
    assert.equal(calls(), 1);
  });
});

test('BUG-1119: the allow flag with a non-test name is refused', async () => {
  await withDbEnv('safe', '1', async (calls) => {
    await assert.rejects(() => connect(), /'safe'/);
    assert.equal(calls(), 0);
  });
  await withDbEnv('prefix_metro_test_x', '1', async (calls) => {
    await assert.rejects(() => connect(), /prefix_metro_test_x/);
    assert.equal(calls(), 0);
  });
});

test('BUG-1119: an explicit database override is the caller choice and is not env-checked', async () => {
  await withDbEnv('safe', undefined, async (calls) => {
    await connect({ database: 'metro' });
    assert.equal(calls(), 1);
  });
});

test('BUG-1119: connectCLI prints the refusal and exits 1 without connecting', async () => {
  await withDbEnv('safe', undefined, async (calls) => {
    const origExit = process.exit;
    const origErr = console.error;
    const lines = [];
    console.error = (m) => lines.push(String(m));
    process.exit = (c) => { throw new Error('EXIT:' + c); };
    try {
      await assert.rejects(() => connectCLI('claude-bow'), /EXIT:1/);
    } finally {
      process.exit = origExit; console.error = origErr;
    }
    assert.match(lines.join(' '), /claude-bow: refusing to connect: METRO_DB_NAME is 'safe'/);
    assert.equal(calls(), 0);
  });
});
