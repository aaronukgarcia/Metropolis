package save

import "sync"

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
}

// NewManager constructs a *Manager rooted at root, saving/loading the
// given participants. correlationID is attached to every registry-
// sourced error this Manager constructs (GR#1).
func NewManager(root string, participants []Participant, correlationID string) *Manager {
	return &Manager{
		root:          root,
		participants:  participants,
		correlationID: correlationID,
	}
}

// SetMaxDecodedBytes overrides the per-shard decompressed-bytes budget
// (SEC-038) Load/LoadLatest enforce. Pass 0 to mean "no limit" (the
// default).
func (m *Manager) SetMaxDecodedBytes(n int64) {
	m.maxDecodedBytes = n
}

// Root returns this Manager's save root directory.
func (m *Manager) Root() string {
	return m.root
}
