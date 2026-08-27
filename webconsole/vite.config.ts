import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { writeVersionModule } from './scripts/gen-version.mjs';

// FEAT-1972079872: regenerate the git-derived version module before every
// build and dev-server start, so src/generated/version.ts is always current
// with HEAD. The generator is fail-safe (falls back to "dev" when git is
// unavailable) so the build never breaks. The npm prebuild/predev scripts
// also run it, which is what makes it exist before `tsc` runs in `build`.
// It also writes version.live.json (see below).
function gitVersionPlugin() {
  return {
    name: 'metropolis-git-version',
    buildStart() {
      writeVersionModule();
    },
    configResolved() {
      // Cover `vite dev` and `vite build` invocations equally.
      writeVersionModule();
    },
  };
}

// HOT version upgrade (Aaron, 2026-08-27): serve /version.json by reading
// version.live.json FRESH from disk on each request. That file lives at the
// webconsole root, is NOT imported by any module, and is added to
// server.watch.ignored below — so the post-commit hook rewriting it triggers
// NO HMR and NO page reload. The running app polls /version.json (see
// src/sim/liveVersion.tsx), sees the new number, and updates the badge hot
// while the sim keeps ticking. No mid-game reset.
const LIVE_VERSION_FILE = resolve(__dirname, 'version.live.json');
function liveVersionPlugin() {
  return {
    name: 'metropolis-live-version',
    configureServer(server: import('vite').ViteDevServer) {
      server.middlewares.use('/version.json', (_req, res) => {
        try {
          const body = readFileSync(LIVE_VERSION_FILE, 'utf8');
          res.setHeader('Content-Type', 'application/json');
          res.setHeader('Cache-Control', 'no-store');
          res.end(body);
        } catch {
          // Not generated yet — respond empty; the app keeps its build-time value.
          res.statusCode = 204;
          res.end();
        }
      });
    },
  };
}

export default defineConfig({
  plugins: [gitVersionPlugin(), liveVersionPlugin(), react()],
  server: {
    watch: {
      // Never reload the page just because the live version file changed — the
      // whole point is that a commit updates the version without resetting the
      // running game.
      ignored: ['**/version.live.json'],
    },
  },
});
