// Package social implements §40 Social Services — "the safety net beneath
// everything", the floor of the Detroit spiral. It is code.json's
// engine.social module (GUID b31dcdb1-b4f7-4d12-abec-9279e74412db),
// "caseload generation decomposed; Slow-Fuse projections on cuts".
//
// # The five decomposed provision categories (§40)
//
// Caseload is tracked separately for the five documented provisions, each
// responding to its own driver subset — never one blended "social need"
// score with five labels (AC-2):
//
//   - Family support & child protection ← deprivation, crowding, financial
//     stress, and discrete crisis events.
//   - Homelessness ← deprivation, unemployment duration, financial stress.
//   - Disability & carers ← deprivation only (not unemployment duration —
//     the AC-2 isolation anchor).
//   - Fostering/adoption ← crowding, financial stress, plus escalations from
//     family support.
//   - Addiction services ← the nightlife/deprivation coupling.
//
// The family-stress drivers (crowding and financial stress) are consumed from
// engine.wellbeing's registered Crowding and FinancialStress drivers via the
// FamilyStressSource seam (AC-3) — this package never re-derives crowding or
// the 35% rent-burden threshold itself.
//
// # The child-protection cohort-audit marker (§40, AC-6)
//
// §40's "underfunding shows up 10 years later as attainment down and crime
// up in the affected cohort — the citizen records make this literal and
// auditable" is discharged at the moment of decision, not deferred:
// RecordChildProtectionIntervention writes a HealthBand marker to the
// affected citizen's record through engine.citizens' command path
// (LifeEventHealth, per engine.citizens.md AC-1b) at the same simulated
// month the decision is made — under underfunding the marker is
// HealthCritical, inspectable without a decade-long run.
//
// # The case-accounting identity (AC-11)
//
// For every category and every accounting month the following identity holds
// exactly:
//
//	OpenCases == OpenCasesLastMonth
//	    + NewCasesOpened
//	    − CasesResolved
//	    − CasesEscalated
//	    − CasesLostToFollowUp
//
// Every term is independently sourced from the case ledger: opened cases from
// the steady-state and crisis generators, resolved/escalated/lost from the
// case-closure log. An escalation is never a silent close — it closes the
// source case and reopens a linked, traceable case in the destination
// category in the SAME month, so the destination's NewCasesOpened count pairs
// with the source's CasesEscalated count. Lost-to-follow-up is a documented
// flagged fallback (relocated/untraceable), never a "resolved" close of an
// abandoned case.
//
// # Outbound contracts (GR#20)
//
// This package consumes engine.services (category registration and
// funding→quality, AC-4), engine.citizens (the command-based marker write,
// AC-6), engine.wellbeing (the family-stress drivers, AC-3), and
// engine.projections (the Slow-Fuse gate and curve-provider registry, AC-10)
// — through their registered contracts alone.
//
// # Remaining unregistered edge (AC-12)
//
// The caseload-accounting identity (AC-11) is NOT yet registered with
// engine.invariant: code.json's engine.social outbound calls list
// engine.services/citizens/wellbeing/projections only, with no
// engine.social → engine.invariant edge. Until that edge lands, the AC-11
// local test suite is the interim proof of correctness, and whether the
// caseload stock needs its own registration versus subsumption under
// engine.citizens' people-stock is an open Aaron ruling (see the acceptance
// file's Escalations). The engine.projections edge (AC-10) IS registered
// (c36778b).
package social
