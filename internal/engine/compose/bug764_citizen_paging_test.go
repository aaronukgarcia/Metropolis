package compose

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// BUG-764 — citizens.CitizensAPI.EnableDiskPaging had ZERO production
// callers before this fix: compose.Wire never called it, so no runnable
// path could bound resident citizen memory. These tests prove Wire now
// wires it, per-lineage, with a fail-closed identity check.

// TestWire_BUG764_CitizenPagingEnabled is the RED-PROOF test: it asserts on
// an OBSERVABLE PRODUCTION SIDE EFFECT of the wiring (real .page files on
// disk under a tight residency budget), mirroring citizens'
// TestPageStoreWiredIntoCitizensAPI idiom exactly — so it reddens if Wire's
// call to wireCitizenPaging/c.EnableDiskPaging is ever removed or bypassed
// (manually verified during this item's build: commenting out the
// wireCitizenPaging call in compose.go's Wire makes this test fail with
// zero .page files written).
func TestWire_BUG764_CitizenPagingEnabled(t *testing.T) {
	dir := t.TempDir()
	e := core.NewEngine(core.WithWorldSeed(7))
	comp, err := Wire(e, &Deps{
		CitizenPaging: CitizenPagingOptions{
			Enabled:           true,
			MaxResidentShards: 4, // tight budget: forces eviction traffic
			PageDir:           dir,
		},
	})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if comp == nil {
		t.Fatal("Wire returned nil Composition")
	}

	// Seed citizens across many distinct shards -- det.ShardForEntity hashes
	// sequential ids across the 256 cold shards, so this reliably spans well
	// beyond the 4-shard resident budget, forcing PageStore.Store to run.
	// A near-zero-valued ColdRecord (only ID set) validates fine (every
	// enum field's zero value is its own valid member) — this test proves
	// the WIRING, not demographic realism.
	ids := []uint64{1001, 1002, 1003, 1004, 1005, 1006, 1007, 1008, 1009, 1010}
	for _, id := range ids {
		rec := citizens.ColdRecord{ID: id}
		if err := comp.state.citizens.SeedColdRecords([]citizens.ColdRecord{rec}, "bug764"); err != nil {
			t.Fatalf("SeedColdRecords(%d): %v", id, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	pageFiles := 0
	for _, ent := range entries {
		if filepath.Ext(ent.Name()) == ".page" {
			pageFiles++
		}
	}
	if pageFiles == 0 {
		t.Fatal("Wire's CitizenPaging knob is set, but zero .page files were written -- compose.Wire never actually called citizens.CitizensAPI.EnableDiskPaging (BUG-764 regression: paging built but not wired)")
	}
}

// TestWire_BUG764_CitizenPagingDisabledByDefault proves the zero value is a
// pure no-op: a Wire call with no CitizenPaging knob set behaves exactly as
// before this item (no page directory needed, no error).
func TestWire_BUG764_CitizenPagingDisabledByDefault(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(7))
	if _, err := Wire(e, nil); err != nil {
		t.Fatalf("Wire(e, nil): %v", err)
	}
}

// TestWire_BUG764_ConfigValidation proves Wire refuses an incomplete
// CitizenPaging configuration (ErrCitizenPagingConfigInvalid) rather than
// either silently ignoring it or crashing inside citizens.EnableDiskPaging.
func TestWire_BUG764_ConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		opts CitizenPagingOptions
	}{
		{"empty PageDir", CitizenPagingOptions{Enabled: true, MaxResidentShards: 4}},
		{"zero MaxResidentShards", CitizenPagingOptions{Enabled: true, PageDir: filepath.Join(os.TempDir(), "bug764-unused")}},
		{"negative MaxResidentShards", CitizenPagingOptions{Enabled: true, MaxResidentShards: -1, PageDir: filepath.Join(os.TempDir(), "bug764-unused2")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := core.NewEngine(core.WithWorldSeed(7))
			_, err := Wire(e, &Deps{CitizenPaging: tc.opts})
			if err == nil {
				t.Fatalf("Wire accepted an invalid CitizenPaging config (%+v) without error", tc.opts)
			}
			var re *errs.E
			if !errors.As(err, &re) || re.Code != ErrCitizenPagingConfigInvalid {
				t.Fatalf("expected ErrCitizenPagingConfigInvalid, got %v", err)
			}
		})
	}
}

// TestCitizenPageDir_PerLineage proves CitizenPageDir gives two different
// lineages (persist.CityKey values) two different directories EVEN WHEN
// they share the same world seed -- the exact aliasing risk the BUG-713
// round proved ("worldSeed alone aliases two same-seed cities") one layer
// up, at the compose/persist boundary this item wires.
func TestCitizenPageDir_PerLineage(t *testing.T) {
	root := t.TempDir()
	const sharedSeed = uint64(99)
	cityA := persist.CityKey{TenantID: "local", CityID: "alpha"}
	cityB := persist.CityKey{TenantID: "local", CityID: "bravo"}

	dirA := CitizenPageDir(root, cityA, sharedSeed)
	dirB := CitizenPageDir(root, cityB, sharedSeed)
	if dirA == dirB {
		t.Fatalf("two different lineages (same seed %d) resolved to the SAME page directory: %s", sharedSeed, dirA)
	}

	// Stability: the SAME identity always resolves to the SAME directory
	// (required for a restart to find its own paged shards again).
	if again := CitizenPageDir(root, cityA, sharedSeed); again != dirA {
		t.Fatalf("CitizenPageDir is not stable for the same identity: %s != %s", again, dirA)
	}
}

// TestWire_BUG764_PagingDirRefusesForeignIdentity proves the identity-stamp
// safety net: a directory already stamped for one persist.CityKey (by an
// earlier Wire call that enabled paging under it) is REFUSED
// (ErrCitizenPagingIdentityMismatch) by a later Wire call that names a
// DIFFERENT identity against the SAME literal directory -- e.g. an operator
// mistake pointing two different cities' PageDir at the same path directly
// (bypassing CitizenPageDir's own by-construction separation, proven above).
func TestWire_BUG764_PagingDirRefusesForeignIdentity(t *testing.T) {
	dir := t.TempDir()

	e1 := core.NewEngine(core.WithWorldSeed(1))
	comp1, err1 := Wire(e1, &Deps{
		PersistCity:   persist.CityKey{TenantID: "local", CityID: "city-one"},
		CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 4, PageDir: dir},
	})
	if err1 != nil {
		t.Fatalf("first Wire (stamping city-one): %v", err1)
	}

	e2 := core.NewEngine(core.WithWorldSeed(1)) // same world seed, different city
	_, err := Wire(e2, &Deps{
		PersistCity:   persist.CityKey{TenantID: "local", CityID: "city-two"},
		CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 4, PageDir: dir},
	})
	if err == nil {
		t.Fatal("second Wire (city-two, SAME directory) should have been refused")
	}
	var re *errs.E
	if !errors.As(err, &re) || re.Code != ErrCitizenPagingIdentityMismatch {
		t.Fatalf("expected ErrCitizenPagingIdentityMismatch, got %v", err)
	}

	// F1 correction (round finding, opus-round-bug764): the SAME identity
	// re-using its own stamped directory while the FIRST composition is
	// STILL LIVE (comp1 never closed) must ALSO be refused now -- the
	// identity check alone cannot tell "a legitimate restart" apart from
	// "two live compositions of the identical lineage sharing a directory
	// right now" (the round's own F1 corruption finding), so a second live
	// opener is refused regardless of whether its identity matches.
	e3 := core.NewEngine(core.WithWorldSeed(1))
	_, err3 := Wire(e3, &Deps{
		PersistCity:   persist.CityKey{TenantID: "local", CityID: "city-one"},
		CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 4, PageDir: dir},
	})
	if err3 == nil {
		t.Fatal("a second live Wire of the SAME identity against the SAME directory (first composition never closed) should have been refused")
	}
	var claimErr *errs.E
	if !errors.As(err3, &claimErr) || claimErr.Code != ErrCitizenPagingDirectoryClaimed {
		t.Fatalf("expected ErrCitizenPagingDirectoryClaimed, got %v", err3)
	}

	// Only AFTER comp1 releases its claim (Composition.Close(), the real
	// "graceful shutdown" contract) does re-using the SAME identity's own
	// stamped directory succeed -- the legitimate restart case.
	if err := comp1.Close(); err != nil {
		t.Fatalf("comp1.Close(): %v", err)
	}
	e4 := core.NewEngine(core.WithWorldSeed(1))
	comp4, err4 := Wire(e4, &Deps{
		PersistCity:   persist.CityKey{TenantID: "local", CityID: "city-one"},
		CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 4, PageDir: dir},
	})
	if err4 != nil {
		t.Fatalf("re-Wire with the SAME identity after comp1.Close() should succeed: %v", err4)
	}
	_ = comp4.Close()
}
