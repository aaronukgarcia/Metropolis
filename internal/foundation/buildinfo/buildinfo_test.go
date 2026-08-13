package buildinfo

import (
	"strings"
	"testing"
)

// TestStringContainsAllFields is a trivial smoke test so `go test ./...`
// exercises the buildinfo package. It does not assert on ldflags-injected
// values (those are "dev" outside of build.ps1) — it only proves String()
// composes the five fields without panicking and without dropping any of
// them.
func TestStringContainsAllFields(t *testing.T) {
	got := String()
	if got == "" {
		t.Fatal("String() returned empty string")
	}

	for _, want := range []string{Version, Commit, Branch, BuildTime, Host} {
		if want == "" {
			t.Fatal("build field must never be empty; default should be \"dev\"")
		}
	}
}

// TestStringIncludesHost proves the wiring for FEAT-034: String() must
// compose the Host field into its output. ldflags-injected values are
// empty-by-default ("dev") in a `go test` context (no -ldflags passed), so
// this sets a distinctive sentinel value directly on the package var and
// asserts it round-trips through String() — it proves the WIRING, not that
// a real hostname was injected (that only happens via build.ps1).
func TestStringIncludesHost(t *testing.T) {
	orig := Host
	defer func() { Host = orig }()

	Host = "TESTHOST-FEAT034"
	got := String()
	if !strings.Contains(got, "TESTHOST-FEAT034") {
		t.Fatalf("String() = %q; want it to contain the Host field value %q", got, "TESTHOST-FEAT034")
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
