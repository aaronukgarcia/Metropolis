// serve-bundle.test.mjs — FEAT-1972079924 dogfood bundle serving mode.
//
// Tests for webconsole/scripts/serve-bundle.mjs.
//
// BAR-1 fix (2026-08-31, r1 REJECT): these tests import and exercise the REAL exported
// createRequestHandler() from serve-bundle.mjs — there is no hand-copied reimplementation
// here anymore. An http server is spun up around the imported handler on an ephemeral port.
//
// Coverage:
// 1. SPA routing: unknown paths → index.html
// 2. /version.json serves version.live.json (cache-busted)
// 3. Asset serving with correct MIME types
// 4. RED-proof: break SPA fallback and fail
// 5. BAR-2: prefix-boundary directory-traversal / sibling-directory containment
// 6. BAR-3: malformed version.live.json degrades to 204, never garbage-as-JSON
//
// Run with: `npm test` (node --test test/)

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import { mkdirSync, writeFileSync, rmSync, existsSync } from 'node:fs';
import { createServer } from 'node:http';
import { createRequestHandler, isContained, getMimeType } from '../scripts/serve-bundle.mjs';

const __dirname = dirname(fileURLToPath(import.meta.url));
const FIXTURE_DIST = resolve(__dirname, '.', 'fixtures', 'stub-dist');
const FIXTURE_VERSION = resolve(__dirname, '.', 'fixtures', 'version.live.json');

/**
 * Clean up test fixtures.
 */
function cleanupFixtures() {
  try {
    rmSync(resolve(__dirname, 'fixtures'), { recursive: true, force: true });
  } catch {
    // Ignore cleanup errors
  }
}

/**
 * Set up test fixtures: a minimal dist/ directory and version.live.json.
 */
function setupFixtures() {
  cleanupFixtures();
  mkdirSync(FIXTURE_DIST, { recursive: true });

  // index.html: the SPA entry point
  writeFileSync(
    resolve(FIXTURE_DIST, 'index.html'),
    `<!DOCTYPE html>
<html>
<head>
  <title>Metropolis Dogfood</title>
  <link rel="stylesheet" href="/style.css" />
</head>
<body>
  <div id="app"></div>
  <script src="/bundle.js"></script>
</body>
</html>`
  );

  // style.css: a CSS asset
  writeFileSync(
    resolve(FIXTURE_DIST, 'style.css'),
    `body { background: #1b1f27; color: #e6e6e6; }`
  );

  // bundle.js: a JavaScript asset
  writeFileSync(
    resolve(FIXTURE_DIST, 'bundle.js'),
    `console.log('dogfood bundle loaded');`
  );

  // A subdirectory with an asset (test routing through directories)
  mkdirSync(resolve(FIXTURE_DIST, 'assets'), { recursive: true });
  writeFileSync(
    resolve(FIXTURE_DIST, 'assets', 'icon.svg'),
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><circle cx="50" cy="50" r="40" /></svg>`
  );

  // version.live.json: the live version file
  writeFileSync(
    FIXTURE_VERSION,
    JSON.stringify({
      version: 'v0.3.0-test-1-gabcdef1',
      numericVersion: '0.3.0.test1',
      gitAvailable: true,
      generatedAt: new Date().toISOString(),
    })
  );
}

/**
 * Make an HTTP request to the server. Returns { status, headers, body }.
 */
async function fetchFromServer(port, pathname, options = {}) {
  const http = await import('node:http');
  return new Promise((resolvePromise, reject) => {
    const req = http.request(
      {
        hostname: 'localhost',
        port,
        path: pathname,
        method: options.method || 'GET',
        headers: options.headers || {},
      },
      (res) => {
        let body = '';
        res.on('data', (chunk) => {
          body += chunk;
        });
        res.on('end', () => {
          resolvePromise({
            status: res.statusCode,
            headers: res.headers,
            body,
          });
        });
      }
    );
    req.on('error', reject);
    req.end();
  });
}

/**
 * Start a REAL server built from the production createRequestHandler(), on an ephemeral
 * port. Returns { port, stop() }.
 */
async function startServer(distDir, versionFile) {
  const handler = createRequestHandler(distDir, versionFile);
  const server = createServer(handler);
  await new Promise((resolvePromise, reject) => {
    server.listen(0, 'localhost', resolvePromise);
    server.on('error', reject);
  });
  return {
    server,
    port: server.address().port,
    stop() {
      return new Promise((resolvePromise) => {
        server.close(resolvePromise);
      });
    },
  };
}

/**
 * Invoke the exported handler directly with a hand-built req/res pair — no real HTTP
 * socket, no Node http-parser normalization in front of it. This is how BAR-2's raw
 * req.url traversal strings are exercised: req.url is whatever string we set, verbatim.
 */
function invokeHandlerDirectly(handler, rawUrl) {
  const req = { url: rawUrl, headers: { host: 'localhost' } };
  const chunks = [];
  let statusCode;
  let responseHeaders;
  let ended = false;
  const res = {
    writeHead(status, headers) {
      statusCode = status;
      responseHeaders = headers;
    },
    end(chunk) {
      if (chunk) chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
      ended = true;
    },
  };
  handler(req, res);
  return {
    status: statusCode,
    headers: responseHeaders,
    body: Buffer.concat(chunks).toString('utf8'),
    ended,
  };
}

test('setup: fixtures created', () => {
  setupFixtures();
  assert.ok(existsSync(FIXTURE_DIST), 'stub dist dir created');
  assert.ok(existsSync(resolve(FIXTURE_DIST, 'index.html')), 'index.html created');
  assert.ok(existsSync(FIXTURE_VERSION), 'version.live.json created');
});

test('SPA fallback: unknown path returns index.html', async () => {
  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/game/city/overview');
    assert.equal(res.status, 200, 'should return 200 for SPA fallback');
    assert.ok(res.body.includes('<!DOCTYPE html>'), 'should return index.html content');
    assert.ok(res.body.includes('Metropolis Dogfood'), 'should contain index.html title');
  } finally {
    await stop();
  }
});

test('SPA fallback: root path returns index.html', async () => {
  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/');
    assert.equal(res.status, 200);
    assert.ok(res.body.includes('<!DOCTYPE html>'));
  } finally {
    await stop();
  }
});

test('asset serving: CSS returned with correct MIME type', async () => {
  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/style.css');
    assert.equal(res.status, 200);
    assert.equal(res.headers['content-type'], 'text/css; charset=utf-8');
    assert.ok(res.body.includes('background: #1b1f27'));
  } finally {
    await stop();
  }
});

test('asset serving: JavaScript returned with correct MIME type', async () => {
  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/bundle.js');
    assert.equal(res.status, 200);
    assert.equal(res.headers['content-type'], 'application/javascript; charset=utf-8');
    assert.ok(res.body.includes('dogfood bundle loaded'));
  } finally {
    await stop();
  }
});

test('asset serving: subdirectory assets (SVG)', async () => {
  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/assets/icon.svg');
    assert.equal(res.status, 200);
    assert.equal(res.headers['content-type'], 'image/svg+xml');
    assert.ok(res.body.includes('circle'));
  } finally {
    await stop();
  }
});

test('/version.json endpoint: serves version.live.json with cache-busting headers', async () => {
  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/version.json');
    assert.equal(res.status, 200);
    assert.equal(res.headers['content-type'], 'application/json; charset=utf-8');
    assert.equal(res.headers['cache-control'], 'no-store');
    const data = JSON.parse(res.body);
    assert.equal(data.version, 'v0.3.0-test-1-gabcdef1');
    assert.equal(data.numericVersion, '0.3.0.test1');
    assert.equal(data.gitAvailable, true);
  } finally {
    await stop();
  }
});

test('/version.json endpoint: cache-control no-store prevents caching', async () => {
  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/version.json');
    // The liveVersion.tsx poller appends ?ts=... for cache busting.
    const res2 = await fetchFromServer(port, '/version.json?ts=12345');
    assert.equal(res.status, 200);
    assert.equal(res2.status, 200);
    assert.equal(res.headers['cache-control'], 'no-store');
    assert.equal(res2.headers['cache-control'], 'no-store');
  } finally {
    await stop();
  }
});

test('RED-proof: break SPA fallback and fail if index.html is missing', async () => {
  // Create a dist without index.html
  const badDist = resolve(__dirname, 'fixtures', 'bad-dist');
  mkdirSync(badDist, { recursive: true });
  writeFileSync(resolve(badDist, 'bundle.js'), 'console.log("hi");');
  // Explicitly do NOT create index.html — the SPA fallback should fail

  const { port, stop } = await startServer(badDist, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/game/city/overview');
    assert.equal(res.status, 404, 'should return 404 when index.html missing and no file matches path');
  } finally {
    await stop();
  }
});

test('404: a missing file WITH an extension is a real 404, not SPA fallback', async () => {
  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    // SPA fallback only applies to extensionless paths (client-side router routes).
    // A path with an extension is presumed to be a real asset request — if it's missing,
    // that is a genuine 404, not a route to hand off to index.html.
    const res = await fetchFromServer(port, '/nonexistent.txt');
    assert.equal(res.status, 404, 'a missing asset with an extension is a real 404');
  } finally {
    await stop();
  }
});

test('SPA fallback: a missing extensionless route falls back to index.html', async () => {
  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/nonexistent-route');
    assert.equal(res.status, 200, 'extensionless unknown route falls back to index.html');
    assert.ok(res.body.includes('<!DOCTYPE html>'));
  } finally {
    await stop();
  }
});

// --- BAR-2: prefix-boundary directory-traversal / sibling-directory containment ---

test('isContained: rejects a sibling directory that merely shares a string prefix', () => {
  // This is the exact shape of the r1 flaw: distDirPath = /root/dist, sibling = /root/dist-evil.
  // A naive `startsWith(normalizedDist)` check passes for this pair; isContained() must not.
  assert.equal(isContained('/root/dist', '/root/dist-evil/secret.txt'), false);
  assert.equal(isContained('/root/dist', '/root/dist/ok.txt'), true);
  assert.equal(isContained('/root/dist', '/root/dist'), true);
});

test('BAR-2: sibling dist-evil directory is never reachable via traversal', async () => {
  // Plant a sibling directory next to FIXTURE_DIST whose NAME is FIXTURE_DIST's own basename
  // plus a suffix ("stub-dist" -> "stub-dist-evil"). This exact shape is what defeats a naive
  // `normalizedFilepath.startsWith(normalizedDist)` check: the string "stub-dist-evil" DOES
  // start with the string "stub-dist" even though the directory is a sibling, not a child.
  const evilDir = resolve(__dirname, 'fixtures', 'stub-dist-evil');
  mkdirSync(evilDir, { recursive: true });
  writeFileSync(resolve(evilDir, 'secret.txt'), 'TOP SECRET — must never be served');

  const handler = createRequestHandler(FIXTURE_DIST, FIXTURE_VERSION);

  // 1. Real HTTP request with an encoded-slash traversal (the r1 attacker's exact form):
  //    the WHATWG URL parser does NOT treat %2f as a path-segment delimiter, so this
  //    string survives URL normalization unchanged as a single opaque segment — it only
  //    becomes a real ../ traversal once decodeURIComponent runs, which is exactly why
  //    isContained() (not URL normalization) has to be the actual gate.
  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/..%2fstub-dist-evil%2fsecret.txt');
    assert.notEqual(res.status, 200, 'encoded-slash traversal must not succeed');
    assert.ok(!res.body.includes('TOP SECRET'), 'secret content must never be served');
  } finally {
    await stop();
  }

  // 2. Direct handler invocation with the raw, unnormalized req.url string verbatim —
  //    no real HTTP socket / http-parser in front of it at all.
  const direct = invokeHandlerDirectly(handler, '/../stub-dist-evil/secret.txt');
  assert.notEqual(direct.status, 200, 'raw ../ traversal via req.url must not succeed');
  assert.ok(!direct.body.includes('TOP SECRET'), 'secret content must never be served');

  const directEncoded = invokeHandlerDirectly(handler, '/..%2fstub-dist-evil%2fsecret.txt');
  assert.notEqual(directEncoded.status, 200, 'raw encoded-slash traversal via req.url must not succeed');
  assert.ok(!directEncoded.body.includes('TOP SECRET'), 'secret content must never be served');
});

test('BAR-2: extensionless encoded-traversal MUST decode and reject with 403 (regression for decodeURIComponent removal)', async () => {
  // CRITICAL FIX (r2 bug): the r1 test uses a .txt file, so when decodeURIComponent was removed,
  // the pathname still has an extension, the SPA fallback check `!extname(pathname)` is false,
  // and the request 404s instead of being caught by isContained as a 403. This test plants
  // an extensionless secret file and uses an extensionless encoded-traversal path. Without
  // the decode step, the encoded %2f stays literal, extname sees no extension, SPA fallback
  // happens, and we 200 (wrong). The fix requires decodeURIComponent to be called BEFORE
  // extname and isContained checks.
  const evilDir = resolve(__dirname, 'fixtures', 'stub-dist-evil');
  mkdirSync(evilDir, { recursive: true });
  // Plant an extensionless secret file (no .txt, no .json, nothing).
  writeFileSync(resolve(evilDir, 'secretnoext'), 'EXTENSIONLESS SECRET — must be blocked by isContained');

  const handler = createRequestHandler(FIXTURE_DIST, FIXTURE_VERSION);

  // Attack: /..%2fstub-dist-evil%2fsecretnoext — no extension on the final segment.
  // If decodeURIComponent runs, it becomes /../stub-dist-evil/secretnoext, isContained rejects,
  // and we get 403. If decodeURIComponent is skipped, it stays encoded, extname('/..%2f...') sees
  // '.../f/secre...', misses the traversal, and SPA-falls-back to index.html (200 — WRONG).
  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/..%2fstub-dist-evil%2fsecretnoext');
    // EXACT assertion: 403, not just "not 200", not "!= 404". This catches both the correct
    // rejection AND the SPA-fallback false negative.
    assert.equal(res.status, 403, 'extensionless encoded-traversal MUST be rejected with 403 after decode');
    assert.ok(!res.body.includes('EXTENSIONLESS SECRET'), 'secret content must never be served');
  } finally {
    await stop();
  }

  // Direct handler test as well.
  const directEncoded = invokeHandlerDirectly(handler, '/..%2fstub-dist-evil%2fsecretnoext');
  assert.equal(directEncoded.status, 403, 'direct encoded-traversal (extensionless) must also be 403');
  assert.ok(!directEncoded.body.includes('EXTENSIONLESS SECRET'));
});

test('BAR-2: mid-path encoded traversal isolates the decode step (leading segment is not "..")', async () => {
  // The extensionless test above still does NOT regress when decodeURIComponent is removed,
  // because isContained's rel.startsWith('..') fires on the LITERAL un-decoded filename
  // '..%2f...' too (it begins with the two chars ".."), giving 403 for the wrong reason.
  // This payload puts a real segment FIRST so the un-decoded form is a single filename
  // beginning with 'sub' (contained, 404/SPA — NOT the secret, NOT 403), while the decoded
  // form 'sub/../../stub-dist-evil/secretnoext' genuinely escapes and is rejected 403.
  // Removing the decode therefore changes 403 -> 200, so THIS test regresses on the mutation.
  const evilDir = resolve(__dirname, 'fixtures', 'stub-dist-evil');
  mkdirSync(evilDir, { recursive: true });
  writeFileSync(resolve(evilDir, 'secretnoext'), 'EXTENSIONLESS SECRET — must be blocked by isContained');

  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/sub%2f..%2f..%2fstub-dist-evil%2fsecretnoext');
    assert.equal(res.status, 403, 'mid-path encoded traversal MUST decode-then-reject with 403');
    assert.ok(!res.body.includes('EXTENSIONLESS SECRET'), 'secret content must never be served');
  } finally {
    await stop();
  }
});

test('BAR-2: legit extensionless SPA routes still fall back to index.html (control)', async () => {
  // CONTROL: ensure the fix doesn't break legitimate extensionless routes. A path like
  // /game/city/overview (no extension, no traversal) should still SPA-fallback to index.html (200).
  // This proves the decode+extname logic is correct and the previous test's 403 is NOT a
  // overly strict blanket rejection of all extensionless paths.
  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/game/city/overview');
    assert.equal(res.status, 200, 'legit extensionless SPA route must fall back to index.html');
    assert.ok(res.body.includes('<!DOCTYPE html>'), 'response must be index.html content');
  } finally {
    await stop();
  }
});

// --- BAR-3: malformed version.live.json must not be served as valid JSON ---

test('BAR-3: malformed version.live.json degrades to 204, not garbage-as-JSON', async () => {
  const badVersionFile = resolve(__dirname, 'fixtures', 'bad-version.live.json');
  writeFileSync(badVersionFile, '{ this is not valid JSON,,, ');

  const { port, stop } = await startServer(FIXTURE_DIST, badVersionFile);
  try {
    const res = await fetchFromServer(port, '/version.json');
    assert.equal(res.status, 204, 'malformed JSON must respond 204, same as absent');
    assert.equal(res.body, '', 'a 204 response must carry no body');
  } finally {
    await stop();
  }
});

test('BAR-3: valid version.live.json still serves 200 (control case)', async () => {
  const { port, stop } = await startServer(FIXTURE_DIST, FIXTURE_VERSION);
  try {
    const res = await fetchFromServer(port, '/version.json');
    assert.equal(res.status, 200);
    assert.doesNotThrow(() => JSON.parse(res.body));
  } finally {
    await stop();
  }
});

test('getMimeType: falls back to octet-stream for unknown extensions', () => {
  assert.equal(getMimeType('/foo/bar.unknownext'), 'application/octet-stream');
  assert.equal(getMimeType('/foo/bar.css'), 'text/css; charset=utf-8');
});

test('cleanup: fixtures removed', () => {
  cleanupFixtures();
  assert.ok(!existsSync(FIXTURE_DIST), 'stub dist dir cleaned up');
});
