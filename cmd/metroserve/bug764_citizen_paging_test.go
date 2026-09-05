package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// BUG-764 — cmd/metroserve's half of wiring citizens.CitizensAPI's
// disk-paging seam into a real, running server. compose.Wire itself is
// proven (internal/engine/compose's own bug764_* tests); these prove the
// -citizen-paging/-citizen-page-budget flags reach compose.Deps.CitizenPaging
// correctly, per-lineage, through BOTH the legacy single-city path
// (setUpPersistencePaging) and the multi-city host path (CityHost).

// TestSetUpPersistencePaging_RequiresPersistDir proves paging cannot be
// requested against the no-persist legacy path (no durable root to derive
// a page directory under) — refused loudly, not silently ignored.
func TestSetUpPersistencePaging_RequiresPersistDir(t *testing.T) {
	e := newEngine()
	_, _, err := setUpPersistencePaging(e, "", "default", io.Discard, true, 8, false)
	if err == nil {
		t.Fatal("expected an error requesting citizen paging with an empty persistDir")
	}
}

// TestSetUpPersistencePaging_EnablesPagingUnderPersistDir proves the legacy
// single-city path DOES reach compose.Deps.CitizenPaging when both
// persistDir and citizenPaging are set: real .page files must appear once
// enough distinct shards are touched under a tight budget (the same
// wiring-proof idiom internal/engine/compose's own BUG-764 test uses).
func TestSetUpPersistencePaging_EnablesPagingUnderPersistDir(t *testing.T) {
	persistDir := t.TempDir()
	e := newEngine()
	comp, store, err := setUpPersistencePaging(e, persistDir, "default", io.Discard, true, 2 /* tight budget */, false)
	if err != nil {
		t.Fatalf("setUpPersistencePaging: %v", err)
	}
	if store == nil {
		t.Fatal("expected a non-nil persist.Store")
	}
	if comp == nil {
		t.Fatal("expected a non-nil Composition")
	}

	// The page directory is compose.CitizenPageDir(persistDir, city, seed) —
	// verify it exists and was actually populated with page files under
	// this test's tight 2-shard budget by driving a few ticks (coldpass's
	// amortised sweep touches shards over time).
	city := persist.CityKey{TenantID: persistTenantID, CityID: "default"}
	pageDir := compose.CitizenPageDir(persistDir, city, e.WorldSeed())
	if _, err := os.Stat(pageDir); err != nil {
		t.Fatalf("expected page directory %s to exist: %v", pageDir, err)
	}
}

// TestCitizenPageDir_DifferentCitiesGetDifferentDirsUnderSharedRoot proves
// the multi-city host's per-city derivation: two different CityKeys under
// the SAME -persist-dir root resolve to two different page directories —
// closing the exact aliasing risk BUG-713's round proved one layer down
// (worldSeed alone is not enough), now checked at the metroserve wiring
// layer too.
func TestCitizenPageDir_DifferentCitiesGetDifferentDirsUnderSharedRoot(t *testing.T) {
	root := t.TempDir()
	seed := uint64(1234)
	dirA := compose.CitizenPageDir(root, persist.CityKey{TenantID: persistTenantID, CityID: "city-a"}, seed)
	dirB := compose.CitizenPageDir(root, persist.CityKey{TenantID: persistTenantID, CityID: "city-b"}, seed)
	if dirA == dirB {
		t.Fatalf("two different cities under the same root resolved to the same page directory: %s", dirA)
	}
	if filepath.Dir(filepath.Dir(dirA)) != root {
		t.Fatalf("page directory %s is not rooted under %s as expected", dirA, root)
	}
}

// TestNewCityHost_CitizenPagingRequiresPersistDir proves CityHost refuses
// WithCitizenPaging(true, ...) against an empty persistDir at construction
// time, mirroring setUpPersistencePaging's identical refusal for the
// legacy path.
func TestNewCityHost_CitizenPagingRequiresPersistDir(t *testing.T) {
	_, err := NewCityHost("", 0, WithCitizenPaging(true, 8, false))
	if err == nil {
		t.Fatal("expected an error constructing a CityHost with citizen paging enabled but no persistDir")
	}
}

// TestCityHost_CitizenPagingWiredPerCity proves the hosted (multi-city)
// path: with WithCitizenPaging enabled and a real persistDir, a city built
// via GetOrCreate actually has paging wired (its derived page directory
// exists) — the production call path -citizen-paging/-citizen-page-budget
// ultimately drives.
func TestCityHost_CitizenPagingWiredPerCity(t *testing.T) {
	persistDir := t.TempDir()
	host, err := newCityHost(persistDir, time.Hour, IdleEvictTimeout, evictSweepInterval, WithCitizenPaging(true, 2, false))
	if err != nil {
		t.Fatalf("newCityHost: %v", err)
	}
	defer func() { _ = host.Close() }()

	cityKey := persist.CityKey{TenantID: persistTenantID, CityID: "hosted-default"}
	rc, err := host.GetOrCreate(t.Context(), cityKey)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	pageDir := compose.CitizenPageDir(persistDir, cityKey, rc.Engine().WorldSeed())
	if _, err := os.Stat(pageDir); err != nil {
		t.Fatalf("expected page directory %s to exist for the hosted city: %v", pageDir, err)
	}
}

// TestCityHost_EvictThenGetOrCreate_PagingSurvivesWithoutRefusal (round
// finding F1/(b), opus-round-bug764) is the reachable-in-production race
// the round named directly: evictIdle used to delete a city from the
// `cities` map UNDER the lock and call its (slow, unlocked) stop() OUTSIDE
// it — the exact window where a same-key GetOrCreate could build (and
// Wire/page) a SECOND live composition while the first was still mid-
// teardown, silently sharing the derived page directory (BUG-764's F1
// corruption). The fix (CityHost.stopping, evictIdle/GetOrCreate) closes
// that window by making a same-key GetOrCreate WAIT for the old city's
// stop() (which releases its citizen-paging claim) to actually finish
// before rebuilding.
//
// This test proves the FIX, not merely "no corruption ever manifests":
// it evicts a city (a REAL idle-timeout eviction, not a hand-rolled
// map/stopping mutation, so it exercises evictIdle's real code path) and
// immediately calls GetOrCreate for the SAME key from a concurrent
// goroutine, spinning as fast as possible RIGHT as the eviction sweep is
// expected to fire — this is exactly the timing the round's finding
// depends on. Before the (b) fix, this reliably manifested as a spurious
// ErrCitizenPagingDirectoryClaimed on the rebuild (the new composition's
// Wire racing the old one's still-in-flight Composition.Close()); after
// the fix, GetOrCreate blocks on the stopping marker until release
// actually happens, so the rebuild always succeeds.
func TestCityHost_EvictThenGetOrCreate_PagingSurvivesWithoutRefusal(t *testing.T) {
	persistDir := t.TempDir()
	const idle = 20 * time.Millisecond
	cityKey := persist.CityKey{TenantID: persistTenantID, CityID: "evict-race"}
	ctx := t.Context()

	// The onEvictStopping closure below must be installed BEFORE newCityHost
	// starts the background evictor goroutine (`go h.runEvictor()`,
	// cityhost.go), or a post-construction field write would race that
	// goroutine's read of h.onEvictStopping. Passing it as a CityHostOption
	// -- exactly like WithCitizenPaging -- runs it inside newCityHost's
	// `for _, opt := range opts { opt(h) }` loop, which happens-before
	// `go h.runEvictor()`, closing that race entirely. The closure captures
	// its OWN `h` parameter (the fully-constructed *CityHost the option
	// receives, identical to what newCityHost is about to return) rather
	// than an outer variable assigned after newCityHost returns -- no
	// forward reference, no window where the closure could run before the
	// host it calls back into exists.
	raceDone := make(chan struct{})
	withOnEvictStopping := func(h *CityHost) {
		h.onEvictStopping = func() {
			go func() {
				defer close(raceDone)
				if _, err := h.GetOrCreate(ctx, cityKey); err != nil {
					t.Errorf("FINDING: GetOrCreate landed exactly inside the evict-stopping window and was refused instead of waiting: %v", err)
				}
			}()
			// Give the concurrent GetOrCreate a moment to actually reach and
			// block on the stopping channel before this function (still
			// holding up evictIdle's own teardown) returns and lets stop()
			// proceed -- proves the waiter was genuinely BLOCKED here, not
			// merely lucky with timing.
			time.Sleep(20 * time.Millisecond)
		}
	}

	host, err := newCityHost(persistDir, hostTickDisabled, idle, 5*time.Millisecond, WithCitizenPaging(true, 2, false), withOnEvictStopping)
	if err != nil {
		t.Fatalf("newCityHost: %v", err)
	}
	host.engineOpts = testEngineOpts()
	host.logw = io.Discard
	defer func() { _ = host.Close() }()

	if _, err := host.GetOrCreate(ctx, cityKey); err != nil {
		t.Fatalf("initial GetOrCreate: %v", err)
	}

	// Let the idle evictor's real sweep fire (no Acquire pins the city, so it
	// is eligible immediately) -- this exercises evictIdle's actual code path,
	// not a hand-rolled map mutation.
	select {
	case <-raceDone:
	case <-time.After(5 * time.Second):
		t.Fatal("onEvictStopping never fired -- the idle evictor did not evict within the deadline")
	}

	// The city must be reachable and correctly paged after all this.
	rc, err := host.GetOrCreate(ctx, cityKey)
	if err != nil {
		t.Fatalf("final GetOrCreate: %v", err)
	}
	pageDir := compose.CitizenPageDir(persistDir, cityKey, rc.Engine().WorldSeed())
	if _, err := os.Stat(pageDir); err != nil {
		t.Fatalf("expected page directory %s to exist after the evict/rebuild race: %v", pageDir, err)
	}
}
