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
// applied at all. 5ms is chosen as comfortably above typical Go
// scheduler/timer-resolution noise on Windows (documented default timer
// tick ~15.6ms historically, ~1ms on modern builds — 5ms sits inside
// that band without being so large it would mask a real 10% regression
// at the scale this gate is meant to catch) while remaining far below
// the multi-second monthly-tick times a real (non-skeleton) engine.core
// is expected to reach once citizens/finance/etc. modules land.
const MinMeasurableDuration = 5 * time.Millisecond
