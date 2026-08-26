import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { writeVersionModule } from './scripts/gen-version.mjs';

// FEAT-1972079872: regenerate the git-derived version module before every
// build and dev-server start, so src/generated/version.ts is always current
// with HEAD. The generator is fail-safe (falls back to "dev" when git is
// unavailable) so the build never breaks. The npm prebuild/predev scripts
// also run it, which is what makes it exist before `tsc` runs in `build`.
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

export default defineConfig({
  plugins: [gitVersionPlugin(), react()],
});
