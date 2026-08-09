package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestPalette_BothShipDistinctAndComplete(t *testing.T) {
	if DefaultPalette.Name == ColourblindPalette.Name {
		t.Fatalf("default and colourblind palettes have the same Name %q", DefaultPalette.Name)
	}
	tokens := []Token{
		TokenMoney, TokenWater, TokenPower, TokenDanger,
		TokenWarning, TokenDecay, TokenSelection,
	}
	seenDefault := map[Token]bool{}
	seenCB := map[Token]bool{}
	for _, tok := range tokens {
		dc := DefaultPalette.Color(tok)
		cc := ColourblindPalette.Color(tok)
		if dc == 0 {
			t.Errorf("DefaultPalette missing/zero colour for token %v", tok)
		}
		if cc == 0 {
			t.Errorf("ColourblindPalette missing/zero colour for token %v", tok)
		}
		seenDefault[tok] = true
		seenCB[tok] = true
	}
	if len(seenDefault) != len(tokens) || len(seenCB) != len(tokens) {
		t.Fatalf("not every token was checked")
	}

	// The two palettes must actually differ (a "colourblind alternate"
	// that's byte-identical to default isn't an alternate).
	same := true
	for _, tok := range tokens {
		if DefaultPalette.Color(tok) != ColourblindPalette.Color(tok) {
			same = false
			break
		}
	}
	if same {
		t.Fatalf("DefaultPalette and ColourblindPalette are colour-for-colour identical")
	}
}

func TestPalette_DangerAndWarningDistinctInBothPalettes(t *testing.T) {
	if DefaultPalette.Color(TokenDanger) == DefaultPalette.Color(TokenWarning) {
		t.Fatalf("DefaultPalette: danger and warning share a colour")
	}
	if ColourblindPalette.Color(TokenDanger) == ColourblindPalette.Color(TokenWarning) {
		t.Fatalf("ColourblindPalette: danger and warning share a colour")
	}
}

func TestPalette_UnknownTokenIsSafeDefault(t *testing.T) {
	if c := DefaultPalette.Color(Token(999)); c != tcell.ColorDefault {
		t.Fatalf("out-of-range token returned %v, want tcell.ColorDefault", c)
	}
}

func TestPalette_SelectionStyleReverses(t *testing.T) {
	base := DefaultPalette.Style(TokenMoney)
	sel := DefaultPalette.SelectionStyle(base)
	_, _, attrs := sel.Decompose()
	if attrs&tcell.AttrReverse == 0 {
		t.Fatalf("SelectionStyle did not set the Reverse attribute: %v", attrs)
	}
}
