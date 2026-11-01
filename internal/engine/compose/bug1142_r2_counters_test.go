package compose

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-1142 r1 F2 rework: every construction-accrual counter round-trips
// through the compose ledger wire, each with a distinct non-zero value (so a
// dropped or swapped field is caught -- the sweep scenario only exercises the
// external bucket, no builders' merchant exists there).
func TestBUG1142R2_ConstructionCountersRoundTripThroughLedgerWire(t *testing.T) {
	_, comp, _, _ := b1142r1Wire(t)
	st := comp.state
	st.materialsDrawnCumulative = 111
	st.constructionAccrualLocal = 222
	st.constructionAccrualExternal = 333
	st.constructionSettledLocal = 444
	st.constructionSettledExternal = 555
	raw, err := json.Marshal(composeLedgerToWire(st))
	if err != nil {
		t.Fatal(err)
	}
	var w composeLedgerWire
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	st.materialsDrawnCumulative, st.constructionAccrualLocal, st.constructionAccrualExternal = 0, 0, 0
	st.constructionSettledLocal, st.constructionSettledExternal = 0, 0
	composeLedgerFromWire(st, w)
	got := [5]int64{st.materialsDrawnCumulative, st.constructionAccrualLocal, st.constructionAccrualExternal, st.constructionSettledLocal, st.constructionSettledExternal}
	if got != [5]int64{111, 222, 333, 444, 555} {
		t.Fatalf("counters after round trip = %v, want [111 222 333 444 555]", got)
	}
}

// An OLD bundle (no materialsDrawnCumulative key) must re-base the baseline on
// the restored build queue's drawn tonnage, never restart at 0 (which would
// re-charge every prior draw at the next settlement).
func TestBUG1142R2_AbsentCumulativeRebasesOnRestoredQueue(t *testing.T) {
	e, comp, _, _ := b1142r1Wire(t)
	b1142r1BuyAndBuild(t, e, []protocol.CellRef{{X: 0, Y: 0}, {X: 1, Y: 0}})
	if err := e.AdvanceTicks(errs.NewCorrelationID(), 45); err != nil {
		t.Fatal(err)
	}
	st := comp.state
	drawn := st.totalMaterialsDrawn()
	if drawn <= 0 {
		t.Fatalf("precondition: nothing drawn")
	}
	st.materialsDrawnCumulative = 0
	composeLedgerFromWire(st, composeLedgerWire{}) // key absent -> nil pointer
	if st.materialsDrawnCumulative != drawn {
		t.Fatalf("absent cumulative re-based to %d, want the queue's drawn total %d", st.materialsDrawnCumulative, drawn)
	}
}
