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

	// SetGameModeIfAbsent durably records mode as the city's ORIGINATING
	// FEAT-143 gameinit mode ("real"/"unlimited") the first time this is
	// ever called for city, and is a no-op on every later call — mirrors
	// SetWorldSeedIfAbsent exactly (BUG-737: a session's mode must be
	// immutable across a metroserve restart, AC-3, and the persist layer
	// has the identical "never recorded" vs "already recorded" problem
	// SetWorldSeedIfAbsent solved for the world seed). Returns the mode
	// now durably on record for city — either the one just written
	// (first call) or the pre-existing one (every later call, regardless
	// of what mode was passed this time). This package never validates
	// mode against gameinit.Mode's two known values (no import of
	// internal/engine/gameinit — mirrors the plain-string discipline
	// compose.Deps.GameMode itself uses); the composition root is the
	// one place that enforces the enum.
	SetGameModeIfAbsent(ctx context.Context, city CityKey, mode string) (recorded string, err error)

	// GameMode returns the durably-recorded originating gameinit mode for
	// city, and ok=true iff one has ever been recorded via
	// SetGameModeIfAbsent. ok=false covers TWO CASES that a caller MUST
	// tell apart via HasGameModeEpoch below before deciding what to do
	// (BUG-737 round-2 lead ruling, 2026-09-05 — the migration-story
	// finding that the original P1-4 design got wrong): a city with no
	// durable data at all (ok=false, no world seed either — genuinely
	// fresh, safe to establish genesis), and a city whose durable data
	// PREDATES BUG-737's fix or had its gamemode.json sidecar go missing
	// between boots. GameMode ok=false is NEVER, by itself, sufficient
	// to distinguish "safe to silently establish" from "must warn-and-
	// migrate" from "must refuse" — see HasGameModeEpoch's own doc
	// comment for the disambiguation this package now requires callers
	// to perform.
	GameMode(ctx context.Context, city CityKey) (mode string, ok bool, err error)

	// SetGameModeEpoch durably marks, for city, that FEAT-143-aware
	// composition code (compose.go's Wire) has processed its game-mode
	// record at least once (BUG-737 round-2 lead ruling, 2026-09-05).
	// Idempotent: a no-op once already marked for city. Stored
	// INDEPENDENTLY of gamemode.json specifically because the scenario
	// this marker exists to detect is gamemode.json itself going
	// missing between boots (an accidental delete, a lost write, disk
	// corruption) — a marker that lived inside the same file it is
	// meant to outlive would be useless. The concrete implementations
	// store it in the SAME sidecar SetWorldSeedIfAbsent already
	// maintains (seed.json's own "mode_epoch" field, DiskStore) or an
	// equivalent per-city flag (MemStore) — see each implementation's
	// own doc comment; callers must not assume a specific storage
	// location, only that it outlives gamemode.json's deletion and
	// never outlives the city's own world-seed record.
	//
	// REQUIRES a durably recorded world seed (SetWorldSeedIfAbsent must
	// already have been called for the SAME city) — returns
	// ErrGameModeEpochWithoutSeed otherwise (BUG-737 re-round-3 finding
	// P2-1, replacing this method's original "does not error if called
	// first" contract). That original contract let DiskStore's first
	// implementation write a seedless epoch-only seed.json record with
	// an explicit world_seed:0 (the field has no omitempty — 0 is a
	// genuine seed value, not "absent"), which WorldSeed then read back
	// as ok=true, seed=0, permanently bricking the city: a later, real
	// SetWorldSeedIfAbsent(realSeed) would see "already recorded" and
	// refuse to ever stamp the true seed. compose.go's Wire never
	// triggers this refusal in practice — it always calls
	// SetWorldSeedIfAbsent before SetGameModeEpoch, for the same city,
	// every single time PersistStore is set — but the contract itself
	// must reject the ordering from any other caller rather than permit
	// constructing a bricked city.
	SetGameModeEpoch(ctx context.Context, city CityKey) error

	// HasGameModeEpoch reports whether SetGameModeEpoch has ever been
	// called for city (BUG-737 round-2 lead ruling, 2026-09-05). This is
	// the disambiguator GameMode's own doc comment requires callers to
	// consult whenever GameMode reports ok=false for a city that DOES
	// have a durably recorded WorldSeed (i.e. it has genuinely been
	// Wired before, just not under mode-aware code):
	//
	//   - HasGameModeEpoch == false: this city has NEVER been processed
	//     by FEAT-143-aware code — a genuinely pre-BUG-737 legacy city
	//     (seed.json present from an old boot, gamemode.json/the epoch
	//     marker never written because the concept did not exist yet).
	//     The lead ruling (round-2, replacing the original P1-4 design's
	//     blanket refusal): STAMP the requested mode ONCE, raise
	//     gameinit.ErrLegacyGameModeStamped (a WARN-severity registry
	//     event, never silent, never fatal) naming the city and the
	//     mode being stamped, and mark the epoch so every LATER boot is
	//     governed by GameMode's normal match/mismatch rule (AC-3 holds
	//     from the second boot onward).
	//   - HasGameModeEpoch == true: this city HAS been processed by
	//     FEAT-143-aware code before (a prior boot already stamped a
	//     mode AND marked the epoch), yet GameMode now reports ok=false
	//     — the gamemode.json sidecar itself went missing BETWEEN boots
	//     on an already-migrated city. This is fail-closed: REFUSE
	//     (save.ErrGameModeMismatch, naming the missing mode), never
	//     silently re-stamp whatever mode this boot happens to request.
	HasGameModeEpoch(ctx context.Context, city CityKey) (bool, error)
}
