import React from 'react';
import ReactDOM from 'react-dom/client';
import App from './App';
import { reportUnhandledRejection, reportWindowError, installConsoleTap, setAppVersion } from './sim/backend';
import { versionRaw } from './sim/version';
import './styles.css';

// GR#1/GR#7: trap the full context on all four paths. The handlers (extracted,
// DOM-free, unit-tested in error-trapping.test.mjs) pass the JS stack these bare
// listeners used to drop, normalize non-Error values (BUG-442), and record with
// registry-sourced error codes (FEAT-1972079916).
window.addEventListener('error', (e) => reportWindowError(e));
window.addEventListener('unhandledrejection', (e) =>
  reportUnhandledRejection((e as PromiseRejectionEvent).reason)
);

// BAR-F3 (round-r1): wire the real, build-time git-derived version into
// backend.ts's error envelope. See backend.ts's comment on setAppVersion for
// why this is a setter call here rather than a static import inside backend.ts.
setAppVersion(versionRaw);

// BAR-K4b (round-r1): the actual monkey-patch install now lives in backend.ts
// (installConsoleTap) so it is unit-testable against a fake console; this is
// just the one-line wiring call for the real console.
installConsoleTap(console);

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
