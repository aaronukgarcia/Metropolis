package synth

import (
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/solver"
)

// MinSyntheticCitizens is the smallest legal Params.CitizenCount Generate
// accepts (AC-1b(b)). A zero- or negative-citizen "synthetic city" is
// degenerate, not a meaningful test input.
const MinSyntheticCitizens int64 = 1

// MaxSyntheticCitizens is the hard ceiling Generate enforces on
// Params.CitizenCount BEFORE any allocation begins (AC-1b — weakness
// pattern #4: citizenCount sizes real allocation and generation work,
// the SEC-009 shape named explicitly in this item's acceptance criteria
// header).
//
// Basis (ASM-083, logged against this item's BOW record, matching the
// acceptance doc's own escalation "For Bill" note): the spec (M0-ENG
// §2.4/§1) names only the two TEST presets this package must support (1M,
// 10M) — it never states a hard ceiling for the generator itself. Rather
// than invent a new number, this constant IS
// solver.LocalCitizenCeilingHigh (internal/foundation/solver, module key
// int.solver — A9: "local CPU covers up to 20-30M citizens end-to-end").
// A9 already names, for a related but distinct reason, the exact point
// past which local CPU is not the system's intended path; a synthetic-
// city generator asked to fabricate a population beyond that line has no
// stronger claim to legitimacy than the real citizen store would at the
// same size. Referencing the constant directly (not copying its value)
// means there is nothing here that can silently drift out of sync with
// int.solver's own figure (GR#3) — no drift test is needed because there
// is no duplication to drift.
const MaxSyntheticCitizens = solver.LocalCitizenCeilingHigh

// MinSprawl and MaxSprawl bound Params.Sprawl's domain (AC-7b): 0.0 is a
// maximally dense/compact synthetic city, 1.0 a maximally sprawling one —
// the two ends of the normalised [0,1] range generator.go's placement
// math scales cell layout by.
const (
	MinSprawl = 0.0
	MaxSprawl = 1.0
)

// RegressionThreshold is the fraction of monthly-tick-time growth that
// fails the perf gate (AC-6, AC-10). This is a spec-mandated figure, not
// a tuned one: M0-ENG §6 point 5 states "a commit that regresses
// monthly-tick time >10% at the 1M-citizen synthetic fails" in exactly
// these words.
const RegressionThreshold = 0.10

// MinMeasurableDuration is the perf gate's noise floor (see baseline.go's
// CompareToBaseline doc comment for the full BUG-031 rationale this
// constant exists to avoid repeating): a percentage regression computed
// against a near-zero absolute duration is dominated by GC/scheduler
// jitter, not real simulated work, so both the baseline and the current
// measurement must clear this floor before RegressionThreshold is
// applied at all.
//
// # Re-derivation (BUG-034, supersedes ASM-173's "chosen, not spec-
// # derived" framing — this is now evidence-based, though the evidence
// # is still local, not CI-runner, pending BUG-034's CI probe job)
//
// ASM-173 picked 5ms a priori, against no real measurement, "comfortably
// above typical Go scheduler/timer-resolution noise" — exactly the kind
// of number BUG-034's brief warns is indistinguishable from a guess
// until someone actually samples the thing being gated. This dispatch
// did that sampling: six real Preset1M (1,000,000-citizen, zero
// registered PhaseHooks — see PhaseHookCountInHeadlessPath)
// headless.Run measurements, 12 simulated months each, run locally on
// 2026-08-10 via cmd/perfci, gave PerMonthTick jitter of:
//
//	mean 0.524ms, stddev 0.201ms, min 0.293ms, max 0.884ms
//
// 5ms clears the observed maximum by ~5.7x and the mean by ~9.5x — a
// real, sampled safety margin, not an assumed one. It is being LEFT
// UNCHANGED at 5ms rather than tightened toward the observed jitter,
// for two reasons logged here rather than silently decided: (1) this
// sample is from a dedicated Windows dev box, not the shared/virtualized
// windows-latest Actions runner the gate actually runs on, and CI
// tenancy jitter is well documented to run higher than a dedicated
// machine's — tightening the floor toward locally-observed noise would
// risk exactly the false-positive BUG-031 shape this constant exists to
// prevent, the first time CI is simply busier than this box was; (2) a
// tighter floor buys no real gate coverage yet, because engine.core is
// still a zero-phase-hook walking skeleton (PhaseHookCount is 0 on
// every RunPerf/RunGate call today) — there is no real per-tick
// simulation work for a tighter floor to protect until Sprint 3 lands
// real PhaseHooks and PerMonthTick stops being dominated by dispatch
// overhead. 5ms is deliberately kept as a generous, evidence-CONFIRMED
// (not evidence-CONTRADICTED) floor until that happens.
//
// Breaks if: the eventual CI-runner probe job (BUG-034's
// perf-1m-probe workflow_dispatch job, .github/workflows/ci.yml) shows
// materially higher runner jitter than this local sample — this
// constant must be re-checked against THAT data, not just this dev
// box's, before Sprint 3's real gate goes live; logged as a follow-up
// against BUG-034 rather than assumed resolved here.
const MinMeasurableDuration = 5 * time.Millisecond
