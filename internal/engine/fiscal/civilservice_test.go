package fiscal

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// TestCivilServiceGrossNet asserts the §54 "civil-service truth" pair (AC-4):
// net < gross at any positive income-tax rate and net == gross only at a zero
// income-tax rate, with both figures simultaneously queryable and the clawback
// driven by engine.tax's income-tax instrument (never a baked-in constant).
func TestCivilServiceGrossNet(t *testing.T) {
	f, _, taxAPI := newTestFiscal(t)
	incomeID := incomeInstrumentID(t, taxAPI)

	const gross finance.Money = 1_000_000_000 // £1,000
	if err := f.SetCivilServiceWageBill(gross); err != nil {
		t.Fatalf("SetCivilServiceWageBill: %v", err)
	}

	if got := f.CivilServiceGross(); got != gross {
		t.Fatalf("CivilServiceGross() = %d, want %d", int64(got), int64(gross))
	}

	// Positive income-tax rate: net must be strictly less than gross, and the
	// clawback must be a positive fraction of gross.
	if err := taxAPI.SetRate(incomeID, 20.0); err != nil {
		t.Fatalf("SetRate(20%%): %v", err)
	}
	clawback, err := f.CivilServiceClawback()
	if err != nil {
		t.Fatalf("CivilServiceClawback: %v", err)
	}
	if clawback <= 0 {
		t.Fatalf("CivilServiceClawback() = %d, want > 0 at a 20%% income-tax rate", int64(clawback))
	}
	net, err := f.CivilServiceNet()
	if err != nil {
		t.Fatalf("CivilServiceNet: %v", err)
	}
	if net >= gross {
		t.Errorf("CivilServiceNet() = %d, want < gross %d at a positive income-tax rate", int64(net), int64(gross))
	}
	if net != gross-clawback {
		t.Errorf("CivilServiceNet() = %d, want gross-clawback = %d", int64(net), int64(gross-clawback))
	}

	// Zero income-tax rate: net must equal gross exactly (no clawback).
	if err := taxAPI.SetRate(incomeID, 0.0); err != nil {
		t.Fatalf("SetRate(0%%): %v", err)
	}
	netZero, err := f.CivilServiceNet()
	if err != nil {
		t.Fatalf("CivilServiceNet (0%%): %v", err)
	}
	if netZero != gross {
		t.Errorf("CivilServiceNet() at 0%% rate = %d, want == gross %d", int64(netZero), int64(gross))
	}
}

// TestCivilServiceNegativeBillRejected asserts the wage-bill input boundary
// (GR#16 — money is never negative).
func TestCivilServiceNegativeBillRejected(t *testing.T) {
	f, _, _ := newTestFiscal(t)
	if err := f.SetCivilServiceWageBill(-1); err == nil {
		t.Fatal("SetCivilServiceWageBill(-1) returned nil error, want ErrInvalidInput")
	}
}
