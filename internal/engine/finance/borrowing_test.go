package finance

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// borrowingDataPath walks upward from the test working directory to find
// data/borrowing_instruments.json, so the loader tests run regardless of
// how go test is invoked.
func borrowingDataPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		p := filepath.Join(dir, "data", FileBorrowingInstruments)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("%s not found walking upward from cwd", FileBorrowingInstruments)
	return ""
}

// loadTestTable loads and validates the shipped data file.
func loadTestTable(t *testing.T) InstrumentTable {
	t.Helper()
	table, err := LoadInstrumentTable(borrowingDataPath(t), "borrowing-test")
	if err != nil {
		t.Fatalf("LoadInstrumentTable: %v", err)
	}
	return table
}

// isInstrumentErr reports whether err carries the intended
// ErrInvalidBorrowingInstrument code, and ONLY that code. MET-G211 is
// registered in data/errors.json (via tools/plan/add-error.js), so errs.New
// emits the exact code — a MET-F003 fallback would mean the code had drifted
// out of the registry, and this helper must catch that rather than tolerate
// it (AC-8's GR#7 exact-code assertion).
func isInstrumentErr(err error) bool {
	var e *errs.E
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == ErrInvalidBorrowingInstrument
}

// TestLoanSourceDistinction (AC-1) proves imf and government are distinct
// sources with distinct rate-range data and distinct availability: a
// clean-credit (score 1000), low-debt city can borrow from government but
// not from the IMF lender-of-last-resort source; a distressed city (score
// below the threshold) can.
func TestLoanSourceDistinction(t *testing.T) {
	table := loadTestTable(t)

	gov, ok := table.Sources[LoanSourceGovernment]
	if !ok {
		t.Fatal("government source not registered")
	}
	imf, ok := table.Sources[LoanSourceIMF]
	if !ok {
		t.Fatal("imf source not registered")
	}

	// Distinct rate-range data, not a shared range with a different label.
	if gov.Secured == imf.Secured || gov.Unsecured == imf.Unsecured {
		t.Fatalf("government and imf share identical rate ranges: gov secured=%+v unsecured=%+v, imf secured=%+v unsecured=%+v",
			gov.Secured, gov.Unsecured, imf.Secured, imf.Unsecured)
	}

	clean := CreditScore(1000)
	if !table.SourceAvailable(LoanSourceGovernment, clean) {
		t.Error("government should be available at clean credit")
	}
	if table.SourceAvailable(LoanSourceIMF, clean) {
		t.Error("imf (lender of last resort) must not be available at clean credit")
	}

	// Distressed (score below the data-defined threshold) unlocks the IMF.
	distressed := CreditScore(200)
	if !table.SourceAvailable(LoanSourceIMF, distressed) {
		t.Error("imf should be available below the credit-score threshold")
	}
	if !table.SourceAvailable(LoanSourceGovernment, distressed) {
		t.Error("government should remain available in distress")
	}

	// Availability is a real, testable rule — the IMF threshold is data-defined.
	if gov.Availability.Mode != AvailabilityAlways {
		t.Errorf("government availability mode = %q, want %q", gov.Availability.Mode, AvailabilityAlways)
	}
	if imf.Availability.Mode != AvailabilityBelowCreditScore {
		t.Errorf("imf availability mode = %q, want %q", imf.Availability.Mode, AvailabilityBelowCreditScore)
	}
}

// TestSecuredCheaperThanUnsecured (AC-2) proves a secured instrument's
// data-defined rate floor is strictly below the unsecured floor at the
// same source, and that the resolved rate at the same credit score is
// strictly lower.
func TestSecuredCheaperThanUnsecured(t *testing.T) {
	table := loadTestTable(t)

	for _, src := range []LoanSource{LoanSourceGovernment, LoanSourceIMF} {
		sec, ok := table.RateRangeFor(src, SecuritySecured)
		if !ok {
			t.Fatalf("no secured range for %s", src)
		}
		unsec, ok := table.RateRangeFor(src, SecurityUnsecured)
		if !ok {
			t.Fatalf("no unsecured range for %s", src)
		}
		if sec.MinBp >= unsec.MinBp {
			t.Fatalf("%s secured floor %d >= unsecured floor %d (secured must be strictly cheaper)",
				src, sec.MinBp, unsec.MinBp)
		}

		// Same synthetic credit rating: secured rate strictly below unsecured.
		for _, score := range []CreditScore{1000, 600, 200, 0} {
			if sec.RateFor(score) >= unsec.RateFor(score) {
				t.Fatalf("%s at score %d: secured rate %d >= unsecured rate %d",
					src, score, sec.RateFor(score), unsec.RateFor(score))
			}
		}
	}
}

// TestSecuredRequiresCollateral (AC-2/AC-8) proves a claimed-secured
// instrument must carry a collateral reference, and an unsecured one
// carries none.
func TestSecuredRequiresCollateral(t *testing.T) {
	table := loadTestTable(t)
	f := NewFinanceAPI("ac2-collateral")

	_, err := f.BorrowInstrument(table, BorrowingRequest{
		Source: LoanSourceGovernment, Security: SecuritySecured, Collateral: nil,
		Principal: gbp(100), TermMonths: 12,
	})
	if !isInstrumentErr(err) {
		t.Fatalf("secured instrument with no collateral: got %v, want ErrInvalidBorrowingInstrument", err)
	}
	if len(f.MonthlyObligations(nil)) != 0 {
		t.Fatal("rejected instrument must not be partially constructed / registered")
	}

	// A secured instrument with a collateral reference succeeds.
	if _, err := f.BorrowInstrument(table, BorrowingRequest{
		Source: LoanSourceGovernment, Security: SecuritySecured,
		Collateral: &Collateral{Kind: CollateralLand, AssetID: "parcel-1"},
		Principal:  gbp(100), TermMonths: 12,
	}); err != nil {
		t.Fatalf("secured instrument with collateral: %v", err)
	}
}

// TestInstrumentTableDisclosures (AC-3) proves every instrument-taxonomy
// entry in the loaded data file carries a non-empty disclosure field, and
// that no rate figure is hardcoded in the finance package's non-test
// source (the loader test reads the data file; the absence of inline
// rate literals is asserted below).
func TestInstrumentTableDisclosures(t *testing.T) {
	table := loadTestTable(t)

	requireDisclosure := func(field, d string) {
		t.Helper()
		if strings.TrimSpace(d) == "" {
			t.Errorf("%s: empty disclosure (balance-number regime requires a non-empty placeholder/pending-tuning disclosure)", field)
		}
	}

	for src, spec := range table.Sources {
		requireDisclosure("sources."+string(src), spec.Disclosure)
		requireDisclosure("sources."+string(src)+".availability", spec.Availability.Disclosure)
		requireDisclosure("sources."+string(src)+".secured", spec.Secured.Disclosure)
		requireDisclosure("sources."+string(src)+".unsecured", spec.Unsecured.Disclosure)
	}
	requireDisclosure("pfi", table.PFI.Disclosure)

	// The revenue-share bound is data-sourced and within [0, 1].
	if table.RevenueShareMaxPermille < 0 || table.RevenueShareMaxPermille > permilleScale {
		t.Errorf("RevenueShareMaxPermille = %d, want in [0, %d]", table.RevenueShareMaxPermille, permilleScale)
	}
}

// TestNoInlineRateLiterals (AC-3) mechanically proves the finance
// package's non-test source carries no suspicious inline rate literals —
// every rate is a data-file value (GR#15), never a Go constant like a
// decimal percentage.
func TestNoInlineRateLiterals(t *testing.T) {
	for _, name := range []string{"borrowing.go", "borrowing_data.go"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "internal", "engine", "finance", name))
		if err != nil {
			// Fall back to cwd-relative (already in the package dir).
			b, err = os.ReadFile(name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
		}
		src := string(b)
		for _, lit := range []string{"0.0", "0.1", "0.2", "0.3", "0.4", "0.5", "0.6", "0.7", "0.8", "0.9"} {
			if strings.Contains(src, lit) {
				t.Errorf("%s contains inline decimal literal %q — rates must be data-file-sourced (GR#15)", name, lit)
			}
		}
	}
}

// TestRevenueShareLiveRecompute (AC-4) proves the revenue-share repayment
// is recomputed each period from that period's actual revenue base — not
// memoised from origination — and that a zero base yields exactly zero.
func TestRevenueShareLiveRecompute(t *testing.T) {
	terms, err := NewRevenueShareTerms(500, RevenueBaseCity, "") // 50 percent
	if err != nil {
		t.Fatalf("NewRevenueShareTerms: %v", err)
	}

	month1 := terms.Repayment(gbp(1000))
	month2 := terms.Repayment(gbp(2000))
	if month2 != 2*month1 {
		t.Fatalf("repayment not proportional to revenue base: %d (base £1000) vs %d (base £2000)", month1, month2)
	}
	if terms.Repayment(0) != 0 {
		t.Fatalf("zero revenue base must produce zero repayment, got %d", terms.Repayment(0))
	}

	// A facility base requires a facility ID (AC-8).
	if _, err := NewRevenueShareTerms(500, RevenueBaseFacility, ""); !isInstrumentErr(err) {
		t.Fatalf("facility base with no facility ID: got %v, want ErrInvalidBorrowingInstrument", err)
	}
}

// TestPFITradeoff (AC-5) proves the PFI shape: materially lower
// month-of-construction capex versus a conventional build, a recurring
// nonzero unitary charge, a documented minimum term, and an explicit
// lock-in (early-termination penalty while locked, zero after the term).
func TestPFITradeoff(t *testing.T) {
	table := loadTestTable(t)
	totalCapex := gbp(1_000_000)

	fac, err := NewPFIFacility(table.PFI, "hospital-1", totalCapex)
	if err != nil {
		t.Fatalf("NewPFIFacility: %v", err)
	}

	// PFI upfront capex is materially below the conventional full capex.
	if fac.UpfrontCapex >= totalCapex/2 {
		t.Fatalf("PFI upfront capex %d not materially below conventional %d", fac.UpfrontCapex, totalCapex)
	}
	if fac.UnitaryCharge <= 0 {
		t.Fatal("PFI unitary charge must be nonzero")
	}
	if fac.MinimumTermMonths <= 0 {
		t.Fatal("PFI minimum term must be positive")
	}

	// The unitary charge runs for the full documented minimum term.
	for i := 0; i < fac.MinimumTermMonths; i++ {
		if fac.RemainingMonths() <= 0 {
			t.Fatalf("term ended early at month %d of %d", i, fac.MinimumTermMonths)
		}
		fac.AdvanceMonth()
	}
	if fac.RemainingMonths() != 0 {
		t.Fatalf("remaining months = %d after full term, want 0", fac.RemainingMonths())
	}

	// Lock-in: penalty while locked in, zero once the term has run.
	if table.PFI.LockInMode == LockInEarlyTerminationPenaltyMonths {
		early := &PFIFacility{
			UnitaryCharge:                 gbp(10),
			MinimumTermMonths:             120,
			LockInMode:                    LockInEarlyTerminationPenaltyMonths,
			EarlyTerminationPenaltyMonths: 12,
		}
		if early.EarlyTerminationPenalty() <= 0 {
			t.Fatal("early-termination penalty must be nonzero while locked in")
		}
		early.ElapsedMonths = 120
		if early.EarlyTerminationPenalty() != 0 {
			t.Fatal("early-termination penalty must be zero after the minimum term")
		}
	}
}

// TestPFIInsolvencyIntegration (AC-6) proves a PFI facility's unitary
// charge is part of the same obligation set IsInsolvent consumes — not a
// bespoke path — and that 3 months of an unmet obligation with no credit
// fires the existing game-over signal.
func TestPFIInsolvencyIntegration(t *testing.T) {
	table := loadTestTable(t)
	f := NewFinanceAPI("ac6-pfi")

	fac, err := NewPFIFacility(table.PFI, "hospital-1", gbp(1_000_000))
	if err != nil {
		t.Fatalf("NewPFIFacility: %v", err)
	}
	if _, err := f.RegisterPFIFacility(fac); err != nil {
		t.Fatalf("RegisterPFIFacility: %v", err)
	}

	obls := f.MonthlyObligations(nil)
	var charge Money
	for _, o := range obls {
		if o.Kind == "unitary-charge" {
			charge, _ = satAddMoney(charge, o.Amount)
		}
	}
	if charge <= 0 {
		t.Fatal("PFI unitary charge is not present as a nonzero monthly obligation")
	}

	// 3 consecutive months where the obligation cannot be met and no credit
	// is available — computed from the REAL obligation set via
	// RecordMonthResultFromObligations, never a caller-supplied bool. With
	// zero funds and a nonzero unitary charge, obligations are unmet, so the
	// SAME RecordMonthResult mechanism drives the counter. If the PFI charge
	// were missing from MonthlyObligations, obligationsMet would be true and
	// the counter would reset — this assertion is load-bearing (AC-6).
	for i := 0; i < insolvencyMonthsForGameOver; i++ {
		f.RecordMonthResultFromObligations(0, nil, false)
	}
	if !f.IsInsolvent() {
		t.Fatal("PFI obligation must feed the existing insolvency game-over signal")
	}
}

// TestRevenueShareInsolvencyIntegration (AC-6) proves a revenue-share
// facility with a nonzero revenue base that the player nonetheless fails
// to fund still feeds the same insolvency mechanism.
func TestRevenueShareInsolvencyIntegration(t *testing.T) {
	table := loadTestTable(t)
	f := NewFinanceAPI("ac6-rs")

	terms, err := NewRevenueShareTerms(500, RevenueBaseCity, "") // 50 percent
	if err != nil {
		t.Fatalf("NewRevenueShareTerms: %v", err)
	}
	ins, err := f.BorrowInstrument(table, BorrowingRequest{
		Source: LoanSourceGovernment, Security: SecurityUnsecured,
		Principal: 0, TermMonths: 120, RevenueShare: terms,
	})
	if err != nil {
		t.Fatalf("BorrowInstrument: %v", err)
	}

	bases := map[InstrumentID]Money{ins.ID: gbp(1000)}
	obls := f.MonthlyObligations(bases)
	var share Money
	for _, o := range obls {
		if o.Kind == "revenue-share" {
			share, _ = satAddMoney(share, o.Amount)
		}
	}
	if share <= 0 {
		t.Fatal("revenue-share repayment is not present as a nonzero monthly obligation")
	}

	for i := 0; i < insolvencyMonthsForGameOver; i++ {
		f.RecordMonthResultFromObligations(0, bases, false)
	}
	if !f.IsInsolvent() {
		t.Fatal("revenue-share obligation must feed the existing insolvency game-over signal")
	}
}

// TestCreditRatingIncludesInstrumentExposure (AC-7) proves the credit
// rating's debt denominator includes a PFI facility's committed exposure:
// two identical city states differing only by one PFI facility rate
// differently, in the documented direction (more exposure ⇒ same-or-worse,
// never better).
func TestCreditRatingIncludesInstrumentExposure(t *testing.T) {
	table := loadTestTable(t)

	build := func(withPFI bool) *FinanceAPI {
		f := NewFinanceAPI("ac7")
		seedTreasury(t, f, gbp(1_000_000))
		f.BeginMonth(1)
		if _, err := f.PostWages(gbp(10_000)); err != nil {
			t.Fatalf("PostWages: %v", err)
		}
		if _, err := f.CollectTax(TaxRates{IncomeRate: 1000}, gbp(10_000), 0, 0); err != nil {
			t.Fatalf("CollectTax: %v", err)
		}
		if withPFI {
			fac, err := NewPFIFacility(table.PFI, "hospital-1", gbp(1_000_000))
			if err != nil {
				t.Fatalf("NewPFIFacility: %v", err)
			}
			if _, err := f.RegisterPFIFacility(fac); err != nil {
				t.Fatalf("RegisterPFIFacility: %v", err)
			}
		}
		return f
	}

	clean := build(false)
	withPFI := build(true)

	if withPFI.TotalExposure() <= clean.TotalExposure() {
		t.Fatalf("PFI facility must add committed exposure: with=%d without=%d",
			withPFI.TotalExposure(), clean.TotalExposure())
	}
	// STRICT difference, per AC-7 ("asserts the ratings differ in the
	// documented direction"): more committed exposure must strictly LOWER the
	// rating, never leave it equal and never improve it. An equality here is
	// the exact regression a lazy build could ship by dropping
	// totalExposureLocked's PFI exposure from the credit-rating denominator —
	// the earlier same-or-worse assertion was too weak to catch that.
	if withPFI.CreditRatingNow() >= clean.CreditRatingNow() {
		t.Fatalf("more committed exposure must strictly lower the rating: with=%d without=%d",
			withPFI.CreditRatingNow(), clean.CreditRatingNow())
	}
}

// TestMalformedInstrumentRejected (AC-8) proves the four named malformed
// cases are rejected with the registry-sourced instrument-validation code
// and never silently defaulted / partially constructed.
func TestMalformedInstrumentRejected(t *testing.T) {
	table := loadTestTable(t)

	t.Run("missing source", func(t *testing.T) {
		f := NewFinanceAPI("ac8-src")
		if _, err := f.BorrowInstrument(table, BorrowingRequest{
			Source: "", Security: SecurityUnsecured, Principal: gbp(100), TermMonths: 12,
		}); !isInstrumentErr(err) {
			t.Fatalf("missing source: got %v, want ErrInvalidBorrowingInstrument", err)
		}
		if len(f.MonthlyObligations(nil)) != 0 {
			t.Fatal("missing-source instrument must not be registered")
		}
	})

	t.Run("secured without collateral", func(t *testing.T) {
		f := NewFinanceAPI("ac8-coll")
		if _, err := f.BorrowInstrument(table, BorrowingRequest{
			Source: LoanSourceGovernment, Security: SecuritySecured, Collateral: nil,
			Principal: gbp(100), TermMonths: 12,
		}); !isInstrumentErr(err) {
			t.Fatalf("secured without collateral: got %v, want ErrInvalidBorrowingInstrument", err)
		}
		if len(f.MonthlyObligations(nil)) != 0 {
			t.Fatal("rejected secured instrument must not be registered")
		}
	})

	t.Run("revenue share percentage outside [0,1]", func(t *testing.T) {
		if terms, err := NewRevenueShareTerms(permilleScale+1, RevenueBaseCity, ""); !isInstrumentErr(err) {
			t.Fatalf("share permille %d: got %v (%+v), want ErrInvalidBorrowingInstrument",
				permilleScale+1, err, terms)
		}
		if terms, err := NewRevenueShareTerms(-1, RevenueBaseCity, ""); !isInstrumentErr(err) {
			t.Fatalf("negative share permille: got %v (%+v), want ErrInvalidBorrowingInstrument", err, terms)
		}
	})

	t.Run("pfi with no minimum term", func(t *testing.T) {
		spec := table.PFI
		spec.MinimumTermMonths = 0
		if fac, err := NewPFIFacility(spec, "hospital-1", gbp(1_000_000)); !isInstrumentErr(err) {
			t.Fatalf("zero minimum term: got %v (%+v), want ErrInvalidBorrowingInstrument", err, fac)
		}
	})
}

// TestNoWallClock (AC-9) proves the borrowing-instrument source reads no
// wall clock: revenue-share recomputation and PFI accrual are driven by
// the simulation month / injected inputs, never time.Now/time.Since.
func TestNoWallClock(t *testing.T) {
	for _, name := range []string{"borrowing.go", "borrowing_data.go"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "internal", "engine", "finance", name))
		if err != nil {
			b, err = os.ReadFile(name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
		}
		src := string(b)
		for _, banned := range []string{"time.Now", "time.Since", "math/rand", "rand."} {
			if strings.Contains(src, banned) {
				t.Errorf("%s contains %q — borrowing arithmetic must be wall-clock- and RNG-free (AC-9/AC-11)", name, banned)
			}
		}
	}
}

// TestNoFloatMoneyFields (AC-10) proves no borrowing-instrument type has a
// float32/float64 field — every monetary figure is int64 fixed point.
func TestNoFloatMoneyFields(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(RateRange{}),
		reflect.TypeOf(AvailabilitySpec{}),
		reflect.TypeOf(SourceSpec{}),
		reflect.TypeOf(RevenueShareTerms{}),
		reflect.TypeOf(PFISpec{}),
		reflect.TypeOf(PFIFacility{}),
		reflect.TypeOf(BorrowingInstrument{}),
		reflect.TypeOf(Collateral{}),
	}
	for _, typ := range types {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			switch f.Type.Kind() {
			case reflect.Float32, reflect.Float64:
				t.Errorf("%s.%s is %s — borrowing money computation must be int64 fixed point (AC-10)", typ.Name(), f.Name, f.Type.Kind())
			}
		}
	}
}

// TestRevenueShareSaturation (AC-10) proves a pathological revenue base
// saturates to a positive int64 (never wraps negative) and a zero base is
// exactly zero.
func TestRevenueShareSaturation(t *testing.T) {
	terms := RevenueShareTerms{SharePermille: permilleScale} // 100 percent

	if got := terms.Repayment(0); got != 0 {
		t.Fatalf("zero base repayment = %d, want 0", got)
	}
	huge := terms.Repayment(Money(int64(1 << 62)))
	if huge < 0 {
		t.Fatalf("huge base repayment wrapped negative: %d", huge)
	}
	// The result must saturate rather than overflow-wrap into a plausible-but-wrong value.
	if huge < terms.Repayment(gbp(100)) {
		t.Fatalf("huge base repayment %d below a small base's %d (wrap)", huge, terms.Repayment(gbp(100)))
	}
}

// TestBorrowingResolutionDeterministicAndRaceFree (AC-11) proves
// instrument resolution (rate selection, revenue-share recomputation, PFI
// exposure) is a deterministic function of loaded data and injected state:
// the same snapshot resolves to the same exposure regardless of worker
// count, and concurrent registration is race-free under -race.
func TestBorrowingResolutionDeterministicAndRaceFree(t *testing.T) {
	table := loadTestTable(t)

	// Rate selection is a pure function of the score: the same input always
	// resolves to the same output (determinism), and the linear-in-range
	// mapping means perfect credit (1000) pays the floor, the worst credit
	// (0) pays the ceiling, and a mid score lands strictly between them.
	sec := table.Sources[LoanSourceGovernment].Secured
	if a, b := sec.RateFor(600), sec.RateFor(600); a != b {
		t.Fatalf("RateFor must be deterministic for the same score: %d != %d", a, b)
	}
	if sec.RateFor(1000) != sec.MinBp {
		t.Fatalf("perfect credit must pay the range floor: RateFor(1000)=%d minBp=%d", sec.RateFor(1000), sec.MinBp)
	}
	if sec.RateFor(0) != sec.MaxBp {
		t.Fatalf("worst credit must pay the range ceiling: RateFor(0)=%d maxBp=%d", sec.RateFor(0), sec.MaxBp)
	}
	if mid := sec.RateFor(600); mid <= sec.RateFor(1000) || mid >= sec.RateFor(0) {
		t.Fatalf("RateFor must be strictly monotonic over the range: RateFor(1000)=%d RateFor(600)=%d RateFor(0)=%d",
			sec.RateFor(1000), mid, sec.RateFor(0))
	}

	run := func(workers int) (Money, Money) {
		f := NewFinanceAPI("ac11")
		seedTreasury(t, f, gbp(1_000_000))

		const n = 24
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := w; i < n; i += workers {
					fac, err := NewPFIFacility(table.PFI, "hosp", gbp(1000))
					if err != nil {
						t.Errorf("NewPFIFacility: %v", err)
						return
					}
					if _, err := f.RegisterPFIFacility(fac); err != nil {
						t.Errorf("RegisterPFIFacility: %v", err)
						return
					}
					if _, err := f.BorrowInstrument(table, BorrowingRequest{
						Source: LoanSourceGovernment, Security: SecuritySecured,
						Collateral: &Collateral{Kind: CollateralLand},
						Principal:  gbp(500), TermMonths: 120,
					}); err != nil {
						t.Errorf("BorrowInstrument: %v", err)
						return
					}
				}
			}(w)
		}
		wg.Wait()

		return f.TotalExposure(), obligationTotal(f.MonthlyObligations(nil))
	}

	exp1, obl1 := run(1)
	exp6, obl6 := run(6)
	if exp1 != exp6 {
		t.Fatalf("exposure differs across worker counts: 1=%d 6=%d", exp1, exp6)
	}
	if obl1 != obl6 {
		t.Fatalf("obligation total differs across worker counts: 1=%d 6=%d", obl1, obl6)
	}
}

// obligationTotal sums the amounts of an obligation set (test helper).
func obligationTotal(obls []Obligation) Money {
	var total Money
	for _, o := range obls {
		total, _ = satAddMoney(total, o.Amount)
	}
	return total
}
