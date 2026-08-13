package save

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Manager orchestrates SaveManual/Autosave/Milestone against one save
// root directory and one registered [Participant] list. The zero value
// is not usable — construct via NewManager.
//
// A *Manager is safe for concurrent use: mu enforces AC-11's
// single-save-in-flight guard (a second SaveManual/Autosave/Milestone
// call arriving while one is already running on this Manager is
// rejected with ErrSaveInProgress rather than queued or allowed to
// interleave shard writes — see writeBundle's TryLock). List/Load do
// NOT take mu: they are protected instead by AC-9's atomic-promotion
// design (AC-16) — a reader only ever observes a fully-promoted bundle
// or none at all, so it never needs to coordinate with an in-flight
// writer.
type Manager struct {
	mu            sync.Mutex
	root          string
	participants  []Participant
	correlationID string

	// maxDecodedBytes bounds ReadShard's decompressed-bytes budget
	// (SEC-038, mirrored from int.serializer) for every Load this
	// Manager performs. Zero means "no limit" — the zero value is
	// usable as-is for callers that don't need the bound (this
	// package's own tests use small fixtures).
	maxDecodedBytes int64

	// self holds the address NewManager gave this Manager at
	// construction, so checkNotCopied can detect a later struct copy
	// (`m2 := *m`) and reject calls on it (SEC-020-class, surfaced by
	// astgate's live-tree scan flagging writeBundleLocked as an
	// unguarded mutex-hazard candidate — Manager has both mu
	// sync.Mutex and an aliasable participants slice). Mirrors
	// Engine.self/Registry.self/Store.self/Screen.self exactly (GR#3
	// — don't invent a new pattern).
	//
	// atomic.Pointer, not a plain *Manager field, for the SEC-016
	// pre-lock ordering guarantee: checkNotCopied must be safely
	// readable from a goroutine that has NOT taken mu (indeed, must be
	// called BEFORE mu is ever touched, since a copy's own mu can be
	// "currently locked" if the copy was taken mid-lock on the
	// original — acquiring it would then hang forever). A plain
	// pointer field read/written without synchronisation is a data
	// race under Go's memory model; atomic.Pointer's Load/Store do.
	self atomic.Pointer[Manager]
}

// NewManager constructs a *Manager rooted at root, saving/loading the
// given participants. correlationID is attached to every registry-
// sourced error this Manager constructs (GR#1).
func NewManager(root string, participants []Participant, correlationID string) *Manager {
	m := &Manager{
		root:          root,
		participants:  participants,
		correlationID: correlationID,
	}
	m.self.Store(m)
	return m
}

// checkNotCopied reports whether the receiver is a struct copy of some
// other Manager value (SEC-020-class, mirroring Engine.checkNotCopied/
// Registry.checkNotCopied/Store.checkNotCopied/Screen.checkNotCopied —
// same mechanism, same ordering rationale, restated here because each
// guarded type owns its own check, GR#3 does not apply across package
// boundaries with no shared import path).
//
// Deliberately lock-free — a single atomic.Pointer.Load, requiring
// nothing else, not m.mu — so it is safe and correct to call BEFORE
// m.mu is ever touched.
//
// Why this matters for Manager specifically: mu is a sync.Mutex VALUE
// (a copy gets its own, independent lock) while participants is a
// reference type a copy ALIASES. An unrejected copy is therefore a
// second lock domain that can read the SAME backing participants slice
// as the original under the mistaken belief that holding its own mu is
// exclusive access — exactly SEC-020's "two locks, one referent" shape.
//
// Pre-lock ordering is non-negotiable (SEC-016): a struct copy's mu can
// be byte-for-byte "currently locked" if the copy was taken while the
// original's mu was held (`m2 := *m` while another goroutine has
// m.mu.Lock()'d) — acquiring, or even attempting to acquire, a copy's
// own mu in that state can block forever, since nothing will ever
// Unlock() that specific copy's address. A guard placed AFTER the lock
// can never run for that attack, because the attack IS acquiring the
// lock; rejecting the copy here, before TryLock()/Lock() is ever
// called, means that hang path is never reached at all.
//
// A nil m.self.Load() (a Manager constructed as a bare `Manager{}` or
// `new(Manager)` rather than via NewManager, so self was never stored)
// is treated the same as a mismatch and rejected the same way — every
// documented construction path is NewManager, so an unset self is
// itself a misuse this same error correctly names.
func (m *Manager) checkNotCopied(ctx map[string]any) error {
	if m.self.Load() != m {
		return errs.New(ErrManagerCopied, m.correlationID, ctx)
	}
	return nil
}

// SetMaxDecodedBytes overrides the per-shard decompressed-bytes budget
// (SEC-038) Load/LoadLatest enforce. Pass 0 to mean "no limit" (the
// default).
func (m *Manager) SetMaxDecodedBytes(n int64) {
	// SEC-020-class: identity check before touching any field — see
	// checkNotCopied's doc comment. Mirrors Registry.SetToggleHook's
	// silent-no-op-on-copy shape for a void setter.
	if err := m.checkNotCopied(map[string]any{"method": "SetMaxDecodedBytes"}); err != nil {
		return
	}
	m.maxDecodedBytes = n
}

// Root returns this Manager's save root directory.
func (m *Manager) Root() string {
	if err := m.checkNotCopied(map[string]any{"method": "Root"}); err != nil {
		return ""
	}
	return m.root
}
