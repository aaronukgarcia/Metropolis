package rules

// TB is the slice of testing.TB that [SetOldestSupportedForTest] needs. It
// is declared here so this package never imports "testing" in production
// code; *testing.T satisfies it.
type TB interface {
	Helper()
	Cleanup(func())
}

// SetOldestSupportedForTest sets [OldestSupported] to v for the duration of
// the test and restores the previous value when the test (and its
// subtests) finish. It is the one sanctioned way to exercise the retirement
// refusal: production code never assigns OldestSupported, and a registry
// test fails if a non-test file calls this helper. The variable is global,
// so a test that uses it must not run in parallel.
func SetOldestSupportedForTest(t TB, v Version) {
	t.Helper()
	old := OldestSupported
	OldestSupported = v
	t.Cleanup(func() { OldestSupported = old })
}

// SetCurrentForTest makes [Effective] answer v for the duration of the test
// and restores the previous value when the test finishes, so a test can
// stand in for a future build that has wired rule v: versions up to v become
// known, and a restore of older history is "older rules". It exists because
// [Current] is a constant (0 while every registered rule is pending wiring).
// v must be at least [Current]. Production code never calls it (a registry
// test fails if a non-test file does). Not for parallel tests.
func SetCurrentForTest(t TB, v Version) {
	t.Helper()
	if v < Current {
		panic("rules.SetCurrentForTest: v is below Current")
	}
	old := testCurrent.Load()
	testCurrent.Store(int64(v))
	t.Cleanup(func() { testCurrent.Store(old) })
}
