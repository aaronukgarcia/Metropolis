package compose

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// Independent destructive round on BUG-764 (attacker: opus-round-bug764).
// These tests attack the compose-side citizen-paging wiring: the identity
// sidecar, the shared-page-directory hazard, and the "stale pages newer
// than the save" resurrection shape (the BUG-687 class).

const atkSeed = uint64(764900)

// atkSeedCitizens seeds n cold records spread across the 256 cold shards so
// a tight residency budget forces real eviction/reload traffic.
func atkSeedCitizens(t *testing.T, comp *Composition, n int) {
	t.Helper()
	recs := make([]citizens.ColdRecord, 0, n)
	for i := 0; i < n; i++ {
		recs = append(recs, citizens.ColdRecord{ID: uint64(100000 + i)})
	}
	if err := comp.state.citizens.SeedColdRecords(recs, "atk764"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
}

func atkPageFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	n := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".page" {
			n++
		}
	}
	return n
}

func atkDrive(t *testing.T, e *core.Engine, months int64) {
	t.Helper()
	if err := e.AdvanceTicks("atk764", months*int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
}

// ATTACK 1 (the crash-window claim). The identity stamp is written BEFORE
// EnableDiskPaging is ever called, so a crash between MkdirAll and the
// stamp write can only ever leave an EMPTY directory -- no page file can
// exist without its stamp. This test pins that ordering by construction:
// after a failed EnableDiskPaging-less window there are no pages, and it
// also pins the residual hole -- a directory whose stamp is REMOVED (or
// never written, e.g. hand-deleted) while its pages survive is silently
// ADOPTED by the next, different identity.
func TestAttackBUG764_UnstampedDirWithForeignPagesIsAdopted(t *testing.T) {
	dir := t.TempDir()
	cityA := persist.CityKey{TenantID: "local", CityID: "city-A"}
	cityB := persist.CityKey{TenantID: "local", CityID: "city-B"}

	eA := core.NewEngine(core.WithWorldSeed(atkSeed))
	compA, err := Wire(eA, &Deps{
		PersistCity:   cityA,
		CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: dir},
	})
	if err != nil {
		t.Fatalf("Wire A: %v", err)
	}
	atkSeedCitizens(t, compA, 400)
	if got := atkPageFiles(t, dir); got == 0 {
		t.Fatal("vacuous: city A wrote no page files")
	}
	stamp := filepath.Join(dir, pageIdentityFileName)
	if _, err := os.Stat(stamp); err != nil {
		t.Fatalf("identity stamp missing after first enable: %v", err)
	}

	// Simulate the residual hole: stamp gone, pages remain.
	if err := os.Remove(stamp); err != nil {
		t.Fatalf("remove stamp: %v", err)
	}
	if got := atkPageFiles(t, dir); got == 0 {
		t.Fatal("vacuous: no foreign pages left in the directory")
	}

	eB := core.NewEngine(core.WithWorldSeed(atkSeed))
	compB, err := Wire(eB, &Deps{
		PersistCity:   cityB,
		CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: dir},
	})
	if err != nil {
		t.Logf("FINDING CLEARED: an unstamped directory holding a foreign city's pages was REFUSED: %v", err)
		return
	}
	t.Logf("FINDING: city B ADOPTED an unstamped directory still holding city A's %d page files (stamp is the only defence and it is not tamper-evident)", atkPageFiles(t, dir))

	// Does the adoption actually LEAK city A's citizens into city B? Drive B
	// and compare against an identical B in a pristine directory.
	atkDrive(t, eB, 1)

	cleanDir := t.TempDir()
	eC := core.NewEngine(core.WithWorldSeed(atkSeed))
	compC, err := Wire(eC, &Deps{
		PersistCity:   cityB,
		CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: cleanDir},
	})
	if err != nil {
		t.Fatalf("Wire control B: %v", err)
	}
	atkDrive(t, eC, 1)
	if compB.PopulationHash() != compC.PopulationHash() || compB.Population() != compC.Population() {
		t.Fatalf("FINDING (P1): adopting an unstamped foreign page directory LEAKED citizens -- pop %d/hash %x vs clean %d/%x",
			compB.Population(), compB.PopulationHash(), compC.Population(), compC.PopulationHash())
	}
	t.Logf("no data leak observed: adoption is currently harmless because every shard starts resident after Wire and eviction Stores before nil-ing (pop=%d)", compB.Population())
}

// ATTACK 1b: a corrupt / empty / null identity sidecar.
func TestAttackBUG764_MalformedIdentitySidecar(t *testing.T) {
	cases := map[string]string{
		"garbage":   "not json at all",
		"empty":     "",
		"jsonNull":  "null",
		"truncated": `{"tenant_id":"local","city_i`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, pageIdentityFileName), []byte(body), 0o644); err != nil {
				t.Fatalf("write sidecar: %v", err)
			}
			e := core.NewEngine(core.WithWorldSeed(atkSeed))
			_, err := Wire(e, &Deps{
				PersistCity:   persist.CityKey{TenantID: "local", CityID: "city-A"},
				CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: dir},
			})
			if err == nil {
				t.Logf("FINDING: sidecar %q was ACCEPTED (no refusal) -- directory adopted", body)
				return
			}
			var re *errs.E
			if !errors.As(err, &re) {
				t.Fatalf("non-registry error for sidecar %q: %v", body, err)
			}
			t.Logf("refused with %s: %v", re.Code, err)
			if re.Code != ErrCitizenPagingConfigInvalid && re.Code != ErrCitizenPagingIdentityMismatch {
				t.Fatalf("unexpected code %s", re.Code)
			}
		})
	}
}

// ATTACK 3 (the BUG-687 resurrection shape). The page directory holds pages
// written at a LATER tick of the SAME city (identity matches, so nothing is
// refused). The player then loads an EARLIER save into a paging-enabled
// composition pointed at that same directory. Citizens from the future must
// not leak in.
func TestAttackBUG764_StalePagesNewerThanTheSave(t *testing.T) {
	dir := t.TempDir()
	city := persist.CityKey{TenantID: "local", CityID: "city-time"}

	e1 := core.NewEngine(core.WithWorldSeed(atkSeed), core.WithPoolSize(1))
	comp1, err := Wire(e1, &Deps{
		PersistCity:   city,
		CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: dir},
	})
	if err != nil {
		t.Fatalf("Wire 1: %v", err)
	}
	atkSeedCitizens(t, comp1, 400)
	atkDrive(t, e1, 1)

	saveEarly := t.TempDir()
	if err := comp1.Save(saveEarly); err != nil {
		t.Fatalf("Save early: %v", err)
	}
	earlyHash := comp1.PopulationHash()
	earlyPop := comp1.Population()

	// Keep running: the page directory now holds LATER-tick shards.
	atkDrive(t, e1, 2)
	if comp1.PopulationHash() == earlyHash {
		t.Fatal("vacuous: two further months did not change the population hash")
	}
	if got := atkPageFiles(t, dir); got == 0 {
		t.Fatal("vacuous: no page files on disk")
	}

	// Control: load the early save into a NEVER-paged composition.
	eCtl := core.NewEngine(core.WithWorldSeed(atkSeed), core.WithPoolSize(1))
	compCtl, err := Wire(eCtl, nil)
	if err != nil {
		t.Fatalf("Wire control: %v", err)
	}
	if err := compCtl.Load(saveEarly); err != nil {
		t.Fatalf("Load control: %v", err)
	}
	if compCtl.PopulationHash() != earlyHash {
		t.Fatalf("control load already diverged: %x vs %x", compCtl.PopulationHash(), earlyHash)
	}

	// F1 correction (round finding, opus-round-bug764): a realistic
	// "restart and reload an older save" scenario has the FIRST
	// composition actually stopped first -- releasing its exclusive claim
	// -- before a second composition opens the same directory. Without
	// this, comp2's Wire below is correctly refused now
	// (ErrCitizenPagingDirectoryClaimed) as a SEPARATE, already-covered
	// finding (ATTACK 2) rather than reaching this attack's own target (the
	// BUG-687 stale-pages-resurrection shape), which needs comp2 to
	// actually build.
	if err := comp1.Close(); err != nil {
		t.Fatalf("comp1.Close(): %v", err)
	}

	// Attack: load the SAME early save into a paging-enabled composition
	// pointed at the directory holding the FUTURE pages.
	e2 := core.NewEngine(core.WithWorldSeed(atkSeed), core.WithPoolSize(1))
	comp2, err := Wire(e2, &Deps{
		PersistCity:   city,
		CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: dir},
	})
	if err != nil {
		t.Fatalf("Wire 2 (same identity, stale future pages present): %v", err)
	}
	if err := comp2.Load(saveEarly); err != nil {
		t.Fatalf("Load into paging composition: %v", err)
	}
	if got := comp2.Population(); got != earlyPop {
		t.Fatalf("FINDING: population after loading an older save over future pages = %d, want %d", got, earlyPop)
	}
	if got := comp2.PopulationHash(); got != earlyHash {
		t.Fatalf("FINDING (BUG-687 shape): citizens from the FUTURE leaked in -- hash %x, want %x", got, earlyHash)
	}

	// And it must keep simulating identically from there.
	atkDrive(t, e2, 1)
	atkDrive(t, eCtl, 1)
	if comp2.PopulationHash() != compCtl.PopulationHash() {
		t.Fatalf("FINDING: post-load divergence between paged-over-stale-pages and never-paged control: %x vs %x",
			comp2.PopulationHash(), compCtl.PopulationHash())
	}
}

// ATTACK 2 (shared directory / multi-city host) — REWRITTEN post-fix
// (round REJECT, opus-round-bug764 F1): the original version of this test
// proved two live compositions of the SAME identity, wired against the SAME
// page directory, silently corrupted each other (diverging PopulationHash
// from a solo control and from each other, no error, no latch — real
// file-level interleaving on the shared .page files). The fix
// (claimPageDir, citizen_paging.go) makes the SECOND concurrent opener of
// an identical, still-live claim REFUSED (ErrCitizenPagingDirectoryClaimed)
// rather than silently accepted — this test now asserts exactly that
// refusal, and separately proves the ONE composition that DID win the
// claim ticks correctly and matches a solo control (the corruption
// mechanism is gone, not merely hidden by both sides failing).
func TestAttackBUG764_TwoLiveCompositionsShareOneDirectory(t *testing.T) {
	dir := t.TempDir()
	city := persist.CityKey{TenantID: "local", CityID: "city-shared"}

	mk := func() (*core.Engine, *Composition, error) {
		e := core.NewEngine(core.WithWorldSeed(atkSeed), core.WithPoolSize(1))
		c, err := Wire(e, &Deps{
			PersistCity:   city,
			CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: dir},
		})
		return e, c, err
	}

	// Solo control first, in its own directory.
	soloDir := t.TempDir()
	eSolo := core.NewEngine(core.WithWorldSeed(atkSeed), core.WithPoolSize(1))
	compSolo, err := Wire(eSolo, &Deps{
		PersistCity:   city,
		CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: soloDir},
	})
	if err != nil {
		t.Fatalf("Wire solo: %v", err)
	}
	defer func() { _ = compSolo.Close() }()
	atkSeedCitizens(t, compSolo, 400)
	atkDrive(t, eSolo, 1)
	want := compSolo.PopulationHash()

	// Two CONCURRENT claimants of the same identity/directory: exactly one
	// must win, the other must be refused with ErrCitizenPagingDirectoryClaimed
	// -- never both silently accepted (that was the F1 corruption hazard).
	type result struct {
		e    *core.Engine
		comp *Composition
		err  error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, c, err := mk()
			results <- result{e: e, comp: c, err: err}
		}()
	}
	wg.Wait()
	close(results)

	var winners []result
	var refusals []result
	for r := range results {
		if r.err == nil {
			winners = append(winners, r)
		} else {
			refusals = append(refusals, r)
		}
	}

	if len(winners) != 1 {
		t.Fatalf("FINDING: expected exactly ONE winning claimant of the shared directory, got %d (and %d refusals) -- either the claim let both through (F1 unfixed) or refused both", len(winners), len(refusals))
	}
	if len(refusals) != 1 {
		t.Fatalf("expected exactly one refusal, got %d", len(refusals))
	}
	var re *errs.E
	if !errors.As(refusals[0].err, &re) || re.Code != ErrCitizenPagingDirectoryClaimed {
		t.Fatalf("expected the refused claimant's error to be ErrCitizenPagingDirectoryClaimed, got %v", refusals[0].err)
	}

	// The ONE winner must still tick correctly and match the solo control --
	// proving the fix did not just turn corruption into a different kind of
	// silent breakage for the composition that legitimately won the claim.
	win := winners[0]
	defer func() { _ = win.comp.Close() }()
	atkSeedCitizens(t, win.comp, 400)
	if err := win.e.AdvanceTicks("atk764-shared", int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("winning composition failed to tick: %v", err)
	}
	if got := win.comp.PopulationHash(); got != want {
		t.Errorf("FINDING: the winning composition diverged from the solo control: %x want %x", got, want)
	}
}

// ATTACK 4 (zero side effects when off). A default Wire must create nothing
// at all under a pre-existing root.
func TestAttackBUG764_DefaultOffCreatesNothing(t *testing.T) {
	root := t.TempDir()
	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(atkSeed))
	comp, err := Wire(e, &Deps{PersistCity: persist.CityKey{TenantID: "local", CityID: "c"}})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	atkSeedCitizens(t, comp, 100)
	after, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("FINDING: default (paging-off) Wire created %d entries under an unrelated root", len(after)-len(before))
	}
	// And the derived directory for that identity must not exist either.
	if _, err := os.Stat(CitizenPageDir(root, persist.CityKey{TenantID: "local", CityID: "c"}, atkSeed)); !os.IsNotExist(err) {
		t.Fatalf("FINDING: derived page dir exists with paging off (stat err=%v)", err)
	}
}

// ATTACK 5 (CitizenPageDir separation). Every field must move the hash, and
// the zero CityKey must not collide with a real one.
func TestAttackBUG764_PageDirSeparation(t *testing.T) {
	root := "R"
	seen := map[string]string{}
	add := func(label, path string) {
		if prev, ok := seen[path]; ok {
			t.Fatalf("FINDING: page dir collision between %q and %q -> %s", prev, label, path)
		}
		seen[path] = label
	}
	add("A/1/seed1", CitizenPageDir(root, persist.CityKey{TenantID: "A", CityID: "1"}, 1))
	add("A/1/seed2", CitizenPageDir(root, persist.CityKey{TenantID: "A", CityID: "1"}, 2))
	add("A/2/seed1", CitizenPageDir(root, persist.CityKey{TenantID: "A", CityID: "2"}, 1))
	add("B/1/seed1", CitizenPageDir(root, persist.CityKey{TenantID: "B", CityID: "1"}, 1))
	add("zero/seed1", CitizenPageDir(root, persist.CityKey{}, 1))
	// Concatenation aliasing probe: "AB"+"" vs "A"+"B" must not collide.
	add("AB/empty", CitizenPageDir(root, persist.CityKey{TenantID: "AB", CityID: ""}, 1))
	add("A/B", CitizenPageDir(root, persist.CityKey{TenantID: "A", CityID: "B"}, 1))
	// Determinism.
	if a, b := CitizenPageDir(root, persist.CityKey{TenantID: "A", CityID: "1"}, 1),
		CitizenPageDir(root, persist.CityKey{TenantID: "A", CityID: "1"}, 1); a != b {
		t.Fatalf("FINDING: CitizenPageDir is not deterministic: %s vs %s", a, b)
	}
	// A path-traversal city id must not escape the root.
	got := CitizenPageDir(root, persist.CityKey{TenantID: "../../etc", CityID: "../../x"}, 1)
	if filepath.Clean(got) != got || len(got) < len(root) {
		t.Fatalf("FINDING: page dir escaped the root: %s", got)
	}
}
