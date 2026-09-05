package core

import (
	"errors"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestBUG370_FastAndPooledPathsAgree is BUG-370's table-driven equivalence
// proof: the fast path (runPhaseForHookFast, a SingleShardHook) and the
// pooled path (the same shard-0 effects, forced through det.RunPhase by
// NOT opting into SingleShardHook) must give IDENTICAL results — both on
// a duplicate-(Shard,Sequence) input (both must error, nothing applied)
// and on a valid multi-effect input (both must apply the same effects in
// the same canonical order).
//
// This exists alongside TestSingleShardHook_FastPathMatchesPooledPath and
// TestSingleShardHook_FastPathRejectsDuplicateSequence (phase_test.go),
// which separately proved the valid-input and duplicate-input cases for
// the fast path in isolation before BUG-370. What this test adds is the
// BUG-370 finding itself: proving both paths now go through the SAME
// shared check (det.RejectAdjacentDuplicateKey, foundation/det/
// barrier.go) rather than two independently-maintained copies of the
// duplicate-rejection logic — one of which (foundation/integration's
// executeSingleShard) had silently omitted it entirely until this fix.
func TestBUG370_FastAndPooledPathsAgree(t *testing.T) {
	cases := []struct {
		name          string
		shard0Effects []Effect
		wantErr       bool
	}{
		{
			name: "duplicate sequence errors identically on both paths",
			shard0Effects: []Effect{
				{Sequence: 2, Payload: "dup-a"},
				{Sequence: 2, Payload: "dup-b"},
				{Sequence: 0, Payload: "unrelated"},
			},
			wantErr: true,
		},
		{
			name: "valid multi-effect input applies identically on both paths",
			shard0Effects: []Effect{
				{Sequence: 3, Payload: "s0:3"},
				{Sequence: 1, Payload: "s0:1"},
				{Sequence: 0, Payload: "s0:0"},
				{Sequence: 2, Payload: "s0:2"},
			},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastApplied, fastErr := runFastPath(t, tc.shard0Effects)
			pooledApplied, pooledErr := runPooledPath(t, tc.shard0Effects)

			if tc.wantErr {
				if fastErr == nil {
					t.Fatal("fast path: want error, got nil")
				}
				if pooledErr == nil {
					t.Fatal("pooled path: want error, got nil")
				}
				if !errors.Is(fastErr, &errs.E{Code: det.ErrBarrierDuplicate}) {
					t.Fatalf("fast path error = %v, want ErrBarrierDuplicate (%s)", fastErr, det.ErrBarrierDuplicate)
				}
				if !errors.Is(pooledErr, &errs.E{Code: det.ErrBarrierDuplicate}) {
					t.Fatalf("pooled path error = %v, want ErrBarrierDuplicate (%s)", pooledErr, det.ErrBarrierDuplicate)
				}
				if len(fastApplied) != 0 {
					t.Fatalf("fast path applied = %v, want nothing applied on a rejected duplicate", fastApplied)
				}
				if len(pooledApplied) != 0 {
					t.Fatalf("pooled path applied = %v, want nothing applied on a rejected duplicate", pooledApplied)
				}
				return
			}

			if fastErr != nil {
				t.Fatalf("fast path: unexpected error: %v", fastErr)
			}
			if pooledErr != nil {
				t.Fatalf("pooled path: unexpected error: %v", pooledErr)
			}
			if len(fastApplied) != len(pooledApplied) {
				t.Fatalf("fast path applied %d effects, pooled path applied %d, want equal", len(fastApplied), len(pooledApplied))
			}
			for i := range fastApplied {
				if fastApplied[i] != pooledApplied[i] {
					t.Fatalf("applied[%d]: fast=%v pooled=%v — fast path diverged from pooled path", i, fastApplied[i], pooledApplied[i])
				}
			}
		})
	}
}

// runFastPath drives shard0Effects through a SingleShardHook (the
// runPhaseForHookFast route) and returns what was applied plus any error.
func runFastPath(t *testing.T, shard0Effects []Effect) ([]Effect, error) {
	t.Helper()
	var mu, applyMu sync.Mutex
	calls := 0
	var calledShards []int
	var applied []Effect

	hook := singleShardCountingHook{&countingHook{
		mu: &mu, calls: &calls, calledShards: &calledShards,
		shard0Effects: shard0Effects, applyMu: &applyMu, applied: &applied,
	}}

	e := NewEngine(WithPoolSize(4))
	if err := e.RegisterPhaseHook(PhaseDailyTick, hook); err != nil {
		t.Fatalf("RegisterPhaseHook (fast): %v", err)
	}
	err := e.AdvanceTicks("corr-bug370-fast", 1)

	applyMu.Lock()
	defer applyMu.Unlock()
	return append([]Effect{}, applied...), err
}

// runPooledPath drives the SAME shard0Effects through the pooled path
// (a hook that does NOT implement SingleShardHook, forcing det.RunPhase's
// full 256-shard dispatch) and returns what was applied plus any error.
func runPooledPath(t *testing.T, shard0Effects []Effect) ([]Effect, error) {
	t.Helper()
	var mu, applyMu sync.Mutex
	calls := 0
	var calledShards []int
	var applied []Effect

	hook := &countingHook{
		mu: &mu, calls: &calls, calledShards: &calledShards,
		shard0Effects: shard0Effects, applyMu: &applyMu, applied: &applied,
	}

	e := NewEngine(WithPoolSize(4))
	if err := e.RegisterPhaseHook(PhaseDailyTick, hook); err != nil {
		t.Fatalf("RegisterPhaseHook (pooled): %v", err)
	}
	err := e.AdvanceTicks("corr-bug370-pooled", 1)

	applyMu.Lock()
	defer applyMu.Unlock()
	return append([]Effect{}, applied...), err
}
