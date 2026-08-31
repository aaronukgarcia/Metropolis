package persist

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCityKeyTraversalCannotEscapeRoot proves the encodeSegment
// hashing defense: a hostile CityID/TenantID (path traversal, absolute
// path, path separators) can never cause the disk store to read or
// write outside its root, and never collides with a different city's
// data.
func TestCityKeyTraversalCannotEscapeRoot(t *testing.T) {
	root := t.TempDir()
	s, err := NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	ctx := context.Background()

	hostile := CityKey{TenantID: "../../../../etc", CityID: "../../../passwd"}
	benign := CityKey{TenantID: "tenant-benign", CityID: "city-benign"}

	if err := s.AppendJournal(ctx, hostile, []byte("hostile-record")); err != nil {
		t.Fatalf("AppendJournal(hostile): %v", err)
	}
	if err := s.AppendJournal(ctx, benign, []byte("benign-record")); err != nil {
		t.Fatalf("AppendJournal(benign): %v", err)
	}

	// Every file the store created must resolve to a path strictly
	// inside root.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("filepath.Abs(root): %v", err)
	}
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(absRoot, absPath)
		if err != nil {
			return err
		}
		if strings.HasPrefix(rel, "..") {
			t.Errorf("file escaped store root: %s (rel %s)", path, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	// The hostile write must not have clobbered or leaked into the
	// benign city's journal.
	benignRecords, err := s.ReadJournal(ctx, benign)
	if err != nil {
		t.Fatalf("ReadJournal(benign): %v", err)
	}
	if len(benignRecords) != 1 || string(benignRecords[0]) != "benign-record" {
		t.Fatalf("benign journal corrupted: %v", benignRecords)
	}

	hostileRecords, err := s.ReadJournal(ctx, hostile)
	if err != nil {
		t.Fatalf("ReadJournal(hostile): %v", err)
	}
	if len(hostileRecords) != 1 || string(hostileRecords[0]) != "hostile-record" {
		t.Fatalf("hostile journal not isolated correctly: %v", hostileRecords)
	}
}

// TestJournalTornWriteIsIgnoredNotSurfacedAsCorrupt is the AC-5
// fault-injection proof: N complete records plus one record torn at a
// byte offset strictly inside its serialized payload (never
// conveniently at a frame boundary) must read back as exactly N
// records — the torn tail silently excluded, never returned as a
// corrupt-but-present record.
func TestJournalTornWriteIsIgnoredNotSurfacedAsCorrupt(t *testing.T) {
	root := t.TempDir()
	s, err := NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	ctx := context.Background()
	city := CityKey{TenantID: "t", CityID: "torn"}

	const n = 5
	for i := 0; i < n; i++ {
		if err := s.AppendJournal(ctx, city, []byte(strings.Repeat("x", 20+i))); err != nil {
			t.Fatalf("AppendJournal %d: %v", i, err)
		}
	}

	// Simulate a crash mid-append: hand-craft one more frame and write
	// only a PREFIX of it that ends strictly inside the payload bytes
	// (not at the frame's start, not at its end) directly to the
	// journal file, bypassing the Store API entirely — this is what a
	// torn O_APPEND write left behind by a killed process looks like.
	fullFrame := encodeFrame([]byte("this record never finishes writing"))
	tornPoint := frameLenSize + 10 // strictly inside the payload
	if tornPoint <= frameLenSize || tornPoint >= len(fullFrame) {
		t.Fatalf("test bug: torn point %d not strictly inside payload of frame len %d", tornPoint, len(fullFrame))
	}
	journalPath := filepath.Join(root, encodeSegment(city.TenantID), encodeSegment(city.CityID), journalFileName)
	f, err := os.OpenFile(journalPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open journal directly: %v", err)
	}
	if _, err := f.Write(fullFrame[:tornPoint]); err != nil {
		t.Fatalf("write torn frame: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen (fresh instance, simulating a process restart after the
	// crash) and read.
	fresh, err := NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore (reopen): %v", err)
	}
	got, err := fresh.ReadJournal(ctx, city)
	if err != nil {
		t.Fatalf("ReadJournal after torn write: %v", err)
	}
	if len(got) != n {
		t.Fatalf("record count after torn write = %d, want exactly %d (torn tail must be ignored, not surfaced)", len(got), n)
	}
	for i := 0; i < n; i++ {
		if len(got[i]) != 20+i {
			t.Fatalf("record %d length = %d, want %d — a complete prior record was corrupted", i, len(got[i]), 20+i)
		}
	}
}

// TestSnapshotOrphanedTempFileNeverAppearsCommitted simulates a crash
// between PutSnapshot's temp-file write and its atomic rename: the
// orphaned temp file must never be listed or readable as a committed
// snapshot, and prior committed snapshots must be unaffected.
func TestSnapshotOrphanedTempFileNeverAppearsCommitted(t *testing.T) {
	root := t.TempDir()
	s, err := NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	ctx := context.Background()
	city := CityKey{TenantID: "t", CityID: "snap-crash"}

	id1, err := s.PutSnapshot(ctx, city, []byte("committed-snapshot"))
	if err != nil {
		t.Fatalf("PutSnapshot: %v", err)
	}

	// Hand-craft an orphaned temp file the way atomicWrite's os.CreateTemp
	// would, but never rename it into place — simulating the crash
	// window between "temp file fsync'd" and "rename completed".
	snapDir := filepath.Join(root, encodeSegment(city.TenantID), encodeSegment(city.CityID), snapshotsDirName)
	orphan := filepath.Join(snapDir, formatSeq(2)+snapshotSuffix+snapshotTmpPrefix+"deadbeef")
	if err := os.WriteFile(orphan, []byte("never-committed"), 0o644); err != nil {
		t.Fatalf("write orphan temp file: %v", err)
	}

	ids, err := s.ListSnapshots(ctx, city)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(ids) != 1 || ids[0] != id1 {
		t.Fatalf("ListSnapshots = %v, want exactly [%s] (orphan temp file must not appear)", ids, id1)
	}

	data, err := s.GetSnapshot(ctx, city, id1)
	if err != nil {
		t.Fatalf("GetSnapshot(committed): %v", err)
	}
	if string(data) != "committed-snapshot" {
		t.Fatalf("committed snapshot content = %q, want committed-snapshot", data)
	}

	if _, err := s.GetSnapshot(ctx, city, SnapshotID(formatSeq(2))); err != ErrNotFound {
		t.Fatalf("GetSnapshot(orphaned seq) err = %v, want ErrNotFound", err)
	}
}

// TestCrossProcessRehydrateByteIdentical is AC-6's proof: a store
// written by one Store instance (standing in for one process) reads
// back byte-identical from a brand-new Store instance opened on the
// same directory (standing in for a freshly started process that
// never held this city in memory) — the structural basis Phase 2's
// failover-by-replay cites.
func TestCrossProcessRehydrateByteIdentical(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	city := CityKey{TenantID: "tenant-cross", CityID: "city-cross"}

	// "Process A".
	procA, err := NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore (A): %v", err)
	}
	journalRecords := [][]byte{
		[]byte("cmd-1"), []byte("cmd-2"), []byte("cmd-3"),
	}
	for _, rec := range journalRecords {
		if err := procA.AppendJournal(ctx, city, rec); err != nil {
			t.Fatalf("AppendJournal (A): %v", err)
		}
	}
	snapID, err := procA.PutSnapshot(ctx, city, []byte("snapshot-payload"))
	if err != nil {
		t.Fatalf("PutSnapshot (A): %v", err)
	}
	// procA is now discarded with no explicit shutdown — "process A
	// exits" — matching AC-6's check exactly.
	procA = nil
	_ = procA

	// "Process B": a Store instance that has never touched this
	// CityKey in memory, opened fresh on the same root.
	procB, err := NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore (B): %v", err)
	}

	gotJournal, err := procB.ReadJournal(ctx, city)
	if err != nil {
		t.Fatalf("ReadJournal (B): %v", err)
	}
	if len(gotJournal) != len(journalRecords) {
		t.Fatalf("B saw %d journal records, want %d", len(gotJournal), len(journalRecords))
	}
	for i := range journalRecords {
		if !bytes.Equal(gotJournal[i], journalRecords[i]) {
			t.Fatalf("B record[%d] = %q, want %q", i, gotJournal[i], journalRecords[i])
		}
	}

	gotSnap, err := procB.GetSnapshot(ctx, city, snapID)
	if err != nil {
		t.Fatalf("GetSnapshot (B): %v", err)
	}
	if string(gotSnap) != "snapshot-payload" {
		t.Fatalf("B snapshot = %q, want snapshot-payload", gotSnap)
	}

	keys, err := procB.ListCities(ctx, city.TenantID)
	if err != nil {
		t.Fatalf("ListCities (B): %v", err)
	}
	if len(keys) != 1 || keys[0] != city {
		t.Fatalf("ListCities (B) = %v, want [%v]", keys, city)
	}
}

// TestNewDiskStoreRejectsMissingRootCreation is a small hygiene check
// that store construction itself surfaces a real error rather than a
// silently-empty store when the root cannot be created.
func TestNewDiskStoreCreatesRootIfMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "does", "not", "exist", "yet")
	s, err := NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore should create nested root: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("root not created: %v", err)
	}
	// Sanity: the store is actually usable.
	if err := s.AppendJournal(context.Background(), CityKey{TenantID: "t", CityID: "c"}, []byte("x")); err != nil {
		t.Fatalf("AppendJournal on freshly created root: %v", err)
	}
}
