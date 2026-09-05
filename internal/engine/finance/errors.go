package finance

// Registry error codes for engine.finance (MOD-022). Range: G200-G299,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module). The E layer (E000-E999) is fully
// claimed by eleven earlier engine modules; engine.citizens and
// engine.projections hold the first two G-layer blocks (G000-G099,
// G100-G199), so engine.finance is the third G-layer engine claim.
// Checked against data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-G2" internal/ cmd/` before claiming, per BUG-008's
// lesson — no prior MET-G2xx code existed either place. Every code below
// IS registered in data/errors.json with real severity/module/message/
// remedy fields (GR#7); the internal/foundation/errs source-scan test
// guards against drift.
const (
	// ErrUnbalancedTransaction: a transaction whose total debits do not
	// equal its total credits was rejected at post time (AC-12). Never a
	// plug entry, never a partial post — the ledger is unchanged.
	ErrUnbalancedTransaction = "MET-G200"

	// ErrInsufficientFunds: posting a transaction would drive a RoleMoney
	// account's balance below zero and the account has no available credit
	// line to cover it (AC-13). Rejected rather than allowed to go
	// negative unbounded.
	ErrInsufficientFunds = "MET-G201"

	// ErrUnknownAccount: a post or query referenced an AccountID that has
	// not been opened via OpenAccount.
	ErrUnknownAccount = "MET-G202"

	// ErrDuplicateAccount: OpenAccount was called with an AccountID that
	// is already open.
	ErrDuplicateAccount = "MET-G203"

	// ErrCopiedValue: a FinanceAPI method was called on a struct-copied
	// value, not the one NewFinanceAPI constructed (SEC-020-class).
	ErrCopiedValue = "MET-G204"

	// ErrNegativeAmount: a transaction entry, loan principal, investment
	// capex, or settlement amount was negative. Money is never negative.
	ErrNegativeAmount = "MET-G205"

	// ErrLoanUnavailable: Borrow was requested for a milestone tier the
	// injected MilestoneGate has not reached (AC-5).
	ErrLoanUnavailable = "MET-G206"

	// ErrInvalidLoanTerms: a Borrow request carried a non-positive
	// principal, non-positive term, or unknown milestone tier.
	ErrInvalidLoanTerms = "MET-G207"

	// ErrUnknownLoan: RepayLoan/MissPayment was called with a loan id
	// this FinanceAPI never issued.
	ErrUnknownLoan = "MET-G208"

	// ErrInvalidInvestment: an investment programme carried non-positive
	// capex or a non-positive payback horizon (AC-8).
	ErrInvalidInvestment = "MET-G209"

	// ErrInvalidFirm: a SimpleFirm carried a negative revenue or cost
	// input (AC-9).
	ErrInvalidFirm = "MET-G210"

	// ErrOpexDataInvalid: data/opexintegration.json (FEAT-094's OPEX
	// balance data) could not be read or parsed (I/O or JSON-decode
	// failure) — carries a {cause} from the wrapped error. See
	// ErrOpexDataSchema for schema/field-level validation failures,
	// which carry no underlying error to quote as a cause.
	ErrOpexDataInvalid = "MET-G211"

	// ErrOpexDataSchema: data/opexintegration.json (or the
	// METROPOLIS_DATA_DIR resolution) failed schema/field validation —
	// a {field} failed a named {rule}. Split from ErrOpexDataInvalid
	// (BUG/FEAT-094 round finding: 6 of 8 call sites had no {cause} to
	// supply, rendering the literal token "{cause}" to the user and
	// failing TestRenderGate_WholeTreeHasNoLiteralTokens).
	ErrOpexDataSchema = "MET-G216"

	// ErrOpexConfigNotSet: an OPEX-integration method that needs balance
	// data (SetOpexConfig/LoadOpexConfig) was called before it was set.
	ErrOpexConfigNotSet = "MET-G212"

	// ErrMaintenanceDemandNegative: PostMaintenance was called with a
	// negative engineer-day demand figure (AC-11).
	ErrMaintenanceDemandNegative = "MET-G213"

	// ErrMaintenanceFundedNegative: PostMaintenance was called with a
	// negative funded amount (AC-11).
	ErrMaintenanceFundedNegative = "MET-G214"

	// ErrCapexUnclassified: PostCapexSpend was called with a non-positive
	// capital cost — a refit/rebuild with no declared capital cost is not
	// a capital event (AC-11).
	ErrCapexUnclassified = "MET-G215"
	// ErrPrivateWagePayrollShortfall (BUG-548, 2026-09-05; re-minted from
	// the originally-claimed MET-G211 after FEAT-094's fully-verdicted
	// estate landed first and independently claimed MET-G211-MET-G216 in
	// this same package — MET-G217 is the next free code above that
	// block): AcctFirms' working-capital credit line rejected this
	// month's private-sector wage post (PostWagesFromFirms). The
	// monthlyWagesFloor safety net is still guaranteed via a treasury
	// top-up (compose.go's financeHook.ApplyEffect), so households are
	// never left fully unpaid, but a real payroll shortfall occurred and
	// must be USER-VISIBLE (GR#17) — compose.go surfaces this on the
	// news/status feed, not just a log line.
	ErrPrivateWagePayrollShortfall = "MET-G217"

	// ErrModeGateFailed (FEAT-143 round finding P2-B): the injected
	// ModeGate's Unlimited(correlationID) call returned an error --
	// typically the SEC-020 copy-guard tripping on a struct-copied
	// *gameinit.GameInit passed to SetModeGate. unlimitedLocked fails
	// CLOSED toward the stricter mode (Real) whenever this happens
	// (GR#17: the failure must be visible, never silently treated as
	// "Unlimited", which would be the fail-OPEN direction) and records
	// this error via the registry rather than swallowing it, so a
	// composition-root wiring mistake leaves a trace instead of quietly
	// running every session as Unlimited.
	ErrModeGateFailed = "MET-G218"
)
