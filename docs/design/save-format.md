# int.serializer — StateSerializer + save/fixture schema

Module key: `int.serializer` · GUID `8ee49e96-0de9-4326-9b90-e94622874f94` ·
BoW code `INT-002` · path `internal/foundation/serialize/` · spec ref A3;
§5.3; V.2.2; M0-ENG §2.2.

Sprint-0 freeze review page. Covers the bundle layout, header fields, shard
encoding, determinism guarantees, migration rules, the binary-format trigger
criteria (A3), and `metctl` usage.

Status: **awaiting freeze review.**

## Why this module exists

The save format IS the fixture format (M0-ENG §2.2, H-REPLAY) — one
serialisation to rule them all. Every consumer in code.json's inbound list
for `int.serializer` (engine core, engine.citizens, cloud.azure,
feat.debugmode, feat.saveux, harness.headless, harness.replay) reads and
writes state through the same `StateSerializer` contract, whether that state
is a save-under-load, a recorded H-REPLAY fixture, an H-HEADLESS scenario
snapshot, or a `metctl export`.

R2 of Amendment A1 ("JSON everywhere is a death sentence for saves")
accepted the critique in part: 100 M citizens through a naive JSON
marshaller is minutes of CPU. The resolution (A3) is this package:
`StateSerializer` from day one, NDJSON canonical for everything below a
size threshold, a versioned binary format reserved for above it, and
`metctl export` as the permanent lossless escape hatch back to JSON. JSON
stays the project's universal accessibility format without ever sitting on
a hot path.

## Bundle layout on disk

```
<save-name>/
  header.json                     -- Header (see below)
  shards/
    citizens.0000.ndjson.gz       -- one shard, NDJSON+gzip encoded
    citizens.0001.ndjson.gz
    buildings.0000.ndjson.gz
    ...
```

- `<save-name>` is the bundle directory; its base name is the save/fixture
  name as the rest of the system sees it.
- `header.json` is its own small file, readable in isolation (F12 info
  panel, a save-list screen, `metctl verify`) without touching any shard.
- Each shard is one file under `shards/`. The file name is
  `<ShardMeta.Name>` plus an extension chosen by `ShardMeta.Encoding`
  (`.ndjson.gz` for `"ndjson+gzip"`, the only encoding implemented today —
  `shardFileExt` in `savebundle.go` is the single place that mapping lives,
  so adding an encoding later is a one-function change).
- `CreateBundleDir` refuses to write into an existing directory — no
  silent merge into a stale bundle. Overwriting a save is the caller's
  explicit decision (remove-then-create), not this package's.

## The `StateSerializer` contract

```go
type Record struct {
    Kind string // caller-defined label; never interpreted by this package
    Data []byte // opaque JSON document, written verbatim
}

type RecordSource func() (rec Record, ok bool, err error)   // pull, one at a time
type RecordHandler func(Record) error                        // push, one at a time

type StateSerializer interface {
    WriteShard(w io.Writer, meta ShardMeta, next RecordSource) (ShardMeta, error)
    ReadShard(r io.Reader, handle RecordHandler) error
}
```

Design choices worth flagging for review:

- **The serializer does not know domain types.** Kind is a label the
  *engine* assigns when it registers record kinds; `Data` is opaque JSON
  bytes in, opaque JSON bytes out. This keeps `internal/foundation/serialize`
  free of any dependency on engine packages — it imports only
  `internal/foundation/buildinfo` (for `AppVersion`) and the standard
  library.
- **Pull for writing, push for reading.** `WriteShard` pulls from the
  caller via `RecordSource` so it controls backpressure; `ReadShard` pushes
  to the caller via `RecordHandler` so the caller never has to buffer a
  whole shard either. Neither direction holds more than one record's bytes
  in memory — required at 100 M-citizen scale (§5.3: "headless tools can
  process saves without loading whole worlds").
- **`ShardMeta` round-trips through `WriteShard`.** Callers pass in
  `Name`/`Kind`/`Encoding` and get back a copy with `RecordCount`,
  `ByteSize`, and `SHA256` filled in, ready to append to
  `Header.ShardIndex`. No second pass over the data is needed to compute
  those.

## The header

`Header` (`header.go`) is the bundle's own small JSON file:

| Field | Type | Notes |
|---|---|---|
| `FormatVersion` | string (semver) | This package's wire format version. Starts `"1.0.0"`. Independent of `AppVersion`. |
| `WorldSeed` | int64 | The deterministic world seed (§5.3 determinism rule). |
| `CreatedAtTick` | int64 | Simulation tick at write time — **not** a wall-clock timestamp. |
| `GameMonth` | int64 | In-world calendar month at write time. |
| `AppVersion` | string | Build identity (`buildinfo.Version` or equivalent) that wrote the bundle. Informational only — never used for compatibility decisions. |
| `DebugTouched` | bool | **Sticky.** See below. |
| `ShardIndex` | []ShardMeta | One entry per shard, in write order. |

No field in `Header` is wall-clock time. `CreatedAtTick` is a deterministic,
replayable input (the sim tick), which is what lets bundle bytes be
reproducible from `(worldSeed, command log)` alone — the same property the
CI determinism gate relies on for its snapshot hashing (§1.2).

### DebugTouched — sticky by construction

§14 / M0-ENG §3: switching debug mode on is recorded in the save header and
**flagged forever**, for balance-data hygiene. This package enforces the
"once true, always true" rule at the API level rather than by convention:
`Header.DebugTouched` has no public setter that can clear it.

```go
func (h *Header) TouchDebug()                    // sets true, unconditionally
func (h *Header) MergeDebugTouched(incoming bool) // h.DebugTouched ||= incoming
```

`MergeDebugTouched` exists for the carry-forward cases — a save-over
reusing an existing header, or `metctl export` re-emitting a bundle — so a
previously debug-touched save can never come back clean through this
package's API. (A determined caller can still hand-edit `header.json`
outside the program; that is a deliberate scope boundary, not a gap in this
package.)

### Migration rules (V.2 open item 2)

- A reader accepts any saved `FormatVersion` whose **major** equals the
  reader's supported major (`CurrentFormatVersion`'s major). Minor/patch
  differences within the same major are always readable: minor bumps are
  additive (new optional field), patch bumps are clarifications/bugfixes,
  and neither may change the meaning of an existing field.
- A reader **refuses** a newer major, with a clear error naming both
  versions (`CheckFormatVersion`). No silent best-effort read.
- A reader also refuses an **older** major today — no migrator exists yet.
  When one is needed, it's expected to live in this package as an explicit
  `MigrateV1toV2`-style step invoked by the bundle-open path *before*
  `CheckFormatVersion` would otherwise reject it, not as a silent fallback
  inside `CheckFormatVersion` itself.
- Bumping major is a deliberately rare, reviewed event — every consumer
  (engine load path, `metctl`, fixtures) needs a coordinated update.

## The NDJSON encoding — canonical

`NDJSONSerializer` (`ndjson.go`) is the only implemented encoding. Each
shard is a gzip stream; each line inside it is one JSON object
`{"kind": "...", "data": <raw record JSON>}`. Both directions stream:
`WriteShard` gzips and hashes as it writes each line, `ReadShard` decodes
one line at a time from a growable line reader (not `bufio.Scanner`, whose
default token-size cap would silently mis-split an unusually large record).

### Determinism guarantee

Same records in, same order, ⇒ identical bytes out, every time. This is
what makes the CI determinism gate's snapshot hashing (§1.2: `sha256(save)`
must match across repeated runs and across worker-pool sizes) meaningful
for save bytes, not just in-memory world state. Two things make it true:

1. Each record's `Data` is embedded as `json.RawMessage` and written
   verbatim — never re-marshalled — so there's no second encoding pass to
   introduce field-ordering or number-formatting drift.
2. The `gzip.Writer` header fields that otherwise vary run-to-run
   (`ModTime`, `OS`, `Name`, `Comment`) are pinned: `OS = 255` ("unknown" —
   also `compress/gzip`'s own default, pinned here explicitly rather than
   relied upon) and `ModTime` left at its zero value; `Name`/`Comment` are
   never set. `flate` compression itself is already deterministic for a
   given input and level.

Byte-determinism depends on the *caller's* `RecordSource` also being
deterministic (same records, same order) — this package cannot enforce
that; `TestNDJSONByteDeterminism` only proves the encoder side holds up
given a deterministic source.

### Integrity — `ValidateBundle`

`ValidateBundle` (`savebundle.go`) reads the header (with the version
check above) and then, for every shard in `ShardIndex`, re-hashes the shard
file and compares `SHA256`/`ByteSize` against what's recorded. It streams
each shard through the hash — never loads a whole shard into memory — and
does **not** decode shard contents: it's an integrity check of the encoded
bytes, not a semantic replay. All mismatches across all shards are
collected via `errors.Join` so one call reports every problem, not just the
first.

## The binary format — reserved, not implemented

`BinarySerializer` (`binary.go`) exists only as a named type whose methods
return a clear not-implemented error (placeholder string constant
`"MET-F300"`, since `internal/foundation/errs` may not be merged yet — see
the TODO in `binary.go` to switch to `errs.New` once it is; `data/errors.json`
already reserves `F300`-`F399` for `foundation.serialize`).

Intended design, documented so freeze review can push back on it now rather
than after the fact:

- A versioned, little-endian binary shard format, following the same
  major/minor/patch acceptance rules as the header.
- Per-record layout mirroring the columnar struct-of-arrays cold citizen
  store (A1): field-level compression (bucketed enums, delta-coded ages,
  bit-packed states) — the same 60-100 B/citizen target that motivates SoA
  storage in memory, not a naive struct dump.
- Still shard-streamable — same never-buffer-the-whole-shard contract as
  `NDJSONSerializer`. Throughput is the only reason this format exists, so
  it must not regress streamability.
- `metctl export` reads any binary bundle via `BinarySerializer.ReadShard`
  and re-writes it via `NDJSONSerializer.WriteShard` — the permanent
  lossless escape hatch back to JSON (R2).

**Trigger criteria (A3):** the binary format arrives when profiling of the
amortised cold pass (§5.3, A1/A2) shows JSON marshalling — on the
*background snapshot path*, never the tick path, which doesn't touch
serialization at all — is the bottleneck at scale. Not before. Until then
NDJSON+gzip is canonical for every save and fixture regardless of size; the
"size threshold" mentioned in A3 is explicitly **not yet chosen** — see
Open Questions.

## `metctl` usage

```
metctl                          # M0 skeleton behaviour: print build identity, exit
metctl verify <save-dir>        # header + per-shard hash validation
metctl export <save-dir> [-out dir]   # decompress+verify (NDJSON today) or
                                       # lossless binary->NDJSON (once binary exists)
```

`verify` calls `ValidateBundle` and prints a one-line summary
(`formatVersion`, `worldSeed`, `createdAtTick`, `gameMonth`,
`debugTouched`, shard count) on success; any header or hash problem exits
non-zero with the collected error text.

`export` reads the header, then for each shard picks
`NDJSONSerializer.ReadShard` or `BinarySerializer.ReadShard` by
`ShardMeta.Encoding`, and re-encodes every record as a plain (uncompressed)
NDJSON file — one `{"kind", "data"}` object per line, matching the internal
line shape exactly — under `-out` (default `<save-dir>.export`).

Both subcommands are implemented as ordinary functions (`runVerify`,
`runExport` in `cmd/metctl/main.go`) invoked directly by their tests, not
just exercised via the compiled binary.

## Open questions for freeze review

1. **NDJSON→binary size threshold (A3).** Not chosen yet — needs a number
   or a formula (per-save total size? per-shard? citizen count?) once
   profiling data exists. This package is structurally ready either way
   (`ShardMeta.Encoding` already discriminates per shard, not per bundle,
   so a bundle could in principle mix encodings across shards if that ever
   turns out useful).
2. **Shard sizing/naming convention.** This package takes `ShardMeta.Name`
   as given by the caller (e.g. `"citizens.0000"`); it does not itself
   decide how many shards a save has or how citizens/buildings/etc. are
   partitioned across them. That's presumably the 256-fixed-shard scheme
   from §1.2, but the mapping from simulation shard to save shard hasn't
   been specified — worth confirming they're the same 256, or deliberately
   different.
3. **Older-major migration.** Rules are documented but no migrator exists;
   fine for M0 (`FormatVersion` starts at `1.0.0`, there is no older major
   yet) but worth flagging so `MigrateV1toV2`-shaped work isn't a surprise
   later.
4. **`AppVersion` provenance.** `NewHeader` takes `appVersion` as a plain
   string parameter rather than importing `buildinfo` itself and calling
   `buildinfo.Version` directly — deliberate, to keep `serialize` from
   depending on `buildinfo`'s package-level mutable vars inside its own
   tests, but callers (the eventual save/load feature, `feat.saveux`) need
   to remember to pass `buildinfo.Version` explicitly.
5. **Cross-shard consistency.** `ValidateBundle` checks each shard's bytes
   against its own recorded hash; it does not check that
   `Header.ShardIndex` matches the actual files present in `shards/`
   (e.g. an extra untracked file, or a shard file deleted with the header
   left stale). Worth deciding whether `metctl verify` should also assert
   `shards/` contains exactly the files `ShardIndex` names, no more, no
   less.
