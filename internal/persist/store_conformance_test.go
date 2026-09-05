package persist

import (
	"bytes"
	"context"
	"errors"
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

	// BUG-737 (FEAT-143 wiring, round finding P1-4): the GameMode sidecar
	// mirrors WorldSeed's own conformance coverage exactly, sub-test for
	// sub-test.

	t.Run("game_mode_absent_by_default", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "never-moded"}

		mode, ok, err := s.GameMode(ctx, city)
		if err != nil {
			t.Fatalf("GameMode on never-moded city: %v", err)
		}
		if ok {
			t.Fatalf("GameMode ok=true mode=%q before any SetGameModeIfAbsent call, want ok=false", mode)
		}
	})

	t.Run("game_mode_set_once_first_call_wins", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "moded-once"}

		recorded, err := s.SetGameModeIfAbsent(ctx, city, "unlimited")
		if err != nil {
			t.Fatalf("SetGameModeIfAbsent (first): %v", err)
		}
		if recorded != "unlimited" {
			t.Fatalf("first SetGameModeIfAbsent recorded = %q, want %q", recorded, "unlimited")
		}

		// A second call with a DIFFERENT mode must NOT overwrite —
		// AC-3's whole point is "the ORIGINATING mode", immutable across
		// a restart, same as WorldSeed's own first-call-wins contract.
		recorded2, err := s.SetGameModeIfAbsent(ctx, city, "real")
		if err != nil {
			t.Fatalf("SetGameModeIfAbsent (second, different mode): %v", err)
		}
		if recorded2 != "unlimited" {
			t.Fatalf("second SetGameModeIfAbsent recorded = %q, want %q (first-call-wins, never overwritten)", recorded2, "unlimited")
		}

		mode, ok, err := s.GameMode(ctx, city)
		if err != nil {
			t.Fatalf("GameMode after two SetGameModeIfAbsent calls: %v", err)
		}
		if !ok {
			t.Fatal("GameMode ok=false after a successful SetGameModeIfAbsent")
		}
		if mode != "unlimited" {
			t.Fatalf("GameMode = %q, want %q", mode, "unlimited")
		}
	})

	t.Run("game_mode_empty_never_stamped", func(t *testing.T) {
		// BUG-737 round finding P1-2: an empty mode ("no chooser
		// opinion") must NEVER be durably stamped — unlike WorldSeed
		// (where 0 is a legitimate value), GameMode's zero value is
		// explicitly "absent", so a later call with a genuine mode must
		// still be able to win.
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "empty-first"}

		if recorded, err := s.SetGameModeIfAbsent(ctx, city, ""); err != nil {
			t.Fatalf("SetGameModeIfAbsent(\"\"): %v", err)
		} else if recorded != "" {
			t.Fatalf("SetGameModeIfAbsent(\"\") recorded = %q, want \"\" (nothing stamped)", recorded)
		}

		if _, ok, err := s.GameMode(ctx, city); err != nil {
			t.Fatalf("GameMode after empty stamp attempt: %v", err)
		} else if ok {
			t.Fatal("GameMode ok=true after ONLY an empty SetGameModeIfAbsent call — an empty mode must never be readable as recorded")
		}

		// A LATER call with a genuine mode must still win — proving the
		// empty attempt did not poison the "already recorded" state.
		recorded, err := s.SetGameModeIfAbsent(ctx, city, "unlimited")
		if err != nil {
			t.Fatalf("SetGameModeIfAbsent(\"unlimited\") after empty attempt: %v", err)
		}
		if recorded != "unlimited" {
			t.Fatalf("SetGameModeIfAbsent(\"unlimited\") recorded = %q, want %q — an earlier empty call permanently blocked a real chooser", recorded, "unlimited")
		}
	})

	t.Run("game_mode_isolated_per_city", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		cityX := CityKey{TenantID: "t", CityID: "mode-city-x"}
		cityY := CityKey{TenantID: "t", CityID: "mode-city-y"}

		if _, err := s.SetGameModeIfAbsent(ctx, cityX, "unlimited"); err != nil {
			t.Fatalf("SetGameModeIfAbsent(X): %v", err)
		}
		modeY, okY, err := s.GameMode(ctx, cityY)
		if err != nil {
			t.Fatalf("GameMode(Y): %v", err)
		}
		if okY {
			t.Fatalf("city Y reports a recorded mode (%q) after only city X was ever stamped — mode isolation leak", modeY)
		}
	})

	t.Run("stamping_a_mode_alone_does_not_make_a_city_exist", func(t *testing.T) {
		// Mirrors the identical WorldSeed regression test above:
		// SetGameModeIfAbsent must never, by itself, make Exists/
		// ListCities report a city with no real durable data.
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "mode-only-no-data"}

		if _, err := s.SetGameModeIfAbsent(ctx, city, "real"); err != nil {
			t.Fatalf("SetGameModeIfAbsent: %v", err)
		}
		ok, err := s.Exists(ctx, city)
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if ok {
			t.Fatal("Exists = true after ONLY a game-mode stamp, no journal or snapshot — a mode-only city must stay invisible")
		}
	})

	// BUG-737 round-2 lead ruling (2026-09-05): the mode-epoch marker
	// conformance coverage, mirroring the world-seed/game-mode ones
	// above sub-test for sub-test.

	t.Run("game_mode_epoch_absent_by_default", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "never-epoched"}

		has, err := s.HasGameModeEpoch(ctx, city)
		if err != nil {
			t.Fatalf("HasGameModeEpoch on never-touched city: %v", err)
		}
		if has {
			t.Fatal("HasGameModeEpoch = true before any SetGameModeEpoch call, want false")
		}
	})

	t.Run("game_mode_epoch_requires_world_seed", func(t *testing.T) {
		// BUG-737 re-round-3 finding P2-1: SetGameModeEpoch on a city
		// with NO recorded world seed must refuse with
		// ErrGameModeEpochWithoutSeed, never silently establish a
		// seedless epoch-only record (which, on the original DiskStore
		// implementation, meant an explicit world_seed:0 that WorldSeed
		// then read back as ok=true, seed=0 — bricking the city against
		// ever recording its real seed).
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "epoch-without-seed"}

		err := s.SetGameModeEpoch(ctx, city)
		if err == nil {
			t.Fatal("SetGameModeEpoch on a seedless city succeeded, want ErrGameModeEpochWithoutSeed")
		}
		if !errors.Is(err, ErrGameModeEpochWithoutSeed) {
			t.Fatalf("SetGameModeEpoch error = %v, want ErrGameModeEpochWithoutSeed", err)
		}
		// The refusal must not itself brick anything: a subsequent
		// legitimate SetWorldSeedIfAbsent then SetGameModeEpoch must
		// still succeed cleanly.
		if _, err := s.SetWorldSeedIfAbsent(ctx, city, 42); err != nil {
			t.Fatalf("SetWorldSeedIfAbsent after refused epoch: %v", err)
		}
		if err := s.SetGameModeEpoch(ctx, city); err != nil {
			t.Fatalf("SetGameModeEpoch after seeding: %v", err)
		}
	})

	t.Run("game_mode_epoch_set_once_idempotent", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "epoched-once"}

		if _, err := s.SetWorldSeedIfAbsent(ctx, city, 1); err != nil {
			t.Fatalf("SetWorldSeedIfAbsent: %v", err)
		}
		if err := s.SetGameModeEpoch(ctx, city); err != nil {
			t.Fatalf("SetGameModeEpoch (first): %v", err)
		}
		if err := s.SetGameModeEpoch(ctx, city); err != nil {
			t.Fatalf("SetGameModeEpoch (second, idempotent no-op): %v", err)
		}
		has, err := s.HasGameModeEpoch(ctx, city)
		if err != nil {
			t.Fatalf("HasGameModeEpoch after SetGameModeEpoch: %v", err)
		}
		if !has {
			t.Fatal("HasGameModeEpoch = false after a successful SetGameModeEpoch")
		}
	})

	t.Run("game_mode_epoch_isolated_per_city", func(t *testing.T) {
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		cityX := CityKey{TenantID: "t", CityID: "epoch-city-x"}
		cityY := CityKey{TenantID: "t", CityID: "epoch-city-y"}

		if _, err := s.SetWorldSeedIfAbsent(ctx, cityX, 1); err != nil {
			t.Fatalf("SetWorldSeedIfAbsent(X): %v", err)
		}
		if err := s.SetGameModeEpoch(ctx, cityX); err != nil {
			t.Fatalf("SetGameModeEpoch(X): %v", err)
		}
		hasY, err := s.HasGameModeEpoch(ctx, cityY)
		if err != nil {
			t.Fatalf("HasGameModeEpoch(Y): %v", err)
		}
		if hasY {
			t.Fatal("city Y reports an epoch marker after only city X was ever marked — epoch isolation leak")
		}
	})

	t.Run("game_mode_epoch_survives_game_mode_deletion", func(t *testing.T) {
		// The whole point of storing the epoch marker independently of
		// gamemode.json (BUG-737 round-2 lead ruling): SetGameModeIfAbsent
		// followed by SetGameModeEpoch, then a caller emulating a
		// deleted/lost gamemode.json by never touching GameMode again —
		// HasGameModeEpoch must still report true, since a real
		// implementation stores it in a DIFFERENT sidecar (DiskStore:
		// seed.json; MemStore: a separate struct field) that a
		// gamemode.json deletion never touches. This conformance test
		// cannot literally delete a file (MemStore has none), so it
		// proves the INDEPENDENCE property the only way that generalises
		// across both implementations: setting the epoch and the mode
		// are two separate calls, and the epoch persists even though
		// nothing here ever re-touches GameMode after establishing it.
		s := newStore(t) // static type Store — see storeFactory's signature
		ctx := context.Background()
		city := CityKey{TenantID: "t", CityID: "epoch-independent"}

		if _, err := s.SetWorldSeedIfAbsent(ctx, city, 1); err != nil {
			t.Fatalf("SetWorldSeedIfAbsent: %v", err)
		}
		if _, err := s.SetGameModeIfAbsent(ctx, city, "unlimited"); err != nil {
			t.Fatalf("SetGameModeIfAbsent: %v", err)
		}
		if err := s.SetGameModeEpoch(ctx, city); err != nil {
			t.Fatalf("SetGameModeEpoch: %v", err)
		}
		has, err := s.HasGameModeEpoch(ctx, city)
		if err != nil {
			t.Fatalf("HasGameModeEpoch: %v", err)
		}
		if !has {
			t.Fatal("HasGameModeEpoch = false after SetGameModeEpoch — the epoch marker must be independently durable")
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
