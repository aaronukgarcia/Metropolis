import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { writeVersionModule, getLiveVersionData } from './scripts/gen-version.mjs';

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

// HOT version upgrade (Aaron, 2026-08-27) + stale-build guard (FEAT-2326609725,
// 2026-09-02): serve /version.json computed LIVE from git HEAD on each request,
// via gen-version.mjs's getLiveVersionData() (2s in-process cache so a client
// poll doesn't spawn `git` every time). This deliberately does NOT read
// version.live.json — that file is only rewritten by the post-commit hook, and
// the 2026-09-02 incident was exactly a long-lived dev server where the served
// version silently drifted from the real on-disk HEAD (the file went stale
// too, so even polling it couldn't have caught the drift). Asking git directly
// at request time is correct regardless of how long this process has been up,
// whether the hook fired, or whether HEAD moved by a checkout/rebase rather
// than a commit.
//
// The running app polls /version.json (see src/sim/liveVersion.tsx), sees the
// new number, and updates the badge hot while the sim keeps ticking (no
// mid-game reset) -- and separately, src/components/StaleBuildBanner.tsx
// compares this endpoint's `sha` against the loaded bundle's build-time-frozen
// APP_VERSION_SHA to warn loudly when the running JS itself is behind disk.
function liveVersionPlugin() {
  return {
    name: 'metropolis-live-version',
    configureServer(server: import('vite').ViteDevServer) {
      server.middlewares.use('/version.json', (_req, res) => {
        try {
          const data = getLiveVersionData();
          res.setHeader('Content-Type', 'application/json');
          res.setHeader('Cache-Control', 'no-store');
          res.end(JSON.stringify(data));
        } catch {
          // git unavailable / read failed — respond empty; the app keeps its
          // build-time value and the stale-build guard fails silent (never a
          // false positive when the endpoint can't be reached).
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
