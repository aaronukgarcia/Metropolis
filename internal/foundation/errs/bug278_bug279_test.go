package errs

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"
)

// TestRejectCopiedLog_UsesInjectedPackageClock is the BUG-278 regression
// guard. rejectCopiedLog is Log's fail-closed path for a struct-copied
// receiver (ASM-074/SEC-020), reachable from every errs.New via the package
// sink. It must timestamp its entry through the package-injectable clock
// (errs.go now()/SetClock), never time.Now() directly — a raw wall-clock read
// on this production-reachable path breaks sim-clock determinism
// (GR#21/M0-ENG §1.1).
//
// The test pins the package clock to a fixed instant far from wall time and
// asserts the rejected entry carries exactly that instant. It is IMPOSSIBLE to
// pass while rejectCopiedLog calls time.Now(): a fixed 2001-instant can never
// equal the wall clock. (RED evidence: reverting log.go:347 to
// time.Now().UTC() fails this with the current date instead of 2001.)
func TestRejectCopiedLog_UsesInjectedPackageClock(t *testing.T) {
	resetSinkForTest()
	t.Cleanup(resetSinkForTest)

	fixed := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	SetClock(func() time.Time { return fixed })
	t.Cleanup(func() { SetClock(time.Now) })

	var buf bytes.Buffer
	orig := NewLogger(&buf)
	cp := loggerByteCopy(orig)

	// A copy's Log() takes the reject path; the Entry has no Ts, so
	// rejectCopiedLog must fill it from the clock.
	err := cp.Log(Entry{Level: "error", Code: "MET-F900", CorrelationID: "corr-278", Module: "m", Msg: "bug278 clock"})
	wantLoggerCopied(t, "Log", err)

	want := fixed.UTC().Format(time.RFC3339Nano)
	found := false
	for _, e := range RecentCopyRejections() {
		if e.CorrelationID == "corr-278" && e.Msg == "bug278 clock" {
			found = true
			if e.Ts != want {
				t.Fatalf("rejectCopiedLog Ts = %q, want injected-clock %q (BUG-278: the reject path must use the injectable clock, not wall time)", e.Ts, want)
			}
			break
		}
	}
	if !found {
		t.Fatal("BUG-278 test entry not found in RecentCopyRejections() — the reject path did not record it")
	}
}

// TestConstruct_RegistryFailureModes_DistinctCodes is the BUG-279 regression
// guard. A registry failure used to collapse unconditionally to MET-F003
// ("unregistered code", severity "error"), leaving the two fatal registry
// codes defined-but-unreachable:
//
//	MET-F001 fatal — registry could not be loaded at all (path/read/parse)
//	MET-F002 fatal — registry loaded but failed schema validation
//	MET-F003 error — a VALID registry simply has no such code
//
// Each sub-test drives a distinct real failure mode through New and asserts the
// distinct code. The F001 and F002 assertions are the ones that were
// impossible before the fix: with the collapse in place every case returned
// MET-F003, so the F001/F002 rows fail (RED). The F003 row proves the fix did
// not over-correct — a genuine unknown code in a valid registry still yields
// F003.
func TestConstruct_RegistryFailureModes_DistinctCodes(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T)
		want  string
	}{
		{
			name: "missing file -> F001 (load failure)",
			setup: func(t *testing.T) {
				t.Setenv(registryPathEnv, filepath.Join(t.TempDir(), "nope.json"))
			},
			want: "MET-F001",
		},
		{
			name: "malformed JSON -> F001 (unparseable, no usable registry)",
			setup: func(t *testing.T) {
				path := writeRegistry(t, t.TempDir(), `{not valid json`)
				t.Setenv(registryPathEnv, path)
			},
			want: "MET-F001",
		},
		{
			name: "bad code format -> F002 (validation failure)",
			setup: func(t *testing.T) {
				path := writeRegistry(t, t.TempDir(), `{"version":1,"codes":{"BAD-CODE":{"severity":"error","module":"m","message":"m","remedy":"r"}}}`)
				t.Setenv(registryPathEnv, path)
			},
			want: "MET-F002",
		},
		{
			name: "missing required field -> F002 (validation failure)",
			setup: func(t *testing.T) {
				path := writeRegistry(t, t.TempDir(), `{"version":1,"codes":{"MET-F901":{"severity":"error","module":"m","message":"m","remedy":""}}}`)
				t.Setenv(registryPathEnv, path)
			},
			want: "MET-F002",
		},
		{
			name: "malformed template token -> F002 (validation failure)",
			setup: func(t *testing.T) {
				path := writeRegistry(t, t.TempDir(), `{"version":1,"codes":{"MET-F901":{"severity":"error","module":"m","message":"bad {value!q}","remedy":"r"}}}`)
				t.Setenv(registryPathEnv, path)
			},
			want: "MET-F002",
		},
		{
			name: "valid registry, unknown code -> F003 (unregistered code)",
			setup: func(t *testing.T) {
				path := writeRegistry(t, t.TempDir(), validRegistry)
				t.Setenv(registryPathEnv, path)
			},
			want: "MET-F003",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)
			resetRegistryForTest()
			resetSinkForTest()
			t.Cleanup(func() {
				resetRegistryForTest()
				resetSinkForTest()
			})

			// MET-F999 is present in NONE of these registries (validRegistry
			// defines only MET-F900), so for the valid-registry case it is a
			// genuine unknown code (→ F003), and for the broken-registry cases
			// the requested code is irrelevant — the failure mode alone decides.
			e := New("MET-F999", "corr-279", nil)
			if e.Code != tc.want {
				t.Fatalf("New under %q: Code = %q, want %q (BUG-279: registry failure modes must be distinct, not all collapsed to MET-F003)", tc.name, e.Code, tc.want)
			}
		})
	}
}

// TestConstruct_RegistryLoadFailure_IsFatalSeverity proves the BUG-279 severity
// half: MET-F001/F002 are logged at their declared "fatal" severity, not the
// downgraded "error" the MET-F003 collapse produced. The audit entry's Level
// is the observable channel for that.
func TestConstruct_RegistryLoadFailure_IsFatalSeverity(t *testing.T) {
	t.Setenv(registryPathEnv, filepath.Join(t.TempDir(), "nope.json"))
	resetRegistryForTest()
	resetSinkForTest()
	t.Cleanup(func() {
		resetRegistryForTest()
		resetSinkForTest()
	})

	e := New("MET-F900", "corr-279-sev", nil)
	if e.Code != "MET-F001" {
		t.Fatalf("precondition: Code = %q, want MET-F001", e.Code)
	}

	found := false
	for _, entry := range Recent() {
		if entry.CorrelationID == "corr-279-sev" && entry.Code == "MET-F001" {
			found = true
			if entry.Level != "fatal" {
				t.Fatalf("MET-F001 logged at Level %q, want \"fatal\" (BUG-279: registry outage must not be downgraded to error)", entry.Level)
			}
			break
		}
	}
	if !found {
		t.Fatal("no MET-F001 audit entry found for the load failure")
	}
}

// TestRegistryError_NilErrorGuard is the BUG-278 nil-guard regression test.
// (*registryError).Error() must never panic, even when constructed with a nil
// err field — e.g., a *registryError{err: nil} can occur transiently in code
// that does not depend on a non-nil err. The guard is critical in
// constructRegistryFailure (errs.go ~259) which unconditionally calls
// regErr.Error() to fill the {cause} context key. RED evidence: removing the
// nil guard from registry.go causes this test to panic with "runtime error:
// invalid memory address or nil pointer dereference".
func TestRegistryError_NilErrorGuard(t *testing.T) {
	// Construct a *registryError with err==nil directly; this mimics the
	// panic scenario if the guard is missing.
	re := &registryError{
		kind: registryLoadFailed,
		path: "data/errors.json",
		err:  nil, // the critical condition: err is nil
	}

	// Error() must not panic and must return a stable, non-empty string.
	result := re.Error()
	if result == "" {
		t.Fatal("registryError.Error() returned empty string (expected stable fallback)")
	}
	if result != "registry failure (no underlying cause)" {
		t.Logf("registryError.Error() with nil err = %q (not the hardcoded fallback, but that is OK as long as it does not panic)", result)
	}
}
