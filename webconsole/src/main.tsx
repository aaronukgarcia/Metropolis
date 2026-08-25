import React from 'react';
import ReactDOM from 'react-dom/client';
import App from './App';
import { recordError } from './sim/backend';
import './styles.css';

window.addEventListener('error', (e) => recordError(e.message || 'unknown error'));
window.addEventListener('unhandledrejection', (e) =>
  recordError(`unhandled rejection: ${String((e as PromiseRejectionEvent).reason).slice(0, 200)}`)
);

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
