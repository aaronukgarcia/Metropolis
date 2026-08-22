package finance

import "encoding/json"

const ViewSubscriptionName = "f2.finance"
const wireSchemaVersion = 1
const maxPatchWireBytes = 1 << 20 // 1 MiB

type wirePLItem struct {
	Label            string `json:"label"`
	ValueMicropounds int64  `json:"valueMicropounds"`
}

type wirePLView struct {
	Period   string       `json:"period"`
	Revenues []wirePLItem `json:"revenues"`
	Expenses []wirePLItem `json:"expenses"`
}

type wireBalanceItem struct {
	Label            string `json:"label"`
	ValueMicropounds int64  `json:"valueMicropounds"`
}

type wireBalanceSheetView struct {
	Assets      []wireBalanceItem `json:"assets"`
	Liabilities []wireBalanceItem `json:"liabilities"`
	NetWorth    int64             `json:"netWorth"`
}

type wireLoanState struct {
	ID                     string  `json:"id"`
	PrincipalMicropounds   int64   `json:"principalMicropounds"`
	RatePercent            float64 `json:"ratePercent"`
	TermMonths             int     `json:"termMonths"`
	NextPaymentMicropounds int64   `json:"nextPaymentMicropounds"`
}

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

type wirePublicPayrollView struct {
	WageCostMicropounds    int64 `json:"wageCostMicropounds"`
	TaxClawbackMicropounds int64 `json:"taxClawbackMicropounds"`
}

type wireSankeyBand struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Amount int64  `json:"amount"`
}

type wireFiscalCircuitView struct {
	Bands []wireSankeyBand `json:"bands"`
}

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
}

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
