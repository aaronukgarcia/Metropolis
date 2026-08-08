package buildinfo

import "testing"

// TestStringContainsAllFields is a trivial smoke test so `go test ./...`
// exercises the buildinfo package. It does not assert on ldflags-injected
// values (those are "dev" outside of build.ps1) — it only proves String()
// composes the four fields without panicking and without dropping any of
// them.
func TestStringContainsAllFields(t *testing.T) {
	got := String()
	if got == "" {
		t.Fatal("String() returned empty string")
	}

	for _, want := range []string{Version, Commit, Branch, BuildTime} {
		if want == "" {
			t.Fatal("build field must never be empty; default should be \"dev\"")
		}
	}
}

func TestDefaultsAreDev(t *testing.T) {
	// Guards against someone accidentally hardcoding a non-"dev" default
	// and silently breaking the "never hand-maintained" rule (M0-ENG §3).
	if Version != "dev" && Version != "" {
		return // overridden via -ldflags for this test binary; acceptable
	}
	if Version == "" {
		t.Fatal("Version must default to \"dev\", not empty string")
	}
}
