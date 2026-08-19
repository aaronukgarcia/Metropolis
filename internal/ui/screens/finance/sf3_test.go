package finance

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func renderPLInto(pl PLView, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(80, 10)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderPL(buf, rect, pl, have, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return buf, rect
}

func renderLoansInto(loans []LoanState, rating int, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(80, 10)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderLoans(buf, rect, loans, rating, have, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return buf, rect
}

func bufsEqual(a, b *core.Buffer, rect core.Rect) bool {
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		for x := rect.X; x < rect.X+rect.W; x++ {
			if a.Get(x, y) != b.Get(x, y) {
				return false
			}
		}
	}
	return true
}

func fullPatch() wirePatch {
	rating := 8
	return wirePatch{
		SchemaVersion: 1,
		PL: &wirePLView{
			Period: "September 2026",
			Revenues: []wirePLItem{
				{Label: "Taxes", ValueMicropounds: 150_000_000},
			},
			Expenses: []wirePLItem{
				{Label: "Opex", ValueMicropounds: 90_000_000},
			},
		},
		CreditRating: &rating,
		Loans: &[]wireLoanState{
			{ID: "l-1", PrincipalMicropounds: 10_000_000, RatePercent: 5.0, TermMonths: 12, NextPaymentMicropounds: 1_000_000},
		},
	}
}

func mustJSON(t *testing.T, v any) []byte {
	bytes, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes
}

func TestSF3_CreditRatingChanges(t *testing.T) {
	base := fullPatch()
	mutated := fullPatch()

	// Differ in exactly one field: creditRating
	newRating := 12
	mutated.CreditRating = &newRating

	sA := New("corr-sf3-a")
	sA.BindSubscription("sub-a")
	sA.ApplyDelta(protocol.Delta{SubscriptionID: "sub-a", Patch: mustJSON(t, base)})

	sB := New("corr-sf3-b")
	sB.BindSubscription("sub-b")
	sB.ApplyDelta(protocol.Delta{SubscriptionID: "sub-b", Patch: mustJSON(t, mutated)})

	plA, _ := sA.PL()
	plB, _ := sB.PL()
	loansA, _ := sA.Loans()
	loansB, _ := sB.Loans()
	ratingA, _ := sA.CreditRating()
	ratingB, _ := sB.CreditRating()

	// Render P&L
	pa, paRect := renderPLInto(plA, true)
	pb, _ := renderPLInto(plB, true)

	// Render Loans
	la, laRect := renderLoansInto(loansA, ratingA, true)
	lb, _ := renderLoansInto(loansB, ratingB, true)

	// a) The change in credit rating must change the loans/rating panel output.
	if bufsEqual(la, lb, laRect) {
		t.Error("loans/credit pane unchanged after mutating credit rating from 8 to 12 (a)")
	}

	// b) The untouched P&L view must remain byte-identical.
	if !bufsEqual(pa, pb, paRect) {
		t.Error("P&L pane changed even though its fields were untouched between the two runs (b)")
	}
}
