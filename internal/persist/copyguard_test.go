package persist

import (
	"context"
	"errors"
	"testing"
	"time"
	"unsafe"
)

// diskByteCopy / memByteCopy perform SEC-020's attack — a plain struct copy of
// a store — via a raw byte-for-byte memcpy through unsafe.Pointer, mirroring
// internal/engine/citizens/copyguard_test.go's citizensByteCopy. A literal
// `cp := *s` is forbidden here: go vet's copylocks check flags copying a struct
// containing sync/atomic.Pointer, and CI's go vet is a gate. The byte-copy is
// the identical hazard the guard defends against (aliased maps + a copied
// mutex + a self pointer that no longer equals the new address).
func diskByteCopy(s *DiskStore) *DiskStore {
	cp := new(DiskStore)
	*(*[unsafe.Sizeof(DiskStore{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(DiskStore{})]byte)(unsafe.Pointer(s))
	return cp
}

func memByteCopy(s *MemStore) *MemStore {
	cp := new(MemStore)
	*(*[unsafe.Sizeof(MemStore{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(MemStore{})]byte)(unsafe.Pointer(s))
	return cp
}

// This file is a lasting destructive-round regression suite (GR#23, SEC-020)
// for the copy-guard added to DiskStore and MemStore. A Store must always be
// used via the *Store returned by its constructor; a VALUE copy aliases the
// internal maps and copies the sync.Mutex in whatever state it was in, so
// every exported method (and lockFor) must reject a copied store with
// ErrStoreCopied BEFORE taking any lock. This suite must stay green — a
// reddening means the guard stopped firing on some entry point, re-opening
// the copied-locked-mutex deadlock / aliased-map corruption hazard.

// exerciseDiskStore calls every exported DiskStore method once and returns the
// set of non-nil errors observed. Used against both a healthy store (expect
// all nil) and a copied/bare store (expect all ErrStoreCopied).
func exerciseDiskStore(s *DiskStore) []error {
	ctx := context.Background()
	city := CityKey{TenantID: "t", CityID: "c"}
	var errs []error
	record := func(err error) { errs = append(errs, err) }

	record(func() error { return s.AppendJournal(ctx, city, []byte("x")) }())
	record(func() error { _, e := s.ReadJournal(ctx, city); return e }())
	record(func() error { _, e := s.PutSnapshot(ctx, city, []byte("snap")); return e }())
	record(func() error { _, e := s.GetSnapshot(ctx, city, SnapshotID("00000000000000000001")); return e }())
	record(func() error { _, e := s.ListSnapshots(ctx, city); return e }())
	record(func() error { _, e := s.ListCities(ctx, "t"); return e }())
	record(func() error { _, e := s.Exists(ctx, city); return e }())
	return errs
}

func exerciseMemStore(s *MemStore) []error {
	ctx := context.Background()
	city := CityKey{TenantID: "t", CityID: "c"}
	var errs []error
	record := func(err error) { errs = append(errs, err) }

	record(func() error { return s.AppendJournal(ctx, city, []byte("x")) }())
	record(func() error { _, e := s.ReadJournal(ctx, city); return e }())
	record(func() error { _, e := s.PutSnapshot(ctx, city, []byte("snap")); return e }())
	record(func() error { _, e := s.GetSnapshot(ctx, city, SnapshotID("00000000000000000001")); return e }())
	record(func() error { _, e := s.ListSnapshots(ctx, city); return e }())
	record(func() error { _, e := s.ListCities(ctx, "t"); return e }())
	record(func() error { _, e := s.Exists(ctx, city); return e }())
	return errs
}

// TestRegression_CopiedStoreRejected is the core SEC-020 regression pin. It
// proves that (a) a healthy constructed store answers every exported method
// with no ErrStoreCopied, and (b) a value-COPY of that store, a bare
// zero-value store, and a new(...) store all reject EVERY exported method with
// ErrStoreCopied — never a deadlock, panic, or silent mutation. The whole test
// runs under a watchdog: if any guard were NOT the first statement, a copied
// store whose caller reached Lock() would hang here, and the watchdog fails
// the test loudly rather than letting the run time out anonymously.
func TestRegression_CopiedStoreRejected(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)

		// --- DiskStore ---
		s, err := NewDiskStore(t.TempDir())
		if err != nil {
			t.Errorf("NewDiskStore: %v", err)
			return
		}
		// Healthy store: no method may report ErrStoreCopied.
		for i, e := range exerciseDiskStore(s) {
			if errors.Is(e, ErrStoreCopied) {
				t.Errorf("healthy DiskStore method %d wrongly returned ErrStoreCopied", i)
			}
		}

		// A value copy: self still points at the original s, so every method
		// must reject via checkNotCopied BEFORE any lock is taken.
		cp := diskByteCopy(s)
		for i, e := range exerciseDiskStore(cp) {
			if !errors.Is(e, ErrStoreCopied) {
				t.Errorf("copied DiskStore method %d = %v, want ErrStoreCopied", i, e)
			}
		}

		// A bare zero-value store (self never Stored -> nil != &bare) and a
		// new(...) store must be rejected identically.
		var bare DiskStore
		for i, e := range exerciseDiskStore(&bare) {
			if !errors.Is(e, ErrStoreCopied) {
				t.Errorf("bare DiskStore method %d = %v, want ErrStoreCopied", i, e)
			}
		}
		for i, e := range exerciseDiskStore(new(DiskStore)) {
			if !errors.Is(e, ErrStoreCopied) {
				t.Errorf("new(DiskStore) method %d = %v, want ErrStoreCopied", i, e)
			}
		}
		// lockFor on a copy must hand back a throwaway lock, never alias the map.
		if got := cp.lockFor("dir"); got == nil {
			t.Errorf("lockFor on copied DiskStore returned nil")
		}

		// --- MemStore ---
		m := NewMemStore()
		for i, e := range exerciseMemStore(m) {
			if errors.Is(e, ErrStoreCopied) {
				t.Errorf("healthy MemStore method %d wrongly returned ErrStoreCopied", i)
			}
		}
		mcp := memByteCopy(m)
		for i, e := range exerciseMemStore(mcp) {
			if !errors.Is(e, ErrStoreCopied) {
				t.Errorf("copied MemStore method %d = %v, want ErrStoreCopied", i, e)
			}
		}
		var bareM MemStore
		for i, e := range exerciseMemStore(&bareM) {
			if !errors.Is(e, ErrStoreCopied) {
				t.Errorf("bare MemStore method %d = %v, want ErrStoreCopied", i, e)
			}
		}
		for i, e := range exerciseMemStore(new(MemStore)) {
			if !errors.Is(e, ErrStoreCopied) {
				t.Errorf("new(MemStore) method %d = %v, want ErrStoreCopied", i, e)
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("copy-guard test deadlocked — a guard is not the FIRST statement (a copied store reached Lock())")
	}
}

// TestRegression_CopiedStoreNoMutation proves a rejected copy never mutated the
// shared backing state: after hammering a copy with every write method, the
// original store still reads back exactly what IT wrote and nothing the copy
// attempted.
func TestRegression_CopiedStoreNoMutation(t *testing.T) {
	ctx := context.Background()
	s, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	city := CityKey{TenantID: "t", CityID: "c"}
	if err := s.AppendJournal(ctx, city, []byte("original")); err != nil {
		t.Fatalf("append: %v", err)
	}

	cp := diskByteCopy(s)
	_ = cp.AppendJournal(ctx, city, []byte("intruder")) // must be rejected
	_, _ = cp.PutSnapshot(ctx, city, []byte("intruder-snap"))

	got, err := s.ReadJournal(ctx, city)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || string(got[0]) != "original" {
		t.Fatalf("copy mutated shared journal: got %q, want exactly [original]", got)
	}
	snaps, _ := s.ListSnapshots(ctx, city)
	if len(snaps) != 0 {
		t.Fatalf("copy wrote a snapshot through the guard: got %v", snaps)
	}
}
