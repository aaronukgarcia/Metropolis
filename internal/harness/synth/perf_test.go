package synth

import "testing"

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
