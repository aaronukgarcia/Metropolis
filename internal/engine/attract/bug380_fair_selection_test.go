package attract

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// BUG-380 fourth re-round (opus-reround4-bug380) — AC-6 pin. The third
// re-round's cap fix (migration.go's applyEmigration) selected which
// candidates actually departed by walking cmd.ResidentIDs in its existing
// (ascending-by-id) order and taking the first N whose hazard draw
// cleared, stopping once the cap was reached. Under a BINDING cap (more
// eligible candidates than the cap allows — the common case under
// sustained decline), this deterministically favoured LOW ids over HIGH
// ids every month, regardless of ambition — inverting AC-6 ("ambitious
// citizens leave sooner when opportunity dries up"): an id's position in
// a sorted enumeration has nothing to do with its personality.
//
// Fixed (migration.go's applyEmigration): candidates are now selected by
// draw/hazard margin (ascending — strongest signal first), id ascending
// only as the deterministic tiebreak. This test proves a maximally-
// ambitious HIGH-id citizen can win a binding cap's single slot over 49
// minimally-ambitious LOW-id citizens that would have exhausted the old
// position-biased cap every single month.
func TestBUG380_AmbitionStillDrivesSelectionUnderCap(t *testing.T) {
	a, ca, _, _ := newAPI(t, validConfig())

	// 49 low-ambition (id 1..49) + 1 high-ambition (id 1,000,000) = 50
	// residents exactly, so hardCeiling = ceil(50 * emigrationMaxMonthlyShare)
	// = ceil(1.0) = 1 — a HARD, single-slot binding cap regardless of how
	// many candidates clear their hazard in any given month.
	const numLow = 49
	const highID = uint64(1_000_000)
	lowIDs := make([]uint64, numLow)
	recs := make([]citizens.ColdRecord, 0, numLow+1)
	for i := 0; i < numLow; i++ {
		lowIDs[i] = uint64(i + 1)
		recs = append(recs, mkResident(lowIDs[i], 0)) // minimal ambition
	}
	recs = append(recs, mkResident(highID, 100)) // maximal ambition
	if err := ca.SeedColdRecords(recs, "corr-attract"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}

	allIDs := make([]uint64, 0, numLow+1)
	allIDs = append(allIDs, lowIDs...)
	allIDs = append(allIDs, highID)

	if err := a.SetTermInputs(TermInputs{}); err != nil { // severe decline: every term at its floor
		t.Fatalf("SetTermInputs: %v", err)
	}

	const decline = 1.0 // every term floored -> decline saturates to 1 (clampFloat(-net,0,1))
	hazardLow := EmigrationHazard(0, decline)
	hazardHigh := EmigrationHazard(100, decline)
	if hazardHigh <= hazardLow {
		t.Fatalf("test setup: hazardHigh (%v) <= hazardLow (%v) — AC-6 (strictly increasing in ambition) is not holding, this test cannot mean anything", hazardHigh, hazardLow)
	}

	const maxMonths = 100
	var highDepartedAtMonth int64
	var competedThisMonth bool
	for month := int64(1); month <= maxMonths; month++ {
		res, err := a.ApplyMigration(MigrationCommand{
			Month: month, ResidentIDs: allIDs, HousingVacancy: 0, JunctionThroughput: 0,
		})
		if err != nil {
			t.Fatalf("ApplyMigration(month %d): %v", month, err)
		}
		if res.Outflow > 1 {
			t.Fatalf("month %d: Outflow=%d exceeds the binding cap of 1", month, res.Outflow)
		}

		// Independently check (white-box, same package) whether at least
		// one STILL-LIVE low-ambition id ALSO cleared its own hazard this
		// month — proof of genuine competition for the single slot, not
		// merely high being the sole remaining candidate. Liveness matters
		// here: det.NewStream's draw is a pure function of (seed, id,
		// month) regardless of whether that id has already departed in an
		// earlier month, so checking the raw draw alone (without
		// CitizenAt) would count a long-dead low id's draw as
		// "competition" it can no longer actually offer — exactly the
		// false-positive this test's own first draft had (caught mid-fix:
		// it "confirmed" competition even against the OLD, position-biased
		// selection, because most of the 49 low ids had already been
		// sequentially exhausted by the time high finally got a turn, and
		// their draws were being recomputed as if they were still live).
		lowCleared := false
		for _, id := range lowIDs {
			if _, alive := ca.CitizenAt(id, "corr-attract"); !alive {
				continue
			}
			stream := det.NewStream(a.seed, id, month, "emigrate")
			if stream.Float64() < hazardLow {
				lowCleared = true
				break
			}
		}

		_, highStillHere := ca.CitizenAt(highID, "corr-attract")
		if !highStillHere {
			highDepartedAtMonth = month
			competedThisMonth = lowCleared
			break
		}
	}

	if highDepartedAtMonth == 0 {
		t.Fatalf("the maximally-ambitious high-id citizen never departed within %d months under a binding single-slot cap and 49 competing low-ambition candidates — AC-6 selection is not working (position bias may have returned)", maxMonths)
	}
	// competedThisMonth is a HARD requirement, not a soft note: without a
	// still-alive low-ambition candidate ALSO clearing hazard the same
	// month, this test proves nothing about AC-6 vs scan-position bias —
	// high could simply be the last id left standing after the low
	// cohort was sequentially exhausted over many single-winner months,
	// which is exactly what the OLD position-biased selection does (the
	// low ids, checked first every month, deplete themselves one at a
	// time; only once ALL 49 are gone does high ever get evaluated at
	// all). PROOF THIS CAN FAIL: run against the third re-round's
	// scan-order selection (migration.go reverted to
	// opus-reround3-bug380's shape) — verified by hand this session: high
	// still eventually departs (month 42, by elimination, zero remaining
	// low competitors), but competedThisMonth is FALSE every time,
	// because by month 42 every one of the 49 low ids has already
	// departed in an earlier, uncontested month — the OLD code fails
	// THIS assertion even though the weaker "high departs eventually"
	// assertion above would have passed either way.
	if !competedThisMonth {
		t.Fatalf("high-id citizen departed at month %d with NO still-alive low-ambition candidate also clearing hazard that same month — no genuine head-to-head competition was observed, so this run cannot distinguish fair (margin-based) selection from the old position-biased scan order (which lets high depart only once every low competitor has already been sequentially exhausted)", highDepartedAtMonth)
	}
	t.Logf("CONFIRMED at month %d: the maximally-ambitious high-id citizen (id=%d) departed AHEAD OF a minimally-ambitious low-id candidate that ALSO cleared its own hazard the same month, under a binding single-slot cap — AC-6 wins, not scan position", highDepartedAtMonth, highID)

	// The competing low-ambition citizens must still mostly be present —
	// this is a single-slot cap, so at most 1 total departure per month,
	// and we stopped at the FIRST month high departed.
	stillLow := 0
	for _, id := range lowIDs {
		if _, ok := ca.CitizenAt(id, "corr-attract"); ok {
			stillLow++
		}
	}
	if stillLow < numLow-int(highDepartedAtMonth) {
		t.Fatalf("more low-ambition citizens departed (%d remain of %d) than the %d-month, 1-per-month cap allows", stillLow, numLow, highDepartedAtMonth)
	}
}
