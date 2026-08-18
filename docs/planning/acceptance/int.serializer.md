BOW code: INT-002

# Acceptance criteria — int.serializer (INT-002)

**BOW code:** INT-002
**Spec refs:** A3 (Serialization amendment, `docs/METROPOLIS-MASTER-v2.1.md` lines 1344, 1363); §5.3 (Memory & storage at 100M, lines 181-182); V.2.2 (open item 2, "Save/fixture schema", line 1327); M0-ENG §2.2 (H-REPLAY: "the save format IS the fixture format", line 847); M0-ENG §3 (debug-touched save header flag, lines 855, 861); code.json `int.serializer` entry.
**Date:** 2026-08-08
**Status:** active
**Package under test:** `internal/foundation/serialize/` and `cmd/metctl/` (path from `node claude-bow.js show INT-002`)
**Standard gates:** see `README.md` — all apply, package for SG-4/SG-7 is `./internal/foundation/serialize/... ./cmd/metctl/...`.

## Scope

The `StateSerializer` interface, NDJSON-shard save/fixture bundle format (header + shards), and the `metctl export`/`verify` subcommands. Save format IS the fixture format (M0-ENG §2.2) — one serialization to rule them all.

## Acceptance criteria

### Functional

- **AC-1 (GR#20).** A `StateSerializer` interface exists with `WriteShard(w io.Writer, meta ShardMeta, next RecordSource) (ShardMeta, error)` and `ReadShard(r io.Reader, handle RecordHandler) error` (or equivalents covering write-shard-from-stream and read-shard-to-stream). Check: `go doc ./internal/foundation/serialize StateSerializer` shows both methods.
- **AC-2.** At least one NDJSON-backed implementation of `StateSerializer` exists (`NDJSONSerializer` or equivalent). Check: `grep -n "type NDJSONSerializer" internal/foundation/serialize/ndjson.go` matches, and `go build ./internal/foundation/serialize/...` compiles it as satisfying the interface (a `var _ StateSerializer = NDJSONSerializer{}` assertion, or a passing test that assigns it to the interface type).
- **AC-3.** `WriteShard` never buffers more than one `Record` in memory — it streams via the `RecordSource` pull iterator. Check: `grep -n "func.*WriteShard" internal/foundation/serialize/ndjson.go` shows a loop calling `next()` repeatedly and writing each record before requesting the following one (no accumulation into a slice of all records before writing).
- **AC-4.** `ReadShard` never loads a whole shard into memory before invoking the handler — it streams line-by-line (or record-by-record). Check: `grep -n "func.*ReadShard" internal/foundation/serialize/ndjson.go` shows a loop reading one record/line at a time and invoking `handle` per record, not decoding the full shard into a slice first.
- **AC-5 (GR#21; principle-derived, lead-confirmed).** Byte-determinism: writing the same sequence of `Record`s twice via `WriteShard` (same input, same gzip settings) produces byte-identical output, with gzip metadata (mtime, OS byte, name) zeroed/fixed rather than left at library defaults. Check: `go test ./internal/foundation/serialize/... -run TestNDJSON -race -count=1 -v` (or the actual determinism test name) passes, and `grep -rn "byte.*ident\|determinis" internal/foundation/serialize/*_test.go` finds a test that writes twice and compares output bytes (or SHA256) for equality. Rationale (Bill-confirmed): the CI determinism gate (§1.2/A8) hashes world snapshots, so save bytes must be deterministic for the gate to mean anything — a violation here is GR#21 (Red Determinism Gate Stops the Line) territory, auto-P0.
- **AC-6.** `ShardMeta.SHA256` is computed while writing (streaming digest), never a second buffered pass. Check: `grep -n "sha256" internal/foundation/serialize/ndjson.go` (case-insensitive) shows the hash is fed incrementally (e.g. `io.MultiWriter` with a `sha256.New()` hasher, or the hasher's `Write` called per chunk written) rather than re-reading the output afterward.
- **AC-7.** A `Header` type exists carrying `FormatVersion`, `WorldSeed`, `CreatedAtTick` (or equivalent simulation-time field — not wall clock), `DebugTouched`, and a shard index. Check: `go doc ./internal/foundation/serialize Header` lists all these fields (names may vary slightly for the tick/seed fields, but a simulation-tick-based field and a world-seed field must both be present).
- **AC-8.** `DebugTouched` is sticky: once set true, no exported method can clear it back to false. Check: `grep -n "func.*Header.*Touch\|func.*Header.*Debug" internal/foundation/serialize/header.go` shows the only mutators are OR-in operations (e.g. `h.DebugTouched = h.DebugTouched || incoming` or `h.DebugTouched = true`), and a passing test asserts calling the touch/merge function with `false` after it was already `true` leaves it `true` (`grep -rn "DebugTouched" internal/foundation/serialize/*_test.go` finds such a case).
- **AC-9.** Format-version compatibility: a reader accepts any saved `FormatVersion` sharing the reader's MAJOR version (regardless of MINOR/PATCH) and refuses any other MAJOR with a clear error naming both versions. Check: `grep -n "func CheckFormatVersion" internal/foundation/serialize/header.go` matches, and a passing test covers (a) same-major/different-minor accepted, (b) newer-major refused with an error, (c) older-major refused with an error (`grep -rn "CheckFormatVersion" internal/foundation/serialize/*_test.go` shows at least these three cases and `go test ./internal/foundation/serialize/... -run TestCheckFormatVersion -race -count=1 -v` passes).
- **AC-10.** `cmd/metctl` has an `export` subcommand. Check: `grep -n '"export"' cmd/metctl/main.go` matches, and `go run ./cmd/metctl export -h` (or `--help`) exits without a panic and prints usage (exit code 0 or the flag package's standard exit-2-on-help-without-args is acceptable as long as it is not a crash/stack trace).
- **AC-11.** `cmd/metctl` has a `verify` subcommand. Check: `grep -n '"verify"' cmd/metctl/main.go` matches, and `go run ./cmd/metctl verify -h` behaves as in AC-10.
- **AC-12.** `metctl export` produces lossless NDJSON from a bundle written by the binary/NDJSON serializer path — i.e. round-tripping through export recovers the same records. Check: `go test ./cmd/metctl/... -race -count=1 -v` includes a passing round-trip test (`grep -rn "func Test.*[Ee]xport" cmd/metctl/*_test.go` finds one), or (if metctl's own tests don't cover it yet) the serialize package's own bundle round-trip test does (`grep -rn "func Test.*RoundTrip\|func Test.*Bundle" internal/foundation/serialize/*_test.go`).
- **AC-13.** `ValidateBundle` (or equivalent) checks a bundle's header and every shard's recorded `SHA256`/`ByteSize` against the actual on-disk shard files, catching a corrupted or truncated shard. Check: `grep -n "func ValidateBundle" internal/foundation/serialize/savebundle.go` matches, and a passing test corrupts a shard file and asserts `ValidateBundle` returns an error (`grep -rn "func Test.*Validate" internal/foundation/serialize/*_test.go` finds such a case).

### Error handling

- **AC-14.** `ParseSemVer` rejects a malformed version string (wrong number of components, non-numeric component, negative number) with a clear error rather than panicking. Check: a passing test covers at least one malformed case (`grep -rn "func Test.*ParseSemVer\|func Test.*SemVer" internal/foundation/serialize/*_test.go` finds one, and it includes a malformed-input case).
- **AC-15.** `WriteShard`/`ReadShard` propagate the first encountered I/O or decode error rather than swallowing it (e.g. `ReadShard` stops and returns the error from a bad JSON line instead of skipping it silently). Check: a passing test feeds malformed input and asserts a non-nil error is returned (`grep -rn "func Test.*ReadShard\|func Test.*[Mm]alformed" internal/foundation/serialize/*_test.go` finds one).

### Determinism & safety

- **AC-16 (SG-7 scoped; GR#21).** `grep -rn "time.Now" internal/foundation/serialize/*.go` (excluding `_test.go`) returns no matches — `Header.CreatedAtTick` must come from the simulation tick, never wall-clock time, per the package's own doc comment (`grep -n "CreatedAtTick is deliberately NOT\|NOT a wall-clock" internal/foundation/serialize/header.go` matches the stated invariant).
- **AC-17 (GR#21).** No `range` over a Go map produces ordering-sensitive shard/record output. Check: manual scan of `ndjson.go`, `binary.go`, `savebundle.go`, `header.go` for map-range loops feeding write order — none found (Tester records any instance as a FAIL with file:line).
- **AC-18 (GR#21; principle-derived, lead-confirmed).** Gzip output metadata (`ModTime`, `OS`, `Name` on `gzip.Writer.Header`/`gzip.Header`) is fixed/zeroed, not left at Go's library defaults (which embed the current wall-clock mtime by default), so two writes of identical input at different real times still produce identical bytes. Check: `grep -n "ModTime\|gzip.Header" internal/foundation/serialize/ndjson.go` shows the mtime being explicitly zeroed/set to a fixed value. Same GR#21 rationale as AC-5.

### Documentation

- **AC-19.** `internal/foundation/serialize/doc.go` (or equivalent package doc) states the module key `int.serializer` and cites A3/§5.3/M0-ENG §2.2. Check: `grep -n "int.serializer" internal/foundation/serialize/doc.go` and `grep -n "A3\|§5.3\|M0-ENG" internal/foundation/serialize/doc.go` both match.
- **AC-20.** The migration rules (same-major-accept / newer-major-refuse) are documented in prose next to `CheckFormatVersion`, not only implemented. Check: `grep -n "Migration rules" internal/foundation/serialize/header.go` matches a doc comment block preceding the function.

## Out of scope

- The versioned binary shard format's actual encode/decode logic above the size threshold — `binary.go`'s `BinarySerializer` may be a documented stub (`WriteShard`/`ReadShard` returning a not-yet-implemented error) for this item; full binary implementation is future work once a size threshold triggers it, per A3's "above it, snapshots write a versioned binary shard format" being described as a scale-triggered path, not a day-one requirement for every save.
- Azure Blob save sync (`cloud.azure` consumer) — separate module (`MOD-069`).
- Engine-side record-kind decoders (citizen, building, etc.) — those belong to the engine modules that own each record kind; this package only moves opaque bytes + a `Kind` label.
- Older-major migrator implementations (`MigrateVX toVY` steps) — spec explicitly defers these until a migrator is actually needed.

## Escalations

- **Resolved.** GR#20/GR#21 exist (`CLAUDE.md` lines 51-52) — see `foundation.errors.md`'s Escalations section. Cited above as AC-1 (GR#20) and AC-5/AC-16/AC-17/AC-18 (GR#21).
- **Resolved.** The byte-determinism requirement (AC-5/AC-18) was flagged as principle-derived rather than literal spec text. Bill accepted: the CI determinism gate (§1.2/A8) hashes world snapshots, so save bytes must be deterministic for the gate to mean anything. Both ACs are now annotated "principle-derived, lead-confirmed" and kept as-is — this is not a Tester FAIL risk, it's confirmation the requirement stands.
- **ASM-026 (confirm-and-close).** NUL-byte deferral now logged as this ASM; Go os layer fails closed on both GOOS (sound).
- **ASM-149 (confirm-and-close).** ReadShard byte bound is a per-caller parameter (16MiB replay / 0 metctl) per SEC-033 lesson.
- **ASM-061 (confirm-and-close).** cmd/metctl main.go:74 `%s` on FormatVersion safe only via ParseSemVer Atoi gate; verified empirically, now logged.
