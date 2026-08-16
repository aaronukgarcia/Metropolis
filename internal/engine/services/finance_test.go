package services

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// --- AC-8: gross wage vs net fiscal cost (§54) ---------------------------

// TestNetFiscalCostBelowGross (AC-8 first arm): for a positive income tax
// rate, NetFiscalCost < GrossWageCost — the "books show gross vs net"
// distinction is a real, queryable difference, not a narrative one.
func TestNetFiscalCostBelowGross(t *testing.T) {
	a := testLoadedAPI(t)
	registerService(t, a, "police-1", ServicePoliceJail, 100, 10, 10)

	gross, err := a.GrossWageCost("police-1")
	if err != nil {
		t.Fatalf("GrossWageCost: %v", err)
	}
	if gross <= 0 {
		t.Fatalf("GrossWageCost = %v, want positive (staffing need × wage placeholder)", gross)
	}

	net, err := a.NetFiscalCost("police-1", finance.BasisPoints(2000)) // 20% income tax
	if err != nil {
		t.Fatalf("NetFiscalCost: %v", err)
	}
	if net >= gross {
		t.Fatalf("NetFiscalCost(20%%) = %v, want < GrossWageCost %v", net, gross)
	}
}

// TestNetFiscalCostTracksTaxRate (AC-8 second arm / false-pass guard): the
// clawback is a LIVE query of the passed income-tax rate, not a baked-in
// constant — holding gross wage fixed and changing the rate changes
// NetFiscalCost by exactly gross × Δrate / 10000.
func TestNetFiscalCostTracksTaxRate(t *testing.T) {
	a := testLoadedAPI(t)
	registerService(t, a, "police-1", ServicePoliceJail, 100, 10, 10)

	gross, err := a.GrossWageCost("police-1")
	if err != nil {
		t.Fatalf("GrossWageCost: %v", err)
	}

	rateA := finance.BasisPoints(2000) // 20%
	rateB := finance.BasisPoints(4000) // 40%
	netA, err := a.NetFiscalCost("police-1", rateA)
	if err != nil {
		t.Fatalf("NetFiscalCost(rateA): %v", err)
	}
	netB, err := a.NetFiscalCost("police-1", rateB)
	if err != nil {
		t.Fatalf("NetFiscalCost(rateB): %v", err)
	}

	// net = gross - gross×rate/10000, so netA - netB = gross×(4000-2000)/10000 = gross×0.20.
	wantDelta := int64(gross) * (int64(rateB) - int64(rateA)) / incomeTaxBasisPointScale
	gotDelta := int64(netA) - int64(netB)
	if gotDelta != wantDelta {
		t.Fatalf("net changed by %v when rate went %d→%d, want %v (clawback is not a live query of the rate)", gotDelta, rateA, rateB, wantDelta)
	}
	if netB >= netA {
		t.Fatalf("higher rate did not lower net: net(20%%) = %v, net(40%%) = %v", netA, netB)
	}
}

// --- Weakness pattern #2: the duplicated basis-point scale ----------------

// TestIncomeTaxBasisPointScaleMatchesFinance proves the duplicated
// incomeTaxBasisPointScale constant still agrees with engine.finance's real
// fixed-point scale, through finance's public CollectTax API (the scale
// itself is unexported, so the drift test exercises the observable
// behaviour: 100% tax on a wage bill must return the whole wage bill).
func TestIncomeTaxBasisPointScaleMatchesFinance(t *testing.T) {
	f := finance.NewFinanceAPI(testCorrelationID())
	if err := f.BeginMonth(1); err != nil {
		t.Fatalf("BeginMonth: %v", err)
	}
	const wages finance.Money = 1_000_000_000 // £1,000 in micro-pounds
	// Seed the households account so the income-tax post (a households
	// debit) can settle: credit households from the outside world.
	if _, err := f.Post(finance.Transaction{
		Description: "seed household wealth",
		Entries: []finance.Entry{
			{Account: finance.AcctHouseholds, Side: finance.SideCredit, Amount: wages, Category: finance.CatWages},
			{Account: finance.AcctExternal, Side: finance.SideDebit, Amount: wages, Category: finance.CatWages},
		},
	}); err != nil {
		t.Fatalf("seed households: %v", err)
	}
	receipts, err := f.CollectTax(finance.TaxRates{
		IncomeRate: finance.BasisPoints(incomeTaxBasisPointScale), // exactly 100% by OUR constant
	}, wages, 0, 0)
	if err != nil {
		t.Fatalf("CollectTax: %v", err)
	}
	if receipts.Income != wages {
		t.Fatalf("100%% income tax by incomeTaxBasisPointScale=%d collected %v, want %v — the duplicated scale has drifted from finance's", incomeTaxBasisPointScale, receipts.Income, wages)
	}
}
