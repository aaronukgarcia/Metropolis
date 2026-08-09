package stub

import (
	"bytes"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// AC-7(a): delayed-delta mode introduces an observable artificial gap
// before a Delta is pushed.
func TestStubEngine_Chaos_DelayedDeltas(t *testing.T) {
	const minDelay = 40 * time.Millisecond
	const maxDelay = 60 * time.Millisecond

	tr, _ := newTestEngine(t, WithChaos(ChaosConfig{
		Seed: 7,
		DelayedDeltas: DelayConfig{
			Enabled:  true,
			MinDelay: minDelay,
			MaxDelay: maxDelay,
		},
	}))

	sr := send(t, tr, protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "f1.viewport"})
	if !sr.Accepted {
		t.Fatalf("Subscribe rejected: %#v", sr.Error)
	}
	recvDelta(t, tr) // drain the (also delayed) initial snapshot

	start := time.Now()
	r := send(t, tr, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 1})
	if !r.Accepted {
		t.Fatalf("AdvanceTicks rejected: %#v", r.Error)
	}
	// CommandResult itself must NOT be delayed — only Delta emission is.
	if elapsed := time.Since(start); elapsed > minDelay {
		t.Fatalf("CommandResult took %v, want well under the %v delta delay (CommandResult is not chaos-delayed)", elapsed, minDelay)
	}

	recvDelta(t, tr)
	gap := time.Since(start)
	if gap < minDelay {
		t.Fatalf("Delta arrived after %v, want at least MinDelay=%v", gap, minDelay)
	}
}

// AC-7(b): burst-delta mode pushes several Deltas in a tight cluster
// (batch size observable, minimal spacing between them).
func TestStubEngine_Chaos_BurstDeltas(t *testing.T) {
	const burstSize = 5

	tr, _ := newTestEngine(t, WithChaos(ChaosConfig{
		Seed: 3,
		BurstDeltas: BurstConfig{
			Enabled: true,
			Size:    burstSize,
		},
	}))

	sr := send(t, tr, protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "f1.viewport"})
	if !sr.Accepted {
		t.Fatalf("Subscribe rejected: %#v", sr.Error)
	}

	var got []protocol.Delta
	deadline := time.After(500 * time.Millisecond)
collect:
	for len(got) < burstSize {
		select {
		case d := <-tr.Deltas():
			got = append(got, d)
		case <-deadline:
			break collect
		}
	}

	if len(got) != burstSize {
		t.Fatalf("received %d deltas in the burst, want %d", len(got), burstSize)
	}
	for i, d := range got {
		if int(d.Seq) != i+1 {
			t.Fatalf("burst delta[%d].Seq = %d, want %d (monotonic even within a burst)", i, d.Seq, i+1)
		}
		if !bytes.Equal(d.Patch, got[0].Patch) {
			t.Fatalf("burst delta[%d].Patch differs from delta[0] (all copies of one scripted patch)", i)
		}
	}
}

// AC-11/GR#21: chaos affects only artificial timing — with the same seed
// and command sequence, two chaos-configured runs must still produce
// byte-identical Delta content, Seq and Tick.
func TestStubEngine_Chaos_DeterministicContent(t *testing.T) {
	cfg := ChaosConfig{
		Seed: 42,
		DelayedDeltas: DelayConfig{
			Enabled:  true,
			MinDelay: 1 * time.Millisecond,
			MaxDelay: 5 * time.Millisecond,
		},
	}

	run := func(t *testing.T) []protocol.Delta {
		tr, _ := newTestEngine(t, WithChaos(cfg))
		sr := send(t, tr, protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "f1.viewport"})
		if !sr.Accepted {
			t.Fatalf("Subscribe rejected: %#v", sr.Error)
		}
		var deltas []protocol.Delta
		deltas = append(deltas, recvDelta(t, tr))
		for i := 0; i < 3; i++ {
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
			t.Fatalf("delta[%d]: Seq/Tick differ under identical chaos seed: %+v vs %+v", i, a[i], b[i])
		}
		if !bytes.Equal(a[i].Patch, b[i].Patch) {
			t.Fatalf("delta[%d]: Patch differs under identical chaos seed", i)
		}
	}
}

func TestChaosConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     ChaosConfig
		wantErr bool
	}{
		{"disabled zero value", ChaosConfig{}, false},
		{"valid delay", ChaosConfig{DelayedDeltas: DelayConfig{Enabled: true, MinDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}}, false},
		{"negative min delay", ChaosConfig{DelayedDeltas: DelayConfig{Enabled: true, MinDelay: -1}}, true},
		{"inverted range", ChaosConfig{DelayedDeltas: DelayConfig{Enabled: true, MinDelay: 5 * time.Millisecond, MaxDelay: time.Millisecond}}, true},
		{"valid burst", ChaosConfig{BurstDeltas: BurstConfig{Enabled: true, Size: 3}}, false},
		{"burst size too small", ChaosConfig{BurstDeltas: BurstConfig{Enabled: true, Size: 1}}, true},
		{"burst size zero", ChaosConfig{BurstDeltas: BurstConfig{Enabled: true, Size: 0}}, true},
		{"disabled knobs ignore bad values", ChaosConfig{DelayedDeltas: DelayConfig{Enabled: false, MinDelay: -1}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
