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

const (
	journalFileName   = "journal.dat"
	metaFileName      = "meta.json"
	snapshotsDirName  = "snapshots"
	snapshotSuffix    = ".bin"
	snapshotTmpPrefix = ".tmp-"
	dirPerm           = 0o755
	filePerm          = 0o644
)

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
