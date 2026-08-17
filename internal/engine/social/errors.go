package social

// Registry error codes for engine.social (MOD-053). Range: G3600-G3699,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
//
// The E layer (E000-E999) was fully exhausted by earlier engine modules and
// the G layer's blocks through G3500-G3599 were all claimed before this
// module landed (engine.comms G3300-G3399, engine.capexport G3400-G3499,
// feat.checkpoint G3500-G3599 are the most recent claimants), so engine.social
// opens G3600-G3699 under BUG-234's 2026-08-14 code-format widening. Checked
// against data/errors.json's "ranges.reserved" table AND `grep -rn "MET-G36"
// internal/ cmd/` before claiming, per BUG-008's lesson that the table alone
// is not always current — no prior MET-G36xx code existed either place. Every
// code below IS registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the internal/foundation/errs
// source-scan test guards against this ever drifting out of sync.
const (
	// ErrSocialDataInvalid: data/social.json could not be loaded or failed
	// this package's schema validation (missing file, malformed JSON, a
	// non-positive caseload rate, a negative capacity/carers figure, an
	// out-of-domain intervention-harm threshold). Load-time.
	ErrSocialDataInvalid = "MET-G3600"

	// ErrUnknownCategory: a funding command, caseload query, or escalation
	// named a Category value outside the five registered categories. AC-13 —
	// never a silently-created zero-value case for a category that does not
	// exist.
	ErrUnknownCategory = "MET-G3601"

	// ErrUnknownCase: a case query, resolution, escalation, or lost-to-
	// follow-up named a CaseID that is not in the case ledger. AC-13 — never
	// a silently-created zero-value case record for an unknown id.
	ErrUnknownCase = "MET-G3602"

	// ErrInvalidFunding: SetFunding was called with a level outside [0,1].
	// Rejected rather than silently clamped.
	ErrInvalidFunding = "MET-G3603"

	// ErrInvalidEscalation: EscalateCase named a destination category that
	// does not exist. AC-14 — rejected at the write boundary so the case-
	// accounting identity can never drift on a phantom reopen.
	ErrInvalidEscalation = "MET-G3604"

	// ErrDoubleClose: ResolveCase/EscalateCase/LoseToFollowUp was called for
	// a case that is not open (already closed). AC-14 — rejected so a case
	// cannot silently close twice and unbalance the identity.
	ErrDoubleClose = "MET-G3605"

	// ErrSlowFusePayloadMissing: a funding-CUT command (AC-10) whose
	// principal effect lands more than five game-years out was submitted
	// without an attached projected-consequence payload. This module's own
	// pre-submission check rejects it before it ever reaches
	// engine.projections' Slow-Fuse gate.
	ErrSlowFusePayloadMissing = "MET-G3606"

	// ErrInvalidFuseYears: a funding-cut command carried a non-finite
	// (NaN/±Inf) or non-positive FuseYears tag. A degenerate tag must never
	// reach the Slow-Fuse threshold comparison, mirroring engine.projections'
	// own finite-tag guard (AC-10).
	ErrInvalidFuseYears = "MET-G3607"

	// ErrCopiedValue: a SocialAPI method was called on a struct-copied value
	// (SEC-020 family). A copied *SocialAPI would alias its mutex/state
	// across two values, so the copy guard rejects the call.
	ErrCopiedValue = "MET-G3608"

	// ErrDependencyMissing: an operation that needs a wired dependency
	// (citizens, services, projections, family-stress source) was invoked
	// before that dependency was wired. Fails loudly rather than no-op-ing.
	ErrDependencyMissing = "MET-G3609"

	// ErrInvalidSeries: a ProjectedConsequence.Series carried a non-finite
	// (NaN/±Inf) value, which would flow into the projected delta and then
	// into engine.projections' queued step, poisoning the curve. The series
	// is finite-checked at the write boundary before the projection is
	// enqueued.
	ErrInvalidSeries = "MET-G3610"

	// ErrBackDatedMonth: a closure or escalation named a month earlier than
	// the case's OpenedMonth. Closing a case in a month before it opened
	// would record Resolved=1 (Escalated/LostToFollowUp=1) at a month where
	// Opened=0, driving the AC-11 conservation identity
	// (Open = OpenPrev + Opened − Resolved − Escalated − Lost) negative, so
	// the out-of-order month is rejected at the write boundary rather than
	// silently corrupting the conserved stock.
	ErrBackDatedMonth = "MET-G3612"

	// ErrInvalidDriverInput: a steady-state caseload driver value is outside
	// its documented domain — the fraction drivers (Deprivation,
	// NightlifeDensity) are in [0,1], the magnitude drivers (CrowdingStress,
	// FinancialStress) are ≥ 0, and UnemploymentMonths is ≥ 0. An
	// out-of-domain (or non-finite) value is rejected at the boundary rather
	// than silently producing an unbounded case-proposal count (which a large
	// finite value would — Deprivation=1e5 yields ~900k proposals and 1e15
	// would exhaust memory). Enforced in GenerateCaseload/AdvanceMonth.
	ErrInvalidDriverInput = "MET-G3613"

	// ErrCaseloadExceedsLimit: a steady-state caseload generation, an
	// AdvanceMonth open, or a crisis injection proposed a per-month (or
	// per-event) case count above the resource ceiling
	// maxCaseloadProposalsPerMonth. Rejected at the allocation site rather
	// than allocating an unbounded slice/ledger (SEC-195) — the magnitude
	// drivers (CrowdingStress/FinancialStress) and the config rates are all
	// individually finite/non-negative yet can still multiply into a
	// pathological proposal count, so the bound is on the COUNT (a resource
	// concern), never on the driver magnitude (a balance concern).
	ErrCaseloadExceedsLimit = "MET-G3614"

	// ErrProjectionSeriesTooLong: a funding-cut command's projected-
	// consequence Series carried more than maxProjectionSeriesPoints points.
	// Rejected at the write boundary before any O(n) scan or allocation runs
	// over it (SEC-202), mirroring maxCaseloadProposalsPerMonth (SEC-195) —
	// the bound is on the LENGTH (a resource concern), never on the series
	// value magnitude (which is only required finite, per ErrInvalidSeries).
	ErrProjectionSeriesTooLong = "MET-G3615"
)
