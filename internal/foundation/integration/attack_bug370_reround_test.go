package integration

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// runBoth drives the same messages through the fast path and the pooled
// path and returns (fastApplied, slowApplied, fastErr, slowErr) - errors last
// (staticcheck ST1008).
func runBoth(t *testing.T, corr string, msgs []det.Message[string]) ([]string, []string, error, error) {
	t.Helper()
	fast := &multiMsgIntegration{single: true, shard0Msgs: msgs}
	_, fastErr := Execute[uint64, string](corr+"-fast", NewLocalPool(4), fast)
	slowU := &multiMsgIntegration{single: true, shard0Msgs: msgs}
	_, slowErr := Execute[uint64, string](corr+"-pooled", NewLocalPool(4), forceFullPathMultiMsgIntegration{slowU})
	return fast.Applied(), slowU.Applied(), fastErr, slowErr
}

// RE-ROUND F1a: the original {0,5},{1,5},{0,5} shape. Unlike the adopted
// attack test (which only asserts the two paths AGREE - and would pass if
// BOTH silently applied), this asserts both paths ERROR with MET-F203 and
// apply nothing.
func TestReroundBUG370_MixedShardTie_BothMustReject(t *testing.T) {
	msgs := []det.Message[string]{
		{Shard: 0, Sequence: 5, Payload: "s0-a"},
		{Shard: 1, Sequence: 5, Payload: "s1"},
		{Shard: 0, Sequence: 5, Payload: "s0-b"},
	}
	fastApplied, slowApplied, fastErr, slowErr := runBoth(t, "reround-f1a", msgs)
	for _, c := range []struct {
		name    string
		err     error
		applied []string
	}{{"fast", fastErr, fastApplied}, {"pooled", slowErr, slowApplied}} {
		if c.err == nil {
			t.Fatalf("%s: want MET-F203, got nil (applied=%v)", c.name, c.applied)
		}
		if !errors.Is(c.err, &errs.E{Code: det.ErrBarrierDuplicate}) {
			t.Fatalf("%s: err=%v want ErrBarrierDuplicate", c.name, c.err)
		}
		if len(c.applied) != 0 {
			t.Fatalf("%s: PARTIAL APPLY applied=%v", c.name, c.applied)
		}
	}
}

// RE-ROUND F1b: three shards, duplicates at BOTH ENDS of the canonical
// order, every message tied on a Sequence that also appears on another
// shard. Under a Sequence-only sort this shape has many orderings in
// which neither duplicate pair is adjacent.
func TestReroundBUG370_ThreeShards_DuplicatesAtEnds(t *testing.T) {
	msgs := []det.Message[string]{
		{Shard: 0, Sequence: 1, Payload: "s0:1-a"},
		{Shard: 1, Sequence: 1, Payload: "s1:1"},
		{Shard: 2, Sequence: 1, Payload: "s2:1"},
		{Shard: 2, Sequence: 9, Payload: "s2:9-a"},
		{Shard: 1, Sequence: 9, Payload: "s1:9"},
		{Shard: 0, Sequence: 9, Payload: "s0:9"},
		{Shard: 0, Sequence: 1, Payload: "s0:1-b"},
		{Shard: 2, Sequence: 9, Payload: "s2:9-b"},
	}
	for i := 0; i < 200; i++ {
		fastApplied, slowApplied, fastErr, slowErr := runBoth(t, "reround-f1b", msgs)
		if fastErr == nil {
			t.Fatalf("iter %d fast: duplicate hidden, applied=%v", i, fastApplied)
		}
		if slowErr == nil {
			t.Fatalf("iter %d pooled: duplicate hidden, applied=%v", i, slowApplied)
		}
		if !errors.Is(fastErr, &errs.E{Code: det.ErrBarrierDuplicate}) || !errors.Is(slowErr, &errs.E{Code: det.ErrBarrierDuplicate}) {
			t.Fatalf("iter %d: fast=%v pooled=%v want ErrBarrierDuplicate", i, fastErr, slowErr)
		}
		if len(fastApplied) != 0 || len(slowApplied) != 0 {
			t.Fatalf("iter %d: partial apply fast=%v pooled=%v", i, fastApplied, slowApplied)
		}
	}
}

// RE-ROUND: multi-shard VALID input must apply in the identical canonical
// order on both paths - proves the shared comparator, not just the check.
func TestReroundBUG370_MultiShardValid_IdenticalOrder(t *testing.T) {
	msgs := []det.Message[string]{
		{Shard: 2, Sequence: 0, Payload: "s2:0"},
		{Shard: 0, Sequence: 9, Payload: "s0:9"},
		{Shard: 1, Sequence: 4, Payload: "s1:4"},
		{Shard: 0, Sequence: 1, Payload: "s0:1"},
		{Shard: 2, Sequence: 3, Payload: "s2:3"},
		{Shard: 1, Sequence: 0, Payload: "s1:0"},
	}
	want := []string{"s0:1", "s0:9", "s1:0", "s1:4", "s2:0", "s2:3"}
	for i := 0; i < 100; i++ {
		fastApplied, slowApplied, fastErr, slowErr := runBoth(t, "reround-valid", msgs)
		if fastErr != nil || slowErr != nil {
			t.Fatalf("iter %d: fast=%v pooled=%v", i, fastErr, slowErr)
		}
		for j := range want {
			if fastApplied[j] != want[j] {
				t.Fatalf("iter %d fast[%d]=%q want %q (applied=%v)", i, j, fastApplied[j], want[j], fastApplied)
			}
			if slowApplied[j] != want[j] {
				t.Fatalf("iter %d pooled[%d]=%q want %q (applied=%v)", i, j, slowApplied[j], want[j], slowApplied)
			}
		}
	}
}

// RE-ROUND: the shared comparator itself must be a strict weak ordering
// and must order Shard-primary, Sequence-secondary.
func TestReroundBUG370_LessCanonicalKeyContract(t *testing.T) {
	if !det.LessCanonicalKey(0, 9, 1, 0) {
		t.Fatal("Shard must be the PRIMARY key: (0,9) must sort before (1,0)")
	}
	if det.LessCanonicalKey(1, 0, 0, 9) {
		t.Fatal("asymmetry violated")
	}
	if !det.LessCanonicalKey(3, 1, 3, 2) {
		t.Fatal("Sequence must be the secondary key")
	}
	if det.LessCanonicalKey(3, 2, 3, 2) {
		t.Fatal("irreflexivity violated: equal keys must not be less-than")
	}
	if !det.LessCanonicalKey(-1, 0, 0, 0) {
		t.Fatal("negative shard must still order by value")
	}
	// transitivity spot check across the tuple boundary
	if !det.LessCanonicalKey(0, 5, 0, 6) || !det.LessCanonicalKey(0, 6, 1, 0) || !det.LessCanonicalKey(0, 5, 1, 0) {
		t.Fatal("transitivity violated")
	}
}

// RE-ROUND, residual probe (NOT a BUG-370 regression): ApplyBarrier
// rejects a Shard outside [0, NumShards); executeSingleShard does not
// validate the range at all. Logged, not asserted - reported as a
// separate follow-up.
func TestReroundBUG370_ShardRangeDivergenceProbe(t *testing.T) {
	msgs := []det.Message[string]{
		{Shard: det.NumShards + 3, Sequence: 0, Payload: "oob-a"},
		{Shard: det.NumShards + 3, Sequence: 1, Payload: "oob-b"},
	}
	fastApplied, slowApplied, fastErr, slowErr := runBoth(t, "reround-oob", msgs)
	t.Logf("PROBE fast: err=%v applied=%v", fastErr, fastApplied)
	t.Logf("PROBE pooled: err=%v applied=%v", slowErr, slowApplied)
}
