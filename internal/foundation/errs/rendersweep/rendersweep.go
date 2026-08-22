// Package rendersweep is the shared, module-agnostic rendering gate for
// the BUG-313/BUG-317 class of defect: a registry-sourced error whose
// message template fails to substitute a placeholder reaches the user as
// literal "{token}" text, and the failure is invisible to every test that
// asserts only on e.Code.
//
// The only check that catches this defect is RENDERING every code a module
// owns (with the ctx keys its real call sites pass) and failing on any
// surviving brace token. Reading errors.go/errors.json is exactly what
// lets templates drift silently in the first place — BUG-313's round found
// six of ten projections codes broken by reading them, and widening the
// detector exposed BUG-317 (seven more live broken renders in ui.keys).
//
// [Sweep] is that check, factored here so every module's test calls the
// same helper instead of each reimplementing the regex/assert (the
// copy-on-copy pattern BUG-313's fix was beginning to spawn across the
// tree). Do not write another copy — call [Sweep].
package rendersweep

import (
	"fmt"
	"regexp"
)

// unsubstitutedPlaceholder matches any brace-wrapped token still present
// after errs.renderTemplate has rendered a template. A correctly
// substituted message never contains a bare "{identifier}" run (the
// registry's own "code"/"correlationId" builtins always resolve, and
// every other key is either replaced by its ctx value or degrades to the
// literal "{key}" text — see errs.renderTemplate's doc comment). Finding
// one of these in a rendered string is exactly the class of defect
// BUG-313/BUG-317 fix.
//
// Deliberately permissive: `[^}]+`, NOT an identifier-shaped class like
// `[A-Za-z][A-Za-z0-9_]*`. errs.renderTemplate accepts ANY bytes between
// "{" and the next "}" as a token name — it does not require the token to
// look like an identifier. A narrower, "tidier" regex here would miss a
// hyphenated or otherwise non-identifier token (e.g. "{provider-key}")
// that renders broken while this gate stays green. Do not narrow this for
// tidiness.
var unsubstitutedPlaceholder = regexp.MustCompile(`\{[^}]+\}`)

// Sweep renders each code via render and returns a failure message for
// any output that still contains a surviving "{token}" placeholder, or
// that renders empty. It does NOT take *testing.T and never imports it —
// this package is a plain helper, not a test-only package (round D8: a
// non-test package importing testing would drag the testing runtime into
// the production build graph). The caller reports the returned messages
// through its own *testing.T.
//
// render must return the user-visible rendered string for a code — e.g.
// errs.New(code, correlationID, ctx).Display() or .Msg. It may be a
// closure over a per-code ctx map, or (better) a real call site.
func Sweep(codes []string, render func(code string) string) []string {
	var failures []string
	for _, code := range codes {
		out := render(code)
		if loc := unsubstitutedPlaceholder.FindString(out); loc != "" {
			failures = append(failures, fmt.Sprintf("%s renders with an unsubstituted placeholder %q: %s", code, loc, out))
		}
		if out == "" {
			failures = append(failures, fmt.Sprintf("%s rendered an empty string", code))
		}
	}
	return failures
}
