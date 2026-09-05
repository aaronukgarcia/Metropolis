package integration

import (
	"errors"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// multiMsgIntegration is a SingleShard()-capable Integration whose shard 0
// emits exactly shard0Msgs (every other shard contributes nothing),
// recording every ApplyMessage call in applied — this is the integration
// counterpart of engine/core's countingHook (phase_test.go), built to
// drive BUG-370's fast-vs-pooled equivalence proof below.
type multiMsgIntegration struct {
	single     bool
	shard0Msgs []det.Message[string]

	mu      sync.Mutex
	applied []string
}

func (m *multiMsgIntegration) RunShard(shard int) (uint64, []det.Message[string]) {
	if shard != 0 {
		return 0, nil
	}
	return uint64(len(m.shard0Msgs)), m.shard0Msgs
}

func (m *multiMsgIntegration) Combine(acc uint64, r det.ShardResult[uint64]) uint64 {
	return acc + r.Value
}

func (m *multiMsgIntegration) ApplyMessage(payload string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applied = append(m.applied, payload)
}

func (m *multiMsgIntegration) Zero() uint64       { return 0 }
func (m *multiMsgIntegration) UpdateClass() Class { return ClassT1Batchable }
func (m *multiMsgIntegration) SingleShard() bool  { return m.single }

func (m *multiMsgIntegration) Applied() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.applied...)
}

// forceFullPathMultiMsgIntegration forces the pooled/general Execute path
// (SingleShard() == false) over the SAME shard-0-only RunShard behaviour,
// mirroring executor_test.go's forceFullPathIntegration wrapper exactly.
type forceFullPathMultiMsgIntegration struct {
	*multiMsgIntegration
}

func (forceFullPathMultiMsgIntegration) SingleShard() bool { return false }

// TestBUG370_FastAndPooledPathsAgree is BUG-370's table-driven equivalence
// proof for foundation/integration: executeSingleShard (the fast path,
// SingleShard()==true) and Execute's full 256-shard path (the same
// shard-0 behaviour, forced via forceFullPathMultiMsgIntegration) must
// give IDENTICAL results — both on a duplicate-(Shard,Sequence) input
// (both must error, nothing applied) and on a valid multi-message input
// (both must apply the same messages in the same canonical order).
//
// Before this fix, executeSingleShard replicated det.ApplyBarrier's
// canonical sort inline WITHOUT its duplicate-key rejection (BUG-370's
// finding): the exact same duplicate input that errors on the pooled
// path below used to apply silently (both messages, in sorted order) on
// the fast path — a plausible-but-wrong divergence rather than an error.
// Both paths now route through the one shared check
// (det.RejectAdjacentDuplicateKey, foundation/det/barrier.go), so this
// table proves they agree.
func TestBUG370_FastAndPooledPathsAgree(t *testing.T) {
	cases := []struct {
		name       string
		shard0Msgs []det.Message[string]
		wantErr    bool
	}{
		{
			name: "duplicate sequence errors identically on both paths",
			shard0Msgs: []det.Message[string]{
				{Shard: 0, Sequence: 2, Payload: "dup-a"},
				{Shard: 0, Sequence: 2, Payload: "dup-b"},
				{Shard: 0, Sequence: 0, Payload: "unrelated"},
			},
			wantErr: true,
		},
		{
			name: "valid multi-message input applies identically on both paths",
			shard0Msgs: []det.Message[string]{
				{Shard: 0, Sequence: 3, Payload: "s0:3"},
				{Shard: 0, Sequence: 1, Payload: "s0:1"},
				{Shard: 0, Sequence: 0, Payload: "s0:0"},
				{Shard: 0, Sequence: 2, Payload: "s0:2"},
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fast := &multiMsgIntegration{single: true, shard0Msgs: tc.shard0Msgs}
			_, fastErr := Execute[uint64, string]("corr-bug370-fast", NewLocalPool(4), fast)

			slowUnderlying := &multiMsgIntegration{single: true, shard0Msgs: tc.shard0Msgs}
			slow := forceFullPathMultiMsgIntegration{slowUnderlying}
			_, slowErr := Execute[uint64, string]("corr-bug370-pooled", NewLocalPool(4), slow)

			if tc.wantErr {
				if fastErr == nil {
					t.Fatal("fast path: want error, got nil")
				}
				if slowErr == nil {
					t.Fatal("pooled path: want error, got nil")
				}
				if !errors.Is(fastErr, &errs.E{Code: det.ErrBarrierDuplicate}) {
					t.Fatalf("fast path error = %v, want ErrBarrierDuplicate (%s)", fastErr, det.ErrBarrierDuplicate)
				}
				if !errors.Is(slowErr, &errs.E{Code: det.ErrBarrierDuplicate}) {
					t.Fatalf("pooled path error = %v, want ErrBarrierDuplicate (%s)", slowErr, det.ErrBarrierDuplicate)
				}
				if applied := fast.Applied(); len(applied) != 0 {
					t.Fatalf("fast path applied = %v, want nothing applied on a rejected duplicate", applied)
				}
				if applied := slowUnderlying.Applied(); len(applied) != 0 {
					t.Fatalf("pooled path applied = %v, want nothing applied on a rejected duplicate", applied)
				}
				return
			}

			if fastErr != nil {
				t.Fatalf("fast path: unexpected error: %v", fastErr)
			}
			if slowErr != nil {
				t.Fatalf("pooled path: unexpected error: %v", slowErr)
			}
			fastApplied := fast.Applied()
			slowApplied := slowUnderlying.Applied()
			if len(fastApplied) != len(slowApplied) {
				t.Fatalf("fast path applied %d messages, pooled path applied %d, want equal", len(fastApplied), len(slowApplied))
			}
			for i := range fastApplied {
				if fastApplied[i] != slowApplied[i] {
					t.Fatalf("applied[%d]: fast=%q pooled=%q — fast path diverged from pooled path", i, fastApplied[i], slowApplied[i])
				}
			}
		})
	}
}
