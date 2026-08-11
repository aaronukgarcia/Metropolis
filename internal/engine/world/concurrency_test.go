package world

import (
	"sync"
	"testing"
)

// TestConcurrentQueriesAndPurchasesRaceFree is AC-20's race-detector
// coverage: many goroutines hammering CellAt/TileAt/PurchaseTile/
// ApplyOwnershipCommand/Prospect across a shared WorldAPI, run under
// `go test -race`. Spatial "shards" here are simply distinct TileCoords
// per goroutine (M0-ENG §1.2's shard-per-region principle applied to
// this package's own tile-keyed locking, rather than foundation/det's
// tick-path shard pool, which this build-time/query package does not
// itself run phases through).
func TestConcurrentQueriesAndPurchasesRaceFree(t *testing.T) {
	api := NewWorldAPI(TileCoord{15, 13})
	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			tc := TileCoord{X: g % TilesPerSide, Y: (g * 7) % TilesPerSide}
			for i := 0; i < 20; i++ {
				_, _ = api.TileAt(tc, "corr")
				_, _ = api.CellAt(tc, CellLocal{Row: i % TileSizeCells, Col: (i * 3) % TileSizeCells}, "corr")
				_ = api.Prospect(tc, "corr")
				_, _ = api.PocketGeology(tc, "corr")
				_ = api.ApplyOwnershipCommand(OwnershipCommand{
					CorrelationID: "corr", Tile: tc, Local: CellLocal{Row: 0, Col: 0}, NewOwner: uint32(g),
				})
			}
			_ = api.PurchaseTile(PurchaseCommand{CorrelationID: "corr", Tile: tc, BuyerID: uint32(g)})
			_ = api.ApplyOwnershipCommand(OwnershipCommand{
				CorrelationID: "corr", Tile: tc, Local: CellLocal{Row: 1, Col: 1}, NewOwner: uint32(g),
			})
		}(g)
	}
	wg.Wait()
}
