# fixtures/

Permanent H-REPLAY fixtures (`harness.replay`, MOD-013, M0-ENG §2.2).
Recorded/replayed command/event/delta streams from an engine (stub or
real) — the save format IS the fixture format (`internal/foundation/serialize`'s
`NDJSONSerializer` + `Header`, reused directly, never a second encoder).

Fixtures in this directory are **permanent test estate** per M0-ENG §6:
they never get deleted once checked in.

## Naming convention

Each fixture `<name>` is exactly two flat files directly under this
directory:

```
fixtures/<name>.ndjson.gz     # the one NDJSON+gzip shard (the recorded stream)
fixtures/<name>.header.json   # format/protocol version, world seed, engine
                               # identity, and the shard's integrity metadata
```

`<name>` must be a single clean path component — no `/`, `\`, `:`, `..`,
absolute or volume-relative form, and no trailing dot or space (the exact
rule `internal/foundation/serialize.ValidateShardName` enforces for shard
names, reused here rather than re-derived — see
`internal/harness/replay/doc.go`'s "Fixture names are untrusted input"
section).

Deliberately **not** `int.serializer`'s nested `Bundle` directory layout
(`<dir>/header.json` + `<dir>/shards/<name>.ndjson.gz`) — see
`internal/harness/replay/doc.go`'s "On-disk layout" section for why: this
layout must produce files directly under `fixtures/` so tooling can find
them with a plain, non-recursive glob (`fixtures/*.ndjson.gz`).

## Regenerating a fixture

`fixtures/folkestone64-sample` is generated from a short, deterministic
H-STUB (`internal/engine/stub`) session — `Subscribe` to `f1.viewport`,
three `AdvanceTicks`, `Pause`, `Resume` — with chaos disabled, so
regenerating it without any code change reproduces byte-identical output
(GR#21). Regenerate with:

```powershell
go run internal/harness/replay/gen/main.go
```

run from the repo root. The script is excluded from the normal build via
a `//go:build ignore` tag; it is not a `go generate` target because it
has no source to generate FROM beyond the engine/protocol code itself —
it is a recording session, not a code transform.

## Loading a fixture

```go
fx, err := replay.Load("fixtures", "folkestone64-sample")
// mode (a): canned data for a UI's Transport consumer
uiPlayer, err := replay.NewUIPlayer(fx)
// mode (b): regression replay into a live engine
enginePlayer, err := replay.NewEnginePlayer(fx)
```

See `internal/harness/replay/doc.go` for the full package documentation.
