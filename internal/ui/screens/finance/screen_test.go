package finance

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func TestScreen_CopyDetectedAndRejected(t *testing.T) {
	s := New("corr-test")
	
	// Direct copy
	s2 := *s
	
	err := s2.Subscribe(func(protocol.Command) error { return nil })
	if err == nil {
		t.Fatal("Subscribe on copied Screen returned nil error")
	}
	if !errors.Is(err, &errs.E{Code: ErrScreenCopied}) {
		t.Fatalf("Subscribe error on copy = %v, want ErrScreenCopied", err)
	}
}

func TestScreen_AccessorsRejectCopy(t *testing.T) {
	s := New("corr-test")
	s2 := *s

	if s2.HaveData() {
		t.Error("HaveData on copy returned true")
	}
	if s2.Stale() {
		t.Error("Stale on copy returned true")
	}
	if _, ok := s2.PL(); ok {
		t.Error("PL on copy returned true")
	}
	if _, ok := s2.BalanceSheet(); ok {
		t.Error("BalanceSheet on copy returned true")
	}
	if _, ok := s2.Loans(); ok {
		t.Error("Loans on copy returned true")
	}
	if _, ok := s2.TaxSliders(); ok {
		t.Error("TaxSliders on copy returned true")
	}
	if _, ok := s2.PublicPayroll(); ok {
		t.Error("PublicPayroll on copy returned true")
	}
	if _, ok := s2.Sankey(); ok {
		t.Error("Sankey on copy returned true")
	}
}

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
