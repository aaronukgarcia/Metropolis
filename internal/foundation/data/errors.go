package data

// Registry-sourced error codes for this package (module key
// "foundation.data"). Range: F600-F699 — the acceptance doc's suggested
// F400-F499 range was already reserved for foundation.solver in
// data/errors.json's "reserved" table, so this module's delivery report
// picked the first free foundation sub-range instead. Every code below
// IS registered in data/errors.json with real severity/module/message/
// remedy fields (GR#7; closed under BUG-008) — see that file's
// "F600-F699" reserved-range entry and its "codes" section. The
// internal/foundation/errs source-scan test guards against this ever
// drifting out of sync again.
const (
	// CodeDataDirNotFound: data/ directory could not be resolved via
	// $METROPOLIS_DATA_DIR, executable-relative search, or CWD-upward
	// search.
	CodeDataDirNotFound = "MET-F600"

	// CodeFileNotFound: a specific config file was not found at its
	// resolved path.
	CodeFileNotFound = "MET-F601"

	// CodeMalformedJSON: a config file's contents are not well-formed
	// JSON.
	CodeMalformedJSON = "MET-F602"

	// CodeMissingVersion: a config file decoded successfully but its
	// top-level "version" field is missing or zero.
	CodeMissingVersion = "MET-F603"

	// CodeSchemaInvalid: a config file failed schema-level validation
	// (required field, type, or range check) beyond the version check.
	CodeSchemaInvalid = "MET-F604"

	// CodeReloadDebugRequired: Reload was called while debug mode is
	// disabled.
	CodeReloadDebugRequired = "MET-F605"

	// CodeReloadFailed: a reload's read/decode/validate cycle failed;
	// the previously-loaded config remains live (AC-11).
	CodeReloadFailed = "MET-F606"
)
