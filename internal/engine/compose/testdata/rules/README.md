# testdata/rules

Fixtures for FEAT-9995385612 (rule versions in saves). Each rule that is wired gets a directory `v<N>` here, recorded at version N-1; see `compose.CheckRuleFixture`.

- `golden-v0/journal.golden`: the five journal frames of the round-trip session as written before rule versions existed (one frame per line, no trailing newline); version 0 must stay byte-identical to it.
- `golden-v0/header.json.golden`: the composition bundle header of the snapshot taken at tick 65 of that session, before rule versions existed.
- `golden-v0/save-meta.json.golden`: the bundle's save-meta sidecar before rule versions existed (no `ruleVersion` field, because version 0 writes nothing).

The `v1` fixture holds a snapshot of simulation state, so it must be regenerated whenever a daily formula changes outcomes.
Regenerate it from the repo root with: `METROPOLIS_GEN_RULES_FIXTURE=1 go test -count=1 -run TestRuleFixtureV1_Generate ./internal/engine/compose/`

## Layout (BUG-1250)

A persist directory keeps one folder per tenant and one per city, named by 64-character hashes. Stored like that, one fixture file was 206 characters long inside the repository, and Windows could not check the repository out below a folder of about 53 characters. So each `v<N>` fixture is committed in a SHORT layout:

- `v<N>/tenant/city/...`: the persist directory's files (journal, snapshots, seed, meta, gamemode).
- `v<N>/names.txt`: two lines, `tenant=<hash>` and `city=<hash>`, the real folder names.

The tests call `materializeRuleFixture` (copies the fixture into a `t.TempDir()` under the real names) before `CheckRuleFixture` or a store opens it; the generators call `writeShortRuleFixture`. `TestFixtureLayout_NoLongTestdataPaths` fails if any testdata path in the module passes 150 characters. The registry's fixture path (`internal/engine/compose/testdata/rules/v<N>`) is unchanged.

The `v2` fixture (roads, rule 2) is a version-1 session: a home built, a snapshot, then a cell zoned in the tail. Regenerate it with: `METROPOLIS_GEN_RULES_FIXTURE=1 go test -count=1 -run TestRuleFixtureV2_Generate ./internal/engine/compose/`
