package keys

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs/rendersweep"
)

// TestErrorTemplatesRenderNoSurvivingPlaceholders is the BUG-317 gate for
// the renderable-error class: any registry code whose message template has a
// placeholder the real call site does NOT supply reaches the user as literal
// "{token}" text. It drives every code in this class through errs.New with
// the same ctx keys its real call site passes and fails on any surviving
// brace token, using the shared rendersweep.Sweep helper.
//
// The "!q" quote-suffix codes (MET-U304/305/306/307/401/402) are the
// original BUG-317 set. The copyguard codes (MET-U101/U203/U308) and the
// register-conflict/empty-path codes (MET-U301/U309) are the D3 extension —
// the BUG-317 REJECT round found them rendering literal {cause}/
// {conflictsWith} because their templates named a key the call site never
// passed (the copyguards all pass {method}; U301's empty-path branch had no
// conflictsWith at all). Fixing the templates alone would silently regress,
// so the gate sweeps them too.
func TestErrorTemplatesRenderNoSurvivingPlaceholders(t *testing.T) {
	// ctxFor maps each code to the ctx keys its real call site passes:
	// palette.go (codePaletteInvalidArg / codePaletteUnknownCommand),
	// marks.go (codeInvalidMarkID), grammar.go (codeReservedToken /
	// codeRegisterPrefixConflict / codeGrammarCopied / codeRegisterEmptyPath),
	// the copyguard callers of MET-U101 (ui.screen.map) and MET-U203
	// (ui.screen.debug) — both pass {"method": ...} — and
	// internal/ui/screens/devmode/console.go (MET-U401 ErrConsoleNotOpen /
	// MET-U402 ErrCapabilityNotConfigured).
	//
	// The cross-package codes (MET-U101/U203/U401/U402) are literal strings
	// rather than their packages' exported constants: a test in package keys
	// cannot import ui.screen.map/debug/devmode without an import cycle since
	// FEAT-211 (ui.core imports ui.keys, and those screens import ui.core).
	// They stay valid because rendersweep renders them through the real
	// registry, and the map/debug/devmode packages assert their own codes in
	// their own copyguard tests.
	ctxFor := map[string]map[string]any{
		codePaletteInvalidArg: {
			"name":    "zoom",
			"command": "zoom",
			"kind":    "int",
			"value":   "5",
		},
		codeInvalidMarkID: {
			"id": "m",
		},
		codeReservedToken: {
			"token": "Esc",
		},
		codePaletteUnknownCommand: {
			"name": "bogus",
		},
		codeRegisterPrefixConflict: {
			"path":          []string{"z", "o"},
			"conflictsWith": []string{"z", "o", "m"},
			"cause":         "this path is a prefix of an already-registered longer path",
		},
		codeGrammarCopied: {
			"method": "Register",
		},
		codeRegisterEmptyPath: {
			"path": []string{},
		},
		"MET-U101": { // mapscreen.ErrMapScreenCopied
			"method": "Subscribe",
		},
		"MET-U203": { // debug.ErrScreenCopied
			"method": "Collect",
		},
		"MET-U401": { // devmode.ErrConsoleNotOpen
			"action": "inspect",
		},
		"MET-U402": { // devmode.ErrCapabilityNotConfigured
			"capability": "feedback-submit",
		},
	}

	codes := []string{
		codePaletteInvalidArg,
		codeInvalidMarkID,
		codeReservedToken,
		codePaletteUnknownCommand,
		codeRegisterPrefixConflict,
		codeGrammarCopied,
		codeRegisterEmptyPath,
		"MET-U101",
		"MET-U203",
		"MET-U401",
		"MET-U402",
	}

	for _, failure := range rendersweep.Sweep(codes, func(code string) string {
		return errs.New(code, "render-sweep", ctxFor[code]).Display()
	}) {
		t.Error(failure)
	}
}
