package synth

import (
	"bytes"
	"testing"
)

// TestGeneratedWorldFeedsHeadlessRunnerWithoutTranslation is AC-2's
// integration check: a world Generate produces is fed directly into the
// REAL harness.headless package (runHeadless, headless_seam.go, which
// calls headless.Run) using hdr.WorldSeed exactly as Generate returned
// it — no translation step of any kind.
func TestGeneratedWorldFeedsHeadlessRunnerWithoutTranslation(t *testing.T) {
	p := Params{CitizenCount: 15, Seed: 42, Sprawl: 0.4, NetworkShape: NetworkGrid}

	var buf bytes.Buffer
	hdr, err := Generate("t", p, &buf)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if hdr.WorldSeed != int64(p.Seed) {
		t.Fatalf("header.WorldSeed = %d, want %d (untranslated)", hdr.WorldSeed, p.Seed)
	}

	_, totalTicks, _, err := runHeadless("t", hdr, 1)
	if err != nil {
		t.Fatalf("runHeadless: %v", err)
	}
	if totalTicks <= 0 {
		t.Fatalf("runHeadless returned totalTicks=%d, want > 0", totalTicks)
	}
}

// TestRunHeadless_RejectsInvalidMonths proves the seam itself validates
// months, not only RunPerf's own copy of the same check.
func TestRunHeadless_RejectsInvalidMonths(t *testing.T) {
	p := Params{CitizenCount: 5, Seed: 1, Sprawl: 0.1, NetworkShape: NetworkGrid}
	var buf bytes.Buffer
	hdr, err := Generate("t", p, &buf)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	_, _, _, err = runHeadless("t", hdr, 0)
	wantCode(t, err, codeInvalidMonths)
}

// TestParsePhaseTimings_EmptyStreamReturnsNoTimings proves the parser
// degrades gracefully on an empty -report stream (e.g. a run that
// somehow advanced zero ticks) rather than panicking.
func TestParsePhaseTimings_EmptyStreamReturnsNoTimings(t *testing.T) {
	timings := parsePhaseTimings(nil)
	if len(timings) != 0 {
		t.Fatalf("parsePhaseTimings(nil) = %+v, want empty", timings)
	}
}
