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
