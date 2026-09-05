package finance

import "testing"

// attackModeGate is the round's own ModeGate double (independent of the
// author's fakeModeGate, whose field it deliberately does not reuse).
type attackModeGate struct{ u bool }

func (g attackModeGate) Unlimited(string) (bool, error) { return g.u, nil }

// TestAttackFEAT143_UnlimitedDoesNotMint is the round's conservation
// attack: an Unlimited-mode session may drive accounts arbitrarily
// negative, but the total money in circulation must be identical to a
// Real-mode control after the SAME balanced transfers. If the overdraft
// bypass minted money (rather than merely permitting a negative
// balance), this reds.
func TestAttackFEAT143_UnlimitedDoesNotMint(t *testing.T) {
	run := func(unlimited bool, n int) (stock Money, posted int) {
		f := NewFinanceAPI("attack-mint")
		if err := f.SetModeGate(attackModeGate{u: unlimited}); err != nil {
			t.Fatalf("SetModeGate: %v", err)
		}
		before := f.TotalMoneyInCirculation()
		for i := 0; i < n; i++ {
			tx := Transaction{
				Description: "opex debit against an empty treasury",
				Entries: []Entry{
					{Account: AcctTreasury, Side: SideDebit, Amount: 1_000_000, Category: CatOpex},
					{Account: AcctFirms, Side: SideCredit, Amount: 1_000_000, Category: CatOpex},
				},
			}
			if _, err := f.Post(tx); err == nil {
				posted++
			}
		}
		after := f.TotalMoneyInCirculation()
		if after != before {
			t.Fatalf("unlimited=%v: money stock moved %d -> %d across purely balanced internal transfers — the mode gate MINTED money", unlimited, before, after)
		}
		return after, posted
	}

	unlimitedStock, unlimitedPosted := run(true, 24)
	realStock, realPosted := run(false, 24)

	if unlimitedStock != realStock {
		t.Fatalf("money stock diverges by mode: unlimited=%d real=%d — an unlimited session must not mint", unlimitedStock, realStock)
	}
	if unlimitedPosted != 24 {
		t.Fatalf("unlimited posted %d/24 transactions, want 24 (the bypass must let every debit through)", unlimitedPosted)
	}
	if realPosted != 0 {
		t.Fatalf("real posted %d/24 transactions, want 0 (an empty treasury with no credit line must reject every debit)", realPosted)
	}
}

// TestAttackFEAT143_UnlimitedFundsGoNegative pins the observable
// consequence of the bypass: the treasury really does go negative (it is
// a genuine gate, not a large starting balance), and 24 months of that
// never advance insolvency.
func TestAttackFEAT143_UnlimitedFundsGoNegative(t *testing.T) {
	f := NewFinanceAPI("attack-negative")
	if err := f.SetModeGate(attackModeGate{u: true}); err != nil {
		t.Fatalf("SetModeGate: %v", err)
	}
	for month := int64(1); month <= 24; month++ {
		if err := f.BeginMonth(month); err != nil {
			t.Fatalf("BeginMonth(%d): %v", month, err)
		}
		tx := Transaction{
			Description: "monthly payroll from an empty treasury",
			Entries: []Entry{
				{Account: AcctTreasury, Side: SideDebit, Amount: 5_000_000, Category: CatOpex},
				{Account: AcctHouseholds, Side: SideCredit, Amount: 5_000_000, Category: CatOpex},
			},
		}
		if _, err := f.Post(tx); err != nil {
			t.Fatalf("month %d: Post failed in Unlimited mode: %v", month, err)
		}
		res := f.RecordMonthResult(false, false)
		if res.ConsecutiveFailedMonths != 0 || res.GameOver {
			t.Fatalf("month %d: insolvency progressed in Unlimited mode: %+v", month, res)
		}
	}
	bal, ok := f.AccountBalance(AcctTreasury)
	if !ok {
		t.Fatalf("AccountBalance(treasury) not found")
	}
	if bal >= 0 {
		t.Fatalf("treasury balance after 24 unfunded months = %d, want negative (the bypass must be a real gate on the same code, not a big balance)", bal)
	}
	if f.IsInsolvent() {
		t.Fatalf("IsInsolvent() = true in Unlimited mode after 24 failing months")
	}
}

// TestAttackFEAT143_UnlimitedCreditRatingStillDowngrades probes AC-2's
// second named surface: "the insolvency/debt-rating triggers are inert
// (InsolvencyMonths never advances, CreditRatingNow never downgrades to
// failure on its own)". Only InsolvencyMonths was gated. This test
// records the ACTUAL behaviour of CreditRatingNow under an Unlimited
// session whose reserves the bypass allowed to go deeply negative.
func TestAttackFEAT143_UnlimitedCreditRatingStillDowngrades(t *testing.T) {
	// Both arms start from an IDENTICAL, well-funded state seeded via
	// postRaw (no validation), so the ONLY difference is the mode gate.
	build := func(unlimited bool) (CreditScore, Money) {
		f := NewFinanceAPI("attack-rating")
		if err := f.SetModeGate(attackModeGate{u: unlimited}); err != nil {
			t.Fatalf("SetModeGate: %v", err)
		}
		if err := f.BeginMonth(1); err != nil {
			t.Fatalf("BeginMonth: %v", err)
		}
		f.postRaw(Transaction{
			Description: "seed reserves (identical in both arms)",
			Entries: []Entry{
				{Account: AcctReserves, Side: SideCredit, Amount: 10_000_000, Category: CatOpex},
			},
		})
		// Live tax revenue so reserveMonths is a real divisor.
		if _, err := f.Post(Transaction{
			Description: "tax revenue",
			Entries: []Entry{
				{Account: AcctTreasury, Side: SideCredit, Amount: 1_000_000, Category: CatTaxIncome},
				{Account: AcctFirms, Side: SideDebit, Amount: 1_000_000, Category: CatTaxIncome},
			},
		}); err != nil {
			// firms.cash is empty; seed it raw and retry so both arms match.
			f.postRaw(Transaction{Entries: []Entry{{Account: AcctFirms, Side: SideCredit, Amount: 1_000_000, Category: CatOpex}}})
			if _, err := f.Post(Transaction{
				Description: "tax revenue",
				Entries: []Entry{
					{Account: AcctTreasury, Side: SideCredit, Amount: 1_000_000, Category: CatTaxIncome},
					{Account: AcctFirms, Side: SideDebit, Amount: 1_000_000, Category: CatTaxIncome},
				},
			}); err != nil {
				t.Fatalf("seeded tax post still failed (unlimited=%v): %v", unlimited, err)
			}
		}
		// The raid: 5x the reserve balance. Real mode MUST reject it;
		// Unlimited mode's overdraft bypass permits it.
		_, _ = f.Post(Transaction{
			Description: "reserve raid",
			Entries: []Entry{
				{Account: AcctReserves, Side: SideDebit, Amount: 50_000_000, Category: CatOpex},
				{Account: AcctFirms, Side: SideCredit, Amount: 50_000_000, Category: CatOpex},
			},
		})
		bal, _ := f.AccountBalance(AcctReserves)
		return f.CreditRatingNow(), bal
	}

	unlimitedScore, unlimitedReserves := build(true)
	realScore, realReserves := build(false)
	t.Logf("FEAT143 attack: CreditRatingNow unlimited=%d (reserves %d) real=%d (reserves %d)", unlimitedScore, unlimitedReserves, realScore, realReserves)
	if unlimitedReserves >= 0 {
		t.Fatalf("probe is vacuous: the unlimited arm's reserves did not go negative (%d)", unlimitedReserves)
	}
	if realReserves < 0 {
		t.Fatalf("probe is vacuous: the real arm's reserves went negative too (%d) — the overdraft check did not reject the raid", realReserves)
	}
	if unlimitedScore < realScore {
		t.Errorf("AC-2 gap: an Unlimited-mode session's credit rating DOWNGRADED to %d, below the identical Real-mode control's %d, purely because the overdraft bypass let its reserves go negative (%d vs %d). AC-2 requires the debt-rating trigger to be inert in Unlimited mode, but only InsolvencyMonths/RecordMonthResult was gated — CreditRatingNow/creditScoreLocked consults no mode gate", unlimitedScore, realScore, unlimitedReserves, realReserves)
	}
}

// TestAttackFEAT143_RealSemanticsIdenticalNilVsRealGate proves the gate
// is inert in Real mode: a FinanceAPI with NO gate and one with an
// explicit real gate behave identically across the whole
// RecordMonthResult truth table and the overdraft path.
func TestAttackFEAT143_RealSemanticsIdenticalNilVsRealGate(t *testing.T) {
	type step struct{ obligationsMet, creditAvailable bool }
	steps := []step{{false, false}, {false, false}, {true, false}, {false, false}, {false, true}, {false, false}, {false, false}, {false, false}}

	runMonths := func(setGate bool) []MonthResult {
		f := NewFinanceAPI("attack-real-parity")
		if setGate {
			if err := f.SetModeGate(attackModeGate{u: false}); err != nil {
				t.Fatalf("SetModeGate: %v", err)
			}
		}
		out := make([]MonthResult, 0, len(steps))
		for _, s := range steps {
			out = append(out, f.RecordMonthResult(s.obligationsMet, s.creditAvailable))
		}
		return out
	}

	nilGate := runMonths(false)
	realGate := runMonths(true)
	for i := range nilGate {
		if nilGate[i] != realGate[i] {
			t.Fatalf("step %d: nil-gate %+v != real-gate %+v — installing an explicit real gate changed behaviour", i, nilGate[i], realGate[i])
		}
	}
	if !nilGate[len(nilGate)-1].GameOver {
		t.Fatalf("control scenario never reached game over; the parity assertion above would be vacuous")
	}
}

// TestAttackFEAT143_ModeGateSwappableAtRuntime records that
// FinanceAPI.SetModeGate has no lock of its own: it can be called
// repeatedly, mid-session, flipping a Real session into an Unlimited one
// and back. AC-3's immutability lives entirely in gameinit; finance's
// injection seam is not itself locked.
func TestAttackFEAT143_ModeGateSwappableAtRuntime(t *testing.T) {
	f := NewFinanceAPI("attack-swap")
	debit := Transaction{
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: 1000, Category: CatOpex},
			{Account: AcctFirms, Side: SideCredit, Amount: 1000, Category: CatOpex},
		},
	}
	if err := f.SetModeGate(attackModeGate{u: false}); err != nil {
		t.Fatalf("SetModeGate(real): %v", err)
	}
	if _, err := f.Post(debit); err == nil {
		t.Fatalf("precondition: real-mode Post should have failed")
	}
	if err := f.SetModeGate(attackModeGate{u: true}); err != nil {
		t.Fatalf("SetModeGate(unlimited): %v", err)
	}
	if _, err := f.Post(debit); err != nil {
		t.Fatalf("after re-gating to unlimited, Post still failed: %v", err)
	}
	if err := f.SetModeGate(nil); err != nil {
		t.Fatalf("SetModeGate(nil): %v", err)
	}
	if _, err := f.Post(debit); err == nil {
		t.Fatalf("after SetModeGate(nil), Post succeeded — nil must read as Real mode")
	}
	t.Log("FEAT143 attack (informational): finance.SetModeGate is re-callable and accepts nil, so the mid-session re-mode block depends entirely on the composition root calling it exactly once with the locked *GameInit")
}
