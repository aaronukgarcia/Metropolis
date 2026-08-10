package replay

// Registry error codes for harness.replay (MOD-013). Range: H000-H099,
// declared in data/errors.json's "ranges.reserved" table under the new
// "H" (harness) layer — no existing layer fit this package's own source
// (F/P/E/U/T are all already owned elsewhere; harness.stub's P090-P099
// is a deliberate exception for a package that speaks the protocol seam
// as a CONSUMER, per that package's own codes.go doc comment — this
// package is not in that position, it owns its own record/replay
// vocabulary). Checked against BOTH data/errors.json's ranges.reserved
// table AND a live source scan (`grep -rn "MET-[A-Z][0-9]" internal/
// cmd/`) before claiming H000-H099, per BUG-008's lesson that the table
// alone is not always current — no existing MET-H code was found either
// place. Every code below IS registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test
// (TestSourceCodesAreRegisteredAndInRange) guards against this ever
// drifting out of sync, and against another module's range accidentally
// overlapping this one.
const (
	// codeInvalidFixtureName: a fixture name failed
	// serialize.ValidateShardName (AC-2b) when resolved into a path
	// under fixtures/ — rejected outright, never sanitised.
	codeInvalidFixtureName = "MET-H000"

	// codeFixtureVersionMismatch: a loaded fixture's header.FormatVersion
	// major differs from this build's serialize.CurrentFormatVersion
	// major, or its recorded ProtocolVersion differs from
	// protocol.ProtocolVersion (AC-8).
	codeFixtureVersionMismatch = "MET-H001"

	// codeFixtureCorrupt: a fixture's shard bytes failed to decode (bad
	// gzip stream), its computed SHA256/size did not match the header's
	// recorded ShardMeta, or its header.json was malformed/incomplete
	// (AC-9). Never a panic.
	codeFixtureCorrupt = "MET-H002"

	// codeFixtureLoadFailed: an I/O failure (missing file, permission
	// denied, ...) opening or reading a fixture's header or shard file
	// (AC-9) — distinct from codeFixtureCorrupt, which is a decode/
	// integrity failure on bytes that WERE read successfully.
	codeFixtureLoadFailed = "MET-H003"

	// codeReplayTargetClosedEarly: EnginePlayer.Replay's ctx was done
	// before a CommandResult was observed for every command the fixture
	// replayed (AC-3c) — see doc.go's "Premature-close ambiguity"
	// section for the full BUG-020 analogy this generalises.
	codeReplayTargetClosedEarly = "MET-H004"

	// codeRecorderCopied: a Recorder method was called on a struct copy
	// of the value NewRecorder returned (AC-13b, SEC-020-style guard).
	codeRecorderCopied = "MET-H005"

	// codeEnginePlayerCopied: an EnginePlayer method was called on a
	// struct copy of the value NewEnginePlayer returned (AC-13b,
	// SEC-020-style guard).
	codeEnginePlayerCopied = "MET-H006"
)
