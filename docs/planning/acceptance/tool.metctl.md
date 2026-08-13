BOW code: MOD-071

# Acceptance criteria — metctl, the operator/CLI counterpart to metropolis (MOD-071)

**BOW code:** MOD-071 (P1, open) — "cmd/metctl/ — real, tested CLI (main.go+main_test.go): with
no args prints the build identity (M0 skeleton behaviour); `metctl export <save-dir> [-out dir]`
and `metctl verify <save-dir>` operate on int.serializer bundles (M0-ENG §5; A8)."
**Spec refs:** M0-ENG §5 (CLI/operator surface); A8 (R9's CI-enforcement working agreement,
III-C §6); `internal/foundation/serialize` (int.serializer — bundle header, shard read/write,
`ValidateBundle`, `ReadHeader`); MOD-070/`foundation.buildinfo` (`buildinfo.String()`, the same
build-identity line `cmd/metropolis -version` prints); BUG-108 (this item's own registration —
`tool.metctl`, not folded into `foundation.repo`'s catch-all — already resolved per Aaron's
register-don't-exempt precedent; not re-litigated here); GR#7 (registry-sourced errors); GR#20
(contract-first module boundaries, `internal/ui -> internal/engine` import ban).
**Date:** 2026-08-13
**Status:** active — normal pipeline order (criteria written before junior dispatch)
**Package under test:** `cmd/metctl/` (`main.go`, `main_test.go`) — module key `tool.metctl`.
Depends on INT-002 (StateSerializer/`internal/foundation/serialize`), MOD-003 (Go monorepo
skeleton), MOD-070 (`foundation.buildinfo`), all `done`.

## Context for the junior — what "operates on int.serializer bundles" means concretely

`metctl` does **not** go through `internal/engine/save`'s `*Manager` (the `manual/autosave/
milestone/.staging` directory layout, `save-meta.json`, `MET-E8xx` error codes). It talks to
`internal/foundation/serialize` directly against a single bundle directory the caller names on
the command line: `serialize.ReadHeader`/`serialize.ValidateBundle` read `header.json`,
`serialize.OpenShardReader`/`ShardPath` read shard files, and `serialize.NDJSONSerializer`/
`BinarySerializer.ReadShard` decode records per the shard's recorded `Encoding`. A junior
who reaches for `save.Manager.Load` or expects `MET-E8xx` codes out of `metctl` is building
against the wrong layer — the save package's `ErrBundleValidationFailed` (`MET-E814`) is
**not** what `metctl verify`/`export` return; `serialize.ValidateBundle`'s own error, and
`SEC-001`'s shard-name validation error (`MET-F301`, `serialize.ValidateShardName`), are.

## Acceptance criteria

### A. No-args behaviour matches cmd/metropolis's own pattern

- **AC-1.** Running `metctl` with zero arguments prints a single line to stdout in the exact
  shape `"metctl " + buildinfo.String()"` — i.e. `buildinfo.String()`'s own format
  (`metropolis <Version> (<Commit>, <Branch>, built <BuildTime>)`) with the binary name
  swapped for `metctl`, the same pattern `cmd/metropolis -version` uses (`run.go`:
  `fmt.Fprintln(stdout, "metropolis", buildinfo.String())`) — and exits 0. Check: a test
  invoking the command's argument-handling function with an empty args slice captures
  stdout and asserts it contains `buildinfo.String()`'s current output verbatim, not just a
  substring like "metctl". **What a lazy implementation looks like:** hardcoding a static
  "metctl dev" string instead of calling `buildinfo.String()` — passes a loose "prints
  something" check but silently stops reflecting `-ldflags`-injected build identity, which
  is exactly the hand-maintained-version failure mode M0-ENG §3 exists to prevent. A test
  that only asserts non-empty output would not catch this; the check must assert the
  `buildinfo.String()` value specifically appears.
- **AC-2.** No-args behaviour never touches a save bundle, the filesystem beyond argv, or the
  `export`/`verify` code paths — it is unconditionally the first branch checked. Check: a test
  supplying zero args in a working directory containing no save data (a fresh `t.TempDir()`
  as cwd, or simply asserting no filesystem calls are needed) still succeeds — proves the
  build-identity path has no accidental dependency on save-bundle state.

### B. `metctl export <save-dir> [-out dir]`

- **AC-3.** `export` reads the bundle at `<save-dir>` via `serialize.ReadHeader`, then for
  every entry in `Header.ShardIndex` opens the shard (`serialize.OpenShardReader`) and
  re-encodes each record as one JSON object per line — `{"kind": <string>, "data": <raw
  object>}` — into `<out>/<shard-name>.ndjson`, selecting `NDJSONSerializer` or
  `BinarySerializer` to decode by the shard's own recorded `Encoding` field (never assuming
  a fixed encoding). Check: `TestRunExportHappyPath`'s existing shape — build a bundle with
  a known shard containing N records of a known `Kind`, run export, decode every line of the
  output file, and assert the record count and `Kind` match exactly (not just "file is
  non-empty").
- **AC-4.** `-out` defaults to `<save-dir>.export` when omitted, and to the caller-supplied
  path when given; the destination directory is created (`MkdirAll`) if it does not exist.
  Check: a test omitting `-out` and asserting the exported files land at exactly
  `<save-dir>.export/<shard>.ndjson`; a second test supplying `-out` to a directory that does
  not yet exist and asserting export still succeeds and the directory is created.
- **AC-5.** Export against a save-dir that does not exist, or whose `header.json` is missing
  or unreadable, returns a non-nil error (surfaced by `main` as `metctl: <error>` on stderr,
  exit 1) and writes nothing to any destination — no partial `-out` directory is left with a
  subset of shards silently exported as if the run succeeded. Check:
  `TestRunExportMissingBundle`'s existing shape (asserts export on a nonexistent dir errors);
  extend with a check that `-out` was never created (`os.Stat` on the would-be destination
  returns `IsNotExist`) — proving no partial-output side effect, not just a non-nil error.
- **AC-6.** Export against a bundle whose header carries a hostile/traversal shard name
  (`../escaped`, an absolute path, or any name `serialize.ValidateShardName` rejects) fails
  before any write outside `-out`, and the returned error carries the `MET-F301` registry
  code (GR#7 — registry-sourced, not an ad hoc string). Check: `main_test.go`'s existing
  `TestRunExportRejectsHostileShardName` — plants a sentinel path one level above `-out`
  where the traversal would land, asserts export errors, the error message contains
  `MET-F301`, and the sentinel path was never created/overwritten. This is SEC-001's
  write-side containment test and must keep passing unmodified; do not weaken it while
  building the rest of this item.
- **AC-7.** Export's read side is unbounded by design (`ReadShard`'s `maxDecodedBytes = 0`)
  because save bundles are architected for up to 100M-citizen scale — `export` must not
  impose a fixture-sized byte ceiling (that would be a regression borrowed from
  `harness.replay`'s deliberately small `maxFixtureDecodedBytes`, a different consumer with a
  different threat model). Check: a test exporting a shard whose encoded size exceeds any
  plausible fixture-sized limit (e.g. comfortably above `harness.replay`'s bound, if that
  constant is reachable for comparison) succeeds without truncation or a size-limit error.

### C. `metctl verify <save-dir>`

- **AC-8.** `verify` means, concretely: `serialize.ValidateBundle(dir)` — which itself checks
  the header's `FormatVersion` compatibility and rehashes/re-validates every shard listed in
  `ShardIndex` against its recorded checksum/size — and nothing more. `metctl` does not
  re-implement any part of that check itself; it is a thin CLI wrapper. Check: a test that
  monkeys with a shard's on-disk bytes after a valid bundle is built (bit-flip one byte, as
  `TestRunVerifyCorruptShard` already does) and asserts `verify` fails — proving the check is
  live re-validation, not a cached/header-only assumption of correctness.
- **AC-9.** On success, `verify` prints one line to stdout naming the bundle path and a fixed
  set of header-derived fields (format version, world seed, created-at tick, game month,
  debug-touched flag, shard count) and exits 0. `FormatVersion` is rendered with `%q` (quoted),
  not `%s`, because it is header-derived/attacker-influenced data reaching a print path
  (SEC-022-class) even though today's `ParseSemVer` grammar happens to make that currently
  unreachable — the quoting is defense in depth, not decoration, and must not be "simplified"
  away. Check: `TestRunVerifyHappyPath`'s existing shape, extended to assert the printed line
  contains the quoted `formatVersion=` field specifically (not just "exits 0").
- **AC-10.** On failure — bundle directory missing (`TestRunVerifyMissingBundle`'s shape),
  header unreadable, or any shard failing checksum/size/format-version validation
  (`TestRunVerifyCorruptShard`'s shape) — `verify` returns a non-nil error, `main` prints it
  to stderr as `metctl: <error>` and exits 1 (never 0, never a silently-empty stdout with a
  0 exit). Check: both existing tests continue to pass unmodified, plus a process-level test
  (real subprocess invocation, not just the `runVerify` function call — matching
  `tool.bowcli.md`'s precedent that a same-process unit test cannot prove an exit code
  reaches the OS) asserting `os.Exit(1)` on a corrupted bundle and `os.Exit(0)` on a valid
  one, distinguishing the two by process exit status alone.
- **AC-11.** A `FormatVersion` **major**-version mismatch and every other validation failure
  (checksum, size, missing header, a shard path that is a directory instead of a file) are
  NOT distinguished by `metctl verify`'s exit code or top-level behaviour — both are
  `ValidateBundle` failures and both produce exit 1 with `ValidateBundle`'s own error message
  passed through unwrapped by `metctl` (this differs deliberately from `internal/engine/save`'s
  `Manager.Load`, which DOES split these into `ErrFormatVersionMismatch` vs
  `ErrBundleValidationFailed` for its own callers — `metctl` has no such split because it has
  no caller that needs to react differently to the two cases; see "Context for the junior"
  above). Check: a test asserting a format-major-mismatch bundle and a checksum-corrupted
  bundle both produce the same exit code (1) from `metctl verify`, confirming no accidental
  divergent handling crept in.

### D. Subcommand dispatch and exit codes (the shape around A/B/C)

- **AC-12.** `metctl <unknown>` (any first argument that is not `export` or `verify`, and is
  not absent) prints `metctl: unknown subcommand "<arg>" (want: export, verify)` to stderr and
  exits **2** — distinct from the exit-1 code used for a recognised subcommand that ran and
  failed (AC-5, AC-10). Check: a subprocess test invoking `metctl bogus` and asserting exit
  code 2 specifically (not just non-zero) — the AC-5/AC-10 tests already prove exit 1 for
  their cases, so this is the third, distinct code that must not collapse into either.
- **AC-13.** A malformed invocation of a recognised subcommand — wrong argument count (e.g.
  `metctl export` with no save-dir, `metctl verify a b`) or an unknown flag — returns a
  non-nil error from that subcommand's own handler and is reported through the same exit-1
  path as any other subcommand-level failure (this is current, deliberate behaviour: usage
  errors are not distinguished from runtime errors by exit code, only the dispatcher's own
  "unknown subcommand" case gets exit 2). Check: a test asserting `metctl export` (zero
  positional args) and `metctl verify a b` (two positional args) both return non-nil errors
  from `runExport`/`runVerify` with a `usage: metctl ...` message, and (subprocess-level)
  exit 1, not 2 — this pins the current split explicitly so a future refactor cannot silently
  merge the two failure classes without a reviewer noticing the check break.

## GR#20 compliance — explicit statement, not assumed

- **AC-14.** `cmd/metctl` imports only `internal/foundation/buildinfo` and
  `internal/foundation/serialize` (plus the Go standard library) — it does not import
  `internal/ui/...` or `internal/engine/...` at all. GR#20's `internal/ui -> internal/engine`
  import ban therefore does not constrain this item directly (there is no `internal/ui`
  import present or needed for `metctl`'s scope) — `metctl` is a separate composition root
  from `cmd/metropolis`, exactly like the master brief anticipates, and has no UI layer of
  its own to violate the rule from. Check: `go list -f '{{.Imports}}' ./cmd/metctl/...`
  (or equivalent AST/import-graph scan) confirms no `internal/ui` or `internal/engine`
  package appears in `cmd/metctl`'s import set; this check must be able to fail — a test
  fixture temporarily adding a stray `internal/ui` import (in a throwaway branch, not
  committed) should be manually confirmed to flip the check before relying on it.

## Out of scope

- Any change to `internal/engine/save`'s `Manager`/`MET-E8xx` error codes — `metctl` is a
  separate, direct `int.serializer` consumer (see "Context for the junior") and this item
  does not touch that package.
- H-HEADLESS scenario scripting via `metctl` (`main.go`'s own doc comment: "and (later)
  H-HEADLESS scenario scripting") — explicitly future work, not this item's scope.
- Re-litigating BUG-108's registration call (`tool.metctl` vs `foundation.repo`) — already
  resolved; this file's spec-refs line cites it only to avoid contradicting it.
- Compression/format changes to the exported NDJSON shape itself, or supporting a future
  binary-encoded bundle beyond `BinarySerializer.ReadShard`'s existing decode-and-re-emit-as-
  JSON escape hatch (A3/R2) — `export`'s job is losslessly getting back to JSON from whatever
  encoding is recorded, not defining a new one.

## Assumptions logged (process v1.9)

- **ASM — AC-11's "no format-mismatch/generic-failure split in metctl's own exit code" is
  read as intentional-by-omission from the existing implementation, not a gap to fix in this
  item.** If Bill wants `metctl verify` to report format-major mismatches distinctly (e.g. a
  dedicated exit code an operator script could branch on), that is a new AC for a follow-up
  item, not silently added here — see the item text logged to BOW for the "what breaks if
  wrong" detail.
