package compose

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	uifinance "github.com/aaronukgarcia/Metropolis/internal/ui/screens/finance"
)

// FEAT-208 increment 2's own end-to-end regression test, mirroring
// services_publish_test.go's identical structure and rationale for the
// second registered view ("f2.finance"). subscribeAndAwaitFirstDelta
// (services_publish_test.go, same package) is reused unchanged.

// TestFinanceViewSubscriptionName_MatchesUIScreenConstant guards
// against the two independently-maintained copies of "f2.finance"
// (this package's financeViewSubscriptionName, and
// ui/screens/finance/wire.go's ViewSubscriptionName) drifting apart —
// GR#20/SF-1 requires them to be independently maintained, not that
// they silently diverge in VALUE.
func TestFinanceViewSubscriptionName_MatchesUIScreenConstant(t *testing.T) {
	if financeViewSubscriptionName != uifinance.ViewSubscriptionName {
		t.Fatalf("financeViewSubscriptionName = %q, want %q (ui.screen.finance's own ViewSubscriptionName)", financeViewSubscriptionName, uifinance.ViewSubscriptionName)
	}
}

// wireFinanceTestEngine builds a real compose.Wire'd engine (default
// deps — engine.finance is always constructed internally by Wire, no
// test-injection seam exists for it, unlike Services/Crime/Leisure/
// Refuse) and a live subscription pump. Returns the engine, transport,
// and a cancel func the caller must defer.
func wireFinanceTestEngine(t *testing.T) (*core.Engine, *protocol.InProcTransport, context.CancelFunc) {
	t.Helper()
	cid := errs.NewCorrelationID()

	e := core.NewEngine()
	if _, err := Wire(e, &Deps{CorrelationID: cid}); err != nil {
		t.Fatalf("Wire: %v", err)
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := e.StartSubscriptionPump(ctx, transport); err != nil {
		cancel()
		t.Fatalf("StartSubscriptionPump: %v", err)
	}
	go func() { _ = e.RunCommandLoop(ctx, transport) }()

	return e, transport, cancel
}

// TestFinanceView_EndToEnd_DeltaMatchesLiveState is increment 2's own
// version of TestServicesView_EndToEnd_DeltaMatchesLiveState: subscribing
// to "f2.finance" against a REAL compose.Wire'd engine succeeds and the
// delivered patch's balanceSheet decodes through ui.screen.finance's own
// real ApplyDelta/BalanceSheet() round trip. BUG-355 seeds the opening
// grant into FinanceAPI at Wire time, so Treasury equals initialTreasury
// (not zero). Shape is still two assets + one liability.
func TestFinanceView_EndToEnd_DeltaMatchesLiveState(t *testing.T) {
	_, transport, cancel := wireFinanceTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uifinance.ViewSubscriptionName)

	// Decode through the REAL ui.screen.finance ApplyDelta/BalanceSheet
	// path (never a hand-rolled re-implementation here) — proves
	// compose's independently-maintained schema copy (finance_publish.go)
	// actually round-trips through the UI side's own decoder.
	scr := uifinance.New(errs.NewCorrelationID())
	id := protocol.SubscriptionID("sub-test")
	scr.BindSubscription(id)
	scr.ApplyDelta(protocol.Delta{SubscriptionID: id, Patch: delta.Patch})

	bs, have := scr.BalanceSheet()
	if !have {
		t.Fatal("ui.screen.finance BalanceSheet() reported have=false after ApplyDelta")
	}
	wantTreasury := testInitialTreasury(t)
	if len(bs.Assets) != 2 {
		t.Fatalf("len(Assets) = %d, want 2 (Treasury, Reserves)", len(bs.Assets))
	}
	if bs.Assets[0].Label != "Treasury" || bs.Assets[0].ValueMicropounds != wantTreasury {
		t.Errorf("Assets[0] = %+v, want {Treasury %d} (BUG-355 opening grant)", bs.Assets[0], wantTreasury)
	}
	if bs.Assets[1].Label != "Reserves" || bs.Assets[1].ValueMicropounds != 0 {
		t.Errorf("Assets[1] = %+v, want {Reserves 0} (no reserve allocation yet)", bs.Assets[1])
	}
	if len(bs.Liabilities) != 1 || bs.Liabilities[0].Label != "Outstanding Debt" || bs.Liabilities[0].ValueMicropounds != 0 {
		t.Errorf("Liabilities = %+v, want [{Outstanding Debt 0}] (no loans outstanding)", bs.Liabilities)
	}
	if bs.NetWorth != wantTreasury {
		t.Errorf("NetWorth = %d, want %d (treasury+0-0)", bs.NetWorth, wantTreasury)
	}

	// AdvanceTicks also signals the pump; driving one more tick and
	// observing a second, Seq==2 delta proves the view keeps publishing
	// on the live pump, not just once at Subscribe time (mirrors the
	// services test's identical proof for the first view).
	if err := transport.SendCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 1},
	}); err != nil {
		t.Fatalf("SendCommand(AdvanceTicks): %v", err)
	}
	select {
	case r := <-transport.Results():
		if !r.Accepted {
			t.Fatalf("AdvanceTicks rejected: %+v", r.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for AdvanceTicks result")
	}
	select {
	case d := <-transport.Deltas():
		if d.Seq != 2 {
			t.Fatalf("second delta Seq = %d, want 2", d.Seq)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the post-AdvanceTicks f2.finance delta")
	}
}

// TestFinanceView_UnsubscribeThenResubscribe_NoStaleDelta_SeqRestarts is
// increment 2's own copy of the services test of the identical name —
// the BUG-283/284-class regression proof, now exercised against the
// SECOND registered view, proving the class does not recur per-view
// either (the real Publish loop's guarantees, subscribe.go §4, are
// per-subscription, not view-specific — but a dedicated proof per view
// is cheap and removes any doubt that f2.finance's own registration
// entry could somehow bypass them).
func TestFinanceView_UnsubscribeThenResubscribe_NoStaleDelta_SeqRestarts(t *testing.T) {
	_, transport, cancel := wireFinanceTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	firstID, _ := subscribeAndAwaitFirstDelta(t, transport, uifinance.ViewSubscriptionName)

	if err := transport.SendCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindUnsubscribe,
		Payload:         protocol.UnsubscribePayload{SubscriptionID: firstID},
	}); err != nil {
		t.Fatalf("SendCommand(Unsubscribe): %v", err)
	}
	select {
	case r := <-transport.Results():
		if !r.Accepted {
			t.Fatalf("Unsubscribe rejected: %+v", r.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Unsubscribe result")
	}

	secondID, firstDeltaOfSecond := subscribeAndAwaitFirstDelta(t, transport, uifinance.ViewSubscriptionName)
	if secondID == firstID {
		t.Fatalf("resubscribe allocated the SAME SubscriptionID (%s) as the unsubscribed one — the allocator must never reuse IDs", secondID)
	}
	if firstDeltaOfSecond.Seq != 1 {
		t.Fatalf("resubscribed SubscriptionID's first delta Seq = %d, want 1", firstDeltaOfSecond.Seq)
	}

	lastSeq := firstDeltaOfSecond.Seq
	for i := 0; i < 3; i++ {
		if err := transport.SendCommand(protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.NewCorrelationID(),
			Kind:            protocol.KindAdvanceTicks,
			Payload:         protocol.AdvanceTicksPayload{N: 1},
		}); err != nil {
			t.Fatalf("SendCommand(AdvanceTicks %d): %v", i, err)
		}
		select {
		case r := <-transport.Results():
			if !r.Accepted {
				t.Fatalf("AdvanceTicks %d rejected: %+v", i, r.Error)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for AdvanceTicks %d result", i)
		}
		select {
		case d := <-transport.Deltas():
			if d.SubscriptionID == firstID {
				t.Fatalf("BUG-283-class regression: received a delta for the UNSUBSCRIBED SubscriptionID %s after Unsubscribe (Seq=%d)", firstID, d.Seq)
			}
			if d.SubscriptionID != secondID {
				t.Fatalf("delta for unexpected SubscriptionID %s, want %s", d.SubscriptionID, secondID)
			}
			if d.Seq <= lastSeq {
				t.Fatalf("BUG-284-class regression: delta Seq = %d, want > previous %d (monotonic per-subscription)", d.Seq, lastSeq)
			}
			lastSeq = d.Seq
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for delta after AdvanceTicks %d", i)
		}
	}
}

// TestFinanceView_PatchIsValidJSON_NeverErrorsOnFreshEngine is a small,
// focused proof of buildFinanceBalanceSheetPatch's own error-free path —
// distinct from the end-to-end test above in that it decodes the raw
// json.RawMessage directly (not through ui.screen.finance), pinning the
// exact wire shape this package's own duplicate schema produces.
func TestFinanceView_PatchIsValidJSON_NeverErrorsOnFreshEngine(t *testing.T) {
	_, transport, cancel := wireFinanceTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uifinance.ViewSubscriptionName)

	var raw struct {
		SchemaVersion int `json:"schemaVersion"`
		BalanceSheet  *struct {
			Assets []struct {
				Label            string `json:"label"`
				ValueMicropounds int64  `json:"valueMicropounds"`
			} `json:"assets"`
			Liabilities []struct {
				Label            string `json:"label"`
				ValueMicropounds int64  `json:"valueMicropounds"`
			} `json:"liabilities"`
			NetWorth int64 `json:"netWorth"`
		} `json:"balanceSheet"`
	}
	if err := json.Unmarshal(delta.Patch, &raw); err != nil {
		t.Fatalf("json.Unmarshal(delta.Patch): %v", err)
	}
	if raw.SchemaVersion != financeWireSchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", raw.SchemaVersion, financeWireSchemaVersion)
	}
	if raw.BalanceSheet == nil {
		t.Fatal("balanceSheet is absent from the wire patch — buildFinanceBalanceSheetPatch always sets this field")
	}
}
