package demo

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// TestRenderPersonality_TracesToFixture is DEMO-7's check for the
// personality-trait distribution: a fixture histogram/breakdown renders
// data that traces to it, not a hardcoded illustrative shape.
func TestRenderPersonality_TracesToFixture(t *testing.T) {
	rect := core.Rect{X: 0, Y: 0, W: 60, H: 3}

	fixtureA := []TraitBucket{
		{Trait: "Ambitious", Count: 120},
		{Trait: "Sociable", Count: 80},
		{Trait: "Cautious", Count: 40},
	}
	bufA := core.NewBuffer(60, 3)
	RenderPersonality(bufA, rect, fixtureA, tcell.StyleDefault)
	linesA := renderedText(bufA, rect)

	fixtureB := []TraitBucket{
		{Trait: "Ambitious", Count: 999}, // mutated
		{Trait: "Sociable", Count: 80},
		{Trait: "Cautious", Count: 40},
	}
	bufB := core.NewBuffer(60, 3)
	RenderPersonality(bufB, rect, fixtureB, tcell.StyleDefault)
	linesB := renderedText(bufB, rect)

	if linesA[0] == linesB[0] {
		t.Fatalf("Ambitious row unchanged after mutating its count 120 -> 999: %q", linesB[0])
	}
}

// TestRenderLeisureTaste_TracesToFixture is DEMO-7's check for the
// leisure-taste weighting distribution.
func TestRenderLeisureTaste_TracesToFixture(t *testing.T) {
	rect := core.Rect{X: 0, Y: 0, W: 60, H: 2}

	fixtureA := []TasteBucket{
		{Taste: "Sport", Weight: 0.4},
		{Taste: "Culture", Weight: 0.2},
	}
	bufA := core.NewBuffer(60, 2)
	RenderLeisureTaste(bufA, rect, fixtureA, tcell.StyleDefault)
	linesA := renderedText(bufA, rect)

	fixtureB := []TasteBucket{
		{Taste: "Sport", Weight: 0.9}, // mutated
		{Taste: "Culture", Weight: 0.2},
	}
	bufB := core.NewBuffer(60, 2)
	RenderLeisureTaste(bufB, rect, fixtureB, tcell.StyleDefault)
	linesB := renderedText(bufB, rect)

	if linesA[0] == linesB[0] {
		t.Fatalf("Sport row unchanged after mutating its weight 0.4 -> 0.9: %q", linesB[0])
	}
}

func TestRenderPersonality_EmptyIsNoop(t *testing.T) {
	buf := core.NewBuffer(10, 2)
	RenderPersonality(buf, core.Rect{X: 0, Y: 0, W: 10, H: 2}, nil, tcell.StyleDefault)
	for y := 0; y < 2; y++ {
		for x := 0; x < 10; x++ {
			if buf.Get(x, y).Rune != ' ' {
				t.Fatalf("expected blank buffer for an empty fixture, got %q at (%d,%d)", buf.Get(x, y).Rune, x, y)
			}
		}
	}
}
