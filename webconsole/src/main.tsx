import React from 'react';
import ReactDOM from 'react-dom/client';
import App from './App';
import { reportUnhandledRejection, reportWindowError } from './sim/backend';
import './styles.css';

// GR#1: trap the full context. The handlers (extracted, DOM-free, unit-tested in
// error-trapping.test.mjs) pass the JS stack these bare listeners used to drop.
window.addEventListener('error', (e) => reportWindowError(e));
window.addEventListener('unhandledrejection', (e) =>
  reportUnhandledRejection((e as PromiseRejectionEvent).reason)
);

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
