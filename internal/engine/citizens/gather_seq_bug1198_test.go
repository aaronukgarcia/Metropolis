package citizens

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// BUG-1198: GatherInShardOrderSeq must visit the same (id, ok, wealth)
// sequence as GatherInShardOrder over the equivalent slice, with paging off
// AND on, including ids that do not resolve.
func TestGatherInShardOrderSeqMatchesSliceVisitOrder(t *testing.T) {
	for _, paged := range []bool{false, true} {
		api, err := NewCitizensAPI(1198, "bug1198")
		if err != nil {
			t.Fatalf("NewCitizensAPI: %v", err)
		}
		recs := make([]ColdRecord, 0, 400)
		for i := 1; i <= 400; i++ {
			r := mkRecord(uint64(i), uint16(i%9))
			r.BirthMonth = -300
			r.Wealth = int64(i * 31)
			recs = append(recs, r)
		}
		if err := api.SeedColdRecords(recs, "bug1198"); err != nil {
			t.Fatalf("SeedColdRecords: %v", err)
		}
		if paged {
			if err := api.EnableDiskPaging(t.TempDir(), 4, "bug1198"); err != nil {
				t.Fatalf("EnableDiskPaging: %v", err)
			}
		}
		// 1..450: ids 401..450 do not exist (ok == false).
		ids := make([]uint64, 0, 450)
		for i := uint64(1); i <= 450; i++ {
			ids = append(ids, i)
		}
		type visit struct {
			id uint64
			ok bool
			w  int64
		}
		var want, got []visit
		api.GatherInShardOrder(ids, "bug1198", func(id uint64, c Citizen, ok bool) {
			want = append(want, visit{id, ok, c.Wealth})
		})
		api.GatherInShardOrderSeq(func(y func(uint64) bool) {
			for _, id := range ids {
				if !y(id) {
					return
				}
			}
		}, "bug1198", func(id uint64, c Citizen, ok bool) {
			got = append(got, visit{id, ok, c.Wealth})
		})
		if len(want) != 450 || !reflect.DeepEqual(got, want) {
			t.Fatalf("paged=%v: seq visit order differs from slice order (len want %d got %d)", paged, len(want), len(got))
		}
		if paged {
			// Shard-grouped, so NOT id order: the test is only meaningful
			// if the order actually differs from input order.
			same := true
			for i := range want {
				if want[i].id != uint64(i+1) {
					same = false
				}
			}
			if same {
				t.Fatal("paged walk was in id order; shard grouping not exercised")
			}
			// Independent of the code under test: shards ascend, and ids
			// within a shard keep input (ascending) order.
			for i := 1; i < len(got); i++ {
				a, b := det.ShardForEntity(got[i-1].id), det.ShardForEntity(got[i].id)
				if a > b || (a == b && got[i-1].id > got[i].id) {
					t.Fatalf("paged walk not shard-ascending at %d: id %d (shard %d) then id %d (shard %d)", i, got[i-1].id, a, got[i].id, b)
				}
			}
		}
	}
}
