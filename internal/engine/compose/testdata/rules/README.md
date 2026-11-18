# testdata/rules

Fixtures for FEAT-9995385612 (rule versions in saves). Each rule that is wired gets a directory `v<N>` here, recorded at version N-1; see `compose.CheckRuleFixture`.

- `golden-v0/journal.golden`: the five journal frames of the round-trip session as written before rule versions existed (one frame per line, no trailing newline); version 0 must stay byte-identical to it.
- `golden-v0/header.json.golden`: the composition bundle header of the snapshot taken at tick 65 of that session, before rule versions existed.
- `golden-v0/save-meta.json.golden`: the bundle's save-meta sidecar before rule versions existed (no `ruleVersion` field, because version 0 writes nothing).

The `v1` fixture holds a snapshot of simulation state, so it must be regenerated whenever a daily formula changes outcomes.
Regenerate it from the repo root with: `METROPOLIS_GEN_RULES_FIXTURE=1 go test -count=1 -run TestRuleFixtureV1_Generate ./internal/engine/compose/`
