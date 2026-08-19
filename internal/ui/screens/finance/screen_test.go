package finance

import (
	"math"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func TestScreen_ApplyDelta(t *testing.T) {
	s := New("corr-delta")
	s.BindSubscription("sub-1")

	patch := `{
		"schemaVersion": 1,
		"pl": {
			"period": "August 2026",
			"revenues": [{"label": "Taxes", "valueMicropounds": 100000000}],
			"expenses": [{"label": "Salaries", "valueMicropounds": 80000000}]
		},
		"balanceSheet": {
			"assets": [{"label": "Cash", "valueMicropounds": 200000000}],
			"liabilities": [],
			"netWorth": 200000000
		},
		"loans": [
			{"id": "loan-1", "principalMicropounds": 50000000, "ratePercent": 4.5, "termMonths": 24, "nextPaymentMicropounds": 2500000}
		],
		"creditRating": 10,
		"creditRatingHistory": [10.0, 9.0, 10.0],
		"taxSliders": [
			{"id": "council-tax", "label": "Council Tax", "value": 1.2, "min": 0.5, "max": 2.0, "step": 0.1, "incidenceDescription": "Residents"}
		],
		"publicPayroll": {
			"wageCostMicropounds": 50000000,
			"taxClawbackMicropounds": 10000000
		},
		"sankey": {
			"bands": [
				{"source": "Exports", "target": "Treasury", "amount": 100000000}
			]
		}
	}`

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(patch),
	})

	if !s.HaveData() {
		t.Fatal("expected HaveData to be true")
	}

	pl, ok := s.PL()
	if !ok || pl.Period != "August 2026" || pl.Revenues[0].Label != "Taxes" {
		t.Errorf("PL state mismatched: ok=%t, val=%+v", ok, pl)
	}

	bs, ok := s.BalanceSheet()
	if !ok || bs.NetWorth != 200000000 || bs.Assets[0].Label != "Cash" {
		t.Errorf("Balance sheet mismatched: ok=%t, val=%+v", ok, bs)
	}

	loans, ok := s.Loans()
	if !ok || len(loans) != 1 || loans[0].ID != "loan-1" {
		t.Errorf("Loans mismatched: ok=%t, val=%+v", ok, loans)
	}

	rating, ok := s.CreditRating()
	if !ok || rating != 10 {
		t.Errorf("Credit rating mismatched: ok=%t, val=%d", ok, rating)
	}

	history, ok := s.CreditRatingHistory()
	if !ok || len(history) != 3 || history[1] != 9.0 {
		t.Errorf("Credit history mismatched: ok=%t, val=%+v", ok, history)
	}

	sliders, ok := s.TaxSliders()
	if !ok || len(sliders) != 1 || sliders[0].ID != "council-tax" {
		t.Errorf("Tax sliders mismatched: ok=%t, val=%+v", ok, sliders)
	}

	payroll, ok := s.PublicPayroll()
	if !ok || payroll.WageCostMicropounds != 50000000 || payroll.TaxClawbackMicropounds != 10000000 {
		t.Errorf("Payroll mismatched: ok=%t, val=%+v", ok, payroll)
	}

	sankey, ok := s.Sankey()
	if !ok || len(sankey.Bands) != 1 || sankey.Bands[0].Source != "Exports" {
		t.Errorf("Sankey mismatched: ok=%t, val=%+v", ok, sankey)
	}
}

func TestDrillTargets_EveryFigureHasASource(t *testing.T) {
	pl := PLView{
		Period:   "Sept",
		Revenues: []PLItem{{Label: "Tax", ValueMicropounds: 100}},
	}
	bs := BalanceSheetView{
		Assets: []BalanceItem{{Label: "Cash", ValueMicropounds: 200}},
	}
	loans := []LoanState{{ID: "l1"}}
	sliders := []TaxSliderState{{ID: "s1"}}
	payroll := PublicPayrollView{WageCostMicropounds: 100}
	sankey := FiscalCircuitView{Bands: []SankeyBand{{Source: "a", Target: "b"}}}

	targets := DrillTargets(pl, bs, loans, sliders, payroll, sankey)
	if len(targets) != 7 {
		t.Errorf("expected 7 drill targets, got %d: %+v", len(targets), targets)
	}
	for _, target := range targets {
		if !target.Valid() {
			t.Errorf("invalid drill target: %+v", target)
		}
	}
}

func TestInputValidation_SetTaxRate(t *testing.T) {
	s := New("corr-val")
	send := func(protocol.Command) error { return nil }

	// Test inputs
	if err := s.SetTaxRate(send, "tax", math.NaN()); err == nil {
		t.Error("SetTaxRate accepted NaN")
	}
	if err := s.SetTaxRate(send, "tax", math.Inf(1)); err == nil {
		t.Error("SetTaxRate accepted +Inf")
	}
	if err := s.SetTaxRate(send, "tax", math.Inf(-1)); err == nil {
		t.Error("SetTaxRate accepted -Inf")
	}
}

func TestInputValidation_BorrowLoan(t *testing.T) {
	s := New("corr-val")
	send := func(protocol.Command) error { return nil }

	if err := s.BorrowLoan(send, -100, 12); err == nil {
		t.Error("BorrowLoan accepted negative principal")
	}
	if err := s.BorrowLoan(send, 0, 12); err == nil {
		t.Error("BorrowLoan accepted zero principal")
	}
}

func TestFIN8_LoanRejectionSurface(t *testing.T) {
	s := New("corr-fin8")
	s.ApplyResult(protocol.CommandResult{
		CorrelationID: "corr-fin8",
		Accepted:      false,
		Error: &protocol.ErrorRef{
			Code:    "MET-V303",
			Display: "Insufficient Credit",
		},
	})

	if got := s.LoanRejectedReason(); got != "Insufficient Credit" {
		t.Errorf("LoanRejectedReason = %q, want %q", got, "Insufficient Credit")
	}
}
