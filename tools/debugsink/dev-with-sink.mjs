#!/usr/bin/env node
// tools/debugsink/dev-with-sink.mjs — a tiny launcher that spawns both the Vite
// dev server and the debugsink HTTP server as sibling child processes, forwarding
// SIGINT and exit so Ctrl+C kills both cleanly. Prefixes each child's output with
// a label ([vite]/[sink]) to distinguish streams.
//
// BUG-543 discipline: this file is NOT under a test/ directory; requiring it must
// NEVER have side effects (no auto-spawn). The guard below ensures `require.main`
// entry point only.
//
// Usage: node dev-with-sink.mjs (or via npm: npm run dev:full from webconsole/)

import { spawn } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);

/**
 * Spawn a child process, prefix its output lines with a label, and return
 * a handle to manage it.
 *
 * `prefixLabel` is prepended to stdout and stderr lines so concurrent output
 * from multiple children is readable.
 */
function spawnWithLabel(command, args, opts = {}, prefixLabel) {
  const child = spawn(command, args, {
    stdio: ['inherit', 'pipe', 'pipe'],
    shell: false,
    ...opts,
  });

  if (child.stdout) {
    child.stdout.on('data', (chunk) => {
      const lines = String(chunk).split('\n');
      for (const line of lines) {
        if (line.length > 0) {
          process.stdout.write(`${prefixLabel} ${line}\n`);
        }
      }
    });
  }

  if (child.stderr) {
    child.stderr.on('data', (chunk) => {
      const lines = String(chunk).split('\n');
      for (const line of lines) {
        if (line.length > 0) {
          process.stderr.write(`${prefixLabel} ${line}\n`);
        }
      }
    });
  }

  return child;
}

async function runDevWithSink() {
  // Determine the working directory: when run from webconsole/, this file is at
  // ../tools/debugsink/dev-with-sink.mjs, so we need to cd to the webconsole
  // directory to run `vite` (which expects to run from that directory).
  const webconsoleDir = path.resolve(__filename, '../../..', 'webconsole');

  process.stdout.write('Starting debugsink dev server with MariaDB debug sink...\n');

  // Spawn Vite dev server
  const viteProcess = spawnWithLabel('node', ['node_modules/.bin/vite'], {
    cwd: webconsoleDir,
  }, '[vite] ');

  // Spawn debugsink server
  const debugsinkProcess = spawnWithLabel(process.execPath, [path.resolve(__filename, '..', 'server.js')], {}, '[sink] ');

  // Track both processes
  const children = [viteProcess, debugsinkProcess];
  let shuttingDown = false;

  // Forward SIGINT to both children and wait for both to exit
  function shutdown() {
    if (shuttingDown) return;
    shuttingDown = true;

    process.stdout.write('\nShutting down both services...\n');

    for (const child of children) {
      if (child && !child.killed) {
        try {
          child.kill('SIGINT');
        } catch {
          // Process already exited
        }
      }
    }

    // Give children a moment to exit gracefully, then force-kill if needed
    const forceKillTimer = setTimeout(() => {
      for (const child of children) {
        if (child && !child.killed) {
          try {
            child.kill('SIGKILL');
          } catch {
            // Already dead
          }
        }
      }
    }, 3000);

    // Wait for all children to exit
    Promise.all(children.map((child) => new Promise((resolve) => {
      child.on('exit', resolve);
      // Timeout failsafe
      setTimeout(resolve, 5000);
    }))).then(() => {
      clearTimeout(forceKillTimer);
      process.exit(0);
    });
  }

  process.on('SIGINT', shutdown);
  process.on('SIGTERM', shutdown);

  // If either child exits unexpectedly, exit the launcher too
  for (const child of children) {
    child.on('exit', (code, signal) => {
      if (!shuttingDown) {
        process.stderr.write(`Child process exited with code ${code}, signal ${signal}\n`);
        shutdown();
      }
    });

    child.on('error', (err) => {
      process.stderr.write(`Child process error: ${err.message}\n`);
      if (!shuttingDown) shutdown();
    });
  }
}

// Only run if invoked directly (not required as a module)
if (process.argv[1] === __filename) {
  runDevWithSink().catch((err) => {
    process.stderr.write(`Fatal error: ${err.message}\n`);
    process.exit(1);
  });
}

export { spawnWithLabel, runDevWithSink };
