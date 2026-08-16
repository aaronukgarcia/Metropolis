package firms

import (
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// makeCitizen builds a rich hot Citizen value with the given ambition and
// sector (all other founding inputs fixed), for the pure FoundingProbability
// isolation tests — no CitizensAPI needed to vary a record's own values.
func makeCitizen(ambition int32, sector citizens.Sector) citizens.Citizen {
	var p citizens.Personality
	p[citizens.AxisAmbition] = ambition
	return citizens.Citizen{
		ID:          1,
		Personality: p,
		Education:   citizens.Education{Attainment: 50},
		Wealth:      0,
		Employment:  citizens.Employment{State: citizens.EmploymentEmployed, Sector: sector},
	}
}

// TestFoundingIsPerCitizen (AC-2): founding resolves the founder through
// CitizensAPI and the founded Startup carries that citizen's own ID as
// FounderCitizenID.
func TestFoundingIsPerCitizen(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	c := mustCitizens(t, []citizens.ColdRecord{
		citizenRecord(10, 100, citizens.SectorTertiary, 0),
		citizenRecord(20, 0, citizens.SectorTertiary, 0),
	})
	if err := api.SetCitizens(c); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}

	startups, err := api.EvaluateFounding([]uint64{10, 20}, 1)
	if err != nil {
		t.Fatalf("EvaluateFounding: %v", err)
	}
	if len(startups) != 1 {
		t.Fatalf("expected exactly one founded startup, got %d", len(startups))
	}
	if startups[0].FounderCitizenID != 10 {
		t.Fatalf("founder = %d, want 10 (the high-ambition citizen)", startups[0].FounderCitizenID)
	}
	// The Startup's FounderCitizenID resolves to a real citizen record.
	if _, ok := c.CitizenAt(startups[0].FounderCitizenID, "firms-test"); !ok {
		t.Fatalf("FounderCitizenID %d does not resolve through CitizensAPI", startups[0].FounderCitizenID)
	}
	// The founded firm's FounderCitizenID matches.
	firm, err := api.Firm(startups[0].FirmID)
	if err != nil {
		t.Fatalf("Firm: %v", err)
	}
	if firm.FounderCitizenID != 10 {
		t.Fatalf("firm founder = %d, want 10", firm.FounderCitizenID)
	}
}

// TestFoundingPermutationTracksFounder (AC-3): permuting which ID holds the
// high-ambition value moves the founder ID with the value while the total
// founded count is unaffected.
func TestFoundingPermutationTracksFounder(t *testing.T) {
	run := func(highID uint64) []uint64 {
		api := newAPIWithConfig(t, controlledConfig(), 1)
		c := mustCitizens(t, []citizens.ColdRecord{
			citizenRecord(10, ambitionFor(10, highID, 100), citizens.SectorTertiary, 0),
			citizenRecord(20, ambitionFor(20, highID, 100), citizens.SectorTertiary, 0),
			citizenRecord(30, ambitionFor(30, highID, 100), citizens.SectorTertiary, 0),
		})
		if err := api.SetCitizens(c); err != nil {
			t.Fatalf("SetCitizens: %v", err)
		}
		startups, err := api.EvaluateFounding([]uint64{10, 20, 30}, 1)
		if err != nil {
			t.Fatalf("EvaluateFounding: %v", err)
		}
		founders := make([]uint64, 0, len(startups))
		for _, s := range startups {
			founders = append(founders, s.FounderCitizenID)
		}
		sort.Slice(founders, func(i, j int) bool { return founders[i] < founders[j] })
		return founders
	}

	a := run(10) // high-ambition value on ID 10
	b := run(20) // high-ambition value on ID 20 (multiset of values identical)

	if len(a) != 1 || a[0] != 10 {
		t.Fatalf("population A founders = %v, want [10]", a)
	}
	if len(b) != 1 || b[0] != 20 {
		t.Fatalf("population B founders = %v, want [20] (value follows the permutation)", b)
	}
	if len(a) != len(b) {
		t.Fatalf("founded count changed under permutation: %d vs %d", len(a), len(b))
	}
}

// ambitionFor returns high when id is the high-ID, else 0 — so the multiset
// of values is held identical and only the label moves.
func ambitionFor(id, highID uint64, high int32) int32 {
	if id == highID {
		return high
	}
	return 0
}

// TestIndividualFoundingProbabilityIsolation (AC-3): perturbing exactly one
// citizen's ambition moves only that citizen's own probability.
func TestIndividualFoundingProbabilityIsolation(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	ctx := FoundingContext{}

	base := api.FoundingProbability(makeCitizen(50, citizens.SectorNone), ctx)

	// A different citizen (same values, different ID) has the same
	// probability — but perturbing its ambition moves ONLY it.
	before := api.FoundingProbability(makeCitizen(50, citizens.SectorNone), ctx)
	after := api.FoundingProbability(makeCitizen(90, citizens.SectorNone), ctx)

	if before != base {
		t.Fatalf("identical citizen record diverged: %d vs %d", before, base)
	}
	if after <= before {
		t.Fatalf("perturbing ambition did not raise that citizen's probability: %d -> %d", before, after)
	}
	// The aggregate probability for OTHER citizens (the base) is unaffected.
	if api.FoundingProbability(makeCitizen(50, citizens.SectorNone), ctx) != base {
		t.Fatalf("perturbing one citizen moved another citizen's probability")
	}
}

// TestExitHistoryBoostsFounding (AC-12): a founder with a logged successful
// exit has a higher founding/angel probability than an otherwise-identical
// citizen without one, and the founder-history ledger feeds the evaluator.
func TestExitHistoryBoostsFounding(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	ctx := FoundingContext{}

	base := api.FoundingProbability(makeCitizen(50, citizens.SectorNone), ctx)
	exited := api.FoundingProbability(makeCitizen(50, citizens.SectorNone), FoundingContext{ExitedFounder: true})
	if exited <= base {
		t.Fatalf("exit-history angel boost did not raise probability: %d -> %d", base, exited)
	}

	// The ledger path: a founder with no exit vs the same founder after a
	// logged exit.
	api2 := newAPIWithConfig(t, controlledConfig(), 1)
	c := mustCitizens(t, []citizens.ColdRecord{citizenRecord(7, 50, citizens.SectorTertiary, 0)})
	if err := api2.SetCitizens(c); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}
	if api2.FounderExited(7) {
		t.Fatal("expected no exit history before RecordExit")
	}
	before, err := api2.FoundingProbabilityFor(7, "firms-test")
	if err != nil {
		t.Fatalf("FoundingProbabilityFor: %v", err)
	}
	firmID, err := api2.Found(7)
	if err != nil {
		t.Fatalf("Found: %v", err)
	}
	if err := api2.RecordExit(firmID); err != nil {
		t.Fatalf("RecordExit: %v", err)
	}
	if !api2.FounderExited(7) {
		t.Fatal("expected exit history after RecordExit")
	}
	after, err := api2.FoundingProbabilityFor(7, "firms-test")
	if err != nil {
		t.Fatalf("FoundingProbabilityFor: %v", err)
	}
	if after <= before {
		t.Fatalf("logged exit did not raise the founder's own probability: %d -> %d", before, after)
	}
}

// TestUnknownCitizenAndUnknownFirm (AC-15): founding against an unresolved
// CitizenID, and a command against an unregistered FirmID, both return the
// registry-sourced error and create no placeholder.
func TestUnknownCitizenAndUnknownFirm(t *testing.T) {
	api := newAPIWithConfig(t, controlledConfig(), 1)
	c := mustCitizens(t, []citizens.ColdRecord{citizenRecord(1, 0, citizens.SectorNone, 0)})
	if err := api.SetCitizens(c); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}

	// Unknown citizen: rejected, no placeholder firm created.
	if _, err := api.Found(999); !hasCode(err, ErrUnknownCitizen) {
		t.Fatalf("Found(unknown) = %v, want ErrUnknownCitizen", err)
	}
	if got := len(api.Firms()); got != 0 {
		t.Fatalf("unknown-citizen founding created %d placeholder firm(s)", got)
	}

	// Unknown firm: rejected, no placeholder firm created.
	if _, err := api.Fail(999); !hasCode(err, ErrUnknownFirm) {
		t.Fatalf("Fail(unknown) = %v, want ErrUnknownFirm", err)
	}
	if err := api.Grow(999, []uint64{1}); !hasCode(err, ErrUnknownFirm) {
		t.Fatalf("Grow(unknown) = %v, want ErrUnknownFirm", err)
	}
	if got := len(api.Firms()); got != 0 {
		t.Fatalf("unknown-firm command created %d placeholder firm(s)", got)
	}
}

// TestCultureIndexRisesWithFounding (AC-10): the culture index (startups
// per 1k population over the rolling window) rises when the founding-event
// rate rises, computed from the SAME events the founding path produces.
func TestCultureIndexRisesWithFounding(t *testing.T) {
	const pop = 100
	records := func(high int) []citizens.ColdRecord {
		recs := make([]citizens.ColdRecord, 0, pop)
		for i := 1; i <= pop; i++ {
			amb := int32(0)
			if i <= high {
				amb = 100
			}
			recs = append(recs, citizenRecord(uint64(i), amb, citizens.SectorTertiary, 0))
		}
		return recs
	}
	ids := func() []uint64 {
		out := make([]uint64, 0, pop)
		for i := 1; i <= pop; i++ {
			out = append(out, uint64(i))
		}
		return out
	}

	low := newAPIWithConfig(t, controlledConfig(), 1)
	if err := low.SetCitizens(mustCitizens(t, records(1))); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}
	if _, err := low.EvaluateFounding(ids(), 1); err != nil {
		t.Fatalf("EvaluateFounding: %v", err)
	}
	lowIdx := low.CultureIndex()

	high := newAPIWithConfig(t, controlledConfig(), 1)
	if err := high.SetCitizens(mustCitizens(t, records(2))); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}
	if _, err := high.EvaluateFounding(ids(), 1); err != nil {
		t.Fatalf("EvaluateFounding: %v", err)
	}
	highIdx := high.CultureIndex()

	if lowIdx == 0 || highIdx == 0 {
		t.Fatalf("expected nonzero culture index, got low=%d high=%d", lowIdx, highIdx)
	}
	if highIdx <= lowIdx {
		t.Fatalf("culture index did not rise with founding rate: %d -> %d", lowIdx, highIdx)
	}
}
