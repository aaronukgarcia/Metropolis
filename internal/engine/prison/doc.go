// Package prison is the prison, rehabilitation & re-entry module (MOD-056):
// the §43 prison estate and its "long ledger", exposed as a stateful
// [PrisonAPI] whose balance numbers are loaded from data/prison.json.
//
// Module key: engine.prison (see code.json; GUID 28fa9c10-34fd-48b2-9d31-1ca461641380)
// Spec ref:   §43 (Prison, Rehabilitation & Re-entry); §36 (sold places)
//
// # The reoffending formula (AC-5)
//
// ReoffendingRate = Base(offence, age) − RegimeEffect − ReentrySupport, with
// each term independently sourced: Base from data/prison.json's offence/age
// table, RegimeEffect from the three [PrisonAPI] regime accessors, and
// ReentrySupport from its three named sub-inputs (probation capacity,
// ex-offender employment uptake, housing-on-release). No term is inferred
// from an externally observed target rate.
//
// # Category mismatch RAISES reoffending (AC-3 — read this before editing)
//
// Placing a minor/low-risk offender in a HIGHER-security category than their
// profile matches produces a STRICTLY HIGHER reoffending-rate contribution
// than placing the identical offender in their matched category, all else
// held equal. This is the spec's counterintuitive claim: "stricter" is NOT
// "safer" — holding minor offenders in high-security raises their
// reoffending. A future reader (or an over-eager simplifier) will be tempted
// to treat high-security as "more secure, no side-effect", which silently
// inverts the rule. The penalty is applied as a positive additive term in
// [PrisonAPI.ReoffendingRate], and the direction is asserted by
// TestCategoryMismatchRaisesReoffending.
//
// # The independent intake ledger (AC-2)
//
// This package does NOT trust engine.crime's sentenced-to-prison count as
// proof of arrival. Every admission is independently re-recorded as its own
// ledger entry ([PrisonAPI.Admit]), keyed by citizen ID, district, and month,
// and [PrisonAPI.IntakeCount] exposes the per-(district, month) count.
// [PrisonAPI.IntakeCount] implements engine.crime's PrisonIntake seam, so
// CrimeAPI.VerifyPrisonIntake cross-checks its own figure against this
// independent ledger — a discrepancy between the two modules' own counts is
// detectable, never tautologically equal.
//
// # Overcrowding degrades ALL three regime effects at once (AC-7)
//
// When population exceeds capacity — INCLUDING §36 sold places, which count
// identically to domestic population in the denominator (AC-8) — every one
// of the three regime-effect accessors (education, work, addiction treatment)
// degrades in the same tick, proportionally to severity. It is never the case
// that one line absorbs the whole penalty while the other two coast.
//
// # The Slow-Fuse block (AC-9 — BUG-058)
//
// §43's "the F7 projection makes the case" names engine.projections' Slow-Fuse
// gate, but code.json still registers no engine.prison → engine.projections
// outbound edge (BUG-058). Until that edge lands, rehab-spend funding
// increases ([PrisonAPI.RehabSpend]) carry a FuseYears tag (a value in the
// documented 5–15 range) plus a locally-computed projected-consequence value,
// and this package's own pre-submission check rejects a command missing
// either. The real cross-module submission is deferred; do NOT hand-write an
// engine.projections call before the edge is registered (GR#25).
//
// # Determinism (GR#21)
//
// Every stochastic draw (the reoffend-or-not outcome) uses a counter-based
// hash stream det.NewStream(worldSeed, citizenID, month, purpose) — no
// shared/global RNG source and no wall-clock read anywhere in this package's
// non-test files. No exported method ranges over a Go map in a way that
// affects an observable result: the only map iteration is order-independent
// counting.
//
// # Dependencies (GR#20, contract-first)
//
// The only concrete dependency is engine.crime (the registered
// engine.prison → engine.crime edge), consumed solely as the type of the
// intake ledger's DistrictID and to satisfy the PrisonIntake seam. The
// CitizensAPI existence check (AC-10) is injected as a func(uint64) bool
// predicate via [PrisonAPI.SetCitizenExists] rather than a concrete
// engine.citizens import, because no engine.prison → engine.citizens edge is
// registered (GR#25). The §36 sold-places count is tracked locally via
// [PrisonAPI.SetSoldPlaces] and sourced from engine.capexport's "prison-places"
// Committed line by the composition root.
package prison
