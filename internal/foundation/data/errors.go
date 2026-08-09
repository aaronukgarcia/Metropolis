package data

// Placeholder registry-sourced error codes for this package. These are
// NOT yet entered in data/errors.json (foundation.errors owns that
// registry, and adding new codes is a deliberate registry edit this
// package's delivery leaves for review — see the delivery report). They
// are used exactly as any other errs.New/errs.Wrap code: when the
// registry does not (yet) recognise one of these codes, errs.New/Wrap
// degrades to the well-formed MET-F003 "unregistered code" fallback per
// their documented contract — never a panic, never silent.
//
// Range chosen: F600-F699. NOTE (registry-wiring): the delivery report
// for this item flags that the acceptance doc's suggested F400-F499
// range is already reserved for foundation.solver in data/errors.json's
// "reserved" table; F600-F699 is the first free foundation sub-range
// and is used here instead, pending a real registry entry + reservation
// table update.
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
