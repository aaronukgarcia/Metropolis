package demo

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

// TestTypologies_SF3_OneTypologyChanges is DEMO-5's SF-3-shaped check:
// two fixture states differing only in one typology's demand figure
// must (a) change that typology's rendered demand-vs-stock row and (b)
// leave every other typology's rendered row byte-identical.
func TestTypologies_SF3_OneTypologyChanges(t *testing.T) {
	s := New("corr-typology")

	patchA := wireHousingPatch{
		SchemaVersion: 1,
		Full:          true,
		Typologies: []wireTypology{
			{Typology: "Terrace", Demand: 100, Stock: 90},
			{Typology: "Flat", Demand: 200, Stock: 210},
			{Typology: "Detached", Demand: 30, Stock: 25},
		},
	}
	s.applyHousing(mustJSON(t, patchA))
	rowsA, have := s.Typologies()
	if !have || len(rowsA) != 3 {
		t.Fatalf("Typologies() after patchA = %v, have=%v, want 3 rows", rowsA, have)
	}

	rect := core.Rect{X: 0, Y: 0, W: 60, H: 3}
	bufA := core.NewBuffer(60, 3)
	RenderTypologies(bufA, rect, rowsA, tcell.StyleDefault)
	linesA := renderedText(bufA, rect)

	// Mutate exactly Terrace's demand figure.
	patchB := wireHousingPatch{
		SchemaVersion: 1,
		Full:          true,
		Typologies: []wireTypology{
			{Typology: "Terrace", Demand: 999, Stock: 90}, // mutated
			{Typology: "Flat", Demand: 200, Stock: 210},
			{Typology: "Detached", Demand: 30, Stock: 25},
		},
	}
	s2 := New("corr-typology-2")
	s2.applyHousing(mustJSON(t, patchB))
	rowsB, _ := s2.Typologies()

	bufB := core.NewBuffer(60, 3)
	RenderTypologies(bufB, rect, rowsB, tcell.StyleDefault)
	linesB := renderedText(bufB, rect)

	if linesA[0] == linesB[0] {
		t.Fatalf("Terrace row unchanged after mutating its demand 100 -> 999: %q", linesB[0])
	}
	if linesA[1] != linesB[1] {
		t.Errorf("Flat row changed even though untouched: %q -> %q", linesA[1], linesB[1])
	}
	if linesA[2] != linesB[2] {
		t.Errorf("Detached row changed even though untouched: %q -> %q", linesA[2], linesB[2])
	}
}

// TestTypologies_RetiredMidGame is SF-7/DEMO-9's "data that has become
// unavailable since the last delta" check applied to housing typologies:
// a typology present in one full snapshot but absent from the next
// renders "no longer available" rather than its last stale figures.
func TestTypologies_RetiredMidGame(t *testing.T) {
	s := New("corr-retire")
	s.applyHousing(mustJSON(t, wireHousingPatch{
		SchemaVersion: 1, Full: true,
		Typologies: []wireTypology{
			{Typology: "Terrace", Demand: 100, Stock: 90},
			{Typology: "Bungalow", Demand: 10, Stock: 9},
		},
	}))
	rows, _ := s.Typologies()
	for _, r := range rows {
		if r.Retired {
			t.Fatalf("typology %q retired before any second snapshot", r.Typology)
		}
	}

	// Second full snapshot omits Bungalow -- it has been retired.
	s.applyHousing(mustJSON(t, wireHousingPatch{
		SchemaVersion: 1, Full: true,
		Typologies: []wireTypology{
			{Typology: "Terrace", Demand: 105, Stock: 91},
		},
	}))
	rows2, _ := s.Typologies()

	var bungalow *TypologyRow
	for i := range rows2 {
		if rows2[i].Typology == "Bungalow" {
			bungalow = &rows2[i]
		}
	}
	if bungalow == nil {
		t.Fatalf("Bungalow missing entirely from Typologies() after retirement -- expected retained with Retired=true, not deleted")
	}
	if !bungalow.Retired {
		t.Fatalf("Bungalow.Retired = false after being omitted from a full snapshot, want true")
	}

	rect := core.Rect{X: 0, Y: 0, W: 60, H: 2}
	buf := core.NewBuffer(60, 2)
	RenderTypologies(buf, rect, rows2, tcell.StyleDefault)
	lines := renderedText(buf, rect)
	found := false
	for _, l := range lines {
		if strings.Contains(l, "Bungalow") && strings.Contains(l, "no longer available") {
			found = true
		}
	}
	if !found {
		t.Fatalf("rendered output %q does not show Bungalow as 'no longer available'", lines)
	}
}

// TestTypologies_SparsePatchLeavesOthersUntouched proves sparse (Full ==
// false) f6.housing patches update only the listed typologies.
func TestTypologies_SparsePatchLeavesOthersUntouched(t *testing.T) {
	s := New("corr-sparse")
	s.applyHousing(mustJSON(t, wireHousingPatch{
		SchemaVersion: 1, Full: true,
		Typologies: []wireTypology{
			{Typology: "Terrace", Demand: 100, Stock: 90},
			{Typology: "Flat", Demand: 200, Stock: 210},
		},
	}))
	s.applyHousing(mustJSON(t, wireHousingPatch{
		SchemaVersion: 1, Full: false,
		Typologies: []wireTypology{
			{Typology: "Terrace", Demand: 150, Stock: 90},
		},
	}))
	rows, _ := s.Typologies()
	got := map[string]TypologyRow{}
	for _, r := range rows {
		got[r.Typology] = r
	}
	if got["Terrace"].Demand != 150 {
		t.Errorf("Terrace.Demand = %d, want 150 (sparse update applied)", got["Terrace"].Demand)
	}
	if got["Flat"].Demand != 200 || got["Flat"].Stock != 210 {
		t.Errorf("Flat = %+v, want unchanged {Demand:200 Stock:210} (sparse patch must not touch it)", got["Flat"])
	}
}
