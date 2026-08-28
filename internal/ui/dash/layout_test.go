package dash_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/dash"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func f1Drill() dash.DrillTarget { return dash.DrillTarget{ViewName: "f1.viewport"} }

// TestDefaultLayoutF1 is AC-2: the shipped default layout for F1 parses
// into a valid Layout with the expected tile set (every UI-SPEC §4 named
// tile type).
func TestDefaultLayoutF1(t *testing.T) {
	l := dash.DefaultLayout("f1")
	if l.Screen() != "f1" {
		t.Fatalf("Screen() = %q, want f1", l.Screen())
	}
	kinds := map[dash.TileKind]bool{}
	for _, tile := range l.Tiles() {
		kinds[tile.Kind()] = true
	}
	for _, want := range []dash.TileKind{
		dash.KindBigNum, dash.KindGauge, dash.KindSpark,
		dash.KindTable, dash.KindMiniMap, dash.KindAlerts,
	} {
		if !kinds[want] {
			t.Fatalf("default f1 layout is missing tile kind %q (got %v)", want, kinds)
		}
	}
}

// TestDefaultLayoutFinanceDrillIsRegisteredView is the BUG-428 regression:
// the F1 default-layout finance tiles ("f1.cash", "f1.ledger") must drill to
// the finance screen's REGISTERED view name "f2.finance", never the old,
// unregistered "f2.ledger" codename (which Subscribe rejects, opening F2
// blank). Asserting the exact expected target here makes a future regression
// to any other name fail, complementing the viewgate one-view-registry gate.
func TestDefaultLayoutFinanceDrillIsRegisteredView(t *testing.T) {
	l := dash.DefaultLayout("f1")
	const financeView = "f2.finance"
	financeTiles := map[string]bool{"f1.cash": false, "f1.ledger": false}
	for _, tile := range l.Tiles() {
		if _, ok := financeTiles[tile.ID()]; !ok {
			continue
		}
		financeTiles[tile.ID()] = true
		if got := tile.Drill().ViewName; got != financeView {
			t.Errorf("default f1 tile %q drills to view %q, want the registered %q", tile.ID(), got, financeView)
		}
		// Table tiles carry a per-row DrillTarget too; those must match.
		if spec := tile.Table(); spec != nil {
			for i, row := range spec.Rows {
				if got := row.Drill.ViewName; got != "" && got != financeView {
					t.Errorf("default f1 tile %q row %d drills to view %q, want the registered %q", tile.ID(), i, got, financeView)
				}
			}
		}
	}
	for id, seen := range financeTiles {
		if !seen {
			t.Errorf("expected default f1 layout to contain finance tile %q", id)
		}
	}
}

// TestDefaultLayoutUnknownScreenEmpty is the non-F1 default: a valid,
// empty dashboard, not an error.
func TestDefaultLayoutUnknownScreenEmpty(t *testing.T) {
	l := dash.DefaultLayout("f9")
	if l.Len() != 0 {
		t.Fatalf("DefaultLayout(f9).Len() = %d, want 0", l.Len())
	}
}

// TestLayoutEditorSaveReloadRoundTrip is AC-3: build a layout via the
// editor API (AddTile/RemoveTile/MoveTile), save to profile JSON, reload,
// and assert the reloaded Layout is deep-equal.
func TestLayoutEditorSaveReloadRoundTrip(t *testing.T) {
	editor := dash.NewLayout("f1")

	bignum, err := dash.NewBignumTile("pop", dash.DrillTarget{ViewName: "f1.viewport"},
		dash.BignumSpec{Label: "Population", ValueText: "12,345", Prev: 12000, Curr: 12345, Series: []float64{1, 2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	gauge, err := dash.NewGaugeTile("jobs", f1Drill(), dash.GaugeSpec{Value: 0.75})
	if err != nil {
		t.Fatal(err)
	}
	table, err := dash.NewTableTile("ledger", dash.DrillTarget{ViewName: "f2.ledger"},
		dash.TableSpec{
			Columns: []widgets.Column{{Title: "Line", Width: 8}, {Title: "Amount", Width: 10}},
			Rows: []dash.TableRow{
				{Cells: []string{"Balance", "100"}, Drill: dash.DrillTarget{ViewName: "f2.ledger", EntityID: "line-1"}},
				{Cells: []string{"Tax", "-10"}, Drill: dash.DrillTarget{ViewName: "f2.ledger", EntityID: "line-2"}},
			},
		})
	if err != nil {
		t.Fatal(err)
	}

	for _, tile := range []dash.Tile{bignum, gauge, table} {
		if err := editor.AddTile(tile); err != nil {
			t.Fatal(err)
		}
	}
	if err := editor.MoveTile("jobs", 0); err != nil {
		t.Fatal(err)
	}
	if err := editor.AddTile(mustBignum(t, "extra", f1Drill())); err != nil {
		t.Fatal(err)
	}
	if err := editor.RemoveTile("extra"); err != nil {
		t.Fatal(err)
	}

	data, err := dash.Marshal(editor)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := dash.UnmarshalLayout(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(editor, reloaded) {
		t.Fatalf("save/reload not deep-equal:\n got %+v\nwant %+v", reloaded, editor)
	}
}

func mustBignum(t *testing.T, id string, drill dash.DrillTarget) dash.Tile {
	t.Helper()
	tile, err := dash.NewBignumTile(id, drill, dash.BignumSpec{})
	if err != nil {
		t.Fatal(err)
	}
	return tile
}

func TestAddTileRejectsDuplicateID(t *testing.T) {
	l := dash.NewLayout("f1")
	if err := l.AddTile(mustBignum(t, "dup", f1Drill())); err != nil {
		t.Fatal(err)
	}
	if err := l.AddTile(mustBignum(t, "dup", f1Drill())); err == nil {
		t.Fatal("AddTile accepted a duplicate tile id")
	}
}

func TestRemoveTileUnknownReturnsError(t *testing.T) {
	l := dash.NewLayout("f1")
	if err := l.RemoveTile("nope"); err == nil {
		t.Fatal("RemoveTile(unknown) returned nil error")
	}
}

func TestTilesReturnsDefensiveCopy(t *testing.T) {
	l := dash.NewLayout("f1")
	if err := l.AddTile(mustBignum(t, "a", f1Drill())); err != nil {
		t.Fatal(err)
	}
	if err := l.AddTile(mustBignum(t, "b", f1Drill())); err != nil {
		t.Fatal(err)
	}
	snapshot := l.Tiles()
	snapshot[0], snapshot[1] = snapshot[1], snapshot[0] // reorder the copy
	if l.Tiles()[0].ID() != "a" {
		t.Fatal("reordering the Tiles() copy mutated the layout's own order")
	}
}

// TestMoveTileForward is the regression test for the forward-direction
// off-by-one in MoveTile: `to` is the tile's requested final index, so a
// forward move must land the tile at `to`, not `to-1`. The round-trip test
// above only exercises the backward case (MoveTile("jobs", 0)); these cases
// were all no-ops or one-short under the buggy `to-1` adjustment.
func TestMoveTileForward(t *testing.T) {
	newABCD := func() dash.Layout {
		l := dash.NewLayout("f1")
		for _, id := range []string{"a", "b", "c", "d"} {
			if err := l.AddTile(mustBignum(t, id, f1Drill())); err != nil {
				t.Fatal(err)
			}
		}
		return l
	}

	cases := []struct {
		name string
		id   string
		to   int
		want []string
	}{
		{"to end", "a", 3, []string{"b", "c", "d", "a"}},
		{"adjacent forward", "a", 1, []string{"b", "a", "c", "d"}},
		{"middle forward", "b", 2, []string{"a", "c", "b", "d"}},
		// The backward case is already correct and must stay correct.
		{"backward still correct", "c", 0, []string{"c", "a", "b", "d"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newABCD()
			if err := l.MoveTile(tc.id, tc.to); err != nil {
				t.Fatal(err)
			}
			if got := tileIDs(l); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("MoveTile(%q, %d) = %v, want %v", tc.id, tc.to, got, tc.want)
			}
		})
	}
}

// tileIDs returns the layout's tile ids in render order.
func tileIDs(l dash.Layout) []string {
	tiles := l.Tiles()
	out := make([]string, len(tiles))
	for i, t := range tiles {
		out[i] = t.ID()
	}
	return out
}

// TestLoadProfileMalformedFallsBackToDefault is AC-10: corrupt profile
// JSON returns a registry-sourced error AND the shipped default layout
// for the screen, not a corrupt/partial load.
func TestLoadProfileMalformedFallsBackToDefault(t *testing.T) {
	l, err := dash.LoadProfile([]byte("{ not json"), "f1")
	if err == nil {
		t.Fatal("LoadProfile(malformed) returned nil error")
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("error is not *errs.E: %v", err)
	}
	if e.Code != "MET-U801" {
		t.Fatalf("error code = %q, want MET-U801", e.Code)
	}
	want := dash.DefaultLayout("f1")
	if !reflect.DeepEqual(l, want) {
		t.Fatalf("fallback layout = %+v, want the shipped default", l)
	}
}

// TestLoadProfileCorruptStructFallsBackToDefault: structurally-valid JSON
// that decodes to a tile with a missing DrillTarget is also corrupt, and
// also falls back (AC-10's "corrupt" arm, not just malformed JSON).
func TestLoadProfileCorruptStructFallsBackToDefault(t *testing.T) {
	// A profile whose tile has a zero drill viewName is rejected by
	// tileFromWire's requireDrill, so the whole load falls back.
	corrupt := []byte(`{"screen":"f1","tiles":[{"id":"t","kind":"bignum","drill":{"viewName":""}}]}`)
	l, err := dash.LoadProfile(corrupt, "f1")
	if err == nil {
		t.Fatal("LoadProfile(corrupt tile) returned nil error")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != "MET-U801" {
		t.Fatalf("error = %v, want MET-U801", err)
	}
	if !reflect.DeepEqual(l, dash.DefaultLayout("f1")) {
		t.Fatal("fallback layout is not the shipped default")
	}
}
