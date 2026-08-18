package synth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRunPerf_InvalidMonthsRejected covers RunPerf's own months<=0 guard
// (MET-H304), distinct from ValidateParams' domain checks.
func TestRunPerf_InvalidMonthsRejected(t *testing.T) {
	_, err := RunPerf("t", validParams(), "test", 0)
	wantCode(t, err, codeInvalidMonths)
}

// TestRunPerf_InvalidParamsRejected proves RunPerf validates Params
// before doing any work, the same as Generate — a caller cannot reach
// the tick-driving loop through RunPerf with an out-of-domain request.
func TestRunPerf_InvalidParamsRejected(t *testing.T) {
	p := validParams()
	p.CitizenCount = MaxSyntheticCitizens + 1
	_, err := RunPerf("t", p, "test", 1)
	wantCode(t, err, codeCitizenCountOutOfRange)
}

// TestRunPerf_ReturnsTimingAndWorkCounters is AC-4's happy path, kept
// deliberately small (tiny citizenCount, 1 month) so the full test suite
// stays fast under -race — the 1M/10M-scale presets are exercised by
// cmd/perfci, not by go test.
func TestRunPerf_ReturnsTimingAndWorkCounters(t *testing.T) {
	p := Params{CitizenCount: 25, Seed: 3, Sprawl: 0.2, NetworkShape: NetworkGrid}

	result, err := RunPerf("t", p, "smoke", 1)
	if err != nil {
		t.Fatalf("RunPerf: %v", err)
	}
	if result.Months != 1 {
		t.Fatalf("result.Months = %d, want 1", result.Months)
	}
	if result.TotalTicks <= 0 {
		t.Fatalf("result.TotalTicks = %d, want > 0", result.TotalTicks)
	}
	if result.CitizenCount != p.CitizenCount {
		t.Fatalf("result.CitizenCount = %d, want %d", result.CitizenCount, p.CitizenCount)
	}
	// GenerationTime is real wall-clock time.Since output; a tiny
	// (25-citizen) generation can legitimately measure as 0 on a
	// coarse-resolution clock, so this only asserts it is never
	// negative — asserting a specific non-zero floor here would be
	// exactly the brittle wall-clock assumption this item's brief
	// warned against (BUG-031).
	if result.GenerationTime < 0 {
		t.Fatalf("result.GenerationTime = %v, want >= 0", result.GenerationTime)
	}
	// PerMonthTick is TickTime/Months and must be well-defined (no
	// division-by-zero panic — RunPerf already rejects months<=0 before
	// reaching here, this is just confirming the arithmetic).
	if result.PerMonthTick < 0 {
		t.Fatalf("result.PerMonthTick = %v, want >= 0", result.PerMonthTick)
	}
}

// TestRunPerf_MultipleMonthsAccumulatesTicks proves TotalTicks scales
// with the requested month count (core.DailyTicksPerMonth per month),
// not a fixed constant regardless of the months argument.
func TestRunPerf_MultipleMonthsAccumulatesTicks(t *testing.T) {
	p := Params{CitizenCount: 10, Seed: 4, Sprawl: 0.2, NetworkShape: NetworkGrid}

	r1, err := RunPerf("t", p, "smoke", 1)
	if err != nil {
		t.Fatalf("RunPerf(months=1): %v", err)
	}
	// The multiplier is named once and used for BOTH the input and the
	// expectation, so changing the run length cannot leave the assertion
	// asserting the old ratio (GR#15).
	const monthsMultiple = 3
	r3, err := RunPerf("t", p, "smoke", monthsMultiple)
	if err != nil {
		t.Fatalf("RunPerf(months=%d): %v", monthsMultiple, err)
	}
	if want := monthsMultiple * r1.TotalTicks; r3.TotalTicks != want {
		t.Fatalf("TotalTicks(months=%d) = %d, want %dx TotalTicks(months=1) = %d", monthsMultiple, r3.TotalTicks, monthsMultiple, want)
	}
}

// TestMedianSampleIndex_PicksTheMedianElapsedWindow pins BUG-254's
// sampling reduction: the recorded window must be the MEDIAN repetition,
// so neither a lucky-fast minimum (BUG-254's frozen-baseline ratchet) nor
// a scheduler-preemption maximum can become the gated figure. The fixture
// is deliberately unsorted with the median in neither first nor last
// position, so an implementation that returned min, max, first, or last
// would all fail.
func TestMedianSampleIndex_PicksTheMedianElapsedWindow(t *testing.T) {
	samples := []tickSample{
		{elapsed: 90 * time.Millisecond},
		{elapsed: 41 * time.Millisecond},  // the lucky-fast outlier
		{elapsed: 92 * time.Millisecond},  // the median (sorted: 41, 90, 92, 93, 156)
		{elapsed: 156 * time.Millisecond}, // the preempted outlier
		{elapsed: 93 * time.Millisecond},
	}
	if got := medianSampleIndex(samples); got != 2 {
		t.Fatalf("medianSampleIndex = %d (elapsed %s), want 2 (the 92ms median window)", got, samples[got].elapsed)
	}
	// The caller's slice order is an input, not a scratch buffer — the
	// helper must not have reordered it (RunPerf indexes back into it).
	if samples[1].elapsed != 41*time.Millisecond || samples[3].elapsed != 156*time.Millisecond {
		t.Fatal("medianSampleIndex reordered the caller's samples slice")
	}
}

// TestTickSampleCount_IsOddAndAtLeastThree pins the two structural
// properties RunPerf's median depends on (limits.go documents both): an
// odd count makes the median a genuinely observed window rather than an
// average, and fewer than three samples cannot reject any outlier at all.
// Derived checks, not a pinned value — the count itself may be retuned.
func TestTickSampleCount_IsOddAndAtLeastThree(t *testing.T) {
	if TickSampleCount < 3 {
		t.Fatalf("TickSampleCount = %d, want >= 3 — below that the median rejects no outliers and the sampling is decorative", TickSampleCount)
	}
	if TickSampleCount%2 == 0 {
		t.Fatalf("TickSampleCount = %d, want odd — an even count makes the 'median' an interpolation between two windows, not an observed one", TickSampleCount)
	}
}

// TestImplausibleReason_RejectsGiganticPerMonthTick is BUG-096's core
// regression test: ImplausibleReason previously checked PerMonthTick < 0
// only, so a gigantic-but-positive value (live-verified reproduction:
// 10s, the exact figure Destructive-9 used to seed a permanently-wrong
// baseline that made a real 25x regression report as a 95% improvement)
// was accepted as plausible. RED against the pre-fix ImplausibleReason
// (would return "" for a 10s PerMonthTick); GREEN against the fix, which
// rejects anything over MaxPlausiblePerMonthTick (limits.go).
func TestImplausibleReason_RejectsGiganticPerMonthTick(t *testing.T) {
	r := PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 10 * time.Second, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}
	if reason := r.ImplausibleReason(); reason == "" {
		t.Fatal("ImplausibleReason() = \"\", want a non-empty reason for a 10s PerMonthTick (BUG-096: no upper sanity ceiling)")
	}
}

// TestImplausibleReason_AllowsRealisticPerMonthTick is the companion
// zero-false-positive check: a PerMonthTick comfortably inside any
// plausible real measurement (including this package's own live-verified
// severe-regression figure, 500ms) must NOT be rejected by the new
// ceiling — BUG-096's fix must not become BUG-031's mistake in reverse
// (an absolute ceiling picked too tight for a real, if slow, measurement).
func TestImplausibleReason_AllowsRealisticPerMonthTick(t *testing.T) {
	r := PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 500 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}
	if reason := r.ImplausibleReason(); reason != "" {
		t.Fatalf("ImplausibleReason() = %q, want \"\" for a realistic (if slow) 500ms PerMonthTick", reason)
	}
}

// TestImplausibleReason_RejectsMismatchedPhaseHookCount is BUG-055's
// core regression test: PhaseHookCount was previously accepted verbatim
// with no provenance check at all, so a hand-built record could claim
// any PhaseHookCount, real or forged, with zero friction. RunPerf
// (perf.go) always sets PhaseHookCount from
// PhaseHookCountInHeadlessPath() — never from any other source — so a
// Measured=true record whose PhaseHookCount disagrees with that
// function's current return value cannot have come from a genuine
// RunPerf call. RED against the pre-fix ImplausibleReason (would return
// "" here); GREEN against the fix, which rejects any mismatch.
func TestImplausibleReason_RejectsMismatchedPhaseHookCount(t *testing.T) {
	r := PerfResult{
		CitizenCount:   OneMillionCitizens,
		Months:         3,
		PerMonthTick:   500 * time.Millisecond,
		PhaseHookCount: PhaseHookCountInHeadlessPath() + 1,
		Measured:       true,
	}
	if reason := r.ImplausibleReason(); reason == "" {
		t.Fatal("ImplausibleReason() = \"\", want a non-empty reason for a PhaseHookCount that disagrees with PhaseHookCountInHeadlessPath() (BUG-055)")
	}
}

// TestImplausibleReason_AllowsGenuinePhaseHookCount is the companion
// zero-false-positive check: a record whose PhaseHookCount matches
// PhaseHookCountInHeadlessPath() exactly — as every real RunPerf call
// always produces — must not be rejected by BUG-055's fix.
func TestImplausibleReason_AllowsGenuinePhaseHookCount(t *testing.T) {
	r := PerfResult{
		CitizenCount:   OneMillionCitizens,
		Months:         3,
		PerMonthTick:   500 * time.Millisecond,
		PhaseHookCount: PhaseHookCountInHeadlessPath(),
		Measured:       true,
	}
	if reason := r.ImplausibleReason(); reason != "" {
		t.Fatalf("ImplausibleReason() = %q, want \"\" for a PhaseHookCount matching PhaseHookCountInHeadlessPath()", reason)
	}
}

// TestAppendResult_RejectsMismatchedPhaseHookCount is BUG-055's
// end-to-end regression test: proves the mismatch is caught at the
// actual write boundary (AppendResult), not merely by the standalone
// ImplausibleReason unit tests above.
func TestAppendResult_RejectsMismatchedPhaseHookCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	rec := PerfRecord{
		CommitHash: "attacker",
		Preset:     "1M",
		Result: PerfResult{
			CitizenCount:   MinSyntheticCitizens,
			Months:         1,
			PerMonthTick:   1 * time.Millisecond,
			PhaseHookCount: PhaseHookCountInHeadlessPath() + 1,
			Measured:       true,
		},
	}
	err := AppendResult(path, rec)
	wantCode(t, err, codeImplausibleResult)

	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("AppendResult wrote/touched %q despite rejecting the record", path)
	}
}

// TestLoadLatestBaseline_GiganticFirstRecordNoLongerSilentlySeeds is
// BUG-096's end-to-end regression test, reproducing Destructive-9's exact
// live-verified attack shape: a fresh results file whose FIRST record for
// a preset carries a gigantic-but-positive PerMonthTick (10s) must not be
// accepted by AppendResult at all (closing the hole at the write
// boundary, the same two-boundary shape BUG-073/085/095 already use) —
// so a genuine, severe regression measured afterward has nothing poisoned
// to silently compare against as an "improvement".
func TestLoadLatestBaseline_GiganticFirstRecordNoLongerSilentlySeeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	gigantic := PerfRecord{
		CommitHash: "corrupted-or-planted",
		Preset:     "1M",
		Result:     PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 10 * time.Second, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true},
	}
	err := AppendResult(path, gigantic)
	wantCode(t, err, codeImplausibleResult)
}

// TestImplausibleReason_RejectsZeroValuedCitizenCount is ASM-374's core
// regression test: the pre-fix check used `< 0` only, so a hand-crafted
// CitizenCount=0 record — unreachable from a real RunPerf call, which is
// floor-capped at MinSyntheticCitizens by ValidateParams — was treated as
// plausible and could be persisted as a trusted baseline. RED against the
// pre-fix ImplausibleReason (returns ""); GREEN against the fix.
func TestImplausibleReason_RejectsZeroValuedCitizenCount(t *testing.T) {
	r := PerfResult{CitizenCount: 0, Months: 3, PerMonthTick: 10 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}
	if reason := r.ImplausibleReason(); reason == "" {
		t.Fatal("ImplausibleReason() = \"\", want a non-empty reason for CitizenCount=0 (ASM-374: zero is as structurally impossible as negative)")
	}
}

// TestImplausibleReason_RejectsZeroValuedMonths is ASM-374's Months half.
func TestImplausibleReason_RejectsZeroValuedMonths(t *testing.T) {
	r := PerfResult{CitizenCount: 1000, Months: 0, PerMonthTick: 10 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}
	if reason := r.ImplausibleReason(); reason == "" {
		t.Fatal("ImplausibleReason() = \"\", want a non-empty reason for Months=0 (ASM-374)")
	}
}

// TestImplausibleReason_AllowsZeroPerMonthTick is ASM-374's zero-false-
// positive guard: PerMonthTick==0 is a REAL measurement (TickTime can
// genuinely resolve to zero at walking-skeleton scale — limits.go's
// MinMeasurableDuration re-derivation documents real runs where it did),
// so widening the CitizenCount/Months checks must NOT also reject a zero
// PerMonthTick.
func TestImplausibleReason_AllowsZeroPerMonthTick(t *testing.T) {
	r := PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 0, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}
	if reason := r.ImplausibleReason(); reason != "" {
		t.Fatalf("ImplausibleReason() = %q, want \"\" for a zero PerMonthTick (a real, degenerate walking-skeleton measurement)", reason)
	}
}
