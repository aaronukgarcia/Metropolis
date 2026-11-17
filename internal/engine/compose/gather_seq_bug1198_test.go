package compose

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

// BUG-1198: the streaming visitor must enumerate EXACTLY liveResidentIDs()
// (same ids, same order) and the shard-ordered walk through it must visit
// the same sequence as the old slice-fed GatherInShardOrder, at month 0 and
// after the population has grown by migration/fertility.
func TestEachLiveResidentIDMatchesSliceAndVisitOrder(t *testing.T) {
	e, comp := bugComposition(t, 1)
	for _, month := range []int{0, 14} {
		if month > 0 {
			runMonths(t, e, month)
		}
		st := comp.state
		want := append([]uint64(nil), st.liveResidentIDs()...)
		var got []uint64
		st.eachLiveResidentID(func(id uint64) bool { got = append(got, id); return true })
		if len(want) == 0 || !reflect.DeepEqual(got, want) {
			t.Fatalf("month %d: visitor ids differ from liveResidentIDs() (want %d got %d)", month, len(want), len(got))
		}
		type visit struct {
			id uint64
			ok bool
		}
		var oldWalk, newWalk []visit
		st.citizens.GatherInShardOrder(want, st.cid, func(id uint64, _ citizens.Citizen, ok bool) {
			oldWalk = append(oldWalk, visit{id, ok})
		})
		st.gatherLiveResidents(func(id uint64, _ citizens.Citizen, ok bool) {
			newWalk = append(newWalk, visit{id, ok})
		})
		if !reflect.DeepEqual(oldWalk, newWalk) {
			t.Fatalf("month %d: shard-ordered walk differs from the slice-fed walk", month)
		}
		n := 0
		st.eachLiveResidentID(func(uint64) bool { n++; return n < 3 })
		if n != 3 {
			t.Fatalf("early stop: visited %d, want 3", n)
		}
	}
}
