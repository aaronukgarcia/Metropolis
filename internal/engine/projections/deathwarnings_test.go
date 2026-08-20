package projections

import "testing"

// --- AC-17: MarginToInsolvency tracks a real registered trend ------------

func TestMarginToInsolvencyTracksWorseningTrend(t *testing.T) {
	api := NewProjectionsAPI()
	provider := newFakeTrendProvider()
	if err := api.RegisterCurveProvider(CurveKeyFinanceInsolvencyRisk, provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	provider.setState(map[int64]float64{0: 0, 1: 0}) // healthy
	r1, err := api.MarginToInsolvency(1)
	if err != nil {
		t.Fatalf("MarginToInsolvency (healthy): %v", err)
	}

	provider.setState(map[int64]float64{0: 0, 1: 1}) // one missed payment
	r2, err := api.MarginToInsolvency(1)
	if err != nil {
		t.Fatalf("MarginToInsolvency (one missed): %v", err)
	}

	provider.setState(map[int64]float64{0: 1, 1: 2}) // two consecutive missed
	r3, err := api.MarginToInsolvency(1)
	if err != nil {
		t.Fatalf("MarginToInsolvency (two consecutive missed): %v", err)
	}

	if !(r1.MonthsRemaining > r2.MonthsRemaining && r2.MonthsRemaining > r3.MonthsRemaining) {
		t.Fatalf("months-remaining did not strictly decrease across worsening states: healthy=%v, one-missed=%v, two-missed=%v",
			r1.MonthsRemaining, r2.MonthsRemaining, r3.MonthsRemaining)
	}

	provider.setState(map[int64]float64{0: 2, 1: 1}) // recovering: a payment made
	r4, err := api.MarginToInsolvency(1)
	if err != nil {
		t.Fatalf("MarginToInsolvency (recovering): %v", err)
	}
	if !(r4.MonthsRemaining > r3.MonthsRemaining) {
		t.Errorf("months-remaining did not increase on recovery: two-missed=%v, recovering=%v", r3.MonthsRemaining, r4.MonthsRemaining)
	}
}

func TestMarginToInsolvencyUnknownProviderRejected(t *testing.T) {
	api := NewProjectionsAPI()
	_, err := api.MarginToInsolvency(0)
	assertCode(t, err, ErrUnknownCurveKey)
}

// --- AC-18: MarginToGhostCity tracks a real registered population trend -

func TestMarginToGhostCityTracksWorseningTrend(t *testing.T) {
	api := NewProjectionsAPI()
	provider := newFakeTrendProvider()
	provider.setPeak(100000) // exceeds the 50,000 floor
	if err := api.RegisterCurveProvider(CurveKeyGhostCityPopulation, provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	provider.setState(map[int64]float64{0: 50000, 1: 50000}) // healthy, flat
	r1, err := api.MarginToGhostCity(1)
	if err != nil {
		t.Fatalf("MarginToGhostCity (healthy): %v", err)
	}

	provider.setState(map[int64]float64{0: 50000, 1: 40000}) // first decline
	r2, err := api.MarginToGhostCity(1)
	if err != nil {
		t.Fatalf("MarginToGhostCity (first decline): %v", err)
	}

	provider.setState(map[int64]float64{0: 40000, 1: 20000}) // steeper decline
	r3, err := api.MarginToGhostCity(1)
	if err != nil {
		t.Fatalf("MarginToGhostCity (steeper decline): %v", err)
	}

	if !(r1.MonthsRemaining > r2.MonthsRemaining && r2.MonthsRemaining > r3.MonthsRemaining) {
		t.Fatalf("months-remaining did not strictly decrease across worsening states: healthy=%v, decline1=%v, decline2=%v",
			r1.MonthsRemaining, r2.MonthsRemaining, r3.MonthsRemaining)
	}

	provider.setState(map[int64]float64{0: 20000, 1: 30000}) // recovering
	r4, err := api.MarginToGhostCity(1)
	if err != nil {
		t.Fatalf("MarginToGhostCity (recovering): %v", err)
	}
	if !(r4.MonthsRemaining > r3.MonthsRemaining) {
		t.Errorf("months-remaining did not increase on recovery: decline2=%v, recovering=%v", r3.MonthsRemaining, r4.MonthsRemaining)
	}
}

func TestMarginToGhostCityUnavailableBelowPeakFloor(t *testing.T) {
	api := NewProjectionsAPI()
	provider := newFakeTrendProvider()
	provider.setPeak(30000) // never exceeded the 50,000 floor
	provider.setState(map[int64]float64{0: 1000, 1: 500})
	if err := api.RegisterCurveProvider(CurveKeyGhostCityPopulation, provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	result, err := api.MarginToGhostCity(1)
	if err != nil {
		t.Fatalf("MarginToGhostCity: %v", err)
	}
	if result.Confidence != ConfidenceUnavailable {
		t.Errorf("Confidence = %v, want Unavailable (peak never exceeded 50,000, regardless of current population)", result.Confidence)
	}
}

func TestMarginToGhostCityProviderShapeRejected(t *testing.T) {
	api := NewProjectionsAPI()
	// Registered under the reserved key but NOT implementing
	// GhostCityPeakProvider (a plain fakeProvider has no HistoricPeak).
	if err := api.RegisterCurveProvider(CurveKeyGhostCityPopulation, fakeProvider{def: 1}); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	_, err := api.MarginToGhostCity(0)
	assertCode(t, err, ErrGhostCityProviderShape)
}

// --- AC-19: WarningLedger records the crossing, not a permanent state ----

func TestWarningLedgerRecordsCrossingNotPermanentState(t *testing.T) {
	api := NewProjectionsAPI()
	provider := newFakeTrendProvider()
	if err := api.RegisterCurveProvider(CurveKeyFinanceInsolvencyRisk, provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}

	// Healthy at month 5 — no crossing.
	provider.setState(map[int64]float64{4: 0, 5: 0})
	if _, err := api.MarginToInsolvency(5); err != nil {
		t.Fatalf("MarginToInsolvency(5): %v", err)
	}

	ledger, err := api.WarningLedger()
	if err != nil {
		t.Fatalf("WarningLedger: %v", err)
	}
	if entries := ledger.Query(MetricMarginToInsolvency, 0, 9); len(entries) != 0 {
		t.Fatalf("ledger has %d entries before any crossing, want 0: %+v", len(entries), entries)
	}

	// Worsens enough to cross the (data-sourced, 6-month) insolvency
	// warning threshold at month 10.
	provider.setState(map[int64]float64{9: 0, 10: 1})
	result, err := api.MarginToInsolvency(10)
	if err != nil {
		t.Fatalf("MarginToInsolvency(10): %v", err)
	}

	// A query entirely BEFORE the crossing month still returns nothing.
	if entries := ledger.Query(MetricMarginToInsolvency, 0, 9); len(entries) != 0 {
		t.Errorf("ledger has %d entries for the pre-crossing range [0,9], want 0: %+v", len(entries), entries)
	}

	entries := ledger.Query(MetricMarginToInsolvency, 10, 10)
	if len(entries) != 1 {
		t.Fatalf("ledger has %d entries at the crossing month 10, want exactly 1: %+v", len(entries), entries)
	}
	if entries[0].Metric != MetricMarginToInsolvency {
		t.Errorf("entry.Metric = %v, want MetricMarginToInsolvency", entries[0].Metric)
	}
	if entries[0].Margin != result.MonthsRemaining {
		t.Errorf("entry.Margin = %v, want %v (the margin at the moment of crossing)", entries[0].Margin, result.MonthsRemaining)
	}

	// Querying again at the SAME already-crossed state must not add a
	// second entry (the false-pass risk this AC calls out: an "always
	// warning" ledger would defeat the lead-time proof).
	if _, err := api.MarginToInsolvency(10); err != nil {
		t.Fatalf("MarginToInsolvency(10) second call: %v", err)
	}
	if entries := ledger.Query(MetricMarginToInsolvency, 10, 10); len(entries) != 1 {
		t.Errorf("ledger has %d entries after a second query in the same crossed state, want still exactly 1 (no duplicate crossing record): %+v", len(entries), entries)
	}
}

// --- AC-20: the warning thresholds are data-sourced with a disclosure ----

func TestDeathWarningDataCarriesPlaceholderDisclosure(t *testing.T) {
	cfg, err := loadDeathWarningConfig(testCorrelationID())
	if err != nil {
		t.Fatalf("loadDeathWarningConfig: %v", err)
	}
	if cfg.Insolvency.Disclosure == "" {
		t.Error("insolvency entry has an empty disclosure field")
	}
	if cfg.GhostCity.Disclosure == "" {
		t.Error("ghostCity entry has an empty disclosure field")
	}
	if cfg.Insolvency.WarningThresholdMonths <= 0 || cfg.GhostCity.WarningThresholdMonths <= 0 {
		t.Errorf("warning thresholds must be positive: insolvency=%v, ghostCity=%v", cfg.Insolvency.WarningThresholdMonths, cfg.GhostCity.WarningThresholdMonths)
	}
	if cfg.Insolvency.MinWarningLeadMonths <= 0 || cfg.GhostCity.MinWarningLeadMonths <= 0 {
		t.Errorf("minimum warning lead times must be positive: insolvency=%v, ghostCity=%v", cfg.Insolvency.MinWarningLeadMonths, cfg.GhostCity.MinWarningLeadMonths)
	}
}

// --- AC-17/AC-18 cross-cut: the warning fires BEFORE death, not at/after it ---

// linearTrendProvider is a CurveProvider (and GhostCityPeakProvider via
// peak) whose Value is a pure linear function of monthIndex, so the
// warning-before-death test below can drive a deterministic worsening
// trend month-by-month and compare, independently, the month the
// WarningLedger first records a crossing against the month the
// documented death-condition threshold is actually reached.
type linearTrendProvider struct {
	intercept float64
	slope     float64
	peak      float64
}

func (p linearTrendProvider) Value(m int64) (float64, error) {
	return p.intercept + p.slope*float64(m), nil
}

func (p linearTrendProvider) HistoricPeak() float64 { return p.peak }

// TestWarningFiresBeforeDeathCondition is the load-bearing proof of
// FEAT-068's six-word requirement ("firing without prior warning =
// defect"), for BOTH death conditions. Each subtest registers a
// monotonically-worsening linear trend, walks it forward month by
// month, and records (a) the first month the WarningLedger shows an
// entry — the warning — and (b) the first month the trend actually
// reaches the consuming module's own documented death-condition
// threshold (engine.finance AC-7's 3-consecutive-month insolvency
// streak; engine.spiral AC-7's 10%-of-historic-peak ghost-city floor).
// The assertion is that (a) strictly precedes (b), and that the margin
// recorded at the warning is still positive (the warning was raised
// while runway remained, not at the moment of death). A regression that
// made the ledger record only once the margin reached zero
// (warn-at-death) would leave (a) unset or set (a) >= (b) and fail here.
func TestWarningFiresBeforeDeathCondition(t *testing.T) {
	t.Run("insolvency", func(t *testing.T) {
		api := NewProjectionsAPI()
		// Streak grows 0.25/month; engine.finance AC-7's death threshold
		// is 3 consecutive missed months => reached at month 12.
		trend := linearTrendProvider{slope: 0.25}
		if err := api.RegisterCurveProvider(CurveKeyFinanceInsolvencyRisk, trend); err != nil {
			t.Fatalf("RegisterCurveProvider: %v", err)
		}
		ledger, err := api.WarningLedger()
		if err != nil {
			t.Fatalf("WarningLedger: %v", err)
		}

		var warningMonth, deathMonth int64 = -1, -1
		for m := int64(1); m <= 40; m++ {
			if _, err := api.MarginToInsolvency(m); err != nil {
				t.Fatalf("MarginToInsolvency(%d): %v", m, err)
			}
			if warningMonth == -1 && len(ledger.Query(MetricMarginToInsolvency, m, m)) > 0 {
				warningMonth = m
			}
			if deathMonth == -1 {
				v, _ := trend.Value(m)
				if v >= insolvencyStreakThreshold {
					deathMonth = m
				}
			}
		}

		assertWarningPrecedesDeath(t, "insolvency", warningMonth, deathMonth)
		entry := ledger.Query(MetricMarginToInsolvency, warningMonth, warningMonth)[0]
		if entry.Margin <= 0 {
			t.Errorf("warning recorded with margin %v — must be strictly positive (warned ahead of death, not at it)", entry.Margin)
		}
	})

	t.Run("ghostCity", func(t *testing.T) {
		api := NewProjectionsAPI()
		// Population declines 5000/month from 100000; engine.spiral AC-7's
		// death threshold is 10% of a 100000 historic peak => 10000,
		// reached at month 18.
		trend := linearTrendProvider{intercept: 100000, slope: -5000, peak: 100000}
		if err := api.RegisterCurveProvider(CurveKeyGhostCityPopulation, trend); err != nil {
			t.Fatalf("RegisterCurveProvider: %v", err)
		}
		ledger, err := api.WarningLedger()
		if err != nil {
			t.Fatalf("WarningLedger: %v", err)
		}

		var warningMonth, deathMonth int64 = -1, -1
		for m := int64(1); m <= 40; m++ {
			if _, err := api.MarginToGhostCity(m); err != nil {
				t.Fatalf("MarginToGhostCity(%d): %v", m, err)
			}
			if warningMonth == -1 && len(ledger.Query(MetricMarginToGhostCity, m, m)) > 0 {
				warningMonth = m
			}
			if deathMonth == -1 {
				v, _ := trend.Value(m)
				if v <= trend.peak*ghostCityPopulationFraction {
					deathMonth = m
				}
			}
		}

		assertWarningPrecedesDeath(t, "ghostCity", warningMonth, deathMonth)
		entry := ledger.Query(MetricMarginToGhostCity, warningMonth, warningMonth)[0]
		if entry.Margin <= 0 {
			t.Errorf("warning recorded with margin %v — must be strictly positive (warned ahead of death, not at it)", entry.Margin)
		}
	})
}

// assertWarningPrecedesDeath is the shared temporal-ordering assertion:
// a warning must have been recorded, the trend must have reached death,
// and the former must come strictly before the latter.
func assertWarningPrecedesDeath(t *testing.T, condition string, warningMonth, deathMonth int64) {
	t.Helper()
	if warningMonth == -1 {
		t.Fatalf("%s: no warning was ever recorded in the ledger", condition)
	}
	if deathMonth == -1 {
		t.Fatalf("%s: the trend never reached the death threshold within the walked range", condition)
	}
	if warningMonth >= deathMonth {
		t.Fatalf("%s: warning fired at month %d but death at month %d — the warning must precede death", condition, warningMonth, deathMonth)
	}
}
