# Metropolis-only /health-check steps

The shared `/health-check` skill (SAFE edition) runs the SAFE-wide checks and then this file. These steps were the Metropolis edition of the skill until 2026-11-18, when Aaron ruled the project takes the registry version; they are kept here so nothing was lost.

Run, from the repo root, read-only:

```
node tools/health/check.js
```

It reports (module `tool.healthcheck`, FEAT-027):

1. The CI workflow's job structure, parsed live from `.github/workflows/ci.yml`.
2. The determinism gate (GR#21): present, and its result for the current HEAD. A red gate is an automatic P0. FAIL, UNCONFIRMED and WARN are kept distinct (BUG-021).
3. The `-race` flag on every `go test` step (SEC-028).
4. `build-test-vet` and `lint` status from the latest CI run.
5. The perf CI jobs, present or not.
6. Git sync state: the default branch read live, and ahead/behind against it.
7. Branch protection on the default branch (through `gh api`; a 404 is a FAIL).
8. The Book of Work ready queue and blocked items (`node claude-bow.js ready`).

Then, by hand:

- Every FAIL that is not already on the Book of Work gets an item (P0 for a determinism or `-race` FAIL).
- CI truth comes from `gh run list --branch main --limit 1`, read as text. Never from the exit code of a chained command.
