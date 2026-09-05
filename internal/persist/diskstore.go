package persist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// ErrNotFound is returned when a requested snapshot does not exist.
var ErrNotFound = errors.New("persist: not found")

// ErrStoreCopied is returned when a DiskStore or MemStore value is used
// after being copied (which would copy its sync.Mutex in a possibly
// locked state and alias its internal maps). SEC-020: a store must
// always be used via the pointer returned by its constructor.
var ErrStoreCopied = errors.New("persist: store used after being copied")

// ErrGameModeEpochWithoutSeed is returned by SetGameModeEpoch when city
// has no durably recorded world seed yet (BUG-737 re-round-3 finding
// P2-1): SetGameModeEpoch requires SetWorldSeedIfAbsent to have already
// run for the SAME city. Without this guard, DiskStore's original
// implementation wrote a seedless epoch-only seed.json record with an
// explicit world_seed:0 (the field has no omitempty — the zero value is
// a genuine seed, not "absent"), which WorldSeed then read back as
// ok=true, seed=0 — bricking the city: a later, correct
// SetWorldSeedIfAbsent(realSeed) would see "already recorded" (seed 0)
// and refuse to ever record the real one (BUG-488's own first-write-wins
// contract, now poisoned). compose.go's Wire never triggers this in
// practice (it always calls SetWorldSeedIfAbsent before SetGameModeEpoch
// for the same city, in that order, every time PersistStore is set), but
// the Store contract itself must not permit constructing this state via
// any other call order — see Store.SetGameModeEpoch's own doc comment.
var ErrGameModeEpochWithoutSeed = errors.New("persist: SetGameModeEpoch called before any world seed recorded for city")

const (
	journalFileName   = "journal.dat"
	metaFileName      = "meta.json"
	seedFileName      = "seed.json"     // BUG-488: originating world-seed sidecar
	gameModeFileName  = "gamemode.json" // BUG-737: originating gameinit mode sidecar
	snapshotsDirName  = "snapshots"
	snapshotSuffix    = ".bin"
	snapshotTmpPrefix = ".tmp-"
	dirPerm           = 0o755
	filePerm          = 0o644
)

// citySeed is the sidecar BUG-488 writes once per city directory, the
// first time SetWorldSeedIfAbsent is ever called for it: the world seed
// the city's journal was ORIGINATING replayed under. Distinct from
// cityMeta (which only recovers the unhashed CityKey strings) so a
// pre-BUG-488 city directory with no seed.json is unambiguously "no seed
// ever recorded" rather than a zero-value seed -- WorldSeed's ok=false
// case relies on the file's mere absence, never a zero-value field.
type citySeed struct {
	WorldSeed uint64 `json:"world_seed"`

	// ModeEpoch (BUG-737 round-2 lead ruling, 2026-09-05) is true once
	// FEAT-143-aware code (compose.go's Wire) has processed this city's
	// game-mode record at least once — see Store.SetGameModeEpoch's own
	// doc comment for the full rationale. Deliberately stored in THIS
	// sidecar (seed.json), not gamemode.json: the scenario this field
	// exists to detect is gamemode.json itself going missing between
	// boots, so the marker must live somewhere that scenario does not
	// touch. omitempty keeps every pre-BUG-737 seed.json's on-disk shape
	// unchanged until a mode-aware Wire call actually runs against it.
	ModeEpoch bool `json:"mode_epoch,omitempty"`
}

// cityGameMode is the sidecar BUG-737 writes once per city directory, the
// first time SetGameModeIfAbsent is ever called for it: the FEAT-143
// gameinit mode ("real"/"unlimited", gameinit.Mode's own wire string —
// this package never imports internal/engine/gameinit, mirroring
// citySeed's own opaque-value discipline) the city's journal was
// ORIGINATING replayed under. Distinct from citySeed for the exact same
// reason citySeed is distinct from cityMeta: a pre-BUG-737 city directory
// with no gamemode.json is unambiguously "no mode ever recorded", never a
// guessed/defaulted value.
type cityGameMode struct {
	GameMode string `json:"game_mode"`
}

// cityMeta is the small sidecar written once per city directory so
// ListCities can recover the original (unhashed) CityKey strings —
// the on-disk directory names themselves are one-way hashes (see
// key.go) and are never decoded.
type cityMeta struct {
	TenantID string `json:"tenant_id"`
	CityID   string `json:"city_id"`
}

// DiskStore is a local-disk implementation of Store. It roots an
// entire multi-tenant, multi-city store at one directory:
//
//	root/{sha256(tenantID)}/{sha256(cityID)}/meta.json
//	root/{sha256(tenantID)}/{sha256(cityID)}/journal.dat
//	root/{sha256(tenantID)}/{sha256(cityID)}/snapshots/{seq}.bin
//
// which is the local-disk shape the acceptance doc's key/namespace
// scheme calls for — a directory tree today, and (per the epic's
// Phase 4) the same path shape as a blob-name prefix later, with no
// interface change.
type DiskStore struct {
	root string

	self atomic.Pointer[DiskStore] // SEC-020 copy guard

	mu    sync.Mutex // guards locks map only
	locks map[string]*sync.Mutex
}

var _ Store = (*DiskStore)(nil)

// NewDiskStore opens (creating if necessary) a local-disk Store rooted
// at root.
func NewDiskStore(root string) (*DiskStore, error) {
	if err := os.MkdirAll(root, dirPerm); err != nil {
		return nil, fmt.Errorf("persist: create store root: %w", err)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("persist: resolve store root: %w", err)
	}
	s := &DiskStore{root: abs, locks: make(map[string]*sync.Mutex)}
	s.self.Store(s)
	return s, nil
}

// checkNotCopied guards against a DiskStore value being copied after
// construction (which would copy its sync.Mutex in a locked state and
// alias the locks map). SEC-020: every mutating method calls this before
// taking any lock. A DiskStore must always be used via the *DiskStore
// returned by NewDiskStore.
func (s *DiskStore) checkNotCopied() error {
	if s.self.Load() != s {
		return ErrStoreCopied
	}
	return nil
}

// lockFor returns the per-city mutex for dir, creating it on first
// use. This is the "single-writer-per-city" enforcement documented in
// doc.go: concurrent AppendJournal/PutSnapshot calls for the SAME city
// are serialized so a torn interleaving of two writers' frames can
// never happen; concurrent calls for DIFFERENT cities never contend.
func (s *DiskStore) lockFor(dir string) *sync.Mutex {
	if err := s.checkNotCopied(); err != nil {
		// A copied store is a programming error; the guarded callers all
		// also call checkNotCopied and surface ErrStoreCopied. Panicking
		// here would violate the "return the error" contract, so degrade
		// safely: hand back a throwaway lock rather than aliasing the map.
		return &sync.Mutex{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.locks[dir]
	if !ok {
		l = &sync.Mutex{}
		s.locks[dir] = l
	}
	return l
}

func (s *DiskStore) cityDir(key CityKey) string {
	return filepath.Join(s.root, encodeSegment(key.TenantID), encodeSegment(key.CityID))
}

// GameModeSidecarPath returns the on-disk path of city's gamemode.json
// sidecar (BUG-737 P1-4's own round-required test scenario: "deleting
// gamemode.json between boots must likewise refuse, not re-mode"). This
// is a TEST-SUPPORT accessor only — no production caller needs the path
// itself, only the SetGameModeIfAbsent/GameMode accessors above — added
// because cityDir (and the hashed directory layout this package's own
// doc comment discloses) is unexported, and the restart-refusal scenario
// needs to delete the sidecar from cmd/metroserve's own test package,
// which has no other way to locate it without re-deriving the hashing
// scheme by hand.
func (s *DiskStore) GameModeSidecarPath(city CityKey) string {
	if err := s.checkNotCopied(); err != nil {
		return ""
	}
	return filepath.Join(s.cityDir(city), gameModeFileName)
}

// ensureCityMeta writes the city's meta.json sidecar the first time
// any data is durably written for it. The write is atomic
// (temp-then-rename) and idempotent — a concurrent or repeat call is
// harmless since the content is fixed for a given CityKey.
func (s *DiskStore) ensureCityMeta(dir string, key CityKey) error {
	metaPath := filepath.Join(dir, metaFileName)
	if _, err := os.Stat(metaPath); err == nil {
		return nil // already present
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("persist: stat city meta: %w", err)
	}
	data, err := json.Marshal(cityMeta(key))
	if err != nil {
		return fmt.Errorf("persist: encode city meta: %w", err)
	}
	return atomicWrite(dir, metaFileName, data)
}

// atomicWrite writes data to <dir>/<name> via write-to-temp-then-rename
// in the SAME directory (so the rename is on one filesystem and atomic),
// fsync'ing both the temp file and, on POSIX, the directory entry —
// the same write-then-atomic-rename discipline
// checkpoint.Manager.saveBundle uses for its lineage sidecar (per the
// acceptance doc's AC-5). A crash before the rename leaves only an
// orphaned temp file, never a half-written target; a crash after the
// rename leaves the fully-written target. There is no state in which a
// reader can observe a partially-written <name>.
func atomicWrite(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, name+snapshotTmpPrefix)
	if err != nil {
		return fmt.Errorf("persist: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	// On any error path below, best-effort clean up the orphaned temp
	// file so it never lingers where a lister could trip over it.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("persist: write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("persist: fsync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("persist: close temp file: %w", err)
	}
	target := filepath.Join(dir, name)
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("persist: rename into place: %w", err)
	}
	success = true

	// Best-effort directory-entry fsync so the rename itself survives
	// a crash on filesystems where that matters (POSIX). Not fatal if
	// unsupported (e.g. some platforms return an error opening a
	// directory for read) — the file content is already durable via
	// the temp file's own fsync above; this only hardens the rename's
	// visibility.
	if dh, derr := os.Open(dir); derr == nil {
		_ = dh.Sync()
		_ = dh.Close()
	}
	return nil
}

// AppendJournal implements Store.
func (s *DiskStore) AppendJournal(ctx context.Context, city CityKey, record []byte) error {
	if err := s.checkNotCopied(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := s.cityDir(city)

	lock := s.lockFor(dir)
	lock.Lock()
	defer lock.Unlock()

	// Meta creation and the append itself both happen under the
	// per-city lock: on Windows, two goroutines racing to
	// temp-write-then-rename the SAME meta.json concurrently can
	// return "Access is denied" from os.Rename (the target/temp file
	// is still open in the other goroutine) — serializing here avoids
	// that race entirely, consistent with this package's documented
	// single-writer-per-city model.
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("persist: create city dir: %w", err)
	}
	if err := s.ensureCityMeta(dir, city); err != nil {
		return err
	}

	path := filepath.Join(dir, journalFileName)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("persist: open journal for append: %w", err)
	}
	frame := encodeFrame(record)
	// A single Write of the fully-assembled frame is the unit AC-5's
	// torn-write tests reason about: only a crash truly mid-syscall
	// can leave a partial frame, which decodeFrames then recognises
	// and ignores on the next read.
	if _, err := f.Write(frame); err != nil {
		_ = f.Close()
		return fmt.Errorf("persist: write journal frame: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("persist: fsync journal: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("persist: close journal: %w", err)
	}
	return nil
}

// ReadJournal implements Store.
func (s *DiskStore) ReadJournal(ctx context.Context, city CityKey) ([][]byte, error) {
	if err := s.checkNotCopied(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir := s.cityDir(city)
	path := filepath.Join(dir, journalFileName)

	lock := s.lockFor(dir)
	lock.Lock()
	defer lock.Unlock()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return [][]byte{}, nil
		}
		return nil, fmt.Errorf("persist: open journal for read: %w", err)
	}
	defer func() { _ = f.Close() }()

	records, err := readAllFrames(f)
	if err != nil {
		return nil, fmt.Errorf("persist: read journal: %w", err)
	}
	return records, nil
}

// PutSnapshot implements Store.
func (s *DiskStore) PutSnapshot(ctx context.Context, city CityKey, snapshot []byte) (SnapshotID, error) {
	if err := s.checkNotCopied(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir := s.cityDir(city)

	lock := s.lockFor(dir)
	lock.Lock()
	defer lock.Unlock()

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", fmt.Errorf("persist: create city dir: %w", err)
	}
	if err := s.ensureCityMeta(dir, city); err != nil {
		return "", err
	}
	snapDir := filepath.Join(dir, snapshotsDirName)
	if err := os.MkdirAll(snapDir, dirPerm); err != nil {
		return "", fmt.Errorf("persist: create snapshots dir: %w", err)
	}

	existing, err := listCommittedSnapshotSeqs(snapDir)
	if err != nil {
		return "", err
	}
	next := int64(1)
	if len(existing) > 0 {
		next = existing[len(existing)-1] + 1
	}
	id := SnapshotID(formatSeq(next))

	if err := atomicWrite(snapDir, string(id)+snapshotSuffix, snapshot); err != nil {
		return "", fmt.Errorf("persist: write snapshot: %w", err)
	}
	return id, nil
}

// GetSnapshot implements Store.
func (s *DiskStore) GetSnapshot(ctx context.Context, city CityKey, id SnapshotID) ([]byte, error) {
	if err := s.checkNotCopied(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir := s.cityDir(city)
	snapDir := filepath.Join(dir, snapshotsDirName)

	lock := s.lockFor(dir)
	lock.Lock()
	defer lock.Unlock()

	path := filepath.Join(snapDir, string(id)+snapshotSuffix)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("persist: read snapshot: %w", err)
	}
	return data, nil
}

// DeleteSnapshot implements Store. Removing an id that does not exist (or
// never existed) is a no-op success — pruning is idempotent.
func (s *DiskStore) DeleteSnapshot(ctx context.Context, city CityKey, id SnapshotID) error {
	if err := s.checkNotCopied(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := s.cityDir(city)
	snapDir := filepath.Join(dir, snapshotsDirName)

	lock := s.lockFor(dir)
	lock.Lock()
	defer lock.Unlock()

	path := filepath.Join(snapDir, string(id)+snapshotSuffix)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("persist: delete snapshot: %w", err)
	}
	return nil
}

// ListSnapshots implements Store.
func (s *DiskStore) ListSnapshots(ctx context.Context, city CityKey) ([]SnapshotID, error) {
	if err := s.checkNotCopied(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir := s.cityDir(city)
	snapDir := filepath.Join(dir, snapshotsDirName)

	lock := s.lockFor(dir)
	lock.Lock()
	defer lock.Unlock()

	seqs, err := listCommittedSnapshotSeqs(snapDir)
	if err != nil {
		return nil, err
	}
	ids := make([]SnapshotID, 0, len(seqs))
	for _, seq := range seqs {
		ids = append(ids, SnapshotID(formatSeq(seq)))
	}
	return ids, nil
}

// listCommittedSnapshotSeqs returns the sequence numbers of every
// FULLY COMMITTED snapshot (the ".bin" file, never a lingering
// ".bin.tmp-*" orphan left by a crash between temp-write and rename)
// in ascending order — an explicit sort, never raw os.ReadDir order
// treated as meaningful (GR#21).
func listCommittedSnapshotSeqs(snapDir string) ([]int64, error) {
	entries, err := os.ReadDir(snapDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("persist: list snapshots: %w", err)
	}
	var seqs []int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, snapshotSuffix) {
			continue // excludes .tmp-* orphans and anything else
		}
		if strings.Contains(name, snapshotTmpPrefix) {
			continue
		}
		seqStr := strings.TrimSuffix(name, snapshotSuffix)
		seq, err := strconv.ParseInt(seqStr, 10, 64)
		if err != nil {
			continue // not one of ours; ignore rather than fail the whole list
		}
		seqs = append(seqs, seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs, nil
}

func formatSeq(seq int64) string {
	return fmt.Sprintf("%020d", seq)
}

// ListCities implements Store.
func (s *DiskStore) ListCities(ctx context.Context, tenant string) ([]CityKey, error) {
	if err := s.checkNotCopied(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tenantDir := filepath.Join(s.root, encodeSegment(tenant))
	entries, err := os.ReadDir(tenantDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []CityKey{}, nil
		}
		return nil, fmt.Errorf("persist: list tenant dir: %w", err)
	}

	var keys []CityKey
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(tenantDir, e.Name(), metaFileName)
		data, err := os.ReadFile(metaPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue // city dir exists but never completed a first write
			}
			return nil, fmt.Errorf("persist: read city meta: %w", err)
		}
		var m cityMeta
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("persist: decode city meta: %w", err)
		}
		if m.TenantID != tenant {
			// Defensive: a hash collision or a foreign write should
			// never surface under the wrong tenant's listing.
			continue
		}
		keys = append(keys, CityKey(m))
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].CityID < keys[j].CityID })
	return keys, nil
}

// Exists implements Store.
func (s *DiskStore) Exists(ctx context.Context, city CityKey) (bool, error) {
	if err := s.checkNotCopied(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	dir := s.cityDir(city)
	metaPath := filepath.Join(dir, metaFileName)
	if _, err := os.Stat(metaPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("persist: stat city meta: %w", err)
	}
	return true, nil
}

// SetWorldSeedIfAbsent implements Store (BUG-488). The seed.json sidecar is
// written via the same temp-then-rename atomicWrite discipline as meta.json
// and the FIRST-CALL-WINS semantics are enforced by an os.Stat check under
// the per-city lock (so a concurrent racer for the SAME city can never
// stamp a second, different seed — the single-writer-per-city model
// lockFor already documents).
func (s *DiskStore) SetWorldSeedIfAbsent(ctx context.Context, city CityKey, seed uint64) (uint64, error) {
	if err := s.checkNotCopied(); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	dir := s.cityDir(city)

	lock := s.lockFor(dir)
	lock.Lock()
	defer lock.Unlock()

	if existing, ok, err := s.readCitySeedLocked(dir); err != nil {
		return 0, err
	} else if ok {
		return existing, nil
	}

	// Deliberately does NOT call ensureCityMeta: stamping a seed must
	// never, by itself, make an otherwise-untouched city look like it
	// has durable data (Store.Exists/ListCities are keyed on meta.json
	// alone, written only by a real AppendJournal/PutSnapshot) — a city
	// that is only ever Wired with persistence enabled but never actually
	// journals a command or takes a snapshot must stay invisible to
	// Exists/ListCities, exactly as it did before BUG-488. Only the
	// directory itself is created, so the seed.json write below has
	// somewhere to land.
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return 0, fmt.Errorf("persist: create city dir: %w", err)
	}
	data, err := json.Marshal(citySeed{WorldSeed: seed})
	if err != nil {
		return 0, fmt.Errorf("persist: encode city seed: %w", err)
	}
	if err := atomicWrite(dir, seedFileName, data); err != nil {
		return 0, fmt.Errorf("persist: write city seed: %w", err)
	}
	return seed, nil
}

// WorldSeed implements Store (BUG-488).
func (s *DiskStore) WorldSeed(ctx context.Context, city CityKey) (uint64, bool, error) {
	if err := s.checkNotCopied(); err != nil {
		return 0, false, err
	}
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	dir := s.cityDir(city)

	lock := s.lockFor(dir)
	lock.Lock()
	defer lock.Unlock()

	return s.readCitySeedLocked(dir)
}

// readCitySeedLocked reads dir's seed.json sidecar, if any. Must be called
// with dir's per-city lock already held. A missing file is ok=false, not
// an error — the explicit backward-compatible default for a city with no
// seed ever recorded (BUG-488).
func (s *DiskStore) readCitySeedLocked(dir string) (uint64, bool, error) {
	cs, ok, err := s.readCitySeedStructLocked(dir)
	if err != nil || !ok {
		return 0, ok, err
	}
	return cs.WorldSeed, true, nil
}

// readCitySeedStructLocked reads dir's seed.json sidecar in FULL (the
// world seed AND the BUG-737 round-2 ModeEpoch flag), if present. Must
// be called with dir's per-city lock already held. A missing file is
// ok=false, not an error. Both readCitySeedLocked (WorldSeed's own
// narrower accessor) and SetGameModeEpoch/HasGameModeEpoch below share
// this single decode path (GR#3 — one reader, not two that could drift).
func (s *DiskStore) readCitySeedStructLocked(dir string) (citySeed, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, seedFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return citySeed{}, false, nil
		}
		return citySeed{}, false, fmt.Errorf("persist: read city seed: %w", err)
	}
	var cs citySeed
	if err := json.Unmarshal(data, &cs); err != nil {
		return citySeed{}, false, fmt.Errorf("persist: decode city seed: %w", err)
	}
	return cs, true, nil
}

// readCitySeedRawFieldsLocked reads dir's seed.json sidecar as a raw
// field map (map[string]json.RawMessage) rather than decoding into the
// closed citySeed struct (BUG-737 re-round-3 finding P2-2): this
// package's own citySeed struct only knows about "world_seed" and
// "mode_epoch", so decoding-then-re-encoding through it would SILENTLY
// DROP any other field a different writer (a future increment, an
// operator hand-edit, an Azure-side migration tool) ever added to this
// sidecar — e.g. a hypothetical "lineage_id" or "created_by" field
// would decode fine into citySeed (json.Unmarshal ignores unknown
// fields) but then vanish the next time this file is re-marshalled from
// that same citySeed value. Reading into a raw field map and writing
// the SAME map back (with only the one key this call cares about
// changed) preserves every other field's JSON bytes byte-for-byte,
// because json.RawMessage never decodes/re-encodes the value at all —
// only SetGameModeEpoch (the one call site that re-writes an
// ALREADY-EXISTING file) uses this; SetWorldSeedIfAbsent's own write
// path only ever fires on a genuinely absent file, so there is nothing
// to preserve there. A missing file returns an empty, non-nil map
// (never an error) so a caller can merge into it uniformly whether or
// not the file already existed.
func (s *DiskStore) readCitySeedRawFieldsLocked(dir string) (map[string]json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	data, err := os.ReadFile(filepath.Join(dir, seedFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return fields, nil
		}
		return nil, fmt.Errorf("persist: read city seed: %w", err)
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("persist: decode city seed: %w", err)
	}
	return fields, nil
}

// SetGameModeEpoch implements Store (BUG-737 round-2 lead ruling,
// 2026-09-05; field-preservation + seedless-city guard added round-3,
// findings P2-1/P2-2). Idempotent: a no-op once city's seed.json already
// carries mode_epoch=true. Merges the "mode_epoch" key into seed.json's
// RAW field set (readCitySeedRawFieldsLocked) rather than round-tripping
// through the closed citySeed struct, so any field this package does not
// itself know about survives byte-for-byte (P2-2).
//
// Requires city to already have a durably recorded world seed
// (ErrGameModeEpochWithoutSeed otherwise, P2-1): a seedless epoch record
// would otherwise need an explicit world_seed:0 written into the SAME
// file (citySeed.WorldSeed has no omitempty, since 0 is itself a
// legitimate real seed value, not "absent") — WorldSeed would then
// wrongly report ok=true, seed=0, and BUG-488's own SetWorldSeedIfAbsent
// first-write-wins contract would permanently refuse to ever record the
// city's REAL seed afterward. compose.go's Wire never reaches this
// path in practice (SetWorldSeedIfAbsent always runs, for the same
// city, before SetGameModeEpoch every time PersistStore is set), but the
// Store contract itself must reject this ordering from any other caller
// rather than silently brick the city.
func (s *DiskStore) SetGameModeEpoch(ctx context.Context, city CityKey) error {
	if err := s.checkNotCopied(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := s.cityDir(city)

	lock := s.lockFor(dir)
	lock.Lock()
	defer lock.Unlock()

	fields, err := s.readCitySeedRawFieldsLocked(dir)
	if err != nil {
		return err
	}

	// P2-1: refuse without a recorded world seed. "world_seed" absent
	// from the raw field set means exactly that — never present at all,
	// whether the file itself is missing (fields is the empty map from
	// readCitySeedRawFieldsLocked) or present but seedless (should not
	// happen via this package's own writers, but the raw-field check
	// covers it regardless of cause).
	if _, hasSeed := fields["world_seed"]; !hasSeed {
		return ErrGameModeEpochWithoutSeed
	}

	// Idempotent no-op if already marked true.
	if raw, ok := fields["mode_epoch"]; ok {
		var already bool
		if err := json.Unmarshal(raw, &already); err == nil && already {
			return nil
		}
	}
	encodedTrue, err := json.Marshal(true)
	if err != nil {
		return fmt.Errorf("persist: encode mode_epoch: %w", err)
	}
	fields["mode_epoch"] = encodedTrue

	// Mirrors SetWorldSeedIfAbsent/SetGameModeIfAbsent's own discipline:
	// deliberately does NOT call ensureCityMeta.
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("persist: create city dir: %w", err)
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("persist: encode city seed: %w", err)
	}
	if err := atomicWrite(dir, seedFileName, data); err != nil {
		return fmt.Errorf("persist: write city seed: %w", err)
	}
	return nil
}

// HasGameModeEpoch implements Store (BUG-737 round-2 lead ruling,
// 2026-09-05).
func (s *DiskStore) HasGameModeEpoch(ctx context.Context, city CityKey) (bool, error) {
	if err := s.checkNotCopied(); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	dir := s.cityDir(city)

	lock := s.lockFor(dir)
	lock.Lock()
	defer lock.Unlock()

	cs, ok, err := s.readCitySeedStructLocked(dir)
	if err != nil || !ok {
		return false, err
	}
	return cs.ModeEpoch, nil
}

// SetGameModeIfAbsent implements Store (BUG-737, mirroring
// SetWorldSeedIfAbsent above exactly): the gamemode.json sidecar is
// written via the same temp-then-rename atomicWrite discipline and the
// FIRST-CALL-WINS semantics are enforced by a file-read check under the
// per-city lock, so a concurrent racer for the SAME city can never stamp
// a second, different mode.
func (s *DiskStore) SetGameModeIfAbsent(ctx context.Context, city CityKey, mode string) (string, error) {
	if err := s.checkNotCopied(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir := s.cityDir(city)

	lock := s.lockFor(dir)
	lock.Lock()
	defer lock.Unlock()

	if existing, ok, err := s.readCityGameModeLocked(dir); err != nil {
		return "", err
	} else if ok {
		return existing, nil
	}

	// BUG-737 P1-2 (round finding): mode == "" is "no opinion", never a
	// genuine mode -- an empty string must NEVER be durably stamped.
	// Every pre-fix caller that never had a real chooser (or omitted the
	// trailing variadic) passed "" here, and without this guard it landed
	// in gamemode.json permanently: readCityGameModeLocked's OWN "" ==
	// absent treatment (below) means a later call with a genuine mode
	// would find "nothing recorded" every time on the READ side, but
	// this WRITE side had already created the sidecar file, so a
	// concurrent/later caller passing "" again would keep winning the
	// first-write-wins race against one that finally has a real mode --
	// worse, ANY write here at all (even one this function's own guard
	// now skips) would need readCityGameModeLocked to treat a
	// stored "" as absent too for the read side to ever unblock a later
	// real stamp, which is why that guard exists in BOTH places. Return
	// without creating the sidecar/dir at all, so a later real chooser
	// can still win.
	if mode == "" {
		return "", nil
	}

	// Mirrors SetWorldSeedIfAbsent: deliberately does NOT call
	// ensureCityMeta -- stamping a mode must never, by itself, make an
	// otherwise-untouched city look like it has durable data.
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", fmt.Errorf("persist: create city dir: %w", err)
	}
	data, err := json.Marshal(cityGameMode{GameMode: mode})
	if err != nil {
		return "", fmt.Errorf("persist: encode city game mode: %w", err)
	}
	if err := atomicWrite(dir, gameModeFileName, data); err != nil {
		return "", fmt.Errorf("persist: write city game mode: %w", err)
	}
	return mode, nil
}

// GameMode implements Store (BUG-737).
func (s *DiskStore) GameMode(ctx context.Context, city CityKey) (string, bool, error) {
	if err := s.checkNotCopied(); err != nil {
		return "", false, err
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	dir := s.cityDir(city)

	lock := s.lockFor(dir)
	lock.Lock()
	defer lock.Unlock()

	return s.readCityGameModeLocked(dir)
}

// readCityGameModeLocked reads dir's gamemode.json sidecar, if any. Must
// be called with dir's per-city lock already held. A missing file is
// ok=false, not an error -- the explicit backward-compatible default for
// a city with no mode ever recorded (BUG-737, mirrors
// readCitySeedLocked).
func (s *DiskStore) readCityGameModeLocked(dir string) (string, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, gameModeFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("persist: read city game mode: %w", err)
	}
	var cm cityGameMode
	if err := json.Unmarshal(data, &cm); err != nil {
		return "", false, fmt.Errorf("persist: decode city game mode: %w", err)
	}
	// BUG-737 P1-2 (round finding): a stored EMPTY mode is treated
	// identically to "never recorded" -- defensive-in-depth alongside
	// SetGameModeIfAbsent's own write-side "" guard above, in case a
	// sidecar ever ends up holding "" by some other path (a future
	// caller, a hand-edited file, an older un-guarded write). ok=false
	// here is what lets a later call with a genuine mode still win.
	if cm.GameMode == "" {
		return "", false, nil
	}
	return cm.GameMode, true, nil
}
