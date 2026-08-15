package diagrams

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// TestEngineConcurrentRenderIsRaceFree drives the topology-hash cache from
// multiple goroutines requesting the same and different topologies, as
// F2's fiscal circuit and F5's chain diagram would on overlapping UI
// ticks (AC-10). Run with -race.
func TestEngineConcurrentRenderIsRaceFree(t *testing.T) {
	e := NewEngine()
	base := SankeyTopology{
		Sources: []SankeyFlow{{ID: "s1", Name: "tax", Amount: 100}},
		Sinks:   []SankeyFlow{{ID: "k1", Name: "roads", Amount: 100}},
	}

	const n = 8
	results := make([]Result, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			topo := base
			if i%2 == 1 {
				// Different topology on odd lanes, same on even. Deep-copy so
				// the odd lane's mutation never races the shared base slice.
				topo.Sinks = append([]SankeyFlow(nil), base.Sinks...)
				topo.Sinks[0].Amount = float64(100 + i)
			}
			buf := core.NewBuffer(40, 8)
			results[i], errs[i] = e.Sankey(buf, topo, Options{})
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if results[i].Region.W <= 0 || len(results[i].Hits) == 0 {
			t.Fatalf("goroutine %d returned an unexpected empty result: %+v", i, results[i])
		}
	}
	// All even lanes rendered the identical topology and must agree.
	for i := 2; i < n; i += 2 {
		if !hitsEqual(results[0].Hits, results[i].Hits) {
			t.Fatalf("same-topology lane %d disagreed with lane 0", i)
		}
	}
}
