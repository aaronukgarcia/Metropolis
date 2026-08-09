package stub

// MaxAdvanceTicksPerCallForTest exposes the package-private
// maxAdvanceTicksPerCall (codes.go) to external test packages (this
// file's stub_test package, via drift_test.go) without widening
// StubEngine's real exported API — the standard Go export_test.go
// pattern. This file is compiled only for tests (the _test.go suffix)
// and never ships in the production binary.
func MaxAdvanceTicksPerCallForTest() int64 { return maxAdvanceTicksPerCall }
