package worklife

// This file defines worklife's local view of each registered outbound
// module's inbound contract (code.json engine.worklife.outbound.calls) —
// the GR#20 "consume via registered interfaces" shape. Each seam is
// satisfied by the real module (or a thin adapter) at the composition
// root, and by a fake in this package's tests. Both engine.policies and
// engine.wellbeing are not yet wired against worklife at this module's
// first pass, so both are consumed purely through these seams.

// PoliciesAPI is the seam over engine.policies' inbound PoliciesAPI
// (GUID 09d0d31a-a860-4a7d-aaf3-868ec16894e2), narrowed to the working-week
// policy effect (AC-8). worklife consumes the active effect; it never
// defines the policy library, the 996 toggle's cost, or its scope (those
// are MOD-064's, GR#3).
type PoliciesAPI interface {
	// ActiveWorkingWeek returns the active working-week policy's effect and
	// whether one is enacted. ok=false means "default week" (no working-week
	// policy active), so the caller falls back to the data-driven default
	// hours. A non-nil error is propagated unchanged.
	ActiveWorkingWeek(correlationID string) (WorkingWeekEffect, bool, error)
}

// WellbeingAPI is the seam over engine.wellbeing's inbound WellbeingAPI
// (GUID da2c5c2a-495b-43b5-b496-2b641a5ec16a), narrowed to the
// overwork/work-life balance input (AC-12, ASM-956: mapped onto §42's
// discretionary-hours/leisure-fit term, not §18's commute-time driver).
// worklife computes the input; wellbeing owns the happiness arithmetic.
type WellbeingAPI interface {
	// PushWorkLifeBalance delivers one worker's overwork-adjusted work-life
	// balance input (a §42 discretionary-hours-derived value) to the
	// wellbeing module. Higher work hours yield a strictly lower value.
	PushWorkLifeBalance(workerID uint64, balance float64, correlationID string) error
}
