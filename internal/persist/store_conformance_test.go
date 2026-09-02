package persist

import (
	"bytes"
	"context"
	"fmt"
	"testing"
)

// storeFactory builds a fresh, empty Store for one subtest.
type storeFactory func(t *testing.T) Store

// runStoreConformance exercises the full Store contract through the
// interface type only (never the concrete struct) — this is AC-1's
// "false-pass guard": `var s Store = <impl>` and every call goes
// through s, so swapping DiskStore for MemStore here and watching
// these same test bodies pass unmodified is the proof the abstraction
// isn't leaky.
func runStoreConformance(t *testing.T, newStore storeFactory) {
	t.Helper()

	t.Run("journal_append_read_roundtrip_in_order", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "tenant-a", CityID: "city-1"}

		want := [][]byte{
			[]byte("record-0"),
			[]byte("record-1"),
			[]byte("record-2"),
		}
		for _, rec := range want {
			if err := s.AppendJournal(ctx, city, rec); err != nil {
				t.Fatalf("AppendJournal: %v", err)
			}
		}

		got, err := s.ReadJournal(ctx, city)
		if err != nil {
			t.Fatalf("ReadJournal: %v", err)
		}
		if len(got) != len(want) {
			t.Fatalf("record count = %d, want %d", len(got), len(want))
		}
		for i := range want {
			if !bytes.Equal(got[i], want[i]) {
				t.Fatalf("record[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("empty_journal_reads_as_empty_not_error", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "never-written"}

		got, err := s.ReadJournal(ctx, city)
		if err != nil {
			t.Fatalf("ReadJournal on unwritten city: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected zero records, got %d", len(got))
		}
	})

	t.Run("snapshot_write_read_list", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "tenant-a", CityID: "city-2"}

		id1, err := s.PutSnapshot(ctx, city, []byte("snap-1"))
		if err != nil {
			t.Fatalf("PutSnapshot 1: %v", err)
		}
		id2, err := s.PutSnapshot(ctx, city, []byte("snap-2"))
		if err != nil {
			t.Fatalf("PutSnapshot 2: %v", err)
		}
		if id1 == id2 {
			t.Fatalf("expected distinct snapshot ids, got %q twice", id1)
		}

		got1, err := s.GetSnapshot(ctx, city, id1)
		if err != nil {
			t.Fatalf("GetSnapshot 1: %v", err)
		}
		if string(got1) != "snap-1" {
			t.Fatalf("snapshot 1 = %q, want snap-1", got1)
		}

		ids, err := s.ListSnapshots(ctx, city)
		if err != nil {
			t.Fatalf("ListSnapshots: %v", err)
		}
		if len(ids) != 2 || ids[0] != id1 || ids[1] != id2 {
			t.Fatalf("ListSnapshots = %v, want [%s %s] in order", ids, id1, id2)
		}
	})

	t.Run("get missing snapshot is not found", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "c"}
		if _, err := s.PutSnapshot(ctx, city, []byte("x")); err != nil {
			t.Fatalf("PutSnapshot: %v", err)
		}
		if _, err := s.GetSnapshot(ctx, city, SnapshotID("does-not-exist")); err != ErrNotFound {
			t.Fatalf("GetSnapshot missing id: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("delete_snapshot_removes_it_and_is_idempotent", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "tenant-a", CityID: "city-delete"}

		id1, err := s.PutSnapshot(ctx, city, []byte("snap-1"))
		if err != nil {
			t.Fatalf("PutSnapshot 1: %v", err)
		}
		id2, err := s.PutSnapshot(ctx, city, []byte("snap-2"))
		if err != nil {
			t.Fatalf("PutSnapshot 2: %v", err)
		}

		if err := s.DeleteSnapshot(ctx, city, id1); err != nil {
			t.Fatalf("DeleteSnapshot: %v", err)
		}
		if _, err := s.GetSnapshot(ctx, city, id1); err != ErrNotFound {
			t.Fatalf("GetSnapshot after delete: err = %v, want ErrNotFound", err)
		}
		ids, err := s.ListSnapshots(ctx, city)
		if err != nil {
			t.Fatalf("ListSnapshots: %v", err)
		}
		if len(ids) != 1 || ids[0] != id2 {
			t.Fatalf("ListSnapshots after delete = %v, want [%s]", ids, id2)
		}

		// Deleting again (already gone) and deleting a never-existed id are
		// both no-ops, not errors — pruning must be safe to retry.
		if err := s.DeleteSnapshot(ctx, city, id1); err != nil {
			t.Fatalf("DeleteSnapshot (already deleted): %v", err)
		}
		if err := s.DeleteSnapshot(ctx, city, SnapshotID("never-existed")); err != nil {
			t.Fatalf("DeleteSnapshot (never existed): %v", err)
		}
	})

	t.Run("city_isolation_journal_and_snapshots", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		cityA := CityKey{TenantID: "tenant-x", CityID: "alice"}
		cityB := CityKey{TenantID: "tenant-x", CityID: "bob"}

		if err := s.AppendJournal(ctx, cityA, []byte("alice-only")); err != nil {
			t.Fatalf("append A: %v", err)
		}
		if _, err := s.PutSnapshot(ctx, cityA, []byte("alice-snap")); err != nil {
			t.Fatalf("snapshot A: %v", err)
		}

		bJournal, err := s.ReadJournal(ctx, cityB)
		if err != nil {
			t.Fatalf("ReadJournal B: %v", err)
		}
		if len(bJournal) != 0 {
			t.Fatalf("city B saw %d journal records from city A, want 0", len(bJournal))
		}
		bSnaps, err := s.ListSnapshots(ctx, cityB)
		if err != nil {
			t.Fatalf("ListSnapshots B: %v", err)
		}
		if len(bSnaps) != 0 {
			t.Fatalf("city B saw %d snapshots from city A, want 0", len(bSnaps))
		}
	})

	t.Run("list_cities_under_tenant", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		tenant := "tenant-list"
		keys := []CityKey{
			{TenantID: tenant, CityID: "zeta"},
			{TenantID: tenant, CityID: "alpha"},
			{TenantID: "other-tenant", CityID: "should-not-appear"},
		}
		for _, k := range keys {
			if err := s.AppendJournal(ctx, k, []byte("x")); err != nil {
				t.Fatalf("AppendJournal(%v): %v", k, err)
			}
		}

		got, err := s.ListCities(ctx, tenant)
		if err != nil {
			t.Fatalf("ListCities: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("ListCities returned %d cities, want 2: %v", len(got), got)
		}
		// Deterministic order: sorted by CityID ascending.
		if got[0].CityID != "alpha" || got[1].CityID != "zeta" {
			t.Fatalf("ListCities order = %v, want [alpha zeta]", got)
		}
		for _, k := range got {
			if k.TenantID != tenant {
				t.Fatalf("ListCities leaked a foreign tenant: %v", k)
			}
		}
	})

	t.Run("exists", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "c"}

		ok, err := s.Exists(ctx, city)
		if err != nil {
			t.Fatalf("Exists before write: %v", err)
		}
		if ok {
			t.Fatalf("Exists = true before any write")
		}

		if err := s.AppendJournal(ctx, city, []byte("x")); err != nil {
			t.Fatalf("AppendJournal: %v", err)
		}

		ok, err = s.Exists(ctx, city)
		if err != nil {
			t.Fatalf("Exists after write: %v", err)
		}
		if !ok {
			t.Fatalf("Exists = false after a write")
		}
	})

	t.Run("world_seed_absent_by_default", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "never-seeded"}

		seed, ok, err := s.WorldSeed(ctx, city)
		if err != nil {
			t.Fatalf("WorldSeed on never-seeded city: %v", err)
		}
		if ok {
			t.Fatalf("WorldSeed ok=true seed=%d before any SetWorldSeedIfAbsent call, want ok=false", seed)
		}
	})

	t.Run("world_seed_set_once_first_call_wins", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "seeded-once"}

		recorded, err := s.SetWorldSeedIfAbsent(ctx, city, 111)
		if err != nil {
			t.Fatalf("SetWorldSeedIfAbsent (first): %v", err)
		}
		if recorded != 111 {
			t.Fatalf("first SetWorldSeedIfAbsent recorded = %d, want 111", recorded)
		}

		// A second call with a DIFFERENT seed must NOT overwrite — BUG-488's
		// whole point is "the ORIGINATING seed", which is only ever set
		// once per city.
		recorded2, err := s.SetWorldSeedIfAbsent(ctx, city, 222)
		if err != nil {
			t.Fatalf("SetWorldSeedIfAbsent (second, different seed): %v", err)
		}
		if recorded2 != 111 {
			t.Fatalf("second SetWorldSeedIfAbsent recorded = %d, want 111 (first-call-wins, never overwritten)", recorded2)
		}

		seed, ok, err := s.WorldSeed(ctx, city)
		if err != nil {
			t.Fatalf("WorldSeed after two SetWorldSeedIfAbsent calls: %v", err)
		}
		if !ok {
			t.Fatal("WorldSeed ok=false after a successful SetWorldSeedIfAbsent")
		}
		if seed != 111 {
			t.Fatalf("WorldSeed = %d, want 111", seed)
		}
	})

	t.Run("world_seed_isolated_per_city", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		cityX := CityKey{TenantID: "t", CityID: "seed-city-x"}
		cityY := CityKey{TenantID: "t", CityID: "seed-city-y"}

		if _, err := s.SetWorldSeedIfAbsent(ctx, cityX, 42); err != nil {
			t.Fatalf("SetWorldSeedIfAbsent(X): %v", err)
		}
		seedY, okY, err := s.WorldSeed(ctx, cityY)
		if err != nil {
			t.Fatalf("WorldSeed(Y): %v", err)
		}
		if okY {
			t.Fatalf("city Y reports a recorded seed (%d) after only city X was ever stamped — seed isolation leak", seedY)
		}
	})

	t.Run("stamping_a_seed_alone_does_not_make_a_city_exist", func(t *testing.T) {
		// BUG-488 regression: SetWorldSeedIfAbsent must never, by itself,
		// make Exists/ListCities report a city that has no real durable
		// data (no journal record, no snapshot) — that was an actual
		// regression caught while landing this fix
		// (TestAttackInc3b_ForeignCitySnapshotNotUsed briefly broke because
		// an earlier version of this stamp wrote meta.json as a side
		// effect).
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "seed-only-no-data"}

		if _, err := s.SetWorldSeedIfAbsent(ctx, city, 7); err != nil {
			t.Fatalf("SetWorldSeedIfAbsent: %v", err)
		}
		ok, err := s.Exists(ctx, city)
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if ok {
			t.Fatal("Exists = true after ONLY a world-seed stamp, no journal or snapshot — a seed-only city must stay invisible")
		}
	})

	t.Run("concurrent_append_same_city_no_corruption", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "concurrent"}

		const n = 50
		errs := make(chan error, n)
		for i := 0; i < n; i++ {
			go func(i int) {
				errs <- s.AppendJournal(ctx, city, []byte(fmt.Sprintf("rec-%03d", i)))
			}(i)
		}
		for i := 0; i < n; i++ {
			if err := <-errs; err != nil {
				t.Fatalf("concurrent AppendJournal: %v", err)
			}
		}

		got, err := s.ReadJournal(ctx, city)
		if err != nil {
			t.Fatalf("ReadJournal: %v", err)
		}
		if len(got) != n {
			t.Fatalf("record count after concurrent appends = %d, want %d (corruption or lost write)", len(got), n)
		}
		seen := make(map[string]bool, n)
		for _, rec := range got {
			if seen[string(rec)] {
				t.Fatalf("duplicate record %q — corruption", rec)
			}
			seen[string(rec)] = true
		}
	})
}

func TestDiskStoreConformance(t *testing.T) {
	runStoreConformance(t, func(t *testing.T) Store {
		t.Helper()
		st, err := NewDiskStore(t.TempDir())
		if err != nil {
			t.Fatalf("NewDiskStore: %v", err)
		}
		return st
	})
}

func TestMemStoreConformance(t *testing.T) {
	runStoreConformance(t, func(t *testing.T) Store {
		t.Helper()
		return NewMemStore()
	})
}
