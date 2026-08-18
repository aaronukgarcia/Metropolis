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

// RegressionThreshold is the fraction of growth that fails the perf
// gate (AC-6, AC-10). This is a spec-mandated figure, not a tuned one:
// M0-ENG §6 point 5 states "a commit that regresses monthly-tick time
// >10% at the 1M-citizen synthetic fails" in exactly these words.
//
// # BUG-272: same figure, now applied to the PRIMARY (allocation) signal
//
// M0-ENG §6 point 5 names "monthly-tick time" because, when it was
// written, wall-clock time was the only signal this gate had. BUG-272
// found that wall-clock time at the engine's current (post-BUG-269)
// speed is no longer a signal a shared-CI-hardware gate can reliably
// judge at this threshold -- see MinMeasurableAllocs' doc comment for
// the live-verified 40-45% CI jitter this closes. Rather than raise
// this threshold (a gate-weakening move this project explicitly bans)
// or lower the gate's confidence, CompareToBaseline now applies this
// SAME 10% figure to the PRIMARY signal (PerfResult.AllocBytes/
// AllocCount, which do not carry wall-clock jitter) instead of
// PerMonthTick -- the spec's 10% tolerance for "how much worse is too
// much worse" is preserved exactly, only the quantity it is measured
// against has moved to one this package's own evidence shows is stable
// enough to judge it against. Wall-clock is not removed from the gate:
// it becomes a demoted, advisory check with its OWN, separate,
// deliberately much wider threshold (WallClockGrossRegressionThreshold)
// that only ever catches a catastrophic (>2x) slowdown, never ordinary
// runner noise.
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

// MinMeasurableDuration is the perf gate's noise floor for its ADVISORY
// wall-clock check (see baseline.go's CompareToBaseline doc comment for
// the full BUG-031 rationale this constant exists to avoid repeating): a
// percentage regression computed against a near-zero absolute duration
// is dominated by timer quantization and GC/scheduler jitter, not real
// simulated work.
//
// # BUG-272: wall-clock is no longer the PRIMARY signal
//
// This floor still guards the wall-clock figure it always has, but
// wall-clock itself was demoted to an advisory, gross-regression-only
// check (WallClockGrossRegressionThreshold, above) once BUG-272 found
// that even a window comfortably above this floor still carries 40-45%
// run-to-run noise on busy shared CI hardware -- a failure mode this
// floor was never designed to catch (it screens out timer-quantization-
// dominated windows, not scheduler/OS-load jitter on an otherwise-large
// window). The gate's primary, threshold-enforcing signal is now the
// allocation-based one (MinMeasurableAllocs, above), which does not
// carry this class of noise at all.
//
// # BUG-254: this floor applies to the measured tick WINDOW, not the
// # per-month figure
//
// Since BUG-254 the floor is compared against the total measured tick
// span a record's PerMonthTick was derived from — PerMonthTick x Months
// (baseline.go's measuredTickWindow) — NOT against PerMonthTick itself.
// Both the baseline's and the current run's windows must clear this
// floor before RegressionThreshold is applied at all; a sub-floor window
// is BelowNoiseFloor (could-not-evaluate, exit 3 in cmd/perfci), never a
// Regressed verdict and never a silent pass (BUG-071).
//
// Why the window and not the per-month figure: the harness times ONE
// span (headless_seam.go's runHeadless wraps the whole months-long run
// in a single time.Since), so the measurement error — the ~1ms Windows
// timer quantum plus scheduler jitter — attaches to that span ONCE.
// Dividing by Months rescales the signal and the error identically,
// which means the per-month figure's RELATIVE error is quantum/window
// no matter what Months is, and no fixed per-month constant can be
// simultaneously above the noise and below the real signal:
//
//   - The real hook-work per-month tick cost is ~1.9-3.5ms (measured —
//     see the history below), i.e. only 2-3x the 1ms quantum. A
//     per-month floor ABOVE that signal (ASM-173's original 5ms) makes
//     the gate permanently could-not-evaluate against real
//     measurements; a per-month floor BELOW it (the 2ms this constant
//     briefly held after FEAT-082) admits quantum-dominated
//     measurements and fabricates REGRESSED verdicts — BUG-254's
//     live-verified failure: at the old -months 3 (a ~10ms window,
//     only ~10x the quantum), CI history showed 18.9% peak-to-peak
//     spread on identical code against a 10% threshold, local repeat
//     runs spread 4.0-13.4ms on the window (238% on the per-month
//     figure), and LoadLatestBaseline froze a lucky-fast minimum
//     (2.9968ms/month) as the baseline, guaranteeing the next ordinary
//     run tripped the gate with zero code change.
//   - The window is the one quantity the run length CAN grow: at the
//     .github/workflows/ci.yml perf jobs' -months 96, the real window
//     measures ~176-180ms locally (median-of-TickSampleCount, see
//     below) — so a window floor separates real measurements from
//     collapsed ones where no per-month constant could.
//
// # Value: 50ms (BUG-254, evidence-based)
//
//   - Noise arithmetic at the floor boundary: 50ms = 50x the ~1ms
//     Windows timer quantum, so pure quantization contributes at most
//     ~2x1ms/50ms = 4% to a baseline-vs-current delta — a 2.5x margin
//     under RegressionThreshold (10%) in the WORST admissible case. At
//     the actual -months 96 operating point (~176-290ms windows) the
//     quantization bound is 0.7-1.1%. Empirically, 6 back-to-back
//     median-of-TickSampleCount runs at months=96 spread only 2.8%
//     peak-to-peak on the per-month figure (worst single step +2.2%,
//     worst cumulative-vs-anchor +1.6%) vs 9.7% peak-to-peak at
//     months=48 single-machine-under-load — which is why the jobs run
//     96, not 48.
//   - Old-regime measurements stay excluded: at the old -months 3 this
//     floor is equivalent to a 16.7ms/month requirement — strictly
//     above both ASM-173's 5ms and the real ~3-4.5ms signal — so every
//     measurement of the shape that flapped exits 3 rather than being
//     judged. (The floor is a NOISE bar, not a regime detector: a
//     future collapse of per-tick cost back toward walking-skeleton
//     levels at months=96 could still produce a measurable >=50ms
//     window near the top of the historical skeleton range — that
//     reads as a large improvement, which this gate, like any
//     regression gate, deliberately does not block.)
//   - Real windows clear it with margin: local months=96 windows
//     measured 175.6-180.4ms (3.5x the floor) at ~1.83-1.88ms/month —
//     the amortised per-month cost is LOWER than the ~3.3ms months=3
//     figure because the fixed first-tick warmup spreads over more
//     months, and the floor keeps real headroom even against that
//     amortisation (the window clears the floor down to ~0.52ms/month
//     at months=96).
//
// Re-derive if a future change drops the real window at the configured
// -months below ~2x this floor (raise -months or re-measure), or if the
// perf jobs' -months is ever lowered (the floor's per-month equivalent
// rises as months shrink — that direction is safe; raising -months
// without re-checking the floor is also safe).
//
// # Measurement history (kept because each revision cites it)
//
//   - Walking-skeleton era (zero PhaseHooks): local 12-month runs
//     0.101-0.884ms/month, 3-month runs up to 1.736ms/month, several
//     runs with TickTime == 0 while TotalTicks > 0 (the Windows
//     monotonic timer could not resolve the whole run); CI-runner
//     perf-1m-probe 488-926us/month. ASM-173/BUG-034 set a 5ms
//     PER-MONTH floor against this data.
//   - FEAT-082 (composition root, real hooks): CI 1M-real
//     ~3.2-3.5ms/month, perf-smoke (2000 citizens) ~3.7-4.0ms/month at
//     -months 3 — hook-work-dominated, population-independent. The
//     per-month floor was lowered to 2ms to sit under that signal,
//     which is what let quantum noise through (BUG-254, above).
//   - BUG-254 (2026-08-17, this revision): local 1M runs — months=3
//     windows 3.98-13.45ms (1.33-4.48ms/month); months=24 windows
//     41.4-56.2ms (1.73-2.34ms/month); months=48 windows 88.4-94.7ms
//     (1.84-1.97ms/month); months=96 median-sampled windows
//     175.6-180.4ms (1.83-1.88ms/month, 2.8% peak-to-peak over 6
//     runs). Floor re-scoped to the window at 50ms; CI jobs moved to
//     -months 96 with TickSampleCount-median sampling.
const MinMeasurableDuration = 50 * time.Millisecond

// TickSampleCount is how many times RunPerf (perf.go) repeats the
// months-long headless tick run and takes the MEDIAN window as the
// recorded TickTime (BUG-254). One window at -months 96 already bounds
// quantization noise (see MinMeasurableDuration's arithmetic above), but
// single windows still carry occasional multi-millisecond scheduler
// outliers (local months=24 evidence: 4 of 5 windows within 41-50ms, one
// at 56.2ms — a +14% tail on an otherwise-tight cluster); a median of
// five discards up to two such outliers per side entirely, at a cost of
// ~4x one window's wall time (~0.8s at months=96), negligible next to
// the ~4-7s world generation the same run already pays once.
//
// Deliberately a package constant, not a cmd/perfci flag: a sampling
// count that a CI job (or a local reproduction) could dial down
// per-invocation would be a quiet way to weaken the gate's noise
// behaviour without a reviewed code change — the same reasoning that
// removed the -accept-regression flag (BUG-095). Odd on purpose so the
// median is a real observed window, never an average of two.
const TickSampleCount = 5

// MinMeasurableAllocs is BUG-272's noise floor for the gate's PRIMARY
// regression signal: PerfResult.AllocBytes/AllocCount (perf.go), the
// tick-driving call's runtime.MemStats delta across the same
// TickSampleCount-median window TickTime is drawn from.
//
// # Why allocations became the primary signal (BUG-272)
//
// BUG-269 sped the engine to ~172us/month-tick. On shared CI hardware,
// wall-clock run-to-run jitter at that scale is routinely 40%+ (live-
// verified: a docs-only PR with byte-identical engine code reported a
// 45% wall-clock "regression"), which is far above RegressionThreshold
// (10%) -- BUG-254/271 already widened the measured WINDOW (-months) to
// clear MinMeasurableDuration's 50ms floor, but that floor only screens
// out timer-quantization-dominated windows, it does not and cannot
// screen out real scheduler/OS-noise jitter on a busy shared runner,
// which was the actual BUG-272 failure mode. Allocation counters do not
// have this problem: they are a count of a deterministic program event
// (a call to the Go allocator), not a wall-clock sample, so they do not
// carry runner-load jitter at all -- BUG-272's own verification (8 back-
// to-back local runs, -preset 1M -citizens 2000 -months 500) measured
// AllocBytes/AllocCount peak-to-peak spread of ~0.20%/~0.14% against the
// SAME commit, vs ~4.5% peak-to-peak on PerMonthTick on the SAME quiet
// local machine over the SAME runs (and the CI evidence above shows that
// wall-clock figure reaching 40-45% under real shared-runner load) --
// roughly 20-30x tighter than wall-clock noise on this package's own
// evidence, and nowhere close to RegressionThreshold (10%) even in the
// worst observed case. This is NOT bit-for-bit identical determinism --
// a real regression gate must be honest about that rather than claim a
// stronger guarantee than the evidence supports -- the residual ~0.2%
// variance is presumed incidental bookkeeping (map/slice growth order,
// GC-internal accounting) rather than simulated work, and it is small
// enough that RegressionThreshold's existing 10% margin comfortably
// absorbs it without weakening the gate (GR: no threshold widening).
//
// # The floor itself
//
// Analogous to MinMeasurableDuration's wall-clock floor: a percentage
// computed against a near-zero AllocCount is dominated by a handful of
// incidental allocations (a single map resize can be a double-digit
// percentage of a tiny baseline), not real simulated work, so the
// percentage comparison is skipped (BelowNoiseFloor, could-not-evaluate
// -- BUG-071) rather than judged, exactly like the wall-clock floor's
// own "skip rather than mislead" posture. 1000 is deliberately
// conservative relative to any real RunPerf measurement: even the
// smallest legal synthetic run this package can construct (1 citizen, 1
// month) measured 238,346 tick-window allocations in BUG-272's own
// verification -- more than two orders of magnitude above this floor --
// so this only ever fires against a hand-built/degenerate PerfResult
// (this package's own tests construct several), never a genuine RunPerf
// measurement at any preset/citizen count this package is asked to
// support.
const MinMeasurableAllocs uint64 = 1000

// WallClockGrossRegressionThreshold is BUG-272's threshold for the
// DEMOTED, ADVISORY wall-clock check: current.PerMonthTick more than
// DOUBLING baseline.PerMonthTick (a 100% increase, i.e. current > 2x
// baseline). Wall-clock timing is no longer the gate's primary signal
// (MinMeasurableAllocs' doc comment above has the full rationale) --
// ordinary CI jitter measured 40-45% on a byte-identical commit, so any
// threshold anywhere near RegressionThreshold (10%) would still flap.
// This constant exists only to keep a wall-clock safety net for a
// catastrophic slowdown a deterministic allocation count would NOT
// catch on its own (e.g. a busy-wait, a lock contention regression, or
// any other change that burns wall-clock cycles without allocating more)
// -- a >2x regression is roughly 2-2.5x above the worst wall-clock noise
// this package has ever measured or had reported against it, so it
// should essentially never fire on noise alone, while still closing the
// "allocations look fine but the build got catastrophically slower"
// gap. This is a NEW, SEPARATE threshold for a NEW, ADVISORY-only
// purpose -- it does not raise, replace, or weaken RegressionThreshold,
// which remains the PRIMARY (allocation-based) gate's threshold at its
// original spec-mandated 10% (M0-ENG §6 point 5, verbatim, unchanged).
const WallClockGrossRegressionThreshold = 1.0

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
