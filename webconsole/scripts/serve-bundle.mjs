#!/usr/bin/env node
/**
 * serve-bundle.mjs — serve the built Vite bundle (dist/) with SPA routing and live-version polling.
 *
 * FEAT-1972079924: dogfood bundle serving mode.
 * Aaron's ruling: dogfood runs a BUILT bundle; the live-version poller offers upgrades
 * at the player's moment (the existing rebuild-prompt flow); local commits must NOT
 * touch the running page.
 *
 * This script:
 * 1. Serves dist/ (the vite build output) on a configurable port (default 4173)
 * 2. Implements SPA routing: any path that doesn't match a real file → index.html
 * 3. Serves /version.json by reading version.live.json fresh per request (cache-busted)
 * 4. Never hot-reloads (it's a static bundle)
 *
 * BAR-1 (2026-08-31, r1 REJECT fix): the request handler + its helpers are now EXPORTED
 * so tests exercise the REAL production code path instead of a hand-copied reimplementation.
 * createRequestHandler(distDir, versionFilePath) returns the http request listener; the CLI
 * entrypoint below is the only caller that also starts a listening server, and only runs when
 * this file is executed directly (not when imported by a test).
 *
 * Usage:
 *   node scripts/serve-bundle.mjs [port]
 * Defaults to port 4173 if not specified.
 */

import { createServer } from 'node:http';
import { readFileSync, existsSync } from 'node:fs';
import { resolve, extname, dirname, relative, isAbsolute } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const DEFAULT_DIST_DIR = resolve(__dirname, '..', 'dist');
const DEFAULT_VERSION_FILE = resolve(__dirname, '..', 'version.live.json');

// MIME types for common bundle files.
const MIME_TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'application/javascript; charset=utf-8',
  '.mjs': 'application/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.wasm': 'application/wasm',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.gif': 'image/gif',
  '.ico': 'image/x-icon',
  '.webp': 'image/webp',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.ttf': 'font/ttf',
  '.eot': 'application/vnd.ms-fontobject',
};

/**
 * Get MIME type for a file path. Default to application/octet-stream for unknown types.
 */
export function getMimeType(filepath) {
  const ext = extname(filepath).toLowerCase();
  return MIME_TYPES[ext] || 'application/octet-stream';
}

/**
 * Is `targetPath` contained within `baseDir` (or equal to it)?
 *
 * BAR-2 fix: the previous check compared normalized strings with `startsWith(normalizedDist)`,
 * which passes for a SIBLING directory that merely shares the prefix (e.g. `dist-evil` starts
 * with the string `dist`). path.relative() is the correct containment test: a path inside
 * baseDir always relativizes to something that does NOT start with `..` and is not itself
 * absolute (which `path.relative` returns when the two paths share no common root, e.g. across
 * Windows drive letters).
 */
export function isContained(baseDir, targetPath) {
  const rel = relative(baseDir, targetPath);
  return rel === '' || (!rel.startsWith('..') && !isAbsolute(rel));
}

/**
 * Try to read and serve a file from dist/. Returns true if served, false if not found.
 */
export function tryServeFile(res, filepath) {
  try {
    if (!existsSync(filepath)) return false;
    const content = readFileSync(filepath);
    const mime = getMimeType(filepath);
    res.writeHead(200, {
      'Content-Type': mime,
      'Content-Length': content.length,
    });
    res.end(content);
    return true;
  } catch (e) {
    return false;
  }
}

/**
 * Serve version.json by reading versionFilePath fresh (cache-busted).
 * Matches the behavior of vite.config.ts's liveVersionPlugin.
 *
 * BAR-3 fix: malformed JSON must never be forwarded to the client as if it were valid
 * application/json — parse it first and fall back to the same 204 (No Content) response
 * used when the file is simply absent.
 */
export function serveVersionJson(res, versionFilePath) {
  try {
    const body = readFileSync(versionFilePath, 'utf8');
    JSON.parse(body); // throws on malformed JSON — caught below, treated like "absent"
    res.writeHead(200, {
      'Content-Type': 'application/json; charset=utf-8',
      'Cache-Control': 'no-store',
      'Content-Length': Buffer.byteLength(body),
    });
    res.end(body);
  } catch {
    // Not generated yet, or unparseable — respond 204 (No Content), matching dev-server behavior.
    res.writeHead(204, { 'Cache-Control': 'no-store' });
    res.end();
  }
}

/**
 * Build the request handler for a given dist/ directory and version.live.json path.
 * This is the REAL production handler — the CLI entrypoint below and the test suite
 * both call this same function, so there is no parallel reimplementation to drift.
 */
export function createRequestHandler(distDir, versionFilePath) {
  const resolvedDist = resolve(distDir);
  const indexPath = resolve(resolvedDist, 'index.html');

  return function requestHandler(req, res) {
    const url = new URL(req.url, `http://${req.headers.host || 'localhost'}`);
    const pathname = url.pathname;

    // Version polling endpoint (cache-busted).
    if (pathname === '/version.json') {
      serveVersionJson(res, versionFilePath);
      return;
    }

    // Decode percent-encoding ourselves before resolving: the WHATWG URL parser only
    // collapses '..' segments that are already literal '/'-delimited path segments. An
    // encoded slash (%2f) is NOT treated as a segment delimiter by URL parsing, so
    // '/..%2fdist-evil%2fsecret.txt' survives URL normalization untouched — only to become
    // a real '../dist-evil/secret.txt' traversal once something downstream decodes it. We
    // decode here (as any real static file server must, to serve files with spaces/unicode
    // names) and then rely on isContained() — not on URL normalization — as the actual gate.
    let decodedPathname;
    try {
      decodedPathname = decodeURIComponent(pathname);
    } catch {
      res.writeHead(400, { 'Content-Type': 'text/plain' });
      res.end('Bad Request');
      return;
    }

    const filepath = resolve(resolvedDist, decodedPathname === '/' ? 'index.html' : decodedPathname.slice(1));

    // Security: ensure filepath stays within resolvedDist (prevent directory traversal,
    // including the sibling-directory prefix-match flaw fixed in isContained()).
    if (!isContained(resolvedDist, filepath)) {
      res.writeHead(403, { 'Content-Type': 'text/plain' });
      res.end('Forbidden');
      return;
    }

    if (tryServeFile(res, filepath)) {
      return;
    }

    // Not a real file: SPA fallback to index.html (client-side routing).
    if (pathname !== '/' && !extname(pathname)) {
      if (tryServeFile(res, indexPath)) {
        return;
      }
    }

    // Nothing worked: 404.
    res.writeHead(404, { 'Content-Type': 'text/plain' });
    res.end('404 Not Found');
  };
}

// CLI entrypoint — only runs when this file is executed directly (e.g. `node
// scripts/serve-bundle.mjs`), never when imported (by the test suite or otherwise).
if (process.argv[1] && process.argv[1] === fileURLToPath(import.meta.url)) {
  const PORT = parseInt(process.argv[2] || '4173', 10);
  const requestHandler = createRequestHandler(DEFAULT_DIST_DIR, DEFAULT_VERSION_FILE);
  const server = createServer(requestHandler);

  server.listen(PORT, () => {
    console.log(`[serve-bundle] listening on http://localhost:${PORT}`);
    console.log(`[serve-bundle] serving ${DEFAULT_DIST_DIR}`);
    console.log(`[serve-bundle] SPA fallback enabled: unknown paths → index.html`);
    console.log(`[serve-bundle] /version.json serves version.live.json (cache-busted)`);
  });

  server.on('error', (err) => {
    if (err.code === 'EADDRINUSE') {
      console.error(`[serve-bundle] ERROR: port ${PORT} already in use`);
      process.exit(1);
    } else {
      console.error(`[serve-bundle] ERROR:`, err.message);
      process.exit(1);
    }
  });
}
