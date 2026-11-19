package finance

// FEAT-9995385625: the F2 money columns follow the shared whole-pound style,
// and a nine-digit treasury is never cut at the pane widths the game uses.

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func TestFEAT9995385625_NineDigitPLNotCutAtGameWidths(t *testing.T) {
	big := plPounds(123_485_323)
	prev := plPounds(-98_765_432)
	pl := PLView{
		Period:   "Month 12 vs Month 11",
		Revenues: []PLItem{{Label: "Income tax", ValueMicropounds: big, PrevValueMicropounds: plPtr(prev)}},
		Expenses: []PLItem{{Label: "Public sector wages", ValueMicropounds: plPounds(5_000_000), PrevValueMicropounds: plPtr(plPounds(4_000_000))}},
		CityEconomy: []PLItem{
			{Label: "Household consumption", ValueMicropounds: plPounds(2_090), PrevValueMicropounds: plPtr(plPounds(1_760))},
		},
	}
	net := PLItem{Label: "Net treasury change", ValueMicropounds: -big, PrevValueMicropounds: plPtr(prev)}
	pl.Net = &net
	for _, w := range []int{60, 70, 80} {
		rows := plRender(pl, w, 14)
		frame := strings.Join(rows, "\n")
		for _, want := range []string{"£123,485,323", "-£123,485,323", "£5,000,000"} {
			if !strings.Contains(frame, want) {
				t.Errorf("width %d: %q cut or missing:\n%s", w, want, frame)
			}
		}
		if strings.Contains(frame, ".00") || strings.Contains(frame, "£123485323") {
			t.Errorf("width %d: old money style on screen:\n%s", w, frame)
		}
		// Right-aligned: every figure on a row ends at the same column.
		end := -1
		for _, ln := range rows {
			if !strings.Contains(ln, "£") {
				continue
			}
			n := len([]rune(ln))
			if end == -1 {
				end = n
			}
			if n != end {
				t.Errorf("width %d: money column ragged (%d vs %d): %q", w, n, end, ln)
			}
		}
	}
}

func panelText(w, h int, draw func(buf *core.Buffer, r core.Rect)) string {
	buf := core.NewBuffer(w, h)
	draw(buf, core.Rect{X: 0, Y: 0, W: w, H: h})
	var sb strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := buf.Get(x, y).Rune
			if c == 0 {
				c = ' '
			}
			sb.WriteRune(c)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// Balance sheet, loans, payroll shortfall and public payroll show the same
// style: "£123,485,323", never "£123485323.25".
func TestFEAT9995385625_OtherPanesUseWholePounds(t *testing.T) {
	style := tcell.StyleDefault
	units := int64(123_485_323_250) // 123,485,323 pounds and 25 pence
	bs := BalanceSheetView{
		Assets:      []BalanceItem{{Label: "Treasury", ValueMicropounds: units}},
		Liabilities: []BalanceItem{{Label: "Outstanding Debt", ValueMicropounds: 848_484_000}},
		NetWorth:    -units,
	}
	cases := map[string]struct {
		text string
		want []string
	}{
		"balance": {panelText(60, 14, func(b *core.Buffer, r core.Rect) { RenderBalanceSheet(b, r, bs, true, style) }),
			[]string{"£123,485,323", "£848,484", "Net Worth: -£123,485,323"}},
		"loans": {panelText(100, 8, func(b *core.Buffer, r core.Rect) {
			RenderLoans(b, r, []LoanState{{ID: "L1", PrincipalMicropounds: units, RatePercent: 4.5, TermMonths: 12, NextPaymentMicropounds: 848_484_000}}, 5, nil, "", true, style)
		}), []string{"£123,485,323", "£848,484"}},
		"payroll": {panelText(80, 4, func(b *core.Buffer, r core.Rect) {
			RenderPayrollShortfall(b, r, PayrollShortfallView{Month: 3, AmountMicropounds: units, Months: 1}, true, style)
		}), []string{"£123,485,323"}},
		"public payroll": {panelText(80, 5, func(b *core.Buffer, r core.Rect) {
			RenderPublicPayroll(b, r, PublicPayrollView{WageCostMicropounds: units, TaxClawbackMicropounds: 848_484_000}, true, style)
		}), []string{"Gross Wage Cost: £123,485,323", "Income Tax Clawback: £848,484"}},
	}
	for name, c := range cases {
		for _, want := range c.want {
			if !strings.Contains(c.text, want) {
				t.Errorf("%s: %q missing:\n%s", name, want, c.text)
			}
		}
		if strings.Contains(c.text, "£123485323") || strings.Contains(c.text, ".25") || strings.Contains(c.text, ".00") {
			t.Errorf("%s: old money style on screen:\n%s", name, c.text)
		}
	}
}
