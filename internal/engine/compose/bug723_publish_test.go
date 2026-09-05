package compose

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-723: FinanceAPI.RecordPayrollShortfall/PayrollShortfall (BUG-548)
// were set/cleared every month but nothing outside a test ever read
// them — no compose consumer, no protocol delta, no player surface. This
// file proves the fix through the REAL composition and the REAL publish
// path (st.buildFinanceBalanceSheetPatch), not a hand-rolled
// re-implementation: a starved month must produce a non-zero
// payrollShortfall.amountMicropounds/months on the actual wire patch,
// and a recovered month must clear both back to zero.

type bug723PayrollShortfallWire struct {
	SchemaVersion    int `json:"schemaVersion"`
	PayrollShortfall *struct {
		Month             int64 `json:"month"`
		AmountMicropounds int64 `json:"amountMicropounds"`
		Months            int   `json:"months"`
	} `json:"payrollShortfall"`
}

func bug723DecodePatch(t *testing.T, comp *Composition) bug723PayrollShortfallWire {
	t.Helper()
	raw, err := comp.state.buildFinanceBalanceSheetPatch()
	if err != nil {
		t.Fatalf("buildFinanceBalanceSheetPatch: %v", err)
	}
	var w bug723PayrollShortfallWire
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("json.Unmarshal(patch): %v", err)
	}
	if w.SchemaVersion != financeWireSchemaVersion {
		t.Fatalf("schemaVersion = %d, want %d", w.SchemaVersion, financeWireSchemaVersion)
	}
	if w.PayrollShortfall == nil {
		t.Fatal("payrollShortfall is absent from the wire patch — buildFinanceBalanceSheetPatch must always populate it (BUG-723)")
	}
	return w
}

// TestBUG723_PublishedPatch_CleanMonthReportsZero pins the healthy
// baseline: a fresh engine with no forced exhaustion publishes
// payrollShortfall.amountMicropounds == 0 and .months == 0.
func TestBUG723_PublishedPatch_CleanMonthReportsZero(t *testing.T) {
	e, comp := newTestEngine(t, 90210)
	for month := 1; month <= 2; month++ {
		r2Month(t, e)
	}
	w := bug723DecodePatch(t, comp)
	if w.PayrollShortfall.AmountMicropounds != 0 {
		t.Fatalf("clean run: amountMicropounds = %d, want 0", w.PayrollShortfall.AmountMicropounds)
	}
	if w.PayrollShortfall.Months != 0 {
		t.Fatalf("clean run: months = %d, want 0", w.PayrollShortfall.Months)
	}
}

// TestBUG723_PublishedPatch_StarvedThenRecovered_ThroughRealComposition
// is the RED-PROOF: it drives a starved month (r2ExhaustFirms, the same
// attacker helper attack_bug548_reround2_test.go uses), reads the field
// off the REAL published patch, drives a second starved month to prove
// Months climbs (not just a flat non-zero flag), then recovers
// (r2RefillFirms) and proves BOTH fields drop back to exactly zero on
// the very next month.
//
// Before BUG-723's fix, buildFinanceBalanceSheetPatch never called
// st.finance.PayrollShortfall()/PayrollShortfallMonths() at all, so
// payrollShortfall was always nil/absent from the wire patch regardless
// of the underlying FinanceAPI state — reverting the PayrollShortfall
// field addition on financeBalanceSheetWirePatch (or the two accessor
// calls in buildFinanceBalanceSheetPatch) reproduces exactly the
// bug723DecodePatch nil-Fatal above.
func TestBUG723_PublishedPatch_StarvedThenRecovered_ThroughRealComposition(t *testing.T) {
	e, comp := newTestEngine(t, 424242)
	f := comp.state.finance

	// Establish a clean baseline first (mirrors the BUG-548 r2 test's own
	// clean-months-first shape, so the starve below is an observed
	// TRANSITION, not just "whatever a fresh engine happens to read").
	for month := 1; month <= 3; month++ {
		r2Month(t, e)
	}
	if w := bug723DecodePatch(t, comp); w.PayrollShortfall.AmountMicropounds != 0 || w.PayrollShortfall.Months != 0 {
		t.Fatalf("baseline: payrollShortfall = %+v, want zero amount and zero months before any exhaustion", *w.PayrollShortfall)
	}

	// Starve month 1: the published patch must show a positive amount
	// and Months == 1 (the streak just started).
	r2ExhaustFirms(t, f, 1_000)
	r2Month(t, e)
	if _, byFirms, _, _ := r2WageLegs(f); byFirms != 0 {
		t.Fatalf("starved month: firms still paid %d — the drain did not reproduce the exhaustion", byFirms)
	}
	w1 := bug723DecodePatch(t, comp)
	if w1.PayrollShortfall.AmountMicropounds <= 0 {
		t.Fatalf("starved month 1: published amountMicropounds = %d, want > 0", w1.PayrollShortfall.AmountMicropounds)
	}
	if w1.PayrollShortfall.Months != 1 {
		t.Fatalf("starved month 1: published months = %d, want 1 (streak just started)", w1.PayrollShortfall.Months)
	}

	// Starve month 2: the streak must climb, proving Months is a real
	// consecutive-count and not a re-derived boolean.
	r2Month(t, e)
	w2 := bug723DecodePatch(t, comp)
	if w2.PayrollShortfall.AmountMicropounds <= 0 {
		t.Fatalf("starved month 2: published amountMicropounds = %d, want > 0 (failure persists)", w2.PayrollShortfall.AmountMicropounds)
	}
	if w2.PayrollShortfall.Months != 2 {
		t.Fatalf("starved month 2: published months = %d, want 2", w2.PayrollShortfall.Months)
	}

	// Recover: refill firms; the very next month must clear BOTH fields
	// to exactly zero on the published patch.
	r2RefillFirms(t, f, 100*firmsWageCreditLineMicropounds)
	r2Month(t, e)
	if _, byFirms, _, _ := r2WageLegs(f); byFirms <= 0 {
		t.Fatalf("recovered month: firms paid %d despite a refilled balance — fixture did not reproduce recovery", byFirms)
	}
	w3 := bug723DecodePatch(t, comp)
	if w3.PayrollShortfall.AmountMicropounds != 0 {
		t.Fatalf("recovered month: published amountMicropounds = %d, want 0 — the surface must clear on the wire, not just internally", w3.PayrollShortfall.AmountMicropounds)
	}
	if w3.PayrollShortfall.Months != 0 {
		t.Fatalf("recovered month: published months = %d, want 0 — the streak must reset on recovery", w3.PayrollShortfall.Months)
	}
}

// bug723AdvanceMonth drives one month of ticks through the REAL transport
// (protocol.KindAdvanceTicks), waiting for the accepted result — the
// protocol-round-trip equivalent of r2Month's direct engine.AdvanceTicks
// call, used so this test exercises the actual command/result wire path
// as well as the delta path.
func bug723AdvanceMonth(t *testing.T, transport *protocol.InProcTransport) {
	t.Helper()
	if err := transport.SendCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: int64(core.DailyTicksPerMonth)},
	}); err != nil {
		t.Fatalf("SendCommand(AdvanceTicks): %v", err)
	}
	select {
	case r := <-transport.Results():
		if !r.Accepted {
			t.Fatalf("AdvanceTicks rejected: %+v", r.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for AdvanceTicks result")
	}
}

// bug723AwaitDelta drains f2.finance deltas off the wire until one whose
// patch actually carries a populated payrollShortfall (buildFinance-
// BalanceSheetPatch publishes every tick the pump wakes for, so several
// deltas can arrive per AdvanceTicks call; this reads the LAST one
// available within the deadline, which is the one reflecting the month
// that just completed).
func bug723AwaitDelta(t *testing.T, transport *protocol.InProcTransport) bug723PayrollShortfallWire {
	t.Helper()
	var last *protocol.Delta
	deadline := time.After(3 * time.Second)
	for {
		select {
		case d := <-transport.Deltas():
			dd := d
			last = &dd
		case <-deadline:
			if last == nil {
				t.Fatal("timed out waiting for an f2.finance delta")
			}
			var w bug723PayrollShortfallWire
			if err := json.Unmarshal(last.Patch, &w); err != nil {
				t.Fatalf("json.Unmarshal(delta.Patch): %v", err)
			}
			if w.SchemaVersion != financeWireSchemaVersion {
				t.Fatalf("schemaVersion = %d, want %d", w.SchemaVersion, financeWireSchemaVersion)
			}
			if w.PayrollShortfall == nil {
				t.Fatal("payrollShortfall is absent from the delivered protocol delta patch — BUG-723 fix not reaching the wire")
			}
			return w
		}
	}
}

// TestBUG723_ProtocolRoundTrip_PayrollShortfallReachesTheDelta is the
// full protocol round-trip proof (distinct from the two tests above,
// which call buildFinanceBalanceSheetPatch directly in-process): a real
// compose.Wire'd engine, a real subscription pump, a real
// protocol.InProcTransport carrying real JSON-RPC-shaped Delta values —
// the exact path cmd/metroserve's wsserver and the webconsole's
// protocolClient.ts sit on either end of. Drives a starved month over
// the wire and asserts the delivered delta's payrollShortfall is
// populated and non-zero, then drives a recovered month and asserts it
// clears — proving the fix reaches subscribers, not just
// buildFinanceBalanceSheetPatch's own return value.
func TestBUG723_ProtocolRoundTrip_PayrollShortfallReachesTheDelta(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(1729))
	comp, err := Wire(e, &Deps{})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	f := comp.state.finance

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := e.StartSubscriptionPump(ctx, transport); err != nil {
		t.Fatalf("StartSubscriptionPump: %v", err)
	}
	go func() { _ = e.RunCommandLoop(ctx, transport) }()
	defer func() { _ = transport.Close() }()

	_, firstDelta := subscribeAndAwaitFirstDelta(t, transport, financeViewSubscriptionName)
	var baseline bug723PayrollShortfallWire
	if err := json.Unmarshal(firstDelta.Patch, &baseline); err != nil {
		t.Fatalf("json.Unmarshal(firstDelta.Patch): %v", err)
	}
	if baseline.PayrollShortfall == nil || baseline.PayrollShortfall.AmountMicropounds != 0 {
		t.Fatalf("subscribe-time delta payrollShortfall = %+v, want a populated zero baseline", baseline.PayrollShortfall)
	}

	r2ExhaustFirms(t, f, 1_000)
	bug723AdvanceMonth(t, transport)
	starved := bug723AwaitDelta(t, transport)
	if starved.PayrollShortfall.AmountMicropounds <= 0 {
		t.Fatalf("starved month delta: amountMicropounds = %d, want > 0 — BUG-723's fix must reach the delivered protocol delta, not just the in-process patch builder", starved.PayrollShortfall.AmountMicropounds)
	}
	if starved.PayrollShortfall.Months <= 0 {
		t.Fatalf("starved month delta: months = %d, want > 0", starved.PayrollShortfall.Months)
	}

	r2RefillFirms(t, f, 100*firmsWageCreditLineMicropounds)
	bug723AdvanceMonth(t, transport)
	recovered := bug723AwaitDelta(t, transport)
	if recovered.PayrollShortfall.AmountMicropounds != 0 {
		t.Fatalf("recovered month delta: amountMicropounds = %d, want 0", recovered.PayrollShortfall.AmountMicropounds)
	}
	if recovered.PayrollShortfall.Months != 0 {
		t.Fatalf("recovered month delta: months = %d, want 0", recovered.PayrollShortfall.Months)
	}
}
