package stub

import "github.com/aaronukgarcia/Metropolis/internal/protocol"

// SubIDsForTest returns the IDs of all currently-live subscriptions, read
// under s.mu. It exists so BUG-283's regression test can learn a
// subscription's ID and Unsubscribe it BEFORE its (chaos-delayed) first
// delta has been delivered — the ID is normally carried on that first
// delta, which is exactly the delta the test must prevent from arriving.
// Test-only (the _test.go suffix); never ships in the production binary.
func SubIDsForTest(s *StubEngine) []protocol.SubscriptionID {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]protocol.SubscriptionID, 0, len(s.subs))
	for id := range s.subs {
		ids = append(ids, id)
	}
	return ids
}

// MaxAdvanceTicksPerCallForTest exposes the package-private
// maxAdvanceTicksPerCall (codes.go) to external test packages (this
// file's stub_test package, via drift_test.go) without widening
// StubEngine's real exported API — the standard Go export_test.go
// pattern. This file is compiled only for tests (the _test.go suffix)
// and never ships in the production binary.
func MaxAdvanceTicksPerCallForTest() int64 { return maxAdvanceTicksPerCall }
