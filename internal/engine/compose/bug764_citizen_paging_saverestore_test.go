package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// BUG-764 — item (4): paging state must survive Load/LoadAt, and a save
// taken while shards are paged out must restore IDENTICALLY to a
// never-paged control. This is the determinism half of wiring
// EnableDiskPaging into production (GR#21: a memory-bounding knob must
// never change simulated outcomes).

const bug764SaveRestoreSeed = uint64(764001)

// driveBUG764 advances the composition three months through the real
// AdvanceTicks path — enough for coldpass.go's amortised per-day-tick shard
// sweep (256 shards, one per day-tick) to touch every shard at least once,
// so a tight paging budget genuinely forces eviction/reload traffic during
// the run, not merely at construction.
func driveBUG764(t *testing.T, e *core.Engine) {
	t.Helper()
	cid := "bug764-drive"
	if err := e.AdvanceTicks(cid, 3*int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
}

// TestBUG764_SaveRestoreParity_PagedVsNeverPaged proves a composition whose
// citizen shards were paged out to disk during driven ticks (a TIGHT
// MaxResidentShards budget) round-trips through Save/Load with a
// byte-identical PopulationHash to a NEVER-paged control driven through the
// identical sequence at the identical seed — proving paging is a pure
// memory-management side channel with zero effect on simulated outcome.
func TestBUG764_SaveRestoreParity_PagedVsNeverPaged(t *testing.T) {
	// Control: never-paged, same seed, same driven sequence.
	eControl := core.NewEngine(core.WithWorldSeed(bug764SaveRestoreSeed), core.WithPoolSize(1))
	compControl, err := Wire(eControl, nil)
	if err != nil {
		t.Fatalf("Wire (control): %v", err)
	}
	driveBUG764(t, eControl)
	controlHash := compControl.PopulationHash()
	controlPop := compControl.Population()

	// Paged: SAME seed, SAME driven sequence, but a deliberately tight
	// residency budget (2 of 256 shards) so the amortised cold-pass sweep
	// forces real eviction/reload traffic throughout the run.
	pageDir := t.TempDir()
	ePaged := core.NewEngine(core.WithWorldSeed(bug764SaveRestoreSeed), core.WithPoolSize(1))
	compPaged, err := Wire(ePaged, &Deps{
		CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: pageDir},
	})
	if err != nil {
		t.Fatalf("Wire (paged): %v", err)
	}
	driveBUG764(t, ePaged)
	pagedHash := compPaged.PopulationHash()
	pagedPop := compPaged.Population()

	if pagedPop != controlPop {
		t.Fatalf("Population diverged under paging: paged=%d control=%d", pagedPop, controlPop)
	}
	if pagedHash != controlHash {
		t.Fatalf("PopulationHash diverged under paging (determinism violation, GR#21): paged=%x control=%x", pagedHash, controlHash)
	}

	// Save the PAGED composition (some shards may currently be resident,
	// some paged out on disk — Save must read through shardAt/Source, which
	// transparently reloads either way) and Load into a FRESH,
	// paging-disabled composition — proving the save participant already
	// handles paged shards correctly (BUG-712/713's own claim) end to end
	// through a real Wire/Save/Load cycle, not just at the citizens-package
	// unit level.
	saveDir := t.TempDir()
	if err := compPaged.Save(saveDir); err != nil {
		t.Fatalf("Save (paged composition): %v", err)
	}

	eLoaded := core.NewEngine(core.WithWorldSeed(bug764SaveRestoreSeed), core.WithPoolSize(1))
	compLoaded, err := Wire(eLoaded, nil) // paging OFF on the load target
	if err != nil {
		t.Fatalf("Wire (load target): %v", err)
	}
	if err := compLoaded.Load(saveDir); err != nil {
		t.Fatalf("Load: %v", err)
	}

	loadedHash := compLoaded.PopulationHash()
	loadedPop := compLoaded.Population()
	if loadedPop != pagedPop {
		t.Fatalf("Population did NOT survive Save/Load of a paged composition: loaded=%d saved=%d", loadedPop, pagedPop)
	}
	if loadedHash != pagedHash {
		t.Fatalf("PopulationHash did NOT survive Save/Load of a paged composition: loaded=%x saved=%x", loadedHash, pagedHash)
	}
	if loadedHash != controlHash {
		t.Fatalf("Loaded (formerly-paged) PopulationHash does not match the never-paged control: loaded=%x control=%x", loadedHash, controlHash)
	}
}

// TestBUG764_SaveRestoreParity_ReloadWithPagingReEnabled proves the OTHER
// restore direction: loading a save (taken from a never-paged composition)
// into a composition that itself re-enables paging (e.g. a real
// cmd/metroserve restart against -citizen-paging) reproduces the identical
// PopulationHash too — paging state does not need to match between the
// save and the load target for correctness, only the underlying citizen
// data.
func TestBUG764_SaveRestoreParity_ReloadWithPagingReEnabled(t *testing.T) {
	eControl := core.NewEngine(core.WithWorldSeed(bug764SaveRestoreSeed+1), core.WithPoolSize(1))
	compControl, err := Wire(eControl, nil)
	if err != nil {
		t.Fatalf("Wire (control): %v", err)
	}
	driveBUG764(t, eControl)
	controlHash := compControl.PopulationHash()

	saveDir := t.TempDir()
	if err := compControl.Save(saveDir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	pageDir := t.TempDir()
	eLoaded := core.NewEngine(core.WithWorldSeed(bug764SaveRestoreSeed+1), core.WithPoolSize(1))
	compLoaded, err := Wire(eLoaded, &Deps{
		CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: pageDir},
	})
	if err != nil {
		t.Fatalf("Wire (load target, paging ON): %v", err)
	}
	if err := compLoaded.Load(saveDir); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := compLoaded.PopulationHash(); got != controlHash {
		t.Fatalf("PopulationHash after loading a never-paged save into a paging-enabled composition diverged: got=%x want=%x", got, controlHash)
	}

	// Drive further ticks against the loaded, paging-enabled composition to
	// prove paging remains functional (and non-divergent) post-load — a
	// fresh, non-loaded paging-enabled control run over the SAME
	// post-load tick window would be a different comparison (different
	// starting tick), so this instead just proves no panic/error and a
	// stable, non-zero population continuing to advance.
	driveBUG764(t, eLoaded)
	if compLoaded.Population() <= 0 {
		t.Fatalf("Population non-positive after driving further ticks post-load with paging re-enabled: %d", compLoaded.Population())
	}
}
