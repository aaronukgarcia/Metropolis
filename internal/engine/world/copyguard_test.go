package world

import (
	"errors"
	"sync"
	"testing"
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestBUG064_WorldValueCopy_EnsureTileRejected is BUG-064's regression
// test (AC-26/AC-27/AC-29): astgate's live-tree scan named ensureTile as
// an unguarded reachable function for the World candidate type
// (grid.go:148 pre-fix). A same-package value copy `cp := *w` gets its
// OWN, independently-zeroed mu but ALIASES w.tiles (a map, a reference
// type) — the exact SEC-014/SEC-016 shape Destructive-2 reproduced
// against Engine. Calling ensureTile on the copy must be rejected with
// ErrWorldCopied, never silently succeed against the aliased map.
//
// PRE-FIX STATUS (proven, not assumed): before this fix, World had no
// self field and no checkNotCopied method at all — ensureTile returned
// only *tile, with no error path to reject a copy through. Live-verified
// via a disposable `git worktree add --detach HEAD` (real repo
// untouched, worktree removed after use): copying this exact test file
// into that pre-fix worktree and running `go vet ./internal/engine/world/...`
// fails to even COMPILE —
//
//	internal\engine\world\copyguard_test.go:36:15: assignment mismatch:
//	2 variables but w.ensureTile returns 1 value
//
// — confirming ErrWorldCopied/self/checkNotCopied are all genuinely new
// symbols this fix introduces (not a rename of something already
// there), and that a value-copy of World pre-fix had no guard capable
// of rejecting it at all.
func TestBUG064_WorldValueCopy_EnsureTileRejected(t *testing.T) {
	w := NewWorld(TileCoord{15, 13})

	// Seed some real state on the original so the copy's aliasing is
	// observable, not merely a theoretical concern.
	orig, err := w.ensureTile(TileCoord{5, 5})
	if err != nil {
		t.Fatalf("setup ensureTile on original: %v", err)
	}
	orig.owned = true

	// The attack: a plain struct copy. Legal Go, no unsafe, no reflect —
	// every field of World is unexported, but that does not stop a
	// same-package copy (and WorldAPI itself only ever holds a *World,
	// so this is the realistic shape any future same-package helper that
	// forgot to take a pointer receiver could introduce).
	cp := w2Copy(w)

	// The copy's mu is its own, freshly-zeroed sync.RWMutex — nothing
	// about it looks locked or invalid, so a check that only inspected
	// mu's own state (rather than identity) would find nothing wrong.
	// checkNotCopied instead compares the receiver's address against
	// self, which the copy operation cannot have carried forward to
	// point at itself.
	if _, err := cp.ensureTile(TileCoord{6, 6}); !errors.Is(err, &errs.E{Code: ErrWorldCopied}) {
		t.Fatalf("cp.ensureTile on a value-copied World: err = %v, want ErrWorldCopied", err)
	}

	// Confirm the rejection actually prevented the write — the copy's
	// tiles map is the SAME map as the original's (map headers copy by
	// reference), so an accepted call here would have mutated w.tiles
	// too, not just cp.tiles.
	if _, ok := w.tiles[TileCoord{6, 6}]; ok {
		t.Fatal("BUG-064 regression: rejected ensureTile on the copy still wrote into the ALIASED tiles map")
	}
}

// TestBUG064_WorldAPI_CopiedWorldRejected is AC-28/AC-29's WorldAPI-level
// proof: astgate cannot see WorldAPI's methods as reachable via its
// `w *World` field (this section's own header investigation), so the
// guard on World alone is not sufficient — every WorldAPI method that
// touches a.w.mu must independently reject a copied *World reached
// through a.w. Constructs a WorldAPI wrapping a value-copy of a real,
// populated World (mirroring BUG-064's own live-verified repro: two
// independently-zeroed RWMutex instances over one shared map) and
// confirms EVERY one of the 11 a.w.mu-touching methods rejects it with
// ErrWorldCopied, none silently succeeding against the aliased map.
func TestBUG064_WorldAPI_CopiedWorldRejected(t *testing.T) {
	original := NewWorldAPI(TileCoord{15, 13})
	tc := TileCoord{8, 8}
	if res := original.PurchaseTile(PurchaseCommand{CorrelationID: "corr", Tile: tc, BuyerID: 1}); !res.Accepted {
		t.Fatalf("setup purchase: %+v", res.Error)
	}
	if err := original.Prospect(tc, "corr"); err != nil {
		t.Fatalf("setup prospect: %v", err)
	}

	// The attack: wrap a value-copy of the ORIGINAL World in a fresh
	// WorldAPI. This is exactly the shape astgate cannot see (a struct
	// field of the candidate type, not a receiver/parameter astgate
	// walks) — the whole reason AC-28 exists as a fix separate from
	// AC-26/AC-27.
	copied := &WorldAPI{w: w2Copy(original.w)}

	wantCopied := func(t *testing.T, label string, err error) {
		t.Helper()
		if !errors.Is(err, &errs.E{Code: ErrWorldCopied}) {
			t.Errorf("%s: err = %v, want ErrWorldCopied", label, err)
		}
	}

	if err := copied.ImportAndPlaceStartTile(a90x90Fixture(), "corr"); !errors.Is(err, &errs.E{Code: ErrWorldCopied}) {
		t.Errorf("ImportAndPlaceStartTile: err = %v, want ErrWorldCopied", err)
	}
	if _, err := copied.CellAt(tc, CellLocal{Row: 0, Col: 0}, "corr"); true {
		wantCopied(t, "CellAt", err)
	}
	if _, err := copied.TileAt(tc, "corr"); true {
		wantCopied(t, "TileAt", err)
	}
	if res := copied.ApplyOwnershipCommand(OwnershipCommand{CorrelationID: "corr", Tile: tc, Local: CellLocal{Row: 0, Col: 0}, NewOwner: 99}); res.Accepted || res.Error == nil || res.Error.Code != ErrWorldCopied {
		t.Errorf("ApplyOwnershipCommand: result = %+v, want rejected with ErrWorldCopied", res)
	}
	if res := copied.PurchaseTile(PurchaseCommand{CorrelationID: "corr", Tile: TileCoord{9, 9}, BuyerID: 1}); res.Accepted || res.Error == nil || res.Error.Code != ErrWorldCopied {
		t.Errorf("PurchaseTile: result = %+v, want rejected with ErrWorldCopied", res)
	}
	if _, err := copied.TilePrice(tc, "corr"); true {
		wantCopied(t, "TilePrice", err)
	}
	if err := copied.Prospect(TileCoord{9, 9}, "corr"); true {
		wantCopied(t, "Prospect", err)
	}
	if _, err := copied.IsProspected(tc); true {
		wantCopied(t, "IsProspected", err)
	}
	if _, err := copied.PocketGeology(tc, "corr"); true {
		wantCopied(t, "PocketGeology", err)
	}
	if _, err := copied.GeologyBaseline(tc); true {
		wantCopied(t, "GeologyBaseline", err)
	}
	if _, err := copied.OffMapConnections(); true {
		wantCopied(t, "OffMapConnections", err)
	}

	// Confirm none of the rejected calls above corrupted the ORIGINAL's
	// state via the aliased tiles map (the actual hazard this guard
	// exists to close, not merely "an error came back").
	if info, err := original.TileAt(tc, "corr"); err != nil || info.OwnerID != 1 {
		t.Fatalf("BUG-064 regression: original World's tile state changed after rejected calls on the copy, got %+v err=%v", info, err)
	}
}

// TestBUG064_ConcurrentCopyAndOriginal_NoRace mirrors BUG-064's own
// live-verified reproduction under -race: concurrent ensureTile calls
// through the original's mu and a same-package copy's mu, both
// mutating the SAME underlying map (map headers copy by reference).
// Pre-fix, this produced repeated real WARNING: DATA RACE reports on
// grid.go's map read/write. Post-fix, every call through the copy is
// rejected before its own mu (or the shared map) is ever touched, so
// there is nothing left to race.
func TestBUG064_ConcurrentCopyAndOriginal_NoRace(t *testing.T) {
	w := NewWorld(TileCoord{15, 13})
	cp := w2Copy(w)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			w.mu.Lock()
			_, _ = w.ensureTile(TileCoord{X: i % TilesPerSide, Y: 0})
			w.mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			// cp is never locked here — checkNotCopied rejects before
			// any mu is touched (that ordering is the load-bearing part
			// of the fix, mirroring SEC-016's lesson on Engine).
			if _, err := cp.ensureTile(TileCoord{X: i % TilesPerSide, Y: 1}); !errors.Is(err, &errs.E{Code: ErrWorldCopied}) {
				t.Errorf("cp.ensureTile: err = %v, want ErrWorldCopied", err)
			}
		}
	}()
	wg.Wait()
}

// w2Copy takes a same-package value copy of *World, isolated into its
// own tiny helper so every test above documents the attack shape
// identically (mirrors engine.core's e2Copy convention exactly,
// including the unsafe byte-copy — a plain `cp := *w` is legal, correct
// Go that produces the identical attack shape, but go vet's copylocks
// check would flag the LITERAL assignment at its own call site, which
// would make this test file itself fail `go vet ./internal/engine/world/...`,
// one of this package's own baseline gates. The byte-copy achieves the
// same struct-value copy (same mu bytes, same aliased map header) via a
// route copylocks does not statically recognise as a lock copy).
func w2Copy(w *World) *World {
	c := new(World)
	*(*[unsafe.Sizeof(World{})]byte)(unsafe.Pointer(c)) = *(*[unsafe.Sizeof(World{})]byte)(unsafe.Pointer(w))
	return c
}
