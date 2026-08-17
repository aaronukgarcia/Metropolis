package build

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// renderQueueInto renders the queue pane into a fresh buffer and returns
// the buffer plus the pane rect (label row included) for byte-comparison.
func renderQueueInto(orders []BuildOrder, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(90, 6)
	rect := core.Rect{X: 0, Y: 0, W: 90, H: 6}
	RenderQueue(buf, rect, orders, have, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return buf, rect
}

// renderCatalogueInto renders the catalogue pane into a fresh buffer.
func renderCatalogueInto(entries []CatalogueEntry, have bool) (*core.Buffer, core.Rect) {
	buf := core.NewBuffer(90, 6)
	rect := core.Rect{X: 0, Y: 0, W: 90, H: 6}
	RenderCatalogue(buf, rect, entries, have, widgets.DefaultPalette.Style(widgets.TokenMoney))
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

// TestSF3_OneQueueLeadTimeChanges is this package's instance of the shared
// SF-3 shape: two patches differing in exactly one build order's remaining
// lead time must (a) change that order's rendered row and (b) leave the
// catalogue pane's rendered output byte-identical — so a screen hardcoding
// a value, computing independently of the subscribed view, or wiring the
// wrong field fails it.
func TestSF3_OneQueueLeadTimeChanges(t *testing.T) {
	base := fullPatch()
	mutated := fullPatch()

	mt := append([]wireBuildOrder(nil), (*mutated.Queue)...)
	mt[0].LeadTimeRemaining = 40 // was 15
	mutated.Queue = &mt

	sA := New("corr-sf3-a")
	sA.BindSubscription("sub-a")
	sA.ApplyDelta(protocol.Delta{SubscriptionID: "sub-a", Patch: mustJSON(t, base)})

	sB := New("corr-sf3-b")
	sB.BindSubscription("sub-b")
	sB.ApplyDelta(protocol.Delta{SubscriptionID: "sub-b", Patch: mustJSON(t, mutated)})

	queueA, _ := sA.Queue()
	queueB, _ := sB.Queue()
	catalogueA, _ := sA.Catalogue()
	catalogueB, _ := sB.Catalogue()

	qa, qaRect := renderQueueInto(queueA, true)
	qb, _ := renderQueueInto(queueB, true)
	ca, caRect := renderCatalogueInto(catalogueA, true)
	cb, _ := renderCatalogueInto(catalogueB, true)

	if bufsEqual(qa, qb, qaRect) {
		t.Error("queue pane unchanged after mutating order 1's leadTimeRemaining 15 -> 40 (a)")
	}
	if !bufsEqual(ca, cb, caRect) {
		t.Error("catalogue pane changed even though its field was untouched between the two runs (b)")
	}
}

// TestSF3_OneCatalogueUnlockStateChanges is the same SF-3 shape over the
// catalogue sub-surface: flipping one entry's unlock state must change only
// that pane, leaving the queue pane byte-identical.
func TestSF3_OneCatalogueUnlockStateChanges(t *testing.T) {
	base := fullPatch()
	mutated := fullPatch()

	mt := append([]wireCatalogueEntry(nil), (*mutated.Catalogue)...)
	mt[0].UnlockState = "locked" // was "unlocked"
	mutated.Catalogue = &mt

	sA := New("corr-sf3-c")
	sA.BindSubscription("sub-a")
	sA.ApplyDelta(protocol.Delta{SubscriptionID: "sub-a", Patch: mustJSON(t, base)})

	sB := New("corr-sf3-d")
	sB.BindSubscription("sub-b")
	sB.ApplyDelta(protocol.Delta{SubscriptionID: "sub-b", Patch: mustJSON(t, mutated)})

	queueA, _ := sA.Queue()
	queueB, _ := sB.Queue()
	catalogueA, _ := sA.Catalogue()
	catalogueB, _ := sB.Catalogue()

	qa, qaRect := renderQueueInto(queueA, true)
	qb, _ := renderQueueInto(queueB, true)
	ca, caRect := renderCatalogueInto(catalogueA, true)
	cb, _ := renderCatalogueInto(catalogueB, true)

	if bufsEqual(ca, cb, caRect) {
		t.Error("catalogue pane unchanged after mutating footpath's unlockState unlocked -> locked (a)")
	}
	if !bufsEqual(qa, qb, qaRect) {
		t.Error("queue pane changed even though its field was untouched between the two runs (b)")
	}
}
