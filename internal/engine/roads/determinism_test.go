package roads

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
)

// TestRoadsDeterminism (AC-14) asserts that naming the same seed+id set and
// constructing the same graph produces a byte-identical canonical hash at
// three distinct worker counts (1, 4, 14), each repeated twice, over a
// fixture of hundreds of roads — so a shard-partition-dependent ordering bug
// would actually surface in the compared hash rather than coincidentally
// match. State is CONSTRUCTED (concurrent AddRoad over disjoint ID ranges),
// never raced for timing.
func TestRoadsDeterminism(t *testing.T) {
	const (
		numRoads = 300
		seed     = uint64(42)
		repeats  = 2
	)
	workerCounts := []int{1, 4, 14}

	var reference string
	for _, workers := range workerCounts {
		for r := 0; r < repeats; r++ {
			a, err := LoadDefault(seed, "determinism")
			if err != nil {
				t.Fatalf("LoadDefault: %v", err)
			}
			buildRoadsConcurrently(t, a, workers, numRoads)
			if err := a.Advance(120); err != nil {
				t.Fatalf("Advance: %v", err)
			}
			h := snapshotHash(a)
			if reference == "" {
				reference = h
			} else if h != reference {
				t.Fatalf("determinism divergence at workers=%d repeat=%d: hash %s != reference %s", workers, r, h, reference)
			}
		}
	}
}

// buildRoadsConcurrently adds n roads over workers goroutines, each goroutine
// owning a disjoint ID range (its own node IDs), so the final graph state is
// order-independent. Uses t.Errorf (never Fatalf) inside goroutines.
func buildRoadsConcurrently(t *testing.T, a *RoadsAPI, workers, n int) {
	t.Helper()
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for id := RoadID(w + 1); id <= RoadID(n); id += RoadID(workers) {
				class := RoadClass(int(id) % int(numClasses))
				startID, endID := NodeID(2*id+1), NodeID(2*id+2)
				// Keep row in [0,200) and spread across tile rows, so every
				// position stays inside the documented world domain (SEC-222:
				// AddNode now rejects an out-of-domain local coordinate).
				tileY := int(id) / 150
				row := 10 + int(id)%150
				if err := a.AddNode(AddNodeCommand{CorrelationID: "d", ID: startID, Pos: CellRef{Tile: world.TileCoord{X: 0, Y: tileY}, Local: world.CellLocal{Row: row, Col: 10}}}); err != nil {
					t.Errorf("AddNode start: %v", err)
					return
				}
				if err := a.AddNode(AddNodeCommand{CorrelationID: "d", ID: endID, Pos: CellRef{Tile: world.TileCoord{X: 0, Y: tileY}, Local: world.CellLocal{Row: row, Col: 20}}}); err != nil {
					t.Errorf("AddNode end: %v", err)
					return
				}
				if _, err := a.AddRoad(AddRoadCommand{CorrelationID: "d", ID: id, Start: startID, End: endID, Class: class}); err != nil {
					t.Errorf("AddRoad: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}

// snapshotHash renders the graph's canonical state (sorted by road ID) into
// a sha256 hex digest: name, class, steady lanes, speed limit, endpoints,
// condition and footprint size — the AC-14 "names, graph structure,
// maintenance state" set.
func snapshotHash(a *RoadsAPI) string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	ids := make([]RoadID, 0, len(a.roads))
	for id := range a.roads {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	var sb strings.Builder
	for _, id := range ids {
		rs := a.roads[id]
		fmt.Fprintf(&sb, "%d|%s|%s|%d|%d|%d|%d|%.17g|%d\n",
			rs.id, rs.name, rs.class.String(),
			a.cfg.classes[rs.class].Lanes, rs.speedLimit,
			rs.start, rs.end, rs.condition, len(rs.footprint))
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}
