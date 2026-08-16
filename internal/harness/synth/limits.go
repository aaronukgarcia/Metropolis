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

// CumulativeRegressionThreshold is BUG-083's second, independent
// tolerance: how far the current measurement may drift from the
// ANCHOR (results.go's LoadLatestBaseline reconstructs this as the
// earliest-recorded, or most recently explicitly human-accepted,
// measurement for a preset — see PerfRecord.AcceptedRegression) before
// the gate fails, REGARDLESS of what the step-to-step RegressionThreshold
// check against the immediately-prior baseline says.
//
// # Why this exists (live-verified, not hypothetical)
//
// RegressionThreshold alone gates each commit against the STORED
// baseline, which — before this fix — was simply whatever the last run
// appended. Destructive-7 live-verified that a purely relative gate
// built this way is structurally blind to sustained drift: 30
// successive commits, each exactly 9% over the immediately prior
// stored baseline (under RegressionThreshold), never once tripped
// Regressed, while the stored figure compounded from 100ms to 1.327s
// (13.27x) with zero CI signal at any point. A relative gate anchored
// to a MOVING reference point cannot see cumulative drift, by
// construction — freezing the step-to-step baseline at the last
// non-regressed measurement (BUG-083's headline fix) does not change
// this on its own, because each 9% step individually IS a genuine,
// non-regressed pass against its own immediate predecessor. Catching
// the sum requires comparing against a reference point that does NOT
// move with every passing commit — the anchor.
//
// # Why this is not BUG-031's mistake
//
// BUG-031 hardcoded an absolute WALL-CLOCK DURATION (100ms) picked
// once with no relationship to the machine the test happened to run
// on — a correct, unregressed build blew past it (707ms) simply
// because a shared/busy runner is slower than whatever box the number
// was chosen against. This constant is not a duration: it is a
// PERCENTAGE, computed against a REAL measurement this package itself
// recorded on the same CI infrastructure (never a number invented in
// source), and — critically — it does not silently ratchet forward
// the way the pre-fix baseline did: the anchor only ever advances on a
// deliberate, visible human decision — a git-committed entry in
// accepted.go's AcceptedRegistry naming the exact accepted commit
// (BUG-095), PerfRecord.AcceptedReason persisted alongside it as an
// informational echo — never on an ordinary passing commit. It inherits
// RegressionThreshold's
// "adapts to the machine" property (same ratio math, same stored-
// history basis, CompareToBaseline's doc comment point 1) while adding
// the one property a moving-reference relative gate cannot have: a
// fixed point sustained drift cannot silently walk away from.
//
// # Why 2x RegressionThreshold (ASM, judgment call — not spec-mandated)
//
// M0-ENG §6 point 5 mandates RegressionThreshold (10%) verbatim; it
// says nothing about a cumulative figure, so this multiplier is a
// judgment call, logged as an assumption against BUG-083's BOW record
// rather than silently picked. 2x keeps it strictly looser than the
// step check (so an ordinary single passing commit, or two, never
// trips it — it would be redundant with, and stricter than,
// RegressionThreshold otherwise) while still being tight enough that a
// sustained 9%-per-commit drift pattern (BUG-083's exact live-verified
// attack) cannot survive more than 3-4 commits before the anchor
// comparison catches it, rather than the 30 it took to reach 13.27x
// with no second check at all. (Destructive-verified against this very
// code: 9% compounding sits at 18.81% after step 3 and 29.5% at step 4,
// so the 20% ceiling irreducibly trips on the 4th evaluated commit —
// an earlier draft of this comment claimed "2-3", which the attack run
// corrected.)
const CumulativeRegressionThreshold = 2 * RegressionThreshold

// MinMeasurableDuration is the perf gate's noise floor (see baseline.go's
// CompareToBaseline doc comment for the full BUG-031 rationale this
// constant exists to avoid repeating): a percentage regression computed
// against a near-zero absolute duration is dominated by GC/scheduler
// jitter, not real simulated work, so both the baseline and the current
// measurement must clear this floor before RegressionThreshold is
// applied at all.
//
// # Re-derivation (BUG-034, supersedes ASM-173's "chosen, not spec-
// # derived" framing — this is now evidence-based against BOTH local
// # and CI-runner measurements, not a guess)
//
// ASM-173 picked 5ms a priori, against no real measurement, "comfortably
// above typical Go scheduler/timer-resolution noise" — exactly the kind
// of number BUG-034's brief warns is indistinguishable from a guess
// until someone actually samples the thing being gated. That sampling
// has now happened, twice over:
//
//   - Local (dedicated Windows dev box), Preset1M (1,000,000 citizens,
//     zero registered PhaseHooks — see PhaseHookCountInHeadlessPath):
//     the original 2026-08-10 six-sample pass (12 simulated months) gave
//     PerMonthTick mean 0.524ms, stddev 0.201ms, min 0.293ms, max
//     0.884ms. A 2026-08-14 re-run to close this item's follow-up added
//     eight 3-month runs (non-zero PerMonthTick min 0.518ms, max
//     1.736ms) and six 12-month runs (min 0.101ms, max 0.752ms) — the
//     wider 3-month spread is the expected effect of dividing the same
//     absolute jitter over fewer months, not a different workload.
//   - CI-runner (windows-latest), the environment the real gate runs on:
//     three measured runs of the actual perf-1m-probe job recorded
//     PerMonthTick 488.866us (the stored baseline, run 31539765424,
//     commit 303d3ac), 925.866us (run 31577307387, commit 5bfc381), and
//     a third in between — i.e. ~488-926us across the three, at 6.7-6.8s
//     wall / ~43-45MB peak each.
//
// Conclusion of the re-derivation: the caveat that originally kept 5ms
// — that CI tenancy jitter might run materially higher than a dedicated
// dev box — did NOT materialise. CI-runner PerMonthTick (488-926us) sits
// squarely INSIDE the local range (0.1-1.7ms), not above it, so 5ms
// clears the highest observed figure (1.736ms) by ~2.9x and the CI
// maximum (0.926ms) by ~5.4x. One further, honest data point: several
// runs (3 of 8 local 3-month, 1 of 6 local 12-month) measured TickTime
// == 0 while TotalTicks > 0 — at walking-skeleton scale the Windows
// monotonic timer occasionally cannot even resolve the whole run, the
// most literal possible demonstration that sub-millisecond PerMonthTick
// values are noise and that this floor must stay generously above them.
//
// FEAT-082 landed the composition root, so PhaseHookCountInHeadlessPath()
// is now compose.BaselineOneHookCount() (> 0) and PerMonthTick measures
// real per-tick simulation work, not walking-skeleton dispatch — the exact
// re-derivation trigger this comment named above. Measured on the CI
// runner (windows-latest): 1M-real baseline 3.1981ms, current ~3.38-3.47ms;
// perf-smoke (2000 citizens) ~3.70-4.00ms — the same ~3-4ms regardless of
// population, confirming tick cost is now hook-work-dominated. The floor is
// therefore lowered to 2ms: ~1.6x below the lowest real measurement
// (3.1981ms) so the gate can actually evaluate, ~2.2x above the historical
// CI jitter max (0.926ms), and just above the historical local
// walking-skeleton max (1.736ms). Re-derive again if a future change drops
// real tick cost back below this floor.
const MinMeasurableDuration = 2 * time.Millisecond

// MaxPlausiblePerMonthTick is BUG-096's upper sanity ceiling on
// PerfResult.PerMonthTick (see perf.go's ImplausibleReason doc comment
// for the live-verified failure it closes: a gigantic-but-positive first
// record for a preset silently seeds a permanently-wrong baseline/anchor,
// after which a genuine, severe regression reads as a large IMPROVEMENT).
//
// # Basis (ASM, judgment call — not spec-derived)
//
// Like CumulativeRegressionThreshold's 2x multiplier, M0-ENG names no
// upper bound for this figure, so this is logged as an assumption rather
// than silently picked. 2 seconds is chosen to be DELIBERATELY generous
// relative to any plausible real measurement — every real measurement
// this package has ever produced or is documented to expect is in the
// low-hundreds-of-milliseconds range at most (MinMeasurableDuration's doc
// comment: observed jitter under 1ms; BUG-096's own live-verified
// regression scenario: healthy ~20ms jumping to a severe 500ms) — while
// still sitting comfortably BELOW BUG-096's own live-verified attack
// figure (a hand-planted 10s PerMonthTick, chosen as a round,
// unremarkable-looking number an attacker or a unit-mismatch bug would
// plausibly produce). A ceiling that let 10s through would not close the
// hole it exists to close; 2s is roughly 4x the worst genuine figure this
// package has ever measured and 5x below the live-verified attack value,
// leaving real headroom on both sides. Not tied to MinMeasurableDuration
// by a fixed ratio (unlike CumulativeRegressionThreshold's relationship
// to RegressionThreshold) because the two floors answer different
// questions — one guards against noise dominating a percentage, this one
// guards against a value no real run could produce becoming trusted
// ground truth at all.
//
// Breaks if: engine.core ever legitimately needs multiple real seconds of
// PerMonthTick at full 1M/10M scale (e.g. a future phase hook doing
// genuinely heavy per-tick work) — re-derive from a real measurement at
// that point rather than assuming this ceiling still has headroom,
// exactly as MinMeasurableDuration's own doc comment already asks for
// itself.
const MaxPlausiblePerMonthTick = 2 * time.Second
