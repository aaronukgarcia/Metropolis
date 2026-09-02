# Agent test-verification contract

> **Status:** in force 2026-09-02 (Aaron: *"we need to improve the harness — better watching, better monitoring, better specification for the testing, such a waste"*). Enforced mechanically by `claude-test-scope-guard.js` (PreToolUse). Every build/round/audit brief MUST carry the one-paragraph summary at the bottom.

## The waste this prevents

A dispatched build agent verified its webconsole work by running the **whole** suite — `npm test` (= `node --test "test/*.test.mjs" && tsx --test <14 .tsx files>`). In this environment that:

1. emits **60k+ lines** with node's default `spec` reporter,
2. is **killed by the harness** for flooding,
3. is **re-run** by the agent, which sees the kill as a transient failure.

That unbounded kill-and-retry loop cost **~1h37m and ~309k tokens** and proved nothing that the targeted suites plus CI's `node-test` job don't already prove. The full glob is CI's job, not an agent's.

## The rule

**An agent verifies the files its change touches — never the whole suite.** The full glob is certified by CI (`.github/workflows` → `node-test`) after the push. Running it locally is redundant and, here, actively harmful.

### What to run (in order)

1. **Typecheck** (webconsole changes): `npx tsc --noEmit` from `webconsole/`.
2. **Targeted tests, via the bounded runner** — only the files your change touches or adds:
   ```
   node tools/test/scoped.mjs <file> [<file> ...]
   ```
   `scoped.mjs` dispatches `.mjs/.js` to `node --test` and `.tsx/.ts` to `tsx --test`, runs each with a **hard timeout** (default 240s, `--timeout=<sec>`) and the **concise `dot` reporter** (one char per test, not the per-assertion firehose), prints a one-line tally, and exits non-zero on any failure or timeout. It **cannot flood and cannot hang-and-retry**.
3. **Go changes**: run the specific package/test, e.g. `go test ./internal/engine/compose/ -run TestX -v` — never `go test ./...` inside an agent (that's a CI + lead-`/ci-green` job).
4. **Prove-can-fail**: for a new RED test, stub the code (scratch `cp`/`mv`, never git) to confirm the assertion genuinely fails, then restore byte-identical.

### What NOT to run (blocked by `claude-test-scope-guard.js`)

- `npm test` / `npm run test`
- `node --test` / `tsx --test` with a **glob** target (`test/*.test.mjs`, `**`) or **no** file argument (whole-tree discovery)
- `node --test` / `tsx --test` naming **more than 12** files (effectively a full run)

The guard **fails open** (it is a cost/hygiene control, not security): on any parse ambiguity it allows. It always allows the scoped runner and named runs ≤ 12 files.

### The escape hatch (lead only, supervised)

A deliberate full local run — e.g. the lead reproducing CI in `/ci-green` — uses the **bounded** full set:
```
node tools/test/scoped.mjs --webconsole-ci
```
which runs exactly the files `webconsole/package.json`'s `test` script covers, but with the timeout + `dot` reporter so it completes in one shot. If a raw command is genuinely required, set `CLAUDE_ALLOW_FULL_TEST=1` in the environment **before** it (never inline).

## Monitoring (the lead's duty)

- A dispatched lane that runs long is a smell. The lead watches lane runtime; a verification lane should be **minutes, not hours**. If an agent reports repeated kills of the same command, stop it and re-brief with this contract — do not let it loop.
- Briefs cap verification scope explicitly and forbid the full glob (see the snippet below).

## Mandatory brief snippet (paste into every build/round/audit dispatch)

> **Verification (bounded — do NOT run the whole suite):** verify only the files you touch. Typecheck `npx tsc --noEmit`. Run targeted tests via `node tools/test/scoped.mjs <files>` (bounded + concise). For Go, run the specific package/`-run` only. NEVER run `npm test`, `go test ./...`, or a globbed/bare `node --test`/`tsx --test` — CI certifies the full suite after the push; running it locally floods the harness and wastes tokens (it is blocked by `claude-test-scope-guard.js`). Prove any new RED test can fail via a scratch stub (never git).
