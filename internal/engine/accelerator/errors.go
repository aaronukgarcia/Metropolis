package accelerator

// Registry error codes for engine.accelerator (MOD-077). Range: G2400-G2499,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
//
// The E layer (E000-E999) is fully claimed by eleven earlier engine modules,
// and the G layer's blocks through G2300-G2399 were claimed by engine.news
// by the time this package landed, so engine.accelerator opens G2400-G2499
// under BUG-234's three-to-four-digit code-format widening. Checked against
// data/errors.json's "ranges.reserved" table AND `grep -rn "MET-G24"
// internal/ cmd/` before claiming, per BUG-008's lesson — no prior MET-G24xx
// code existed either place. Every code below IS registered in
// data/errors.json with real severity/module/message/remedy fields (GR#7);
// the internal/foundation/errs source-scan test guards against drift.
const (
	// ErrDataInvalid: data/accelerator.json could not be loaded or failed
	// foundation.data's schema validation (missing file, malformed JSON,
	// non-positive peak multiplier, research multiplier not above 1, negative
	// health/FDI/prestige/threshold figure). Wraps the underlying
	// foundation.data error (already registry-sourced under an F6xx code).
	ErrDataInvalid = "MET-G2400"

	// ErrExpertGateUnmet: the shared expert gate (FEAT-055) returned a
	// rejected verdict for the current research output — the numeric
	// threshold in data/accelerator.json is not met. Money, milestones, and
	// development points cannot substitute (the "money alone cannot buy it"
	// mechanic, AC-3). No facility state is created on rejection (AC-13).
	ErrExpertGateUnmet = "MET-G2401"

	// ErrUnknownAccelerator: a build/operate command named an accelerator
	// key outside the accelerator taxonomy (anything other than the
	// hadron_research_ring catalogue anchor). Rejected loudly, never
	// silently accepted (AC-13).
	ErrUnknownAccelerator = "MET-G2402"

	// ErrNoPermit: the inherited §7 facility permit (feat.facilitypermits)
	// reported that no valid permit is held for the facility. The build path
	// delegates the permit check; it never owns permit state (AC-11).
	ErrNoPermit = "MET-G2403"

	// ErrDrawUnbuilt: a draw (or operate) was requested for an accelerator
	// that has not been built. A draw for an unbuilt facility is rejected,
	// never silently posted as zero demand (AC-13).
	ErrDrawUnbuilt = "MET-G2404"

	// ErrDependencyMissing: a required dependency (research source, gate,
	// consumption UtilityAPI, wellbeing/FDI/permit/decommission seam) was not
	// wired before the operation ran (GR#20: dependencies enter via Set*
	// wiring, never constructed implicitly).
	ErrDependencyMissing = "MET-G2405"

	// ErrCopiedValue: a method was called on a struct-copied *AcceleratorAPI
	// (SEC-020 family). A copied value aliases the original's mutex while
	// holding an independent lock, so it is rejected before the lock is
	// touched.
	ErrCopiedValue = "MET-G2406"

	// ErrAlreadyBuilt: a build command targeted an accelerator that is
	// already built. The accelerator is a one-each §MP mega-project; a second
	// build is rejected rather than silently doubling its draw/spillover.
	ErrAlreadyBuilt = "MET-G2407"
)
