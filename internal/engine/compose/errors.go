package compose

// Registry error codes for feat.compositionroot (FEAT-082). Range:
// G800-G899, declared in data/errors.json's ranges.reserved table (the E
// layer was exhausted by the eleven earlier engine modules; G800-G899 is
// the next free engine block after attract's G700-G799). Every code below
// is registered there with real severity/module/message/remedy fields
// (GR#7). A compose error is always raised via errs.New/errs.Wrap — never
// a bare fmt.Errorf (AC-16).
const (
	// ErrAlreadyComposed: Wire was called on a *core.Engine that already
	// has PhaseHooks registered. The composition root is the only real
	// hook registrar, so a non-zero hook count on entry means the engine
	// was already composed (or externally tampered with). A second Wire
	// call is rejected rather than silently appending duplicate hooks
	// (AC-3).
	ErrAlreadyComposed = "MET-G800"

	// ErrModuleFailed: a required module's construction or wiring failed
	// (e.g. market.LoadDefault could not load data/market.json). The
	// ctx["module"] field names the failed module. Wire returns this
	// without leaving a partially-wired engine (AC-4).
	ErrModuleFailed = "MET-G801"

	// ErrRequiredModuleMissing: a required module was silently absent from
	// the wired set (registering N-1 of N required modules). Distinct from
	// ErrModuleFailed: this is the "quiet success" guard, raised when a
	// module neither constructed nor errored but simply is not present in
	// the composition (AC-4).
	ErrRequiredModuleMissing = "MET-G802"

	// ErrWiringAfterSeal: Wire was invoked after the Engine sealed its
	// hook set (the first AdvanceTicks already ran). Wraps
	// core.ErrEngineSealed (and, via the invariant path,
	// invariant.ErrWiringAfterSeal) so the original cause stays reachable
	// through errors.Unwrap (AC-6).
	ErrWiringAfterSeal = "MET-G803"
)
