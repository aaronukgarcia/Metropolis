package roads

import (
	"errors"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestConcurrentConstructionAndQuery (AC-18) hammers one RoadsAPI from
// multiple goroutines, constructing roads and querying them concurrently.
// Every result is success or the one specific error a query may legitimately
// return (ErrRoadNotFound for a road this goroutine has not added yet) —
// never a third outcome, never a panic. Run under -race by the baseline.
func TestConcurrentConstructionAndQuery(t *testing.T) {
	a := newTestAPI(t)
	const (
		workers   = 8
		perWorker = 60
	)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		w := w // per-iteration capture (Go 1.22+ makes this explicit and safe)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := RoadID(w*perWorker + i + 1)
				startID, endID := NodeID(2*id+1), NodeID(2*id+2)
				if err := a.AddNode(AddNodeCommand{CorrelationID: "c", ID: startID, Pos: testCellRef(id)}); err != nil {
					t.Errorf("AddNode: %v", err)
					return
				}
				if err := a.AddNode(AddNodeCommand{CorrelationID: "c", ID: endID, Pos: testCellRef(id + 1)}); err != nil {
					t.Errorf("AddNode: %v", err)
					return
				}
				if _, err := a.AddRoad(AddRoadCommand{CorrelationID: "c", ID: id, Start: startID, End: endID, Class: ClassTwoLane}); err != nil {
					t.Errorf("AddRoad: %v", err)
					return
				}
				// Own road: must succeed.
				if _, err := a.RoadInfo(id, 0); err != nil {
					t.Errorf("RoadInfo(own %d): %v", id, err)
					return
				}
				// A foreign road: success or ErrRoadNotFound, nothing else.
				if _, err := a.CurrentLaneCount(RoadID(id+workers), 0); err != nil && !errors.Is(err, &errs.E{Code: ErrRoadNotFound}) {
					t.Errorf("CurrentLaneCount(foreign): %v", err)
					return
				}
				if _, err := a.MaintenanceState(id); err != nil {
					t.Errorf("MaintenanceState(own %d): %v", id, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// Every road was added exactly once.
	if len(a.roads) != workers*perWorker {
		t.Fatalf("road count = %d, want %d", len(a.roads), workers*perWorker)
	}
}

// testCellRef builds a valid in-tile CellRef from an integer (row/col kept
// inside [0, 200) so positions stay within one tile).
func testCellRef(v RoadID) CellRef {
	return CellRef{
		Tile:  world.TileCoord{X: 0, Y: 0},
		Local: world.CellLocal{Row: int(v) % 200, Col: (int(v) / 200) % 200},
	}
}
