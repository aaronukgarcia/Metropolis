package balance

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// Registry error codes for balance.harness (MOD-036, FEAT-192).
//
// GR#7: this module has no MET range of its own registered yet — it is a
// tools/ layer module, not an engine module, and its range claim is the
// unresolved open item OD-2 in docs/planning/icd/balance.harness.md §8/§12.
// The codes below claim the T (tooling) layer's T000-T099 sub-range — the
// first Go tooling range; data/errors.json's T layer is currently described
// as JS-only, so T000-T099 is free. They MUST be registered in
// data/errors.json (severity/module/message/remedy, plus a "T000-T099"
// ranges.reserved entry naming balance.harness) before this module's first
// commit lands. Until then errs.New/errs.Wrap degrade each of these codes to
// the generic MET-F003 "unregistered code" fallback with the requested code
// preserved in Ctx["code"] — loud and visible, never a silent ad-hoc string
// error — and internal/foundation/errs/source_scan_test.go does not cover
// tools/, so these literals are not yet subject to its
// registered-and-in-range gate. Every error below is raised via
// errs.New/errs.Wrap, the only legal constructors (GR#7).
const (
	// codeScenarioReadFailed: the -scenario file could not be read, or was
	// not well-formed JSON (AC-7 / ICD §3 Inputs contract). Rejected at load
	// time, before any grid expansion or run.
	codeScenarioReadFailed = "MET-T001"

	// codeScenarioInvalid: the scenario parsed as JSON but failed schema
	// validation — a missing required parameter dimension, an empty parameter
	// range or seed set, a growth-curve coefficient set with the wrong shape,
	// or an invalid metric/target. Rejected at load time, never silently
	// defaulted to an empty or partial grid that runs anyway (AC-7).
	codeScenarioInvalid = "MET-T002"

	// codeCellOutOfDomain: one cell's parameter value fell outside its
	// documented positive domain — a non-positive secondsPerMonthAt1x, months
	// <= 0, a milestone spacing that would place two milestones at the same
	// population tier, an out-of-range citizen count/sprawl, or an unknown
	// network shape (AC-8). The cell is classified rejected with the
	// requested value preserved in the record, never clamped.
	codeCellOutOfDomain = "MET-T003"

	// codeResultsWriteFailed: the sweep results file could not be created or
	// written (ICD §4 Outputs). A sweep never silently reports success with
	// no results written.
	codeResultsWriteFailed = "MET-T004"
)

// newErr constructs a registry-sourced error with a fresh correlation id
// (GR#1). It is the single construction point for this package's own codes,
// so a future data/errors.json range registration changes nothing in Go.
func newErr(code string, ctx map[string]any) *errs.E {
	return errs.New(code, errs.NewCorrelationID(), ctx)
}

// wrapErr wraps a lower-level cause under one of this package's codes.
func wrapErr(code string, cause error, ctx map[string]any) *errs.E {
	return errs.Wrap(code, errs.NewCorrelationID(), cause, ctx)
}
