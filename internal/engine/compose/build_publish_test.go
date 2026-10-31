package compose

import (
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	uibuild "github.com/aaronukgarcia/Metropolis/internal/ui/screens/build"
	uifinance "github.com/aaronukgarcia/Metropolis/internal/ui/screens/finance"
)

// ui.screen.build (F3) AC-B2/AC-B3: "f3.build" is registered, served by
// the real engine, and a queued build shows up in the published queue.

func TestBuildViewSubscriptionName_MatchesUIScreenConstant(t *testing.T) {
	if buildViewSubscriptionName != uibuild.ViewSubscriptionName {
		t.Fatalf("compose %q != ui.screen.build %q", buildViewSubscriptionName, uibuild.ViewSubscriptionName)
	}
}

func TestBuildView_IsRegistered(t *testing.T) {
	for _, n := range RegisteredViewNames() {
		if n == buildViewSubscriptionName {
			return
		}
	}
	t.Fatalf("%q not in registered view set %v: F3's Subscribe would be rejected", buildViewSubscriptionName, RegisteredViewNames())
}

func sendAndAwait(t *testing.T, transport *protocol.InProcTransport, kind protocol.Kind, payload protocol.CommandPayload) protocol.CommandResult {
	t.Helper()
	if err := transport.SendCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            kind,
		Payload:         payload,
	}); err != nil {
		t.Fatalf("SendCommand(%s): %v", kind, err)
	}
	select {
	case r := <-transport.Results():
		return r
	case <-time.After(5 * time.Second):
		t.Fatalf("hang guard: no result for %s", kind)
	}
	panic("unreachable")
}

// TestBuildView_AC_B2_B3_QueuedBuildVisible drives the real
// engine: the first f3.build delta carries zones/catalogue/empty queue;
// after a real Buy + Build the published queue gains the order. The
// ledger half of AC-B3 is NOT claimed here: builds never post today
// (BUG-1142); the gated attack test covers it when that bug falls.
func TestBuildView_AC_B2_B3_QueuedBuildVisible(t *testing.T) {
	_, transport, cancel := wireFinanceTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	bScr := uibuild.New(errs.NewCorrelationID())
	fScr := uifinance.New(errs.NewCorrelationID())

	// Subscribe both views; each first delta echoes its Subscribe's
	// CorrelationID, which is how its SubscriptionID is learned.
	bCorr, fCorr := protocol.NewCorrelationID(), protocol.NewCorrelationID()
	for _, sc := range []struct {
		corr protocol.CorrelationID
		view string
	}{{bCorr, uibuild.ViewSubscriptionName}, {fCorr, uifinance.ViewSubscriptionName}} {
		if r := sendSubscribe(t, transport, sc.corr, sc.view); !r.Accepted {
			t.Fatalf("Subscribe(%s) rejected: %+v", sc.view, r.Error)
		}
	}
	var bID, fID protocol.SubscriptionID
	for bID == "" || fID == "" {
		select {
		case d := <-transport.Deltas():
			switch d.CorrelationID {
			case bCorr:
				bID = d.SubscriptionID
				bScr.BindSubscription(bID)
				bScr.ApplyDelta(d)
			case fCorr:
				fID = d.SubscriptionID
				fScr.BindSubscription(fID)
				fScr.ApplyDelta(d)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("hang guard: first deltas did not arrive")
		}
	}

	// AC-B2: real engine state on the screen.
	zones, haveZ := bScr.Zones()
	cat, haveC := bScr.Catalogue()
	q0, haveQ := bScr.Queue()
	if !haveZ || !haveC || !haveQ || len(zones) == 0 || len(cat) == 0 {
		t.Fatalf("first f3.build delta: zones=%d(%v) catalogue=%d(%v) queue have=%v", len(zones), haveZ, len(cat), haveC, haveQ)
	}
	if len(q0) != 0 {
		t.Fatalf("fresh engine queue has %d orders, want 0", len(q0))
	}
	if cat[0].Unlock != uibuild.UnlockUnlocked {
		t.Fatalf("catalogue[0] unlock = %v, want unlocked badge", cat[0].Unlock)
	}
	// AC-B3: queue a build for a catalogue entry the engine accepts.
	cell := protocol.CellRef{X: 0, Y: 0}
	if r := sendAndAwait(t, transport, protocol.KindBuy, protocol.BuyPayload{Cell: cell}); !r.Accepted {
		t.Fatalf("Buy rejected: %+v", r.Error)
	}
	if r := sendAndAwait(t, transport, protocol.KindBuild, protocol.BuildPayload{Cell: cell, BuildingType: cat[0].ID}); !r.Accepted {
		t.Fatalf("Build(%s) rejected: %+v", cat[0].ID, r.Error)
	}
	if r := sendAndAwait(t, transport, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 95}); !r.Accepted {
		t.Fatalf("AdvanceTicks rejected: %+v", r.Error)
	}

	// Drain deltas (each command signalled the pump) until the queue shows
	// the order; routed by SubscriptionID. The select timeout is a hang
	// guard only, never a pass condition.
	deadline := time.After(10 * time.Second)
	for {
		if q, _ := bScr.Queue(); len(q) == 1 {
			if q[0].Zone != cat[0].ID || q[0].Cell != cell {
				t.Fatalf("queued order = %+v, want zone %q at %+v", q[0], cat[0].ID, cell)
			}
			break
		}
		select {
		case d := <-transport.Deltas():
			switch d.SubscriptionID {
			case bID:
				bScr.ApplyDelta(d)
			case fID:
				fScr.ApplyDelta(d)
			}
		case <-deadline:
			q, _ := bScr.Queue()
			t.Fatalf("hang guard: queue never showed the built order (len=%d)", len(q))
		}
	}
}
