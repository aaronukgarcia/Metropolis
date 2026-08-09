package stub

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

const testTimeout = 2 * time.Second

// newTestEngine wires a StubEngine to a fresh InProcTransport and starts
// Run in a goroutine, returning the caller-facing protocol.Transport
// (AC-1: driven through the same seam a real engine uses) plus the
// StubEngine itself for white-box assertions. Cleaned up automatically.
func newTestEngine(t *testing.T, opts ...Option) (protocol.Transport, *StubEngine) {
	t.Helper()
	tr := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer,
		protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer,
		protocol.DefaultDeltaBuffer,
	)
	eng, err := NewStubEngine(tr, opts...)
	if err != nil {
		t.Fatalf("NewStubEngine: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = eng.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		_ = tr.Close()
		<-done
	})

	var transport protocol.Transport = tr // AC-1: consumed via the Transport interface
	return transport, eng
}

func send(t *testing.T, tr protocol.Transport, kind protocol.Kind, payload protocol.CommandPayload) protocol.CommandResult {
	t.Helper()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            kind,
		Payload:         payload,
	}
	if err := tr.SendCommand(cmd); err != nil {
		t.Fatalf("SendCommand(%s): %v", kind, err)
	}
	select {
	case r := <-tr.Results():
		if r.CorrelationID != cmd.CorrelationID {
			t.Fatalf("result CorrelationID = %q, want %q", r.CorrelationID, cmd.CorrelationID)
		}
		return r
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for CommandResult to %s", kind)
		return protocol.CommandResult{}
	}
}

func recvDelta(t *testing.T, tr protocol.Transport) protocol.Delta {
	t.Helper()
	select {
	case d := <-tr.Deltas():
		return d
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for a Delta")
		return protocol.Delta{}
	}
}

// wellFormedPayload returns a valid payload for each v1 command Kind, and
// whether that Kind needs an active subscription set up first
// (Unsubscribe).
func wellFormedPayload(kind protocol.Kind, subID protocol.SubscriptionID) protocol.CommandPayload {
	switch kind {
	case protocol.KindAdvanceTicks:
		return protocol.AdvanceTicksPayload{N: 1}
	case protocol.KindSetSpeed:
		return protocol.SetSpeedPayload{Speed: 2}
	case protocol.KindPause:
		return protocol.PausePayload{}
	case protocol.KindResume:
		return protocol.ResumePayload{}
	case protocol.KindSubscribe:
		return protocol.SubscribePayload{ViewName: "f1.viewport"}
	case protocol.KindUnsubscribe:
		return protocol.UnsubscribePayload{SubscriptionID: subID}
	case protocol.KindInspectEntity:
		return protocol.InspectEntityPayload{EntityRef: "citizen:1"}
	case protocol.KindDebug:
		return protocol.DebugPayload{Op: "noop"}
	default:
		panic("wellFormedPayload: unhandled kind " + string(kind))
	}
}

// AC-2: every v1 Command Kind returned by protocol.KnownKinds() is
// handled — a well-formed payload of each kind must produce a
// CommandResult, never the engine's "unsupported kind" rejection
// (codeUnknownKind).
func TestStubEngine_AllKnownKindsHandled(t *testing.T) {
	for _, kind := range protocol.KnownKinds() {
		t.Run(string(kind), func(t *testing.T) {
			tr, _ := newTestEngine(t)

			var subID protocol.SubscriptionID
			if kind == protocol.KindUnsubscribe {
				sr := send(t, tr, protocol.KindSubscribe, wellFormedPayload(protocol.KindSubscribe, ""))
				if !sr.Accepted {
					t.Fatalf("setup Subscribe rejected: %#v", sr.Error)
				}
				d := recvDelta(t, tr)
				subID = d.SubscriptionID
			}

			r := send(t, tr, kind, wellFormedPayload(kind, subID))
			if !r.Accepted {
				t.Fatalf("%s rejected: %#v", kind, r.Error)
			}
			if r.Accepted && r.Error != nil {
				t.Fatalf("%s: Accepted result carries a non-nil Error", kind)
			}
		})
	}
}

// AC-4: AdvanceTicks(n) advances Tick by exactly n, reflected in
// subsequently emitted Delta.Tick values, with no real per-tick
// computation (pure arithmetic).
func TestStubEngine_AdvanceTicks_FakeTicks(t *testing.T) {
	tr, eng := newTestEngine(t)

	sr := send(t, tr, protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "f1.viewport"})
	if !sr.Accepted {
		t.Fatalf("Subscribe rejected: %#v", sr.Error)
	}
	recvDelta(t, tr) // initial full snapshot

	if got := eng.Tick(); got != 0 {
		t.Fatalf("initial Tick = %d, want 0", got)
	}

	r := send(t, tr, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 7})
	if !r.Accepted {
		t.Fatalf("AdvanceTicks rejected: %#v", r.Error)
	}
	if r.Tick != 7 {
		t.Fatalf("CommandResult.Tick = %d, want 7", r.Tick)
	}
	if got := eng.Tick(); got != 7 {
		t.Fatalf("Tick() = %d, want 7", got)
	}

	d := recvDelta(t, tr)
	if d.Tick != 7 {
		t.Fatalf("Delta.Tick = %d, want 7", d.Tick)
	}

	r2 := send(t, tr, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 3})
	if r2.Tick != 10 {
		t.Fatalf("second AdvanceTicks CommandResult.Tick = %d, want 10 (7+3)", r2.Tick)
	}
}

// AC-9: AdvanceTicks with a non-positive N is a well-formed envelope
// (Command.Validate passes) whose payload fails the engine's own
// semantic check — must be rejected with a registry-sourced ErrorRef,
// never accepted and never a panic.
func TestStubEngine_AdvanceTicks_InvalidN_Rejected(t *testing.T) {
	cases := []struct {
		name string
		n    int64
	}{
		{"zero", 0},
		{"negative", -1},
		{"large negative", -100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr, _ := newTestEngine(t)
			r := send(t, tr, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: tc.n})
			if r.Accepted {
				t.Fatalf("AdvanceTicks(N=%d) was accepted, want rejected", tc.n)
			}
			if r.Error == nil || r.Error.Code == "" {
				t.Fatalf("rejected result missing a registry ErrorRef: %#v", r.Error)
			}
		})
	}
}

// SEC-006: AdvanceTicksPayload.N must be bounded the same way
// engine.core.AdvanceTicks bounds it — rejected with a registry-sourced
// ErrorRef naming the offending value and the limit, never silently
// clamped, and the tick counter must not move at all on a rejected call.
func TestStubEngine_AdvanceTicks_UpperBound_Rejected(t *testing.T) {
	cases := []struct {
		name string
		n    int64
	}{
		{"limit+1", maxAdvanceTicksPerCall + 1},
		{"far beyond limit", maxAdvanceTicksPerCall * 1000},
		{"MaxInt64", math.MaxInt64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr, eng := newTestEngine(t)
			r := send(t, tr, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: tc.n})
			if r.Accepted {
				t.Fatalf("AdvanceTicks(N=%d) was accepted, want rejected (limit=%d)", tc.n, maxAdvanceTicksPerCall)
			}
			if r.Error == nil || r.Error.Code == "" {
				t.Fatalf("rejected result missing a registry ErrorRef: %#v", r.Error)
			}
			if r.Error.Code != codeAdvanceTicksOutOfBounds {
				t.Fatalf("rejected result Code = %q, want %q", r.Error.Code, codeAdvanceTicksOutOfBounds)
			}
			if got := eng.Tick(); got != 0 {
				t.Fatalf("Tick() after rejected AdvanceTicks(N=%d) = %d, want 0 (reject, never partial advance)", tc.n, got)
			}
		})
	}
}

// SEC-006: N == maxAdvanceTicksPerCall (the boundary) is still legal and
// advances the clock exactly N ticks — the bound rejects only N >
// maxAdvanceTicksPerCall, mirroring engine.core.AdvanceTicks's
// "n > MaxAdvanceTicksPerCall" check exactly (not "n >=").
func TestStubEngine_AdvanceTicks_UpperBound_Boundary(t *testing.T) {
	tr, eng := newTestEngine(t)
	r := send(t, tr, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: maxAdvanceTicksPerCall})
	if !r.Accepted {
		t.Fatalf("AdvanceTicks(N=maxAdvanceTicksPerCall) rejected, want accepted: %#v", r.Error)
	}
	if r.Tick != protocol.Tick(maxAdvanceTicksPerCall) {
		t.Fatalf("CommandResult.Tick = %d, want %d", r.Tick, maxAdvanceTicksPerCall)
	}
	if got := eng.Tick(); got != protocol.Tick(maxAdvanceTicksPerCall) {
		t.Fatalf("Tick() = %d, want %d", got, maxAdvanceTicksPerCall)
	}
}

// SEC-006 (Weakness pattern #1 — "guard the arithmetic, not just the
// input"): bounding N per call does not by itself prove the running
// s.tick total, which accumulates across every call for the life of the
// engine, cannot be driven to overflow. This test forces the scenario
// directly (white-box: same package, sets the unexported tick field)
// rather than relying on ~2.5e15 legal calls to reach it, and asserts a
// call that would overflow protocol.Tick (int64) is rejected — with the
// same registry code as an out-of-bounds N — and leaves the counter
// unchanged, rather than silently wrapping to a negative/nonsensical
// value.
func TestStubEngine_AdvanceTicks_AccumulationOverflow_Rejected(t *testing.T) {
	tr, eng := newTestEngine(t)

	eng.mu.Lock()
	eng.tick = protocol.Tick(math.MaxInt64 - 5)
	eng.mu.Unlock()

	// A perfectly legal, in-bounds N (well under maxAdvanceTicksPerCall)
	// that would nonetheless push the running total past math.MaxInt64.
	r := send(t, tr, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 10})
	if r.Accepted {
		t.Fatalf("AdvanceTicks that would overflow the tick counter was accepted, want rejected: Tick=%d", r.Tick)
	}
	if r.Error == nil || r.Error.Code != codeAdvanceTicksOutOfBounds {
		t.Fatalf("rejected result Code = %#v, want %q", r.Error, codeAdvanceTicksOutOfBounds)
	}
	if got := eng.Tick(); got != protocol.Tick(math.MaxInt64-5) {
		t.Fatalf("Tick() after rejected overflow-risking AdvanceTicks = %d, want unchanged at %d", got, int64(math.MaxInt64-5))
	}

	// A small enough N that it fits exactly must still be accepted —
	// the overflow guard must not be over-eager and reject legal calls
	// near the ceiling.
	r2 := send(t, tr, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 5})
	if !r2.Accepted {
		t.Fatalf("AdvanceTicks(N=5) at the exact remaining headroom was rejected, want accepted: %#v", r2.Error)
	}
	if got := eng.Tick(); got != protocol.Tick(math.MaxInt64) {
		t.Fatalf("Tick() = %d, want math.MaxInt64", got)
	}
}

// AC-9: a Command.Kind the engine's dispatch does not recognise must be
// rejected with a registry-sourced ErrorRef, never a panic. Every Kind
// reaching a real Transport already passed protocol.DecodeCommand's
// commandRegistry, so this exercises StubEngine's own defensive default
// case directly (see codes.go's codeUnknownKind doc).
func TestStubEngine_UnknownKind_Rejected(t *testing.T) {
	tr, eng := newTestEngine(t)
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            "TotallyUnknownKind",
		Payload:         protocol.PausePayload{}, // any registered payload; Kind field is what's being tested
	}
	eng.handle(cmd) // bypass Transport.SendCommand, which would reject this at Validate() first

	select {
	case r := <-tr.Results():
		if r.Accepted {
			t.Fatal("unknown kind was accepted, want rejected")
		}
		if r.Error == nil || r.Error.Code == "" {
			t.Fatalf("rejected result missing a registry ErrorRef: %#v", r.Error)
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for CommandResult")
	}
}

// AC-5: Subscribe returns a usable SubscriptionID (carried on the first
// correlated Delta), deltas carry monotonically increasing per-subscription
// Seq starting at 1, and Unsubscribe stops further deltas.
func TestStubEngine_SubscribeUnsubscribe(t *testing.T) {
	tr, _ := newTestEngine(t)

	sr := send(t, tr, protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "f1.viewport"})
	if !sr.Accepted {
		t.Fatalf("Subscribe rejected: %#v", sr.Error)
	}

	first := recvDelta(t, tr)
	if first.SubscriptionID == "" {
		t.Fatal("first Delta has empty SubscriptionID")
	}
	if first.CorrelationID != sr.CorrelationID {
		t.Fatalf("first Delta.CorrelationID = %q, want %q (echoes the Subscribe)", first.CorrelationID, sr.CorrelationID)
	}
	if first.Seq != 1 {
		t.Fatalf("first Delta.Seq = %d, want 1", first.Seq)
	}

	// Two more scripted deltas via AdvanceTicks: Seq must keep increasing.
	send(t, tr, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 1})
	d2 := recvDelta(t, tr)
	if d2.Seq != 2 {
		t.Fatalf("second Delta.Seq = %d, want 2", d2.Seq)
	}
	if d2.SubscriptionID != first.SubscriptionID {
		t.Fatalf("second Delta.SubscriptionID = %q, want %q", d2.SubscriptionID, first.SubscriptionID)
	}

	send(t, tr, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 1})
	d3 := recvDelta(t, tr)
	if d3.Seq != 3 {
		t.Fatalf("third Delta.Seq = %d, want 3", d3.Seq)
	}

	ur := send(t, tr, protocol.KindUnsubscribe, protocol.UnsubscribePayload{SubscriptionID: first.SubscriptionID})
	if !ur.Accepted {
		t.Fatalf("Unsubscribe rejected: %#v", ur.Error)
	}

	// Further AdvanceTicks must not push any more deltas for this (now
	// dead) subscription.
	send(t, tr, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 1})
	select {
	case d := <-tr.Deltas():
		t.Fatalf("received a Delta after Unsubscribe: %#v", d)
	case <-time.After(100 * time.Millisecond):
		// expected: no more deltas
	}
}

// AC-5/AC-9: Unsubscribe for a subscription that was never opened (or
// already closed) must be rejected with a registry error, not panic or
// silently accept.
func TestStubEngine_Unsubscribe_UnknownID_Rejected(t *testing.T) {
	tr, _ := newTestEngine(t)
	r := send(t, tr, protocol.KindUnsubscribe, protocol.UnsubscribePayload{SubscriptionID: "sub-does-not-exist"})
	if r.Accepted {
		t.Fatal("Unsubscribe of unknown ID accepted, want rejected")
	}
	if r.Error == nil || r.Error.Code == "" {
		t.Fatalf("rejected result missing a registry ErrorRef: %#v", r.Error)
	}
}

// AC-6: deltas are sourced from a scripted/recorded stream, not
// computed — the f1.viewport scripted stream must replay byte-identical
// to scriptedViewportDeltas() as ticks advance.
func TestStubEngine_ScriptedStreamReplay(t *testing.T) {
	tr, _ := newTestEngine(t)

	sr := send(t, tr, protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "f1.viewport"})
	if !sr.Accepted {
		t.Fatalf("Subscribe rejected: %#v", sr.Error)
	}
	first := recvDelta(t, tr)

	wantSnapshot, err := json.Marshal(fullViewportSnapshot(GenerateFolkestone64()))
	if err != nil {
		t.Fatalf("marshal expected snapshot: %v", err)
	}
	if !bytes.Equal(first.Patch, wantSnapshot) {
		t.Fatal("first Delta.Patch does not match fullViewportSnapshot(Folkestone-64)")
	}

	script := scriptedViewportDeltas()
	for i, want := range script {
		send(t, tr, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 1})
		d := recvDelta(t, tr)

		wantBytes, err := json.Marshal(want)
		if err != nil {
			t.Fatalf("marshal script[%d]: %v", i, err)
		}
		if !bytes.Equal(d.Patch, wantBytes) {
			t.Fatalf("script[%d]: Delta.Patch = %s, want %s", i, d.Patch, wantBytes)
		}
	}
}

// AC-11/GR#21: two independent StubEngines given the same command
// sequence (no chaos) must produce byte-identical Delta content, Seq and
// Tick sequences.
func TestStubEngine_Determinism(t *testing.T) {
	run := func(t *testing.T) []protocol.Delta {
		tr, _ := newTestEngine(t)
		sr := send(t, tr, protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "f1.viewport"})
		if !sr.Accepted {
			t.Fatalf("Subscribe rejected: %#v", sr.Error)
		}
		var deltas []protocol.Delta
		deltas = append(deltas, recvDelta(t, tr))
		for i := 0; i < 4; i++ {
			send(t, tr, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 1})
			deltas = append(deltas, recvDelta(t, tr))
		}
		return deltas
	}

	a := run(t)
	b := run(t)

	if len(a) != len(b) {
		t.Fatalf("run lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Seq != b[i].Seq || a[i].Tick != b[i].Tick {
			t.Fatalf("delta[%d]: Seq/Tick differ: %+v vs %+v", i, a[i], b[i])
		}
		if !bytes.Equal(a[i].Patch, b[i].Patch) {
			t.Fatalf("delta[%d]: Patch differs:\n%s\nvs\n%s", i, a[i].Patch, b[i].Patch)
		}
	}
}

// AC-10: invalid chaos configuration must fail loudly at construction,
// never be silently clamped.
func TestNewStubEngine_InvalidChaos_Rejected(t *testing.T) {
	tr := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	defer func() { _ = tr.Close() }()

	_, err := NewStubEngine(tr, WithChaos(ChaosConfig{
		DelayedDeltas: DelayConfig{Enabled: true, MinDelay: -1},
	}))
	if err == nil {
		t.Fatal("NewStubEngine with negative MinDelay = nil error, want non-nil")
	}

	_, err = NewStubEngine(tr, WithChaos(ChaosConfig{
		BurstDeltas: BurstConfig{Enabled: true, Size: 1},
	}))
	if err == nil {
		t.Fatal("NewStubEngine with BurstConfig.Size=1 (enabled) = nil error, want non-nil")
	}
}

// InspectEntity/Debug must additionally emit a canned Event, not just a
// CommandResult.
func TestStubEngine_InspectEntityAndDebug_EmitEvents(t *testing.T) {
	tr, _ := newTestEngine(t)

	r := send(t, tr, protocol.KindInspectEntity, protocol.InspectEntityPayload{EntityRef: "citizen:482913"})
	if !r.Accepted {
		t.Fatalf("InspectEntity rejected: %#v", r.Error)
	}
	select {
	case e := <-tr.Events():
		if e.Kind != "entity.inspected" || len(e.EntityRefs) != 1 || e.EntityRefs[0] != "citizen:482913" {
			t.Fatalf("unexpected Event: %#v", e)
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for entity.inspected Event")
	}

	r2 := send(t, tr, protocol.KindDebug, protocol.DebugPayload{Op: "force-unlock"})
	if !r2.Accepted {
		t.Fatalf("Debug rejected: %#v", r2.Error)
	}
	select {
	case e := <-tr.Events():
		if e.Kind != "debug.op.executed" || e.Fields["op"] != "force-unlock" {
			t.Fatalf("unexpected Event: %#v", e)
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for debug.op.executed Event")
	}
}
