package keys

// BUG-317 Defect 1 render gate for ui.keys.
//
// Four ui.keys registry codes (MET-U304/U305/U306/U307) once carried a
// {token!q} quote-suffix convention that errs.renderTemplate has no
// handling for, so the token was never substituted and the literal
// "{token!q}" reached the user (the command palette telling a player
// their input is wrong while showing them neither what they typed nor
// what was expected). BUG-357 step 2 made a malformed token a registry
// LOAD failure (internal/foundation/errs/registry.go), and the templates
// were normalised to the plain-identifier form errs can substitute; the
// !q modifier is deliberately NOT implemented — an unsupported modifier
// is a hard load failure, not a silently-passed-through literal.
//
// That load gate proves no malformed token exists in the data. It does
// NOT prove the four codes' real CALL SITES actually supply the ctx keys
// their templates name — a missing ctx key renders a literal "{key}" that
// the load gate cannot see (it is well-formed, just unsupplied), exactly
// the drift class that made the projections codes ship broken (BUG-313).
// This gate drives the real production call sites and asserts every
// placeholder is substituted in the rendered Display().

import (
	"regexp"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// unsubstitutedPlaceholder matches any brace-wrapped run STILL present
// after errs.New has rendered the template. Deliberately permissive
// (`[^}]+`, not an identifier-shaped class): errs.renderTemplate accepts
// ANY bytes between "{" and the next "}" as a token name, so a suffixed
// or hyphenated token (e.g. "{value!q}", "{provider-key}") renders broken
// while a narrower "tidier" regex would stay blind to it — the exact hole
// BUG-317 Defect 2 was filed against. Do not narrow this for tidiness.
var unsubstitutedPlaceholder = regexp.MustCompile(`\{[^}]+\}`)

// assertRenders fails t if err's Display() text contains any unsubstituted
// "{token}" — the rendering standard BUG-313/BUG-317 require: prove the
// message is actually readable, not merely that the code matches.
func assertRenders(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected an error, got nil", wantCode)
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("%s: error is %T, not *errs.E: %v", wantCode, err, err)
	}
	if e.Code != wantCode {
		t.Fatalf("code = %q, want %q (message: %s)", e.Code, wantCode, e.Display())
	}
	display := e.Display()
	if loc := unsubstitutedPlaceholder.FindString(display); loc != "" {
		t.Errorf("%s renders with an unsubstituted placeholder %q: %s", wantCode, loc, display)
	}
	if display == "" {
		t.Errorf("%s rendered an empty Display()", wantCode)
	}
}

// TestUIKeysErrorCodesRenderWithRealCallSiteKeys drives the REAL
// production call sites for each of the four ui.keys codes whose
// templates carried the removed {token!q} suffix and asserts the rendered
// text substitutes every placeholder. Never a hand-built errs.New/ctx map
// (which could itself drift from the site it represents) — only the real
// methods.
func TestUIKeysErrorCodesRenderWithRealCallSiteKeys(t *testing.T) {
	// MET-U304 — palette argument rejected. Template names
	// {name}/{command}/{kind}/{value}; the site must supply all four.
	t.Run("MET-U304_ParseCommand_badArg", func(t *testing.T) {
		g := newTestGrammar()
		p := NewPalette(g, "test-corr")
		_ = p.RegisterCommand(CommandSpec{
			Name: "loan",
			Args: []ArgSpec{{Name: "amount", Kind: ArgMoney}, {Name: "term", Kind: ArgDuration}},
		})
		_, err := p.ParseCommand(":loan notmoney 10y")
		assertRenders(t, err, "MET-U304")
	})
	t.Run("MET-U304_ParseCommand_missingArg", func(t *testing.T) {
		g := newTestGrammar()
		p := NewPalette(g, "test-corr")
		_ = p.RegisterCommand(CommandSpec{
			Name: "loan",
			Args: []ArgSpec{{Name: "amount", Kind: ArgMoney}},
		})
		_, err := p.ParseCommand(":loan")
		assertRenders(t, err, "MET-U304")
	})

	// MET-U305 — invalid mark slot. Template names {id}.
	t.Run("MET-U305_SetMark_thirteenthSlot", func(t *testing.T) {
		g := newTestGrammar()
		err := g.SetMark("m", "should-not-land")
		assertRenders(t, err, "MET-U305")
	})

	// MET-U306 — reserved token. Template names {token}.
	t.Run("MET-U306_Register_reservedTopLevel", func(t *testing.T) {
		g := newTestGrammar()
		err := g.Register([]string{"u", "x"}, Action{Name: "bad"})
		assertRenders(t, err, "MET-U306")
	})
	t.Run("MET-U306_RegisterGlobal_reservedEsc", func(t *testing.T) {
		g := newTestGrammar()
		err := g.RegisterGlobal(KeyEsc, Action{Name: "bad"})
		assertRenders(t, err, "MET-U306")
	})

	// MET-U307 — unknown palette command. Template names {name}.
	t.Run("MET-U307_ParseCommand_unknown", func(t *testing.T) {
		g := newTestGrammar()
		p := NewPalette(g, "test-corr")
		_, err := p.ParseCommand(":nosuchcommand 1")
		assertRenders(t, err, "MET-U307")
	})
	t.Run("MET-U307_ParseCommand_emptyInput", func(t *testing.T) {
		g := newTestGrammar()
		p := NewPalette(g, "test-corr")
		_, err := p.ParseCommand("   ")
		assertRenders(t, err, "MET-U307")
	})
}
