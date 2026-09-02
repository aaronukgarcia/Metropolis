package persist

import "context"

// SnapshotID names one snapshot within one city's snapshot history.
// It is opaque to callers (do not parse it) but is guaranteed stable
// and comparable, and ListSnapshots returns IDs in a deterministic
// (ascending) order.
type SnapshotID string

// Store is the durable-persistence abstraction Phase 1 introduces to
// close the Recorder/checkpoint durability gap. It is shaped so both a
// local-disk implementation (this package, today) and a future Azure
// Blob implementation (Phase 4, per the epic's §5) satisfy it
// unchanged — no disk-specific or Azure-specific type appears in this
// interface, and every payload is an opaque []byte the Store never
// interprets.
//
// Every method takes a context.Context so a future network-backed
// implementation (Azure Blob) can honor cancellation/timeouts; the
// local-disk implementation treats ctx as advisory (checked at entry,
// not threaded through individual syscalls, since local disk I/O in
// this package is not expected to block indefinitely).
type Store interface {
	// AppendJournal durably appends one opaque journal record for the
	// given city, in arrival order. It must not return success until
	// the record is durable (see the local-disk implementation's
	// fsync-then-append discipline) — a caller that has received a nil
	// error may rely on the record surviving a crash.
	AppendJournal(ctx context.Context, city CityKey, record []byte) error

	// ReadJournal returns every journal record durably appended for
	// the given city, in append order. A torn/partial record left by
	// a crash mid-append (see AC-5 in the acceptance doc) is silently
	// excluded — ReadJournal never returns a corrupt or truncated
	// record, only complete ones.
	ReadJournal(ctx context.Context, city CityKey) ([][]byte, error)

	// PutSnapshot durably writes one opaque snapshot payload for the
	// given city and returns the SnapshotID it was assigned. Writes
	// are atomic: a reader (GetSnapshot/ListSnapshots) never observes
	// a half-written snapshot — it is either fully absent or fully
	// present.
	PutSnapshot(ctx context.Context, city CityKey, snapshot []byte) (SnapshotID, error)

	// GetSnapshot returns the payload previously stored under id for
	// the given city.
	GetSnapshot(ctx context.Context, city CityKey, id SnapshotID) ([]byte, error)

	// ListSnapshots returns every snapshot ID durably committed for
	// the given city, oldest first (deterministic order — never
	// filesystem/map iteration order).
	ListSnapshots(ctx context.Context, city CityKey) ([]SnapshotID, error)

	// DeleteSnapshot durably removes one previously committed snapshot
	// for the given city (FEAT-1972079936 Phase 1 inc3 — bounded
	// snapshot retention, mirroring internal/engine/checkpoint's
	// MaxRetainedForks pruning pattern; the journal itself is NEVER
	// pruned by this — see the epic's inc3 ruling that a snapshot is a
	// restore-speed optimization, not a journal replacement). Deleting
	// an id that does not exist (already pruned, or never existed) is
	// NOT an error — pruning is idempotent and safe to retry.
	DeleteSnapshot(ctx context.Context, city CityKey, id SnapshotID) error

	// ListCities returns every CityKey with at least one durably
	// committed journal record or snapshot under the given tenant,
	// sorted by CityID (deterministic order).
	ListCities(ctx context.Context, tenant string) ([]CityKey, error)

	// Exists reports whether the given city has any durably committed
	// data (journal or snapshot) in the store.
	Exists(ctx context.Context, city CityKey) (bool, error)

	// SetWorldSeedIfAbsent durably records seed as the city's ORIGINATING
	// world seed the first time this is ever called for city, and is a
	// no-op on every later call (BUG-488: the persist layer has no
	// concept of "the seed changed", only "the seed was never recorded
	// yet" -- a genesis journal carries no bundle header the way a save
	// bundle does, so this sidecar record is what lets a later restore
	// verify it is replaying the journal it thinks it is). Returns the
	// seed now durably on record for city -- either the one just written
	// (first call) or the pre-existing one (every later call, regardless
	// of what seed was passed this time), so a caller can always tell
	// which actually won.
	SetWorldSeedIfAbsent(ctx context.Context, city CityKey, seed uint64) (recorded uint64, err error)

	// WorldSeed returns the durably-recorded originating world seed for
	// city, and ok=true iff one has ever been recorded via
	// SetWorldSeedIfAbsent. ok=false covers two cases a caller must
	// treat identically (no durable seed to check against): a city with
	// no durable data at all, and a city whose durable data predates
	// BUG-488's fix (an old journal/snapshot with no seed ever stamped) --
	// the explicit backward-compatible default is "no check possible",
	// never a guessed value.
	WorldSeed(ctx context.Context, city CityKey) (seed uint64, ok bool, err error)
}
