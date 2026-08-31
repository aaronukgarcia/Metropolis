package attract

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/households"
)

// admitManyMigrants builds a fresh AttractAPI/CitizensAPI pair at the given
// seed and admits one large batch of migrants (a large positive attract
// gap + generous capacity, mirroring TestBidirectionalMigration's positive
// branch), returning the admitted migrant ids — the high-bit-prefixed
// range applyImmigration mints from (migrantIDHighBit|1, |2, ...).
func admitManyMigrants(t *testing.T, seed uint64, month int64) (*AttractAPI, *citizens.CitizensAPI, []uint64) {
	t.Helper()
	a, err := New(validConfig(), seed, "corr-wealth")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ca, err := citizens.NewCitizensAPI(seed, "corr-wealth")
	if err != nil {
		t.Fatalf("citizens.NewCitizensAPI: %v", err)
	}
	h, err := households.NewFromBuildings(testCatalogue(), "corr-wealth")
	if err != nil {
		t.Fatalf("households.NewFromBuildings: %v", err)
	}
	if err := h.SetCitizens(ca); err != nil {
		t.Fatalf("SetCitizens(households): %v", err)
	}
	if err := a.SetCitizens(ca); err != nil {
		t.Fatalf("SetCitizens(attract): %v", err)
	}
	if err := a.SetHouseholds(h); err != nil {
		t.Fatalf("SetHouseholds: %v", err)
	}
	f := finance.NewFinanceAPI("corr-wealth")
	if err := a.SetFinance(f); err != nil {
		t.Fatalf("SetFinance: %v", err)
	}
	if err := a.SetTermInputs(TermInputs{
		JobAvailability:        90,
		ServiceCoverage:        90,
		Environment:            90,
		LeisureFit:             90,
		Safety:                 90,
		MonthlyRentMicroPounds: 0,
	}); err != nil {
		t.Fatalf("SetTermInputs: %v", err)
	}
	res, err := a.ApplyMigration(MigrationCommand{
		Month:              month,
		HousingVacancy:     100,
		JunctionThroughput: 100,
	})
	if err != nil {
		t.Fatalf("ApplyMigration: %v", err)
	}
	if res.Inflow <= 0 {
		t.Fatalf("ApplyMigration admitted no migrants (Inflow=%d) — cannot probe wealth variance", res.Inflow)
	}
	// mintMigrantID starts its counter at 1 and pre-increments (api.go's
	// New sets nextMigrantID:1), so the first minted id is
	// migrantIDHighBit|2, not |1 — offset by one accordingly.
	ids := make([]uint64, 0, res.Inflow)
	for i := uint64(2); i <= uint64(res.Inflow)+1; i++ {
		ids = append(ids, migrantIDHighBit|i)
	}
	return a, ca, ids
}

// TestFEAT1972079927_MigrantWealth_VariesAcrossMigrants proves Q5's
// "migrants arrive with varied wealth" ruling: a batch of admitted
// migrants must NOT all carry the same Wealth (the old behaviour — every
// migrant born at a flat, unset Wealth of 0), and must include at least
// one migrant clearly above the log-normal median and one clearly below
// it (the distribution's real spread, not just two arbitrary distinct
// values).
//
// PROOF THIS CAN FAIL: temporarily replacing migrantWealth's body with
// `return 0` (the old flat placeholder) collapses every value to 0 and
// this test fails on the "not all equal" check — verified by hand during
// development (see this increment's mutation-test evidence), then
// reverted.
func TestFEAT1972079927_MigrantWealth_VariesAcrossMigrants(t *testing.T) {
	_, ca, ids := admitManyMigrants(t, 42, 1)
	if len(ids) < 10 {
		t.Fatalf("only %d migrants admitted, want at least 10 to probe variance meaningfully", len(ids))
	}

	seen := make(map[int64]bool, len(ids))
	var min, max int64 = -1, -1
	var aboveMedian, belowMedian int
	for _, id := range ids {
		cit, ok := ca.CitizenAt(id, "corr-wealth")
		if !ok {
			t.Fatalf("CitizenAt(%d): not found", id)
		}
		if cit.Wealth <= 0 {
			t.Fatalf("migrant %d Wealth = %d, want strictly positive (log-normal draws are always > 0)", id, cit.Wealth)
		}
		seen[cit.Wealth] = true
		if min == -1 || cit.Wealth < min {
			min = cit.Wealth
		}
		if max == -1 || cit.Wealth > max {
			max = cit.Wealth
		}
		if cit.Wealth > migrantWealthMedianMicropounds {
			aboveMedian++
		} else {
			belowMedian++
		}
	}
	if len(seen) < 2 {
		t.Fatalf("all %d migrants share the identical Wealth %d — not varied (flat placeholder regression)", len(ids), min)
	}
	if aboveMedian == 0 || belowMedian == 0 {
		t.Fatalf("wealth distribution is one-sided across %d migrants (above median=%d, below=%d, min=%d, max=%d) — want spread on both sides of the log-normal median", len(ids), aboveMedian, belowMedian, min, max)
	}
}

// TestFEAT1972079927_MigrantWealth_Deterministic proves the bell-curve
// wealth draw is reproducible (FEAT-1972079927's determinism requirement,
// never math/rand or wall-clock): two independent runs at the same world
// seed, same month, admitting the same migrant ids, must produce BYTE-
// IDENTICAL Wealth values.
func TestFEAT1972079927_MigrantWealth_Deterministic(t *testing.T) {
	_, ca1, ids1 := admitManyMigrants(t, 99, 3)
	_, ca2, ids2 := admitManyMigrants(t, 99, 3)
	if len(ids1) != len(ids2) {
		t.Fatalf("two identical-seed runs admitted different migrant counts: %d vs %d", len(ids1), len(ids2))
	}
	for i, id := range ids1 {
		c1, ok1 := ca1.CitizenAt(id, "corr-wealth")
		c2, ok2 := ca2.CitizenAt(ids2[i], "corr-wealth")
		if !ok1 || !ok2 {
			t.Fatalf("migrant %d: CitizenAt ok1=%v ok2=%v", id, ok1, ok2)
		}
		if c1.Wealth != c2.Wealth {
			t.Fatalf("migrant %d: run1 Wealth=%d, run2 Wealth=%d — the wealth draw is not deterministic", id, c1.Wealth, c2.Wealth)
		}
	}
}

// TestFEAT1972079927_MigrantWealth_DiffersAcrossMonths proves the wealth
// stream is keyed by month too (not just id): the same id admitted in a
// different month draws a different value — a sanity check that
// migrantWealth is a real Stream draw, not a pure function of id alone
// that would make every migrant's FIRST-ever wealth figure predictable
// across the whole game.
func TestFEAT1972079927_MigrantWealth_DiffersAcrossMonths(t *testing.T) {
	w1 := migrantWealth(42, migrantIDHighBit|1, 1)
	w2 := migrantWealth(42, migrantIDHighBit|1, 2)
	if w1 == w2 {
		t.Fatalf("migrantWealth(seed, id, month=1) == migrantWealth(seed, id, month=2) == %d — the draw ignores month", w1)
	}
}
