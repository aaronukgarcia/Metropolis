package devmode

// BUG-317 Defect 1 render gate for feat.devmode.
//
// MET-U401 ({action}) and MET-U402 ({capability}) are two of the seven
// codes whose templates once carried the unsupported {token!q} quote
// suffix and rendered a literal placeholder to users. The suffix is
// removed (BUG-357 step 2 makes an unsupported modifier a registry LOAD
// failure — see internal/foundation/errs/registry.go), but that load gate
// cannot see a well-formed token whose ctx key a call site simply fails
// to supply. This gate drives the real Console methods and asserts every
// placeholder is substituted in the rendered Display().

import (
	"regexp"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// unsubstitutedPlaceholder — see the identical rationale in
// internal/ui/keys/render_gate_test.go and the projections gate: `[^}]+`
// is deliberately permissive so a suffixed/hyphenated token that renders
// broken cannot slip past an identifier-shaped regex.
var unsubstitutedPlaceholder = regexp.MustCompile(`\{[^}]+\}`)

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

// TestDevmodeErrorCodesRenderWithRealCallSiteKeys drives the real Console
// surfaces for MET-U401/U402 and asserts the rendered text substitutes
// every placeholder.
func TestDevmodeErrorCodesRenderWithRealCallSiteKeys(t *testing.T) {
	// MET-U401 — a devconsole action attempted before Open. Template
	// names {action}; Inspect/SubmitFeedback on a never-opened console.
	t.Run("MET-U401_Inspect_notOpen", func(t *testing.T) {
		c := New() // never opened
		_, err := c.Inspect("corr-401-inspect", "any-ref")
		assertRenders(t, err, "MET-U401")
	})
	t.Run("MET-U401_SubmitFeedback_notOpen", func(t *testing.T) {
		c := New() // never opened
		err := c.SubmitFeedback("corr-401-feedback", 0, "body")
		assertRenders(t, err, "MET-U401")
	})

	// MET-U402 — an open console whose capability seam is unwired.
	// Template names {capability}; open with the gate satisfied but no
	// WithInspect / WithSubmitFeedback option.
	t.Run("MET-U402_Inspect_unwired", func(t *testing.T) {
		c := New(WithRequireConsole(func(string) error { return nil }))
		if err := c.Open("corr-402-inspect"); err != nil {
			t.Fatalf("Open: %v", err)
		}
		_, err := c.Inspect("corr-402-inspect", "any-ref")
		assertRenders(t, err, "MET-U402")
	})
	t.Run("MET-U402_SubmitFeedback_unwired", func(t *testing.T) {
		c := New(WithRequireConsole(func(string) error { return nil }))
		if err := c.Open("corr-402-feedback"); err != nil {
			t.Fatalf("Open: %v", err)
		}
		err := c.SubmitFeedback("corr-402-feedback", 0, "body")
		assertRenders(t, err, "MET-U402")
	})
}
