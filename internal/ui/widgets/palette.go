package widgets

import "github.com/gdamore/tcell/v2"

// Token is a semantic colour role, not a literal colour. Widgets never
// hardcode a tcell.Color; they ask a Palette for a Token's colour, so
// swapping Palette (default <-> colourblind-safe) recolours every
// widget in the game without touching widget code — the contract
// AC-14/doc.go promises future widgets.
type Token int

const (
	// TokenMoney is UI-SPEC §2's "money green" — cash figures, budget
	// surplus, positive fiscal deltas.
	TokenMoney Token = iota
	// TokenWater is "water blue" — water network overlays, reservoir
	// tiles, the water utility screen's accent.
	TokenWater
	// TokenPower is "power yellow" — electricity network overlays,
	// substation load.
	TokenPower
	// TokenDanger is "danger red" — threshold breaches, failures,
	// outages, the F1 alert line's worst state.
	TokenDanger
	// TokenWarning is "warning amber" — approaching-threshold states,
	// one step below TokenDanger.
	TokenWarning
	// TokenDecay is "decay grey-purple" — blight, deferred maintenance,
	// abandoned/derelict structure overlays.
	TokenDecay
	// TokenSelection is "selection inverse" — UI-SPEC §2 specifies
	// selection as an *inverse-video* treatment, not a hue, so
	// Palette.SelectionStyle applies tcell's Reverse attribute over a
	// base style rather than substituting a colour; TokenSelection's
	// Palette colour entry exists so selection still has a well-defined
	// colour to reverse against on backends that need one explicitly
	// (e.g. degraded 16-colour conhost, core.ConhostProfile).
	TokenSelection

	tokenCount
)

// Palette is a complete set of semantic colours: one tcell.Color per
// Token, shipped as plain data so a screen can pick a Palette at
// capability-probe / player-settings time rather than this package
// hardcoding one look. UI-SPEC §2: "colourblind alternative palettes
// ship day one (deficient red/green is disqualifying in a dashboard
// game)" — DefaultPalette and ColourblindPalette below are both
// complete, both distinct, satisfying that line from first render.
type Palette struct {
	// Name identifies the palette for settings UI / diagnostics.
	Name string
	// colors is indexed by Token; unexported so the only way to build a
	// Palette is NewPalette, which enforces every Token has an entry
	// (AC-2's "every semantic colour has an entry in both").
	colors [tokenCount]tcell.Color
}

// NewPalette builds a Palette from a name and a complete colors map. It
// panics if colors is missing an entry for any Token — a palette
// missing a semantic colour is a programming error to catch at package
// init (both palette vars below are built this way), not a runtime
// condition a widget should have to handle mid-render.
func NewPalette(name string, colors map[Token]tcell.Color) Palette {
	var p Palette
	p.Name = name
	for t := Token(0); t < tokenCount; t++ {
		c, ok := colors[t]
		if !ok {
			panic("widgets: palette " + name + " missing colour for token " + tokenName(t))
		}
		p.colors[t] = c
	}
	return p
}

func tokenName(t Token) string {
	switch t {
	case TokenMoney:
		return "money"
	case TokenWater:
		return "water"
	case TokenPower:
		return "power"
	case TokenDanger:
		return "danger"
	case TokenWarning:
		return "warning"
	case TokenDecay:
		return "decay"
	case TokenSelection:
		return "selection"
	default:
		return "unknown"
	}
}

// Color returns the palette's colour for t. An out-of-range Token
// returns tcell.ColorDefault rather than panicking or indexing out of
// bounds — a widget that receives a bad Token constant (future token
// added to one call site but not this array) degrades to "no colour
// applied," never a crash on T-RENDER (mirrors core.Buffer.Set/Get's
// out-of-range-is-a-no-op discipline).
func (p Palette) Color(t Token) tcell.Color {
	if t < 0 || t >= tokenCount {
		return tcell.ColorDefault
	}
	return p.colors[t]
}

// Style returns tcell.StyleDefault with t's colour as the foreground —
// the common case for text/glyph widgets (borders, gauges, big numbers)
// that want "the danger-red style" without hand-building a tcell.Style
// at every call site.
func (p Palette) Style(t Token) tcell.Style {
	return tcell.StyleDefault.Foreground(p.Color(t))
}

// SelectionStyle applies TokenSelection's inverse-video treatment over
// base, preserving base's colours (Reverse swaps fg/bg at render time
// rather than this package guessing a replacement colour pair) — the
// literal reading of UI-SPEC §2's "selection inverse."
func (p Palette) SelectionStyle(base tcell.Style) tcell.Style {
	return base.Reverse(true)
}

// DefaultPalette is the truecolor palette used on Windows Terminal
// (core.WindowsTerminalProfile). Colours are chosen to read clearly as
// their semantic names on a dark terminal background.
var DefaultPalette = NewPalette("default", map[Token]tcell.Color{
	TokenMoney:     tcell.NewHexColor(0x2ECC71), // money green
	TokenWater:     tcell.NewHexColor(0x3498DB), // water blue
	TokenPower:     tcell.NewHexColor(0xF1C40F), // power yellow
	TokenDanger:    tcell.NewHexColor(0xE74C3C), // danger red
	TokenWarning:   tcell.NewHexColor(0xE67E22), // warning amber
	TokenDecay:     tcell.NewHexColor(0x7D6E83), // decay grey-purple
	TokenSelection: tcell.NewHexColor(0xECF0F1), // selection base (reversed)
})

// ColourblindPalette is the colourblind-safe alternate, built from the
// Okabe-Ito palette (chosen for red/green deficiency safety — the
// common case UI-SPEC §2 calls out as disqualifying if unaddressed).
// Every hue is chosen to stay distinguishable under deuteranopia and
// protanopia simulation, in particular keeping TokenDanger and
// TokenWarning apart (vermillion vs a distinct reddish-purple, rather
// than red vs amber, which collapse together for red-green deficient
// vision) and TokenMoney/TokenWater apart from TokenPower.
var ColourblindPalette = NewPalette("colourblind-safe", map[Token]tcell.Color{
	TokenMoney:     tcell.NewHexColor(0x009E73), // bluish green
	TokenWater:     tcell.NewHexColor(0x0072B2), // blue
	TokenPower:     tcell.NewHexColor(0xF0E442), // yellow
	TokenDanger:    tcell.NewHexColor(0xD55E00), // vermillion
	TokenWarning:   tcell.NewHexColor(0xCC79A7), // reddish purple
	TokenDecay:     tcell.NewHexColor(0x999999), // neutral grey (stands in for grey-purple)
	TokenSelection: tcell.NewHexColor(0xFFFFFF), // selection base (reversed)
})
