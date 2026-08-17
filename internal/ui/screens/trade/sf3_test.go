package trade

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// renderContractsInto renders the contracts pane into a fresh buffer and
// returns the buffer plus the pane rect (label row included) for
// byte-comparison.
func renderContractsInto(contracts []ImportContract, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(80, 6)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 6}
	RenderContracts(buf, rect, contracts, have, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return buf, rect
}

// renderJunctionsInto renders the junction pane into a fresh buffer.
func renderJunctionsInto(junctions []JunctionQueue, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(80, 6)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 6}
	RenderJunctions(buf, rect, junctions, have, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return buf, rect
}

// bufsEqual reports whether two buffers are byte-identical over rect
// (rune and style).
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

// TestSF3_OneContractPriceChanges is this package's instance of the shared
// SF-3 shape: two patches differing in exactly one contract's £/unit price
// must (a) change that contract's rendered row and (b) leave the junction
// queue's rendered output byte-identical — so a screen hardcoding a value,
// computing independently of the subscribed view, or wiring the wrong field
// fails it.
func TestSF3_OneContractPriceChanges(t *testing.T) {
	base := fullPatch()
	mutated := fullPatch()

	// Differ in exactly one field: c-1's pricePerUnitMicropounds. Copy the
	// mutated patch's contracts into a fresh slice first so mutating the
	// copy never touches base's own backing array (a shared-array alias here
	// would mutate base too and make the two patches identical).
	mt := append([]wireContract(nil), (*mutated.Contracts)...)
	mt[0].PricePerUnitMicropounds = 120_000_000 // was 45_000_000
	mutated.Contracts = &mt

	sA := New("corr-sf3-a")
	sA.BindSubscription("sub-a")
	sA.ApplyDelta(protocol.Delta{SubscriptionID: "sub-a", Patch: mustJSON(t, base)})

	sB := New("corr-sf3-b")
	sB.BindSubscription("sub-b")
	sB.ApplyDelta(protocol.Delta{SubscriptionID: "sub-b", Patch: mustJSON(t, mutated)})

	contractsA, _ := sA.Contracts()
	contractsB, _ := sB.Contracts()
	junctionsA, _ := sA.Junctions()
	junctionsB, _ := sB.Junctions()

	ca, caRect := renderContractsInto(contractsA, true)
	cb, _ := renderContractsInto(contractsB, true)
	ja, jaRect := renderJunctionsInto(junctionsA, true)
	jb, _ := renderJunctionsInto(junctionsB, true)

	if bufsEqual(ca, cb, caRect) {
		t.Error("contracts pane unchanged after mutating c-1's price 45_000_000 -> 120_000_000 (a)")
	}
	if !bufsEqual(ja, jb, jaRect) {
		t.Error("junction pane changed even though its field was untouched between the two runs (b)")
	}
}
