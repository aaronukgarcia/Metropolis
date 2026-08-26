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

	// CodeBuildingDanglingConsumptionRef: a buildings.json entry's
	// consumptionRef does not resolve against consumption.json's
	// Classes map (FEAT-010/data.catalogue AC-12) — a cross-file check
	// that can't be expressed as a plain schema violation of
	// buildings.json alone, so it gets its own code distinct from
	// CodeSchemaInvalid.
	CodeBuildingDanglingConsumptionRef = "MET-F607"

	// CodeStoreCopied: Get, OnChange, or Reload was called on a
	// struct-copied Store[T, PT] value (`s2 := *s1`), not the *Store
	// NewStore constructed. BUG-125/SEC-020: mirrors engine.core's
	// ErrEngineCopied exactly — a copy's reloadMu/cbMu are independently
	// zeroed but its cbs slice can still alias the original's backing
	// array, so this is rejected before either lock (or the shared cbs
	// append) is ever reached.
	CodeStoreCopied = "MET-F608"

	// CodeDuplicateKey: a config file's raw JSON contains the same key
	// twice within one object (e.g. two "curves.gasSeasonal" entries in
	// seasonal.json) -- BUG-060. Standard encoding/json unmarshaling into
	// a map/struct silently keeps the last occurrence and discards the
	// first with no signal, which could mask a real data-authoring
	// mistake, so Load walks the raw token stream ahead of Unmarshal to
	// catch it explicitly.
	CodeDuplicateKey = "MET-F609"

	// CodeUnknownField: a config file contains a JSON field that is not
	// a recognized member of the target schema (e.g. a typo'd field name
	// like "recylingRate" instead of "recyclingRate"). Bare json.Unmarshal
	// silently discards unknown fields and decodes to the Go zero value with
	// no error signal (BUG-281), which is a GR#17 silent-failure hazard for
	// a package whose purpose is "no coefficient silently wrong". Load now
	// enables json.Decoder.DisallowUnknownFields() to catch typos before they
	// propagate as silent configuration errors.
	CodeUnknownField = "MET-F610"
)
