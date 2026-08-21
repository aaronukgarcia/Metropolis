package dash_test

import (
	"sync"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// --- helpers -------------------------------------------------------------

func roundTile(t *testing.T, id string) dash.Tile {
	t.Helper()
	tile, err := dash.NewBignumTile(id, dash.DrillTarget{ViewName: "f1.viewport"}, dash.BignumSpec{Label: id})
	if err != nil {
		t.Fatalf("NewBignumTile(%q): %v", id, err)
	}
	return tile
}

func roundIDs(l dash.Layout) []string {
	tiles := l.Tiles()
	out := make([]string, 0, len(tiles))
	for _, t := range tiles {
		out = append(out, t.ID())
	}
	return out
}

func roundEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func roundLayout(t *testing.T, ids ...string) dash.Layout {
	t.Helper()
	l := dash.NewLayout("f1")
	for _, id := range ids {
		if err := l.AddTile(roundTile(t, id)); err != nil {
			t.Fatalf("AddTile(%q): %v", id, err)
		}
	}
	return l
}

// --- ATTACK 1: the defect itself, through the public API -----------------

// TestSEC071Round_RemoveTileCorruptsValueCopy is the canonical SEC-071
// shape: Layout is a VALUE type with a slice field, so `l2 := l1` shares
// the tiles backing array. RemoveTile's in-place left-shift then writes
// through that shared array and corrupts the layout the caller never
// touched. Against unpatched main this fails; the fix's copy-on-write
// clone in RemoveTile is what makes it pass.
func TestSEC071Round_RemoveTileCorruptsValueCopy(t *testing.T) {
	l1 := roundLayout(t, "a", "b", "c")
	l2 := l1

	if err := l2.RemoveTile("a"); err != nil {
		t.Fatalf("RemoveTile: %v", err)
	}

	if got, want := roundIDs(l1), []string{"a", "b", "c"}; !roundEqual(got, want) {
		t.Errorf("SEC-071: mutating a Layout value copy corrupted the source layout:\n got  %v\n want %v", got, want)
	}
	if got, want := roundIDs(l2), []string{"b", "c"}; !roundEqual(got, want) {
		t.Errorf("copy has wrong contents: got %v want %v", got, want)
	}
}

// TestSEC071Round_MoveTileCorruptsValueCopy is the same defect through
// MoveTile, which does a remove-then-insert against the shared array.
func TestSEC071Round_MoveTileCorruptsValueCopy(t *testing.T) {
	l1 := roundLayout(t, "a", "b", "c", "d")
	l2 := l1

	if err := l2.MoveTile("d", 0); err != nil {
		t.Fatalf("MoveTile: %v", err)
	}

	if got, want := roundIDs(l1), []string{"a", "b", "c", "d"}; !roundEqual(got, want) {
		t.Errorf("SEC-071: MoveTile on a value copy corrupted the source layout:\n got  %v\n want %v", got, want)
	}
	if got, want := roundIDs(l2), []string{"d", "a", "b", "c"}; !roundEqual(got, want) {
		t.Errorf("copy has wrong order: got %v want %v", got, want)
	}
}

// TestSEC071Round_AddTileSpareCapacityDivergence is the append variant,
// the one a reviewer is most likely to wave away as "append is safe".
// It is not: after three appends the tiles slice has spare capacity, so
// two independent copies each append into the SAME array slot and the
// second writer silently overwrites the first copy's tile.
func TestSEC071Round_AddTileSpareCapacityDivergence(t *testing.T) {
	l1 := roundLayout(t, "a", "b", "c")
	l2 := l1

	if err := l2.AddTile(roundTile(t, "fromCopy")); err != nil {
		t.Fatalf("AddTile on copy: %v", err)
	}
	if err := l1.AddTile(roundTile(t, "fromSource")); err != nil {
		t.Fatalf("AddTile on source: %v", err)
	}

	if got, want := roundIDs(l2), []string{"a", "b", "c", "fromCopy"}; !roundEqual(got, want) {
		t.Errorf("SEC-071: AddTile on the source overwrote the copy's appended tile (shared spare capacity):\n got  %v\n want %v", got, want)
	}
	if got, want := roundIDs(l1), []string{"a", "b", "c", "fromSource"}; !roundEqual(got, want) {
		t.Errorf("source has wrong contents: got %v want %v", got, want)
	}
}

// TestSEC071Round_MutatingSourceCorruptsEarlierCopy drives the aliasing
// in the OTHER direction: the copy is the innocent party and the source
// is mutated. Copy-on-write must protect both sides, not just the one
// the original test happened to exercise.
func TestSEC071Round_MutatingSourceCorruptsEarlierCopy(t *testing.T) {
	l1 := roundLayout(t, "a", "b", "c")
	snapshot := l1

	if err := l1.RemoveTile("a"); err != nil {
		t.Fatalf("RemoveTile: %v", err)
	}
	if err := l1.MoveTile("c", 0); err != nil {
		t.Fatalf("MoveTile: %v", err)
	}

	if got, want := roundIDs(snapshot), []string{"a", "b", "c"}; !roundEqual(got, want) {
		t.Errorf("SEC-071: mutating the source corrupted an earlier value copy:\n got  %v\n want %v", got, want)
	}
}

// TestSEC071Round_LoadedProfileCopyIsIndependent exercises the same
// defect through the real ingress a player hits: load a saved profile,
// keep the loaded layout as the "on disk" baseline, hand a copy to the
// editor. Editing must not rewrite the baseline.
func TestSEC071Round_LoadedProfileCopyIsIndependent(t *testing.T) {
	data, err := dash.MarshalProfile("p", dash.DefaultLayout("f1"))
	if err != nil {
		t.Fatalf("MarshalProfile: %v", err)
	}
	baseline, err := dash.UnmarshalLayout(data)
	if err != nil {
		t.Fatalf("UnmarshalLayout: %v", err)
	}
	want := roundIDs(baseline)

	editing := baseline
	if err := editing.RemoveTile("f1.jobs"); err != nil {
		t.Fatalf("RemoveTile: %v", err)
	}
	if err := editing.MoveTile("f1.alerts", 0); err != nil {
		t.Fatalf("MoveTile: %v", err)
	}

	if got := roundIDs(baseline); !roundEqual(got, want) {
		t.Errorf("SEC-071: editing a copy of a loaded profile corrupted the loaded baseline:\n got  %v\n want %v", got, want)
	}
}

// --- ATTACK 2: siblings — every other handle out of the type -------------

// TestSEC071Round_DashboardBoundaryIsIndependent checks the SEC-092 half:
// every Layout crossing the Dashboard boundary (in via NewDashboard and
// SetLayout, out via Layout()) must be an independent value, so a caller
// holding its own Layout has no second mutation path into the dashboard.
func TestSEC071Round_DashboardBoundaryIsIndependent(t *testing.T) {
	l := roundLayout(t, "a", "b", "c")
	d := dash.NewDashboard(l, nil, nil)

	// In via NewDashboard.
	if err := l.RemoveTile("a"); err != nil {
		t.Fatalf("RemoveTile: %v", err)
	}
	if got, want := roundIDs(d.Layout()), []string{"a", "b", "c"}; !roundEqual(got, want) {
		t.Errorf("SEC-092: mutating the constructor's layout reached into the dashboard: got %v want %v", got, want)
	}

	// In via SetLayout.
	l2 := roundLayout(t, "x", "y", "z")
	d.SetLayout(l2)
	if err := l2.MoveTile("z", 0); err != nil {
		t.Fatalf("MoveTile: %v", err)
	}
	if got, want := roundIDs(d.Layout()), []string{"x", "y", "z"}; !roundEqual(got, want) {
		t.Errorf("SEC-092: mutating the SetLayout argument reached into the dashboard: got %v want %v", got, want)
	}

	// Out via Layout().
	out := d.Layout()
	if err := out.RemoveTile("x"); err != nil {
		t.Fatalf("RemoveTile: %v", err)
	}
	if got, want := roundIDs(d.Layout()), []string{"x", "y", "z"}; !roundEqual(got, want) {
		t.Errorf("SEC-092: mutating the layout returned by Layout() reached into the dashboard: got %v want %v", got, want)
	}
}

// TestSEC071Round_TilesAccessorIsIndependent covers the remaining slice
// handed out of Layout: Tiles(). Reordering/truncating the returned slice
// must not be visible to the layout.
func TestSEC071Round_TilesAccessorIsIndependent(t *testing.T) {
	l := roundLayout(t, "a", "b", "c")
	got := l.Tiles()
	got[0], got[2] = got[2], got[0]
	got = got[:1]
	_ = got

	if ids, want := roundIDs(l), []string{"a", "b", "c"}; !roundEqual(ids, want) {
		t.Errorf("Tiles() aliased the layout's slice: got %v want %v", ids, want)
	}
}

// --- ATTACK 3: is the clone deep enough? ---------------------------------

// TestSEC071Round_SpecPointersNotReachableAsMutableState is the shallow
// clone probe. Tile carries `table *TableSpec` and `diagram *DiagramSpec`
// POINTERS, so cloneSlice([]Tile) copies the pointer, not the pointee:
// every Layout copy shares the same TableSpec/DiagramSpec objects. That is
// only safe because every exported accessor deep-copies. This test pins
// that invariant — if someone later returns t.table directly, the shallow
// clone becomes a real aliasing hole and this goes red.
func TestSEC071Round_SpecPointersNotReachableAsMutableState(t *testing.T) {
	l1 := dash.DefaultLayout("f1")
	l2 := l1

	tile, ok := l2.FindTile("f1.ledger")
	if !ok {
		t.Fatal("f1.ledger tile missing from default layout")
	}
	spec := tile.Table()
	if spec == nil || len(spec.Rows) == 0 {
		t.Fatal("f1.ledger has no table rows")
	}
	// Reach for the tile's stored rows through every exported handle.
	spec.Rows[0].Drill = dash.DrillTarget{}
	spec.Rows[0].Cells[0] = "CORRUPTED"
	spec.Columns[0].Title = "CORRUPTED"
	if vis := spec.Visible([]int{0}); len(vis) > 0 {
		vis[0].Drill = dash.DrillTarget{}
		if len(vis[0].Cells) > 0 {
			vis[0].Cells[0] = "CORRUPTED"
		}
	}

	// The other copy's stored spec must be untouched.
	check, ok := l1.FindTile("f1.ledger")
	if !ok {
		t.Fatal("f1.ledger tile missing from source layout")
	}
	fresh := check.Table()
	if !fresh.Rows[0].Drill.Valid() {
		t.Error("shallow clone: mutating a Table() copy zeroed the stored row DrillTarget through the shared *TableSpec")
	}
	if fresh.Rows[0].Cells[0] == "CORRUPTED" {
		t.Error("shallow clone: mutating a Table() copy's cells reached the stored row through the shared *TableSpec")
	}
	if fresh.Columns[0].Title == "CORRUPTED" {
		t.Error("shallow clone: mutating a Table() copy's columns reached the stored spec")
	}
	// And the drill-coverage audit — the thing that exists to catch a dead
	// drill target — must still see a clean layout.
	if gaps := dash.AuditDrillCoverage(l1); len(gaps) != 0 {
		t.Errorf("shallow clone let a mutation reach the audited layout: %v", gaps)
	}

	// Sparkline/bignum series: value specs whose slices are the other
	// shallow-clone risk.
	pop, _ := l2.FindTile("f1.population")
	series := pop.Bignum().Series
	if len(series) > 0 {
		series[0] = 99999
	}
	popSrc, _ := l1.FindTile("f1.population")
	if s := popSrc.Bignum().Series; len(s) > 0 && s[0] == 99999 {
		t.Error("shallow clone: mutating a Bignum() copy's Series reached the stored tile")
	}
}

// TestSEC071Round_MarshalIsUnaffectedByCopyEdits is the end-to-end
// consequence check: what the player saves must reflect the layout they
// edited, not a sibling copy's edits leaking in.
func TestSEC071Round_MarshalIsUnaffectedByCopyEdits(t *testing.T) {
	l1 := roundLayout(t, "a", "b", "c")
	before, err := dash.Marshal(l1)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	l2 := l1
	if err := l2.RemoveTile("b"); err != nil {
		t.Fatalf("RemoveTile: %v", err)
	}
	if err := l2.AddTile(roundTile(t, "d")); err != nil {
		t.Fatalf("AddTile: %v", err)
	}

	after, err := dash.Marshal(l1)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("SEC-071: editing a copy changed what the source layout saves:\n before %s\n after  %s", before, after)
	}
}

// --- ATTACK 4: concurrency ----------------------------------------------

// TestSEC071Round_ConcurrentCopyReadVsSourceMutate is the race half of
// the defect. Two goroutines, one holding a value copy taken BEFORE they
// start (so taking the copy is not itself the race), the other mutating
// the source. Without copy-on-write the mutator writes into the array the
// reader is walking — a genuine data race, flagged by -race. With the fix
// the mutator only ever reads the shared array (to clone it) and writes
// its own, so read/read is all that overlaps.
//
// The handshake makes the overlap deterministic rather than hoping the
// scheduler interleaves.
func TestSEC071Round_ConcurrentCopyReadVsSourceMutate(t *testing.T) {
	source := roundLayout(t, "a", "b", "c", "d", "e", "f", "g", "h")
	copyOfSource := source

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			_ = copyOfSource.Tiles()
			_, _ = copyOfSource.FindTile("h")
			buf := core.NewBuffer(20, 60)
			dash.Render(buf, core.Rect{X: 0, Y: 0, W: 20, H: 60}, copyOfSource, widgets.DefaultPalette, tcell.StyleDefault)
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			_ = source.MoveTile("a", 7)
			_ = source.MoveTile("a", 0)
			_ = source.RemoveTile("b")
			_ = source.AddTile(roundTile(t, "b"))
		}
	}()

	close(start)
	wg.Wait()

	if got, want := roundIDs(copyOfSource), []string{"a", "b", "c", "d", "e", "f", "g", "h"}; !roundEqual(got, want) {
		t.Errorf("SEC-071: concurrent mutation of the source rewrote the copy: got %v want %v", got, want)
	}
}

// TestSEC071Round_DashboardCloneTakenUnderTheSameLock checks that the
// clone Layout()/SetLayout take is not a torn read: the clone must happen
// inside the same RWMutex that guards d.layout, so a Layout() overlapping
// an AddTile/RemoveTile sees a complete tile set, never a half-shifted
// one. Run under -race.
func TestSEC071Round_DashboardCloneTakenUnderTheSameLock(t *testing.T) {
	d := dash.NewDashboard(roundLayout(t, "a", "b", "c", "d"), nil, nil)

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			snap := d.Layout()
			// Every snapshot must be internally consistent: no zero-value
			// tile IDs (the signature of reading a half-shifted slice).
			for _, tile := range snap.Tiles() {
				if tile.ID() == "" {
					t.Errorf("torn clone: Layout() returned a zero-value tile")
					return
				}
			}
			// And it must be safe to edit the snapshot while the editor runs.
			_ = snap.MoveTile("c", 0)
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 2000; i++ {
			_ = d.RemoveTile("d")
			_ = d.AddTile(roundTile(t, "d"))
			_ = d.MoveTile("a", 3)
			_ = d.MoveTile("a", 0)
		}
	}()

	close(start)
	wg.Wait()
}

// --- ATTACK 5: cost ------------------------------------------------------

// TestSEC071Round_RenderPathAllocsUnchanged pins the cost question the
// right way round: the fix touches the EDITOR path (AddTile/RemoveTile/
// MoveTile), not the per-render-tick read path. Render must still allocate
// nothing per frame. Allocation counts, never a wall-clock bound — a
// timing assertion in CI is a flake generator (see the verification
// standards note).
func TestSEC071Round_RenderPathAllocsUnchanged(t *testing.T) {
	l := dash.DefaultLayout("f1")
	buf := core.NewBuffer(40, 60)
	rect := core.Rect{X: 0, Y: 0, W: 40, H: 60}

	got := testing.AllocsPerRun(200, func() {
		dash.Render(buf, rect, l, widgets.DefaultPalette, tcell.StyleDefault)
	})
	// Generous ceiling: the point is that the SEC-071 clones do not appear
	// on this path at all, not to freeze widgets' own allocation profile.
	if got > 40 {
		t.Errorf("render tick allocates %.0f objects/frame; the SEC-071 clones must not be on the render path", got)
	}
}

// TestSEC071Round_EditorAllocsAreBounded documents what the fix costs
// where it does apply. AddTile now clones the tiles slice and then
// appends, so an add is O(n) copies rather than amortised O(1) — fine for
// an editor action a human triggers, and the number is pinned here so a
// future change that puts AddTile in a hot loop is visible rather than
// silent.
func TestSEC071Round_EditorAllocsAreBounded(t *testing.T) {
	base := roundLayout(t, "a", "b", "c", "d", "e", "f")
	tile := roundTile(t, "z")

	got := testing.AllocsPerRun(200, func() {
		l := base
		_ = l.AddTile(tile)
	})
	t.Logf("AddTile allocations per call (6-tile layout): %.0f", got)
	if got > 8 {
		t.Errorf("AddTile allocates %.0f objects; expected a clone plus an append, not per-tile allocation", got)
	}
}
