package finance

import "encoding/json"

// ViewSubscriptionName is the protocol view name this screen subscribes to
// ("f2.finance", §4's finance view slot). The composition root decides
// whether the view is actually served (compose's viewRegistrationOrder);
// this package only owns the wire schema and rendering — it never imports
// internal/engine (GR#20).
const ViewSubscriptionName = "f2.finance"

// wireSchemaVersion is the version stamp every f2.finance patch must carry.
// A patch stamped with any other version is rejected (errUnsupportedSchemaVersion)
// rather than best-effort decoded, so engine/UI schema evolution fails loudly.
const wireSchemaVersion = 1

// maxPatchWireBytes bounds a single delta patch payload (1 MiB). A larger
// patch is rejected before decoding (errPatchTooLarge) so a runaway publisher
// cannot exhaust UI memory through the subscription pump.
const maxPatchWireBytes = 1 << 20 // 1 MiB

// wirePLItem is one labelled profit-and-loss line in micropounds (int64
// micro-pounds everywhere, per the master plan's money convention).
type wirePLItem struct {
	Label            string `json:"label"`
	ValueMicropounds int64  `json:"valueMicropounds"`
}

// wirePLView is the period P&L statement: revenue lines, expense lines and
// the period label they were accumulated over.
type wirePLView struct {
	Period   string       `json:"period"`
	Revenues []wirePLItem `json:"revenues"`
	Expenses []wirePLItem `json:"expenses"`
}

// wireBalanceItem is one labelled balance-sheet line in micropounds.
type wireBalanceItem struct {
	Label            string `json:"label"`
	ValueMicropounds int64  `json:"valueMicropounds"`
}

// wireBalanceSheetView is the balance sheet snapshot: assets, liabilities
// and the derived net worth figure the header renders.
type wireBalanceSheetView struct {
	Assets      []wireBalanceItem `json:"assets"`
	Liabilities []wireBalanceItem `json:"liabilities"`
	NetWorth    int64             `json:"netWorth"`
}

// wirePayrollShortfallView mirrors compose's finance_publish.go's
// financePayrollShortfallView field-for-field (BUG-723). AmountMicropounds
// is the most recently recorded shortfall for Month (0 means the most
// recent recorded month posted its full private wage bill); Months is the
// current consecutive-shortfall streak — 0 exactly when AmountMicropounds
// is 0.
type wirePayrollShortfallView struct {
	Month             int64 `json:"month"`
	AmountMicropounds int64 `json:"amountMicropounds"`
	Months            int   `json:"months"`
}

// wireLoanState mirrors one loan's player-facing state: identity, remaining
// principal, rate, term and the next payment due.
type wireLoanState struct {
	ID                     string  `json:"id"`
	PrincipalMicropounds   int64   `json:"principalMicropounds"`
	RatePercent            float64 `json:"ratePercent"`
	TermMonths             int     `json:"termMonths"`
	NextPaymentMicropounds int64   `json:"nextPaymentMicropounds"`
}

// wireTaxSliderState drives one tax instrument slider: bounds/step for the
// widget, the elasticity curve points for the response preview, and a
// pre-rendered incidence description string from the engine.
type wireTaxSliderState struct {
	ID                    string    `json:"id"`
	Label                 string    `json:"label"`
	Value                 float64   `json:"value"`
	Min                   float64   `json:"min"`
	Max                   float64   `json:"max"`
	Step                  float64   `json:"step"`
	ElasticityCurvePoints []float64 `json:"elasticityCurvePoints,omitempty"`
	IncidenceDescription  string    `json:"incidenceDescription"`
}

// wirePublicPayrollView carries the public-sector wage cost and the tax
// clawback against it (the two figures MOD-034's payroll panel renders).
type wirePublicPayrollView struct {
	WageCostMicropounds    int64 `json:"wageCostMicropounds"`
	TaxClawbackMicropounds int64 `json:"taxClawbackMicropounds"`
}

// wireSankeyBand is one source->target flow band of the fiscal-circuit
// diagram, amount in micropounds.
type wireSankeyBand struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Amount int64  `json:"amount"`
}

// wireFiscalCircuitView is the full set of Sankey bands composing the
// fiscal circuit diagram (households <-> treasury <-> firms/debt service).
type wireFiscalCircuitView struct {
	Bands []wireSankeyBand `json:"bands"`
}

// wirePatch is one f2.finance delta patch. Every section is optional: a nil
// section means "no data for this section on this cycle" and CLEARS the
// screen's have-flag for it (the subscriber must not keep showing stale
// figures the engine stopped publishing). Sections present are applied whole.
type wirePatch struct {
	SchemaVersion       int                    `json:"schemaVersion"`
	PL                  *wirePLView            `json:"pl,omitempty"`
	BalanceSheet        *wireBalanceSheetView  `json:"balanceSheet,omitempty"`
	Loans               *[]wireLoanState       `json:"loans,omitempty"`
	CreditRating        *int                   `json:"creditRating,omitempty"`
	CreditRatingHistory *[]float64             `json:"creditRatingHistory,omitempty"`
	TaxSliders          *[]wireTaxSliderState  `json:"taxSliders,omitempty"`
	PublicPayroll       *wirePublicPayrollView `json:"publicPayroll,omitempty"`
	Sankey              *wireFiscalCircuitView `json:"sankey,omitempty"`

	// UnlimitedMoney is FEAT-143 (mkey feat.gameinit)'s AC-7 signal: true
	// while the session is running in Unlimited Money mode (money is not
	// a constraint -- render the infinite indicator instead of the
	// budget-constrained P&L/balance/budget surfaces), false in Real
	// mode. nil means "no mode signal on this cycle" and clears the
	// screen's have-flag exactly like every other optional section here
	// -- the composition root's f2.finance publisher is expected to
	// publish this on every cycle once wired, mirroring
	// gameinit.GameInit.Unlimited() (never re-derived locally; this UI
	// package never imports internal/engine/gameinit, GR#20).
	UnlimitedMoney *bool `json:"unlimitedMoney,omitempty"`

	// PayrollShortfall is BUG-723's fix: mirrors compose's
	// finance_publish.go's financeBalanceSheetWirePatch.PayrollShortfall
	// field-for-field. nil (never sent, via omitempty) means "no shortfall
	// signal this cycle" and clears the screen's have-flag exactly like
	// every other optional section here.
	PayrollShortfall *wirePayrollShortfallView `json:"payrollShortfall,omitempty"`

	// InsolvencyMonths/Insolvent are BUG-769's fix: mirror compose's
	// finance_publish.go's financeBalanceSheetWirePatch.InsolvencyMonths/
	// .Insolvent field-for-field. FinanceAPI.InsolvencyMonths() (the AC-7
	// consecutive-failed-months counter) and FinanceAPI.IsInsolvent() (the
	// 3-consecutive-months game-over signal) have advanced for real since
	// BUG-759's financeHook.ApplyEffect call site landed, but nothing on
	// the wire ever surfaced either — nil (never sent, via omitempty) means
	// "no signal this cycle" (the Go side's Valid()-copy-guard path,
	// unreachable in production) and clears the screen's have-flag exactly
	// like every other optional section here.
	InsolvencyMonths *int  `json:"insolvencyMonths,omitempty"`
	Insolvent        *bool `json:"insolvent,omitempty"`

	// InsolvencyVerdict is BUG-769's second increment: mirrors compose's
	// finance_publish.go's InsolvencyVerdict field-for-field —
	// engine.spiral's DecayAPI.EvaluateInsolvency verdict as a string
	// ("insolvency" | "none"), read via the now-registered
	// feat.compositionroot -> engine.spiral edge. Distinct from (and
	// expected to always agree with) the plain Insolvent bool above,
	// which is FinanceAPI.IsInsolvent() read directly, never through
	// spiral. nil (never sent, via omitempty) means "no signal this
	// cycle" (the Go side's Valid() copy-guard path, unreachable in
	// production).
	InsolvencyVerdict *string `json:"insolvencyVerdict,omitempty"`
}

// decodeWirePatch validates the size and schema-version envelope, then
// decodes raw into a wirePatch. Rejections are registry-sourced
// (errPatchTooLarge / errUnsupportedSchemaVersion); JSON syntax errors are
// returned verbatim for logMalformed to file under ErrMalformedPatch.
func decodeWirePatch(raw json.RawMessage) (wirePatch, error) {
	if len(raw) > maxPatchWireBytes {
		return wirePatch{}, errPatchTooLarge(len(raw), maxPatchWireBytes)
	}
	var p wirePatch
	if err := json.Unmarshal(raw, &p); err != nil {
		return wirePatch{}, err
	}
	if p.SchemaVersion != wireSchemaVersion {
		return wirePatch{}, errUnsupportedSchemaVersion(p.SchemaVersion)
	}
	return p, nil
}
