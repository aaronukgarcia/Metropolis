package protocol

import (
	"errors"
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
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
//
// FEAT-042 AC-19 (documentation, not a wire change): UI-SPEC §4's
// drill-through rule gives its own worked examples of a WHOLE-ENTITY
// drill target — "a congestion percentage opens that junction; a
// school-roll number opens that school" — and both are already
// satisfied today, with zero code change, by this grammar's existing
// entity-scoped segment: a congestion view subscribes as
// "junction.14.approaches" and a school-roll view as
// "school.7.roll" (school's own view names are that module's to define;
// "junction"/"citizen" above are this package's own already-passing
// TestValidateViewName cases). What this grammar does NOT reach is
// UI-SPEC §4's "a cash figure opens its ledger lines" case: a single
// ledger LINE, buried inside an already-open "f2.ledger" view's patch,
// is not a subscription of its own — that narrower, genuinely new gap is
// what EntityID/TargetRef (entity.go) exist to close. See AC-20/AC-21.
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
//
// SEC-023 (SEC-020 wave 1's own sibling hunt): SubscriptionAllocator and
// NewSubscriptionAllocator are both exported, so any caller holding the
// *SubscriptionAllocator NewSubscriptionAllocator returned can
// dereference-and-copy it ('a2 := *a' is legal, unsafe-free, reflect-free
// Go — every field is unexported, but Go does not stop a caller from
// copying a struct value it can address). next is an atomic.Uint64, a
// VALUE (not a reference type), so unlike InProcTransport/
// SubscriptionServer/Engine the copy does NOT alias the original's
// counter — it gets its own, independently-incrementing one, seeded from
// whatever count the original had reached at the moment of the copy.
// That is the whole defect: this type's ENTIRE documented contract is
// "hands out unique SubscriptionIDs" (this comment, three lines up), and
// two independently-incrementing counters that started from the same
// point produce the SAME next ID, then keep diverging — a confused-
// deputy shape (two different subscriptions treated as one SubscriptionID)
// that breaks the H-REPLAY reproducibility guarantee this sequential-
// integer design exists for (docs/design/protocol.md). Proven via a
// deterministic byte-copy PoC in subscription_test.go
// (TestSEC023_DeterministicCopy_CollidesOnNextID): original and copy both
// allocate "sub-3" next, then continue to diverge on every call after.
//
// This is a narrower failure mode than InProcTransport/SubscriptionServer:
// atomic.Uint64 carries a noCopy sentinel (Go 1.19+) specifically so
// go vet's copylocks check catches a literal 'a2 := *a' at build time —
// SEC-023's own investigation confirmed that before reporting (the initial
// assumption that atomics were a vet blind spot was WRONG and was
// corrected). The exposure is the same as everywhere else in this SEC-020
// family: unsafe/reflect-constructed copies, and any future non-literal
// copy form vet cannot see. Unlike InProcTransport/SubscriptionServer/
// Engine, there is no mutex here at all, so the "hang forever on a
// copy's poisoned lock" mode (SEC-016) does not apply — the failure mode
// is silent ID collision, not a deadlock.
//
// self and checkNotCopied mirror the SEC-020 family exactly (see
// InProcTransport.self, transport.go, for the full identity-check
// rationale) — atomic.Pointer, stored once in NewSubscriptionAllocator
// before a is returned, checked lock-free before next is ever touched.
type SubscriptionAllocator struct {
	next atomic.Uint64

	// self holds the address NewSubscriptionAllocator gave this
	// SubscriptionAllocator at construction — see the type doc comment
	// and InProcTransport.self (transport.go) for the full rationale.
	// Set exactly once, at the end of NewSubscriptionAllocator, before a
	// is returned to any caller.
	self atomic.Pointer[SubscriptionAllocator]
}

// NewSubscriptionAllocator returns an allocator whose first Allocate call
// returns SubscriptionID "sub-1".
func NewSubscriptionAllocator() *SubscriptionAllocator {
	a := &SubscriptionAllocator{}
	// Stored exactly once, here, before a is returned to any caller — no
	// goroutine can have a reference to a to race this Store against
	// (SEC-023, mirroring NewInProcTransport/NewEngine/
	// NewSubscriptionServer — see the type doc comment above).
	a.self.Store(a)
	return a
}

// SentinelCopiedSubscriptionID is returned by Allocate when called on a
// struct-copied SubscriptionAllocator, instead of advancing (and thus
// polluting) that copy's own independent counter. It deliberately does
// NOT match the "sub-<positive-integer>" shape a genuine Allocate call
// always produces (no digits after the prefix), so it can never collide
// with — or be mistaken for — a real, uniquely-allocated SubscriptionID,
// and any caller that wants to notice the misuse can compare against
// this exact constant. See the ASM- ranking this decision, and Allocate's
// doc comment, for why a sentinel value was chosen over an error return
// or a panic.
const SentinelCopiedSubscriptionID SubscriptionID = "sub-copied"

// checkNotCopied reports whether the receiver is a struct copy of some
// other SubscriptionAllocator value (SEC-023, mirroring
// InProcTransport.checkNotCopied — transport.go). Deliberately lock-free
// (there is no lock on this type to order against — SEC-016's pre-lock
// requirement does not apply here since there is nothing to hang on —
// but the check still must run BEFORE a.next is ever touched, so that a
// rejected copy's own counter is never advanced and never observably
// diverges from the moment of rejection onward). A nil a.self.Load() (a
// SubscriptionAllocator constructed as a bare
// SubscriptionAllocator{}/new(SubscriptionAllocator) rather than via
// NewSubscriptionAllocator) is treated the same as a mismatch.
func (a *SubscriptionAllocator) checkNotCopied(correlationID string, ctx map[string]any) error {
	if a.self.Load() != a {
		return errs.New(ErrSubscriptionAllocatorCopied, correlationID, ctx)
	}
	return nil
}

// Allocate returns the next unique SubscriptionID. Safe for concurrent
// use.
//
// SEC-023: identity-checked before a.next is ever touched — a rejected
// copy's counter is never advanced, so it cannot silently accumulate
// state that would diverge further from the original. Allocate returns
// SubscriptionID only (no error) so its signature is unchanged for both
// call sites (engine/core/subscribe.go, engine/stub/engine.go) — see the
// logged ASM- entry for why a sentinel was chosen over widening the
// signature to return an error (which would ripple into both consuming
// packages for a defect that, per the SEC-023 finding, has no known live
// call site copying this type today) or returning a value from the
// legitimate ID space (which risks exactly the collision this fix
// exists to prevent, just moved one level up). On a copy, Allocate
// returns SentinelCopiedSubscriptionID every time — a fixed, obviously-
// wrong value rather than a plausible-looking one — and the rejection is
// itself logged via errs.New (GR#1) inside checkNotCopied.
func (a *SubscriptionAllocator) Allocate() SubscriptionID {
	if err := a.checkNotCopied(errs.NewCorrelationID(), nil); err != nil {
		return SentinelCopiedSubscriptionID
	}
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
//
// SEC-020 wave 1 (SubscriptionAllocator's SEC-023 sibling hunt excluded
// SeqTracker per Bill/ASM-059 at the time — it is covered here as part of
// this dispatch instead): SeqTracker and NewSeqTracker are both exported,
// so any caller holding the *SeqTracker NewSeqTracker returned can
// dereference-and-copy it ('t2 := *t' is legal, unsafe-free, reflect-free
// Go). mu is a plain sync.Mutex VALUE, so the copy gets its OWN,
// independent mu — but last (a map, a reference type) still ALIASES the
// original's, exactly Engine.hooks's/SubscriptionServer.subs's shape
// (SEC-003/SEC-019's class). Concurrent unsynchronised map access on
// last is a FATAL runtime crash (concurrent map read/write), not merely
// a race — the failure mode this type's SEC-020 entry names it for. self
// and checkNotCopied mirror SubscriptionServer's pattern exactly (see
// SubscriptionServer.self, engine/core/subscribe.go, and
// InProcTransport.self, transport.go, for the full pre-lock-ordering
// rationale, SEC-016): the pre-lock check is load-bearing (a copy taken
// while the original's mu is held captures mutex bytes reading "locked",
// so acquiring the copy's own mu can hang forever), and a second check
// immediately after acquiring mu is defence in depth.
type SeqTracker struct {
	mu   sync.Mutex
	last map[SubscriptionID]uint64

	// self holds the address NewSeqTracker gave this SeqTracker at
	// construction — see the type doc comment above and
	// InProcTransport.self (transport.go) for the full rationale. Set
	// exactly once, at the end of NewSeqTracker, before t is returned to
	// any caller.
	self atomic.Pointer[SeqTracker]
}

// NewSeqTracker returns an empty tracker.
func NewSeqTracker() *SeqTracker {
	t := &SeqTracker{last: make(map[SubscriptionID]uint64)}
	// Stored exactly once, here, before t is returned to any caller — no
	// goroutine can have a reference to t to race this Store against
	// (SEC-020 wave 1, mirroring NewInProcTransport/NewSubscriptionServer
	// — see the type doc comment above).
	t.self.Store(t)
	return t
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other SeqTracker value (SEC-020 wave 1, mirroring
// SubscriptionServer.checkNotCopied — engine/core/subscribe.go).
// Deliberately lock-free — a single atomic.Pointer.Load, requiring
// nothing else, not t.mu — so it is safe and correct to call BEFORE t.mu
// is ever touched, even for the pre-lock check (SEC-016: a copy's mu can
// be byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held). A nil t.self.Load() (a SeqTracker constructed
// as a bare SeqTracker{}/new(SeqTracker) rather than via NewSeqTracker,
// so self was never stored — and so last is also nil, which panics on
// write but merely returns the zero value on read) is treated the same
// as a mismatch and rejected the same way.
func (t *SeqTracker) checkNotCopied(correlationID string, ctx map[string]any) error {
	if t.self.Load() != t {
		return errs.New(ErrSeqTrackerCopied, correlationID, ctx)
	}
	return nil
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
//
// SEC-020 wave 1: identity-checked BEFORE t.mu is touched (pre-lock,
// load-bearing per SEC-016 — see checkNotCopied's doc comment) and again
// immediately after t.mu is acquired (defence in depth) — mirrors
// SubscriptionServer.Subscribe/Unsubscribe's ordering exactly. A copy
// reports (gap=0, ok=false) — the SAME shape Observe already uses for
// "duplicate or out-of-order, treat this as a bug" above, deliberately
// reused rather than inventing a third outcome: a copy IS exactly that
// kind of bug from this method's caller's point of view (it has never
// legitimately observed anything, so any seq it's asked about is, from
// its own state, indistinguishable from "unexpected"), and reusing the
// existing "something is wrong here" signal means callers that already
// handle ok=false correctly (surface staleness, do not treat the seq as
// accepted) handle a rejected copy correctly too, with no new branch to
// forget.
func (t *SeqTracker) Observe(sub SubscriptionID, seq uint64) (gap uint64, ok bool) {
	if err := t.checkNotCopied(errs.NewCorrelationID(), map[string]any{"subscriptionId": string(sub)}); err != nil {
		return 0, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.checkNotCopied(errs.NewCorrelationID(), map[string]any{"subscriptionId": string(sub)}); err != nil {
		return 0, false
	}

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
//
// SEC-020 wave 1: identity-checked BEFORE t.mu is touched (pre-lock,
// load-bearing) and again immediately after acquisition (defence in
// depth) — same ordering as Observe. Reset has no return value to carry
// a rejection through, so a copy's Reset silently no-ops: there is
// nothing to delete from a copy's aliased last map that the ORIGINAL's
// own Reset (called by whoever legitimately owns the real SeqTracker)
// would not already correctly handle, and forgetting an entry twice, or
// not at all on a value nobody should have been holding, has no
// observable effect on the original's correctness — unlike Observe,
// there is no "wrong answer" a rejected Reset could hand back.
func (t *SeqTracker) Reset(sub SubscriptionID) {
	if err := t.checkNotCopied(errs.NewCorrelationID(), map[string]any{"subscriptionId": string(sub)}); err != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.checkNotCopied(errs.NewCorrelationID(), map[string]any{"subscriptionId": string(sub)}); err != nil {
		return
	}
	delete(t.last, sub)
}
