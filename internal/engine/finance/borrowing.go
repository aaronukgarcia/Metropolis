package finance

import (
	"sort"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-057 borrowing-instrument taxonomy (Tier C). This file layers the
// instrument set Aaron's 2026-08-11 design named ("the player borrows at
// DIFFERENT RATES FOR DIFFERENT THINGS") on top of MOD-022's existing
// loan/credit-rating/insolvency mechanics without replacing them and
// without creating a new module (ASM-768):
//
//   - LoanSource: imf (lender-of-last-resort) vs government, each with its
//     own data-defined rate ranges and its own availability rule (AC-1).
//   - Security: secured (carries a collateral reference) vs unsecured,
//     where the secured range's floor is strictly below the unsecured
//     range's at the same source (AC-2).
//   - RevenueShareTerms: a live-computed periodic repayment of
//     percentage × (that period's actual revenue base) (AC-4).
//   - PFIFacility: deferred capex + a recurring UnitaryCharge over a
//     MinimumTermMonths, with an explicit lock-in choice (AC-5).
//
// Every rate range, spread, revenue-share bound and PFI multiplier is a
// data-file placeholder pending Aaron's balance pass (GR#15, the
// balance-number regime): no final number is invented here, and every
// arithmetic path is int64 micro-pounds fixed point with saturating
// helpers — no float32/float64 ever participates in a money computation
// (AC-10). New instruments feed the same MOD-022 machinery as everything
// else: their obligations are included in [FinanceAPI.MonthlyObligations]
// (AC-6) and their outstanding/committed exposure is included in the
// debt/revenue denominator [FinanceAPI.CreditRatingNow] reads (AC-7).
//
// # Known limitation (FEAT-057 r1 REJECT disclosure)
//
// There is NO settlement path for borrowing instruments yet: a
// [BorrowingInstrument]'s Outstanding balance is set at origination and
// never amortised, [FinanceAPI.MonthlyPayment] charges principal
// amortisation without reducing that balance, and a PFI facility's
// committed exposure only runs down as [PFIFacility.AdvanceMonth] walks
// ElapsedMonths toward MinimumTermMonths. Outstanding therefore only grows
// (as new instruments are issued) and the credit-rating debt denominator
// (AC-7) keeps counting the full principal for the instrument's whole
// life. Building a settlement/repayment path is separate BOW-tracked work;
// this disclosure exists so the surface is honest rather than silently
// half-built (see also doc.go's "Borrowing instruments" section).

// permilleScale is the fixed-point denominator for revenue-share
// percentages and PFI capex fractions: 1000 = 100 percent.
const permilleScale int64 = 1000

// LoanSource distinguishes the two borrowing sources FEAT-057 adds.
// The zero value is not a valid source — an instrument must name one of
// the two registered sources, and each source carries its own
// data-defined rate ranges and availability rule (AC-1).
type LoanSource string

const (
	// LoanSourceIMF is the lender-of-last-resort source: available only
	// under data-defined distress (its availability rule gates it below a
	// credit-score threshold), and priced above government lending.
	LoanSourceIMF LoanSource = "imf"

	// LoanSourceGovernment is the ordinary, always-available borrowing
	// source.
	LoanSourceGovernment LoanSource = "government"
)

// Security classifies an instrument as secured (pledged collateral) or
// unsecured (the city's general credit alone) (AC-2).
type Security uint8

const (
	// SecurityUnsecured borrows on the city's general credit alone; it
	// carries no collateral reference.
	SecurityUnsecured Security = iota

	// SecuritySecured pledges collateral (land/facility/revenue stream);
	// it must carry a non-nil collateral reference and earns a strictly
	// lower rate floor than the unsecured instrument at the same source.
	SecuritySecured
)

// CollateralKind names the asset class a secured instrument pledges
// (AC-2): land, a facility, or a revenue stream.
type CollateralKind string

const (
	CollateralLand          CollateralKind = "land"
	CollateralFacility      CollateralKind = "facility"
	CollateralRevenueStream CollateralKind = "revenue-stream"
)

// Collateral is a secured instrument's collateral reference: an asset
// class (Kind) and an optional specific asset ID (AssetID). What happens
// to the asset on default is explicitly out of scope (ASM-413) — this
// field and the rate-preference direction are all AC-2 requires.
type Collateral struct {
	Kind    CollateralKind
	AssetID string
}

// RateRange is a data-defined rate band in basis points (10000 bp = 100
// percent), carrying the placeholder disclosure the balance-number regime
// requires (AC-1/AC-2/AC-3). MinBp/MaxBp are placeholders pending Aaron's
// balance pass; tests assert direction (secured cheaper than unsecured),
// never a pinned figure.
type RateRange struct {
	MinBp      BasisPoints
	MaxBp      BasisPoints
	Disclosure string
}

// RateFor maps a credit score to a rate inside the range, deterministically
// (AC-11): perfect credit (1000) pays MinBp, the worst credit (0) pays
// MaxBp, linear in between. No RNG, no wall clock — the result is a pure
// function of the score and the loaded range.
func (r RateRange) RateFor(score CreditScore) BasisPoints {
	s := clampScore(int64(score))
	spread := int64(r.MaxBp) - int64(r.MinBp)
	if spread < 0 {
		spread = 0
	}
	return BasisPoints(int64(r.MinBp) + mulDiv(spread, int64(creditScoreMax)-s, int64(creditScoreMax)))
}

// AvailabilityMode is the kind of availability rule a source carries
// (AC-1): always available, or gated below a credit-score threshold
// (lender of last resort).
type AvailabilityMode string

const (
	AvailabilityAlways           AvailabilityMode = "always"
	AvailabilityBelowCreditScore AvailabilityMode = "belowCreditScore"
)

// AvailabilitySpec is one source's availability rule (AC-1). For
// AvailabilityBelowCreditScore the source is available only while the
// credit score is strictly below MaxCreditScore; AvailabilityAlways is
// available regardless.
type AvailabilitySpec struct {
	Mode           AvailabilityMode
	MaxCreditScore CreditScore
	Disclosure     string
}

// Available reports whether the source is available at the given credit
// rating (AC-1). A clean-credit, low-debt city (score near 1000) yields
// false for the IMF "belowCreditScore" rule and true for the government
// "always" rule.
func (a AvailabilitySpec) Available(rating CreditScore) bool {
	switch a.Mode {
	case AvailabilityBelowCreditScore:
		return rating < a.MaxCreditScore
	default:
		return true
	}
}

// SourceSpec is one registered borrowing source's full data row (AC-1):
// its name, availability rule, and the secured/unsecured rate ranges.
type SourceSpec struct {
	Name         string
	Disclosure   string
	Availability AvailabilitySpec
	Secured      RateRange
	Unsecured    RateRange
}

// RevenueBaseKind selects which revenue stream a revenue-share instrument
// tracks (AC-4): the whole-budget city revenue (MOD-022's CollectTax
// aggregate) or a single facility's own P&L revenue.
type RevenueBaseKind uint8

const (
	RevenueBaseCity RevenueBaseKind = iota
	RevenueBaseFacility
)

// RevenueShareTerms is a revenue-share repayment structure (AC-4): a
// fixed-point percentage (SharePermille, 1000 = 100 percent) applied to a
// reference revenue base, recomputed every period against that period's
// actual revenue — never fixed at origination.
type RevenueShareTerms struct {
	SharePermille int64
	Base          RevenueBaseKind
	FacilityID    string // non-empty iff Base == RevenueBaseFacility
	Disclosure    string
}

// NewRevenueShareTerms validates and constructs revenue-share terms
// (AC-8): SharePermille must lie in [0, 1000] (i.e. [0, 1]); a facility
// base requires a non-empty FacilityID. Rejects out-of-range input with
// ErrInvalidBorrowingInstrument, never silently defaults to 0 percent.
func NewRevenueShareTerms(sharePermille int64, base RevenueBaseKind, facilityID string) (*RevenueShareTerms, error) {
	cid := errs.NewCorrelationID()
	if sharePermille < 0 || sharePermille > permilleScale {
		return nil, errs.New(ErrInvalidBorrowingInstrument, cid, map[string]any{
			"field": "revenueShare.sharePermille", "value": sharePermille,
			"rule": "must be in [0, 1000] permille",
		})
	}
	if base == RevenueBaseFacility && facilityID == "" {
		return nil, errs.New(ErrInvalidBorrowingInstrument, cid, map[string]any{
			"field": "revenueShare.facilityID", "value": facilityID,
			"rule": "required when base is facility",
		})
	}
	return &RevenueShareTerms{SharePermille: sharePermille, Base: base, FacilityID: facilityID}, nil
}

// Repayment computes the current period's revenue-share repayment as
// percentage × revenueBase, recomputed from this period's actual base
// (AC-4). A zero (or negative, defensively) base yields exactly zero — not
// a negative balance, not principal forgiveness — and a huge base
// saturates to a positive int64 rather than wrapping negative (AC-10).
func (r RevenueShareTerms) Repayment(revenueBase Money) Money {
	if revenueBase <= 0 || r.SharePermille <= 0 {
		return 0
	}
	return Money(mulDiv(int64(revenueBase), r.SharePermille, permilleScale))
}

// Lock-in modes for PFI facilities (AC-5): an early-termination penalty
// expressed as a number of unitary-charge months, or an explicit
// "not modelled" declaration — a real design choice, never a silent gap.
const (
	LockInEarlyTerminationPenaltyMonths = "earlyTerminationPenaltyMonths"
	LockInNotModelled                   = "notModelled"
)

// PFISpec is the data-defined PFI shape (AC-5): a deferred-capex fraction
// (UpfrontCapexFractionPermille), a recurring unitary-charge multiplier
// (UnitaryChargeMonthlyBp of total capex, per month), a minimum contract
// term (MinimumTermMonths), and the lock-in choice. All figures are
// placeholders pending Aaron's balance pass.
type PFISpec struct {
	UpfrontCapexFractionPermille  int64
	UnitaryChargeMonthlyBp        int64
	MinimumTermMonths             int64
	LockInMode                    string
	EarlyTerminationPenaltyMonths int64
	Disclosure                    string
}

// UpfrontCapexFor is the month-of-construction capex a PFI-funded
// facility actually debits: total × UpfrontCapexFractionPermille / 1000
// (little-to-no upfront capex, the classic PFI trade-off, AC-5).
func (p PFISpec) UpfrontCapexFor(total Money) Money {
	if total <= 0 {
		return 0
	}
	return Money(mulDiv(int64(total), p.UpfrontCapexFractionPermille, permilleScale))
}

// UnitaryChargeFor is the recurring monthly unitary charge: total ×
// UnitaryChargeMonthlyBp / 10000 (a service-style charge, not
// principal+interest, AC-5).
func (p PFISpec) UnitaryChargeFor(total Money) Money {
	if total <= 0 {
		return 0
	}
	return Money(mulDiv(int64(total), p.UnitaryChargeMonthlyBp, basisPointScale))
}

// PFIFacility is one PFI-funded facility (AC-5): little/no upfront capex
// (UpfrontCapex) versus the conventional TotalCapex, a recurring
// UnitaryCharge running for MinimumTermMonths, and an explicit lock-in
// (early-termination penalty or "not modelled").
type PFIFacility struct {
	ID                            PFIID
	FacilityID                    string
	TotalCapex                    Money
	UpfrontCapex                  Money
	UnitaryCharge                 Money
	MinimumTermMonths             int
	ElapsedMonths                 int
	LockInMode                    string
	EarlyTerminationPenaltyMonths int
}

// NewPFIFacility validates and constructs a PFI facility from its data
// spec (AC-8): MinimumTermMonths must be positive; a non-positive term is
// rejected with ErrInvalidBorrowingInstrument, never silently defaulted to
// a zero-length contract.
func NewPFIFacility(spec PFISpec, facilityID string, totalCapex Money) (*PFIFacility, error) {
	cid := errs.NewCorrelationID()
	if spec.MinimumTermMonths <= 0 {
		return nil, errs.New(ErrInvalidBorrowingInstrument, cid, map[string]any{
			"field": "pfi.minimumTermMonths", "value": spec.MinimumTermMonths,
			"rule": "must be positive",
		})
	}
	if spec.UpfrontCapexFractionPermille < 0 || spec.UpfrontCapexFractionPermille > permilleScale {
		return nil, errs.New(ErrInvalidBorrowingInstrument, cid, map[string]any{
			"field": "pfi.upfrontCapexFractionPermille", "value": spec.UpfrontCapexFractionPermille,
			"rule": "must be in [0, 1000] permille",
		})
	}
	if spec.UnitaryChargeMonthlyBp < 0 {
		return nil, errs.New(ErrInvalidBorrowingInstrument, cid, map[string]any{
			"field": "pfi.unitaryChargeMonthlyBp", "value": spec.UnitaryChargeMonthlyBp,
			"rule": "must be >= 0",
		})
	}
	return &PFIFacility{
		FacilityID:                    facilityID,
		TotalCapex:                    totalCapex,
		UpfrontCapex:                  spec.UpfrontCapexFor(totalCapex),
		UnitaryCharge:                 spec.UnitaryChargeFor(totalCapex),
		MinimumTermMonths:             int(spec.MinimumTermMonths),
		LockInMode:                    spec.LockInMode,
		EarlyTerminationPenaltyMonths: int(spec.EarlyTerminationPenaltyMonths),
	}, nil
}

// RemainingMonths is the contract term still to run (clamped at zero).
func (f *PFIFacility) RemainingMonths() int {
	r := f.MinimumTermMonths - f.ElapsedMonths
	if r < 0 {
		return 0
	}
	return r
}

// CommittedExposure is the facility's remaining committed unitary-charge
// stream (UnitaryCharge × RemainingMonths), the debt-equivalent exposure
// AC-7 feeds into the credit-rating denominator.
func (f *PFIFacility) CommittedExposure() Money {
	return Money(mulDiv(int64(f.UnitaryCharge), int64(f.RemainingMonths()), 1))
}

// AdvanceMonth advances the facility one contract month.
func (f *PFIFacility) AdvanceMonth() {
	f.ElapsedMonths++
}

// EarlyTerminationPenalty is the cost of exiting the contract before the
// minimum term, as a number of unitary-charge months (LockInMode ==
// LockInEarlyTerminationPenaltyMonths); zero once the term has run, and
// zero for LockInNotModelled (AC-5's explicit lock-in choice).
func (f *PFIFacility) EarlyTerminationPenalty() Money {
	if f.LockInMode != LockInEarlyTerminationPenaltyMonths {
		return 0
	}
	if f.RemainingMonths() <= 0 {
		return 0
	}
	return Money(mulDiv(int64(f.UnitaryCharge), int64(f.EarlyTerminationPenaltyMonths), 1))
}

// InstrumentTable is the loaded, validated view of
// data/borrowing_instruments.json (AC-1/AC-3): the registered sources,
// the revenue-share percentage bound, and the PFI spec.
type InstrumentTable struct {
	Sources                 map[LoanSource]SourceSpec
	RevenueShareMaxPermille int64
	PFI                     PFISpec
}

// SourceAvailable reports whether src is available at the given credit
// rating (AC-1). An unregistered source is unavailable.
func (t InstrumentTable) SourceAvailable(src LoanSource, rating CreditScore) bool {
	s, ok := t.Sources[src]
	if !ok {
		return false
	}
	return s.Availability.Available(rating)
}

// RateRangeFor returns the data-defined rate range for (src, security),
// and whether that source is registered (AC-2).
func (t InstrumentTable) RateRangeFor(src LoanSource, sec Security) (RateRange, bool) {
	s, ok := t.Sources[src]
	if !ok {
		return RateRange{}, false
	}
	if sec == SecuritySecured {
		return s.Secured, true
	}
	return s.Unsecured, true
}

// InstrumentID identifies a borrowing instrument registered in a
// FinanceAPI.
type InstrumentID uint64

// PFIID identifies a PFI facility registered in a FinanceAPI.
type PFIID uint64

// BorrowingInstrument is one issued borrowing instrument (AC-1/AC-2): a
// loan-type instrument (Source × Security × Collateral, optionally a
// RevenueShareTerms repayment structure). Its rate is fixed at
// origination from the data-defined range and the credit score at the
// time. The zero value is not a valid instrument — use BorrowInstrument.
type BorrowingInstrument struct {
	ID           InstrumentID
	Source       LoanSource
	Security     Security
	Collateral   *Collateral
	Principal    Money
	Outstanding  Money
	TermMonths   int
	RateBp       BasisPoints
	RevenueShare *RevenueShareTerms // non-nil for a revenue-share facility
}

// MonthlyPayment returns the instrument's monthly obligation: for a
// revenue-share instrument it is the live-computed repayment (see
// RevenueShareTerms.Repayment, driven by the injected revenue base); for a
// loan instrument it is interest (outstanding × rate ÷ 12) plus
// straight-line principal amortisation — the same shape as
// [Loan.MonthlyPayment].
func (b *BorrowingInstrument) MonthlyPayment(revenueBase Money) Money {
	if b.RevenueShare != nil {
		return b.RevenueShare.Repayment(revenueBase)
	}
	if b.TermMonths <= 0 {
		return 0
	}
	interest := Money(mulDiv(int64(b.Outstanding), int64(b.RateBp), basisPointScale*12))
	principal := b.Outstanding / Money(b.TermMonths)
	total, _ := satAddMoney(interest, principal)
	return total
}

// BorrowingRequest is BorrowInstrument's input (AC-1/AC-2/AC-4).
type BorrowingRequest struct {
	Source       LoanSource
	Security     Security
	Collateral   *Collateral
	Principal    Money
	TermMonths   int
	RevenueShare *RevenueShareTerms // optional: makes this a revenue-share facility
}

// validateInstrumentRequest validates a BorrowingRequest's taxonomy fields
// (AC-8): a missing source, a claimed-secured instrument with no
// collateral reference, and an out-of-range revenue-share percentage are
// all rejected with ErrInvalidBorrowingInstrument naming the offending
// field — never silently defaulted to unsecured/0 percent.
func validateInstrumentRequest(req BorrowingRequest, correlationID string) error {
	if req.Source == "" {
		return errs.New(ErrInvalidBorrowingInstrument, correlationID, map[string]any{
			"field": "source", "rule": "required (imf or government)",
		})
	}
	if req.Security == SecuritySecured && req.Collateral == nil {
		return errs.New(ErrInvalidBorrowingInstrument, correlationID, map[string]any{
			"field": "collateral", "rule": "required when security is secured",
		})
	}
	if req.RevenueShare != nil {
		if req.RevenueShare.SharePermille < 0 || req.RevenueShare.SharePermille > permilleScale {
			return errs.New(ErrInvalidBorrowingInstrument, correlationID, map[string]any{
				"field": "revenueShare.sharePermille", "value": req.RevenueShare.SharePermille,
				"rule": "must be in [0, 1000] permille",
			})
		}
	}
	return nil
}

// BorrowInstrument issues a borrowing instrument under the loaded
// instrument table (AC-1/AC-2): it validates the request, checks the
// source's availability at the current credit rating, fixes the rate from
// the data-defined range, registers the instrument, and (for a positive
// principal) posts the disbursement. Rejects a malformed request with
// ErrInvalidBorrowingInstrument and an unavailable source with
// ErrLoanUnavailable.
func (f *FinanceAPI) BorrowInstrument(table InstrumentTable, req BorrowingRequest) (BorrowingInstrument, error) {
	if err := f.checkNotCopied("BorrowInstrument"); err != nil {
		return BorrowingInstrument{}, err
	}
	if err := validateInstrumentRequest(req, f.correlationID); err != nil {
		return BorrowingInstrument{}, err
	}
	if req.Principal < 0 {
		return BorrowingInstrument{}, errs.New(ErrNegativeAmount, f.correlationID, map[string]any{
			"field": "principal", "amount": int64(req.Principal),
		})
	}
	if req.TermMonths <= 0 {
		return BorrowingInstrument{}, errs.New(ErrInvalidLoanTerms, f.correlationID, map[string]any{
			"field": "termMonths", "value": req.TermMonths, "rule": "must be positive",
		})
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("BorrowInstrument"); err != nil {
		return BorrowingInstrument{}, err
	}

	rating := f.creditScoreLocked()
	if !table.SourceAvailable(req.Source, rating) {
		return BorrowingInstrument{}, errs.New(ErrLoanUnavailable, f.correlationID, map[string]any{
			"source": string(req.Source),
		})
	}
	rr, ok := table.RateRangeFor(req.Source, req.Security)
	if !ok {
		return BorrowingInstrument{}, errs.New(ErrInvalidBorrowingInstrument, f.correlationID, map[string]any{
			"field": "source", "value": string(req.Source), "rule": "not registered in the instrument table",
		})
	}

	ins := &BorrowingInstrument{
		ID:           f.nextInstrumentID,
		Source:       req.Source,
		Security:     req.Security,
		Collateral:   req.Collateral,
		Principal:    req.Principal,
		Outstanding:  req.Principal,
		TermMonths:   req.TermMonths,
		RateBp:       rr.RateFor(rating),
		RevenueShare: req.RevenueShare,
	}
	f.nextInstrumentID++
	f.instruments[ins.ID] = ins

	if req.Principal > 0 {
		f.post(Transaction{
			Description: "borrowing instrument disbursement",
			Entries: []Entry{
				{Account: AcctTreasury, Side: SideCredit, Amount: req.Principal, Category: CatLoan},
				{Account: AcctDebt, Side: SideDebit, Amount: req.Principal, Category: CatLoan},
			},
		}, true)
	}
	return *ins, nil
}

// RegisterPFIFacility registers a PFI facility and posts its deferred
// (upfront) construction capex debit (AC-5). A facility with a
// non-positive MinimumTermMonths is rejected (AC-8).
func (f *FinanceAPI) RegisterPFIFacility(fac *PFIFacility) (PFIID, error) {
	if err := f.checkNotCopied("RegisterPFIFacility"); err != nil {
		return 0, err
	}
	if fac == nil {
		return 0, errs.New(ErrInvalidBorrowingInstrument, f.correlationID, map[string]any{
			"field": "facility", "rule": "must not be nil",
		})
	}
	if fac.MinimumTermMonths <= 0 {
		return 0, errs.New(ErrInvalidBorrowingInstrument, f.correlationID, map[string]any{
			"field": "minimumTermMonths", "value": fac.MinimumTermMonths, "rule": "must be positive",
		})
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.checkNotCopied("RegisterPFIFacility"); err != nil {
		return 0, err
	}

	id := f.nextPFIID
	f.nextPFIID++
	fac.ID = id
	f.pfiFacilities[id] = fac

	if fac.UpfrontCapex > 0 {
		f.post(Transaction{
			Description: "PFI facility construction (deferred capex)",
			Entries: []Entry{
				{Account: AcctTreasury, Side: SideDebit, Amount: fac.UpfrontCapex, Category: CatConstruction},
				{Account: AcctExternal, Side: SideCredit, Amount: fac.UpfrontCapex, Category: CatConstruction},
			},
		}, true)
	}
	return id, nil
}

// Obligation is one periodic obligation the city must meet this month. It
// is the single "obligations that must be met" set MOD-022 AC-7
// (IsInsolvent) consumes — no borrowing instrument is exempt (AC-6).
type Obligation struct {
	Kind   string // "loan", "revenue-share", or "unitary-charge"
	Source string // stable key, e.g. "instrument/1" or "pfi/2"
	Amount Money
}

// MonthlyObligations returns every obligation due this month, in a fixed
// order (sorted by instrument/PFI ID — never map-iteration order, AC-11):
// every loan instrument's amortising payment, every revenue-share
// instrument's live-computed repayment (using revenueBases, missing/zero
// base resolving to zero — AC-4), and every PFI facility's unitary charge
// (AC-5). This is the one set IsInsolvent's "obligations met" signal is
// computed against, so no new instrument can become a silent side door
// around the insolvency death condition (AC-6).
func (f *FinanceAPI) MonthlyObligations(revenueBases map[InstrumentID]Money) []Obligation {
	if err := f.checkNotCopied("MonthlyObligations"); err != nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if err := f.checkNotCopied("MonthlyObligations"); err != nil {
		return nil
	}

	var out []Obligation
	for _, id := range f.sortedInstrumentIDs() {
		ins := f.instruments[id]
		amount := ins.MonthlyPayment(revenueBases[id])
		kind := "loan"
		if ins.RevenueShare != nil {
			kind = "revenue-share"
		}
		out = append(out, Obligation{Kind: kind, Source: instrumentKey(id), Amount: amount})
	}
	for _, id := range f.sortedPFIIDs() {
		fac := f.pfiFacilities[id]
		out = append(out, Obligation{Kind: "unitary-charge", Source: pfiKey(id), Amount: fac.UnitaryCharge})
	}
	return out
}

// ObligationsDue returns the total of every obligation MonthlyObligations
// reports this month (AC-6) — the amount the city must find before it can
// claim "obligations met". It is the single number the production
// insolvency path compares against the city's available funds.
func (f *FinanceAPI) ObligationsDue(revenueBases map[InstrumentID]Money) Money {
	if err := f.checkNotCopied("ObligationsDue"); err != nil {
		return 0
	}
	var total Money
	for _, o := range f.MonthlyObligations(revenueBases) {
		total, _ = satAddMoney(total, o.Amount)
	}
	return total
}

// CanMeetObligations reports whether funds cover every obligation due this
// month (AC-6). It is the production path that turns MonthlyObligations'
// obligation set into the obligationsMet signal RecordMonthResult consumes:
// the set summed here is the identical set IsInsolvent's game-over signal is
// computed against, so no borrowing instrument can be silently excluded from
// the insolvency death condition.
func (f *FinanceAPI) CanMeetObligations(funds Money, revenueBases map[InstrumentID]Money) bool {
	if err := f.checkNotCopied("CanMeetObligations"); err != nil {
		return false
	}
	return f.ObligationsDue(revenueBases) <= funds
}

// TotalExposure returns the total outstanding/committed exposure across
// every instrument type (AC-7): legacy loan principal, borrowing
// instruments' outstanding principal, and each PFI facility's remaining
// committed unitary-charge stream. It is the debt/revenue denominator
// CreditRatingNow feeds to CreditRating.
func (f *FinanceAPI) TotalExposure() Money {
	if err := f.checkNotCopied("TotalExposure"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.totalExposureLocked()
}

// totalExposureLocked is TotalExposure without the lock (f.mu held). It
// sums in sorted order — never map-iteration order (AC-11/AC-14).
func (f *FinanceAPI) totalExposureLocked() Money {
	debt := f.totalDebt
	for _, id := range f.sortedInstrumentIDs() {
		debt, _ = satAddMoney(debt, f.instruments[id].Outstanding)
	}
	for _, id := range f.sortedPFIIDs() {
		debt, _ = satAddMoney(debt, f.pfiFacilities[id].CommittedExposure())
	}
	return debt
}

// sortedInstrumentIDs returns the instrument IDs in ascending order
// (deterministic — never map-iteration order, GR#21).
func (f *FinanceAPI) sortedInstrumentIDs() []InstrumentID {
	ids := make([]InstrumentID, 0, len(f.instruments))
	for id := range f.instruments {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// sortedPFIIDs returns the PFI facility IDs in ascending order
// (deterministic — never map-iteration order, GR#21).
func (f *FinanceAPI) sortedPFIIDs() []PFIID {
	ids := make([]PFIID, 0, len(f.pfiFacilities))
	for id := range f.pfiFacilities {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// instrumentKey renders an instrument's stable obligation key.
func instrumentKey(id InstrumentID) string {
	return "instrument/" + strconv.FormatUint(uint64(id), 10)
}

// pfiKey renders a PFI facility's stable obligation key.
func pfiKey(id PFIID) string {
	return "pfi/" + strconv.FormatUint(uint64(id), 10)
}
