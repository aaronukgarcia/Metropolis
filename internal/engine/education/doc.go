// Package education implements the §27 Educational Lifecycle (cradle → grave)
// for the Metropolis engine — the "longest fuse in the game". It is
// code.json's engine.education module (GUID
// bae4923b-5043-45e1-82b7-7772afd79b0f), "stage pipeline gated by SeasonAPI
// Sept intake".
//
// # The pipeline
//
// The seven documented §27 stages run as an ordered pipeline: nursery →
// primary → secondary → [sixth form | technical college | leave-at-16] →
// university → adult education → U3A/library. The secondary-exit fork is a
// genuine three-way branch (StageSixthForm, StageTechnicalCollege,
// StageLeaveAt16 — distinct values, not a boolean), distributed by the
// ApplyFork command (AC-13). Each stage's capacity and funding→quality is
// registered against engine.services' generic Service framework (AC-2); the
// September intake gate is engine.season's IsSchoolIntakeMonth (AC-4); age
// eligibility is engine.citizens' derived birthMonth-based Age() (AC-3);
// school-run trips feed engine.traffic's trip-generation surface (AC-5).
//
// # The early-checkable design (why correctness is not a 20-year wait)
//
// §27's own text says a quality shortfall "surfaces 10–20 game-years later".
// This package deliberately does NOT defer any of its effects to that
// horizon — every mechanism whose *effect* is decades away has *intermediate
// state* that is not decades away, and that intermediate state is what the
// package's tests check:
//
//  1. Attainment is written at the moment of each stage transition (AC-6),
//     from the stage's *realised* funding-quality read through
//     engine.services at that transition — never computed lazily on a later
//     read using whatever funding happens to be current then. Two funding
//     levels therefore produce two different attainment scores the month the
//     transition fires, inspectable without a multi-decade run.
//  2. Personality drift is applied incrementally, per stage, in the
//     documented direction (good schooling widens ambition/novelty-seeking;
//     poor schooling narrows them — §5.1), the month a stage's
//     funding-quality is known (AC-7). Only the compounding of many such
//     increments is what actually takes decades; the sign and shape of each
//     increment is checkable from one stage's drift alone.
//  3. The pupil cohort is a conserved stock with an accounting identity
//     (below) checkable from the very first September gate (AC-10).
//  4. The Slow-Fuse projection obligation (A5) is discharged at the moment
//     of the funding decision, not when its consequence lands (AC-9).
//
// # The cohort-accounting identity (AC-10)
//
// For every stage and every accounting month the following identity holds
// exactly:
//
//	EnrolledThisMonth == EnrolledLastMonth
//	    + Intake(new entrants this September gate, or transfers from a feeder stage)
//	    − Promoted(to the next stage)
//	    − ForkedOut(secondary's three-way exit, counted once, not per branch)
//	    − DroppedOut(documented attrition)
//	    − Deceased(via engine.citizens' mortality)
//	    − EmigratedOrRelocated(via engine.citizens)
//
// Every right-hand term is sourced independently from its own event kind in
// the cohort ledger — none is computed as the identity's remainder, so a
// reconciliation failure is a real tracking bug, not a tautology.
//
// # Outbound contracts (GR#20)
//
// This package consumes engine.citizens (age, the command-based
// education-drift write path, life-event departures), engine.services
// (stage registration, funding→quality), engine.season (September gate),
// engine.traffic (school-run trips — consumed as a local contract shape
// because engine.traffic is not yet built), and engine.projections
// (Slow-Fuse gate + curve-provider registry) — through their registered
// contracts alone.
//
// # Remaining unregistered edge (AC-11)
//
// The pupil cohort-accounting identity is NOT yet registered with
// engine.invariant: code.json's engine.education outbound calls list
// engine.citizens/services/season/traffic/projections only, with no
// engine.invariant edge. Until that edge lands, the AC-10 local test suite
// is the interim proof of correctness, and whether the pupil cohort needs
// its own stock registration versus subsumption under engine.citizens'
// people-stock is an open Aaron ruling (see the acceptance file's
// Escalations). The engine.projections edge (AC-9) IS registered (c36778b).
package education
