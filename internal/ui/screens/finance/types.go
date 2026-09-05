package finance

// PLItem represents a single revenue or expense line item in the P&L (FIN-1).
type PLItem struct {
	Label            string `json:"label"`
	ValueMicropounds int64  `json:"valueMicropounds"`
}

// PLView is the full profit and loss view.
type PLView struct {
	Period   string   `json:"period"`
	Revenues []PLItem `json:"revenues"`
	Expenses []PLItem `json:"expenses"`
}

// BalanceItem represents an asset or liability line item in the balance sheet (FIN-2).
type BalanceItem struct {
	Label            string `json:"label"`
	ValueMicropounds int64  `json:"valueMicropounds"`
}

// BalanceSheetView is the full balance sheet view.
type BalanceSheetView struct {
	Assets      []BalanceItem `json:"assets"`
	Liabilities []BalanceItem `json:"liabilities"`
	NetWorth    int64         `json:"netWorth"`
}

// LoanState represents an active loan's details (FIN-3).
type LoanState struct {
	ID                     string  `json:"id"`
	PrincipalMicropounds   int64   `json:"principalMicropounds"`
	RatePercent            float64 `json:"ratePercent"`
	TermMonths             int     `json:"termMonths"`
	NextPaymentMicropounds int64   `json:"nextPaymentMicropounds"`
}

// TaxSliderState represents a taxation parameter control state (FIN-4).
type TaxSliderState struct {
	ID                    string    `json:"id"`
	Label                 string    `json:"label"`
	Value                 float64   `json:"value"`
	Min                   float64   `json:"min"`
	Max                   float64   `json:"max"`
	Step                  float64   `json:"step"`
	ElasticityCurvePoints []float64 `json:"elasticityCurvePoints,omitempty"`
	IncidenceDescription  string    `json:"incidenceDescription"`
}

// PublicPayrollView represents gross-vs-net public payroll truth (FIN-5).
type PublicPayrollView struct {
	WageCostMicropounds    int64 `json:"wageCostMicropounds"`
	TaxClawbackMicropounds int64 `json:"taxClawbackMicropounds"`
}

// SankeyBand represents a single directional flow of money in the Fiscal Circuit (FIN-6).
type SankeyBand struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Amount int64  `json:"amount"`
}

// FiscalCircuitView is the master Sankey view.
type FiscalCircuitView struct {
	Bands []SankeyBand `json:"bands"`
}

// PayrollShortfallView is the player-facing payroll-shortfall status
// surface (BUG-723, GR#17): the amount most recently recorded for Month
// (0 means the most recent recorded month posted its full private wage
// bill) and the current consecutive-shortfall streak in Months (0 exactly
// when AmountMicropounds is 0).
type PayrollShortfallView struct {
	Month             int64
	AmountMicropounds int64
	Months            int
}

// InsolvencyView is the player-facing AC-7 insolvency status surface
// (BUG-769): Months mirrors FinanceAPI.InsolvencyMonths() (the consecutive
// failed-obligations-months counter) and Insolvent mirrors
// FinanceAPI.IsInsolvent() (the 3-consecutive-months game-over signal).
type InsolvencyView struct {
	Months    int
	Insolvent bool

	// Verdict is BUG-769's second increment: engine.spiral's
	// EvaluateInsolvency verdict as a string ("insolvency" | "none") —
	// see wire.go's InsolvencyVerdict field doc comment for the full
	// disclosure. Empty string is a valid, if unusual, decode: it means
	// the wire patch carried InsolvencyMonths/Insolvent but not
	// InsolvencyVerdict (a pre-this-increment server, or a schema drift),
	// distinct from "none" (a real, connected reading of no verdict).
	Verdict string
}
