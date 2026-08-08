package protocol

import (
	"errors"
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"
)

// SubscriptionID identifies one live view subscription (UI-SPEC §6).
// Allocated by the engine side in response to a Subscribe command and
// echoed in UnsubscribePayload and every Delta belonging to it.
type SubscriptionID string

// ViewName naming scheme (documentation, not an enforced type — view
// names travel as plain strings on SubscribePayload.ViewName so new
// views never require a protocol change).
//
// Grammar: lowercase dot-separated segments, optionally with an entity
// ID segment:
//
//	<screen-or-scope>.<projection>[.<id>[.<sub-projection>]]
//
// Examples from UI-SPEC §6 and the F-screen layout (GDD §13):
//
//	"f1.viewport"            — F1's map viewport (params carry origin+extent)
//	"f2.ledger"               — F2's cash/ledger dashboard
//	"junction.14.approaches" — one junction's per-approach queue state
//	"citizen.482913.detail"  — one inspected citizen's life-writing detail
//
// Segment 1 is either an F-screen key ("f1".."f12") for screen-scoped
// dashboards, or an engine-domain noun ("junction", "citizen", "district")
// for entity-scoped views addressed by ID in segment 2. This mirrors
// EntityRef's "typed:id" convention closely enough to stay predictable
// without coupling the two.
var viewNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z0-9]+)+$`)

// ValidateViewName reports whether name follows the naming scheme
// documented above. It is advisory tooling (for tests, fixtures, and the
// engine's Subscribe handler to reject malformed names early) — this
// package does not otherwise interpret view names.
func ValidateViewName(name string) error {
	if !viewNamePattern.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidViewName, name)
	}
	return nil
}

// ErrInvalidViewName is returned by ValidateViewName for a name that
// does not match the documented view-naming grammar.
var ErrInvalidViewName = errors.New("protocol: view name does not match the naming scheme (lowercase.dot.segments)")

// SubscriptionAllocator hands out unique SubscriptionIDs. It is the
// engine side's responsibility to own one instance (typically inside
// T-SUBSCR, M0-ENG §1.1) and allocate from it whenever a Subscribe
// command is accepted. IDs are sequential integers under a fixed prefix
// — deliberately NOT derived from time or randomness, so allocation is
// cheap, allocation-free on the hot path, and reproducible given a fixed
// order of Subscribe commands (useful for H-REPLAY fixtures, MOD-013).
type SubscriptionAllocator struct {
	next atomic.Uint64
}

// NewSubscriptionAllocator returns an allocator whose first Allocate call
// returns SubscriptionID "sub-1".
func NewSubscriptionAllocator() *SubscriptionAllocator {
	return &SubscriptionAllocator{}
}

// Allocate returns the next unique SubscriptionID. Safe for concurrent
// use.
func (a *SubscriptionAllocator) Allocate() SubscriptionID {
	n := a.next.Add(1)
	return SubscriptionID(fmt.Sprintf("sub-%d", n))
}

// SeqTracker observes the Delta.Seq stream for one or more subscriptions
// and detects gaps or out-of-order/duplicate arrivals — the mechanism
// that makes InProcTransport's (or a future gRPC transport's) drop
// policy observable to the receiver instead of silently lossy. T-VIEWS
// (M0-ENG §1.1) owns one instance per UI process.
//
// It does not itself resync or request retransmission — v1 has no
// resubscribe-to-heal-a-gap protocol; a detected gap is surfaced (e.g. to
// the F12 log tail and the UI-SPEC §1 staleness dot) and left as an open
// question for the freeze review (docs/design/protocol.md).
type SeqTracker struct {
	mu   sync.Mutex
	last map[SubscriptionID]uint64
}

// NewSeqTracker returns an empty tracker.
func NewSeqTracker() *SeqTracker {
	return &SeqTracker{last: make(map[SubscriptionID]uint64)}
}

// Observe records seq for sub and reports the outcome:
//   - ok=true, gap=0        : expected next-in-sequence delta (or the
//     first one ever observed for sub).
//   - ok=true, gap=N (N>0)  : in-order but N deltas were skipped
//     (dropped by the transport, or the engine legitimately produced
//     none) between the last observed Seq and this one.
//   - ok=false               : seq is <= the last observed Seq for sub —
//     a duplicate or an out-of-order arrival. gap is 0 in this case;
//     the caller should treat this as a transport bug (InProcTransport's
//     single-writer, single-reader-per-subscription channel design
//     should make this impossible in v1 — see transport.go).
func (t *SeqTracker) Observe(sub SubscriptionID, seq uint64) (gap uint64, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	last, seen := t.last[sub]
	if !seen {
		t.last[sub] = seq
		return 0, true
	}
	if seq <= last {
		return 0, false
	}
	gap = seq - last - 1
	t.last[sub] = seq
	return gap, true
}

// Reset forgets sub, e.g. after Unsubscribe (a subsequent Subscribe to
// the same view starts a fresh Seq stream at 1, which must not be
// compared against the old one).
func (t *SeqTracker) Reset(sub SubscriptionID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.last, sub)
}
