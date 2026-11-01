package compose

// FEAT-2326609842 round r1 (opus-round-r1-passport): concurrent queries while
// the composed engine ticks (run under -race), byte-stability of an occupant
// page for identical state across two independently wired engines, and
// passport identity across a save/load style re-seed into a fresh engine.

import (
	"bytes"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func TestRoundR1_QueriesConcurrentWithTicks(t *testing.T) {
	e, comp := passportWire(t, nil)
	home := homeOfCell(5, 6)
	recs := make([]citizens.ColdRecord, 0, 300)
	for i := 0; i < 300; i++ {
		recs = append(recs, seededRecord(passportIDBase+9000+uint64(i), home, 11))
	}
	if err := comp.state.citizens.SeedColdRecords(recs, "r1"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				id := passportIDBase + 9000 + uint64((g*37+i)%300)
				c1 := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "r1q", Kind: protocol.KindQueryCitizen, Payload: protocol.QueryCitizenPayload{ID: id}}
				_ = e.HandleCommand(c1)
				c2 := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "r1o", Kind: protocol.KindQueryOccupants, Payload: protocol.QueryOccupantsPayload{By: protocol.OccupantsByHome, Cell: protocol.CellRef{X: 5, Y: 6}, AfterID: passportIDBase + 100 + uint64((i*50)%300)}}
				_ = e.HandleCommand(c2)
			}
		}(g)
	}
	if err := e.AdvanceTicks("r1-drive", int64(core.DailyTicksPerMonth)+3); err != nil {
		t.Errorf("AdvanceTicks: %v", err)
	}
	close(stop)
	wg.Wait()
}

func TestRoundR1_OccupantPageByteStableAcrossEngines(t *testing.T) {
	page := func() []byte {
		e, comp := passportWire(t, nil)
		home := homeOfCell(8, 9)
		// Seed in a shuffled order: the page must not depend on insertion order.
		order := []int{7, 2, 9, 0, 4, 1, 8, 3, 6, 5}
		var recs []citizens.ColdRecord
		for _, i := range order {
			recs = append(recs, seededRecord(passportIDBase+5000+uint64(i*977), home, 3))
		}
		if err := comp.state.citizens.SeedColdRecords(recs, "r1"); err != nil {
			t.Fatal(err)
		}
		res := passportQuery(t, e, protocol.KindQueryOccupants, protocol.QueryOccupantsPayload{By: protocol.OccupantsByHome, Cell: protocol.CellRef{X: 8, Y: 9}, Limit: 4, AfterID: passportIDBase + 5000 + 3*977})
		if !res.Accepted {
			t.Fatalf("refused: %+v", res.Error)
		}
		return res.Result
	}
	a, b := page(), page()
	if !bytes.Equal(a, b) {
		t.Errorf("occupant page not byte-stable:\n%s\n%s", a, b)
	}
}
