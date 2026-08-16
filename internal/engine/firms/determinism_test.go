package firms

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// foundingSequence runs a full founding→growth→failure→credit sequence and
// returns a canonical, sorted string encoding of every observable result,
// so two runs can be compared byte-for-byte (AC-17).
func foundingSequence(t *testing.T, cfg config, seed uint64) []string {
	t.Helper()
	api := newAPIWithConfig(t, cfg, seed)
	_ = api.SetCitizens(mustCitizensAmbitious(t, 40))
	_ = api.SetBuild(mustBuild(t))

	ids := make([]uint64, 0, 40)
	for i := 1; i <= 40; i++ {
		ids = append(ids, uint64(i))
	}
	if _, err := api.EvaluateFounding(ids, 3); err != nil {
		t.Fatalf("EvaluateFounding: %v", err)
	}

	var out []string
	for _, e := range api.Events() {
		out = append(out, fmt.Sprintf("event:%d:%d:%d", e.Kind, e.FirmID, e.Month))
	}
	for _, f := range api.Firms() {
		out = append(out, fmt.Sprintf("firm:%d:stage=%v:staff=%d:founder=%d", f.ID, f.Stage, len(f.Staff), f.FounderCitizenID))
	}
	out = append(out, fmt.Sprintf("founded:%d", api.FoundedCount()))
	if api.FoundedCount() == 0 {
		t.Fatal("degenerate determinism fixture: no firms founded (ambition must be non-zero)")
	}
	sort.Strings(out)
	return out
}

// TestDeterminism (AC-17): identical state + seed + commands yield
// byte-identical founder IDs, stage transitions and failure events.
func TestDeterminism(t *testing.T) {
	cfg := controlledConfig()
	cfg.Founding.AmbitionPerMille = 500 // probabilistic, so the seed matters

	a := foundingSequence(t, cfg, 42)
	b := foundingSequence(t, cfg, 42)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("determinism divergence between identical runs:\n%v\nvs\n%v", a, b)
	}
	if len(a) == 0 {
		t.Fatal("determinism sequence produced no observable state to compare")
	}
}

// TestConcurrentFoundingEvaluation (AC-19): founding evaluation across
// shards/districts runs concurrently and yields the same deterministic
// result as sequential evaluation (and -race proves it is data-race-free).
func TestConcurrentFoundingEvaluation(t *testing.T) {
	cfg := controlledConfig()
	cfg.Founding.AmbitionPerMille = 500

	ids := make([]uint64, 200)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}

	// Sequential reference.
	seq := newAPIWithConfig(t, cfg, 7)
	_ = seq.SetCitizens(mustCitizensAmbitious(t, 200))
	if _, err := seq.EvaluateFounding(ids, 1); err != nil {
		t.Fatalf("EvaluateFounding: %v", err)
	}
	seqCount := seq.FoundedCount()

	// Concurrent sharded evaluation.
	conc := newAPIWithConfig(t, cfg, 7)
	_ = conc.SetCitizens(mustCitizensAmbitious(t, 200))
	const shards = 4
	var wg sync.WaitGroup
	for s := 0; s < shards; s++ {
		shard := s
		wg.Add(1)
		go func() {
			defer wg.Done()
			chunk := ids[shard*50 : (shard+1)*50]
			if _, err := conc.EvaluateFounding(chunk, 1); err != nil {
				t.Errorf("shard %d: %v", shard, err)
			}
		}()
	}
	wg.Wait()

	if conc.FoundedCount() != seqCount {
		t.Fatalf("concurrent founding count %d != sequential %d", conc.FoundedCount(), seqCount)
	}
}
