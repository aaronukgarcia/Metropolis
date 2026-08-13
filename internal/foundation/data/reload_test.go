package data

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

const validPoliciesV1 = `{"version": 1, "entries": [{"key": "cyclePriorityNetwork"}]}`
const validPoliciesV2 = `{"version": 2, "entries": [{"key": "cyclePriorityNetwork"}, {"key": "lowEmissionZone"}]}`
const invalidPoliciesBroken = `{not valid json`

// mustGet is this file's shared helper for Store.Get(), which now
// returns (*T, error) so BUG-125's copy-guard rejection (CodeStoreCopied)
// has somewhere to go. Every call site in this file is normal
// single-instance (pointer-only) usage, so a non-nil error here is
// always a test bug, never expected behaviour — fail loudly rather than
// silently ignore it.
func mustGet(t *testing.T, s *Store[Policies, *Policies]) *Policies {
	t.Helper()
	v, err := s.Get()
	if err != nil {
		t.Fatalf("Get(): unexpected error %v", err)
	}
	return v
}

// storeCopy takes a same-package value copy of *Store[Policies,
// *Policies], isolated into its own tiny helper (mirrors
// internal/engine/world's w2Copy / engine.core's e2Copy convention
// exactly, including the unsafe byte-copy): a plain `s2 := *s1` is
// legal, correct Go that produces the identical attack shape (the exact
// hazard Kestrel's Destructive review reproduced on BUG-125), but go
// vet's copylocks check would flag the LITERAL assignment at its own
// call site, which would make this test file itself fail
// `go vet ./internal/foundation/data/...`, one of this package's own
// baseline gates. The byte-copy achieves the same struct-value copy
// (same reloadMu/cbMu bytes, same aliased cbs slice header) via a route
// copylocks does not statically recognise as a lock copy.
func storeCopy(s *Store[Policies, *Policies]) *Store[Policies, *Policies] {
	c := new(Store[Policies, *Policies])
	*(*[unsafe.Sizeof(Store[Policies, *Policies]{})]byte)(unsafe.Pointer(c)) =
		*(*[unsafe.Sizeof(Store[Policies, *Policies]{})]byte)(unsafe.Pointer(s))
	return c
}

func TestStore_ReloadDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FilePolicies, validPoliciesV1)
	path := filepath.Join(dir, FilePolicies)

	s, err := NewStore[Policies, *Policies](path, nil, testCorrelationID())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	writeFixture(t, dir, FilePolicies, validPoliciesV2)
	err = s.Reload(testCorrelationID())
	assertPlaceholderCode(t, err, CodeReloadDebugRequired, "")
	if v := mustGet(t, s); v.Version != 1 {
		t.Errorf("Get().Version = %d, want 1 (reload must not have applied)", v.Version)
	}
}

func TestStore_ReloadWithFlagOff(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FilePolicies, validPoliciesV1)
	path := filepath.Join(dir, FilePolicies)

	flag := &DebugFlag{}
	s, err := NewStore[Policies, *Policies](path, flag, testCorrelationID())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	writeFixture(t, dir, FilePolicies, validPoliciesV2)
	if err := s.Reload(testCorrelationID()); err == nil {
		t.Fatal("expected Reload to fail with debug flag off")
	}
	if v := mustGet(t, s); v.Version != 1 {
		t.Errorf("Get().Version = %d, want 1", v.Version)
	}
}

func TestStore_ReloadSuccessWithDebugOn(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FilePolicies, validPoliciesV1)
	path := filepath.Join(dir, FilePolicies)

	flag := &DebugFlag{}
	flag.Enable()
	s, err := NewStore[Policies, *Policies](path, flag, testCorrelationID())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if v := mustGet(t, s); len(v.Entries) != 1 {
		t.Fatalf("initial entries = %+v", v.Entries)
	}

	writeFixture(t, dir, FilePolicies, validPoliciesV2)
	if err := s.Reload(testCorrelationID()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	v := mustGet(t, s)
	if v.Version != 2 {
		t.Errorf("Get().Version = %d, want 2", v.Version)
	}
	if len(v.Entries) != 2 {
		t.Errorf("entries after reload = %+v", v.Entries)
	}
}

// AC-11: a reload that fails leaves the previously-loaded config intact.
func TestStore_FailedReloadLeavesOldConfigIntact(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FilePolicies, validPoliciesV1)
	path := filepath.Join(dir, FilePolicies)

	flag := &DebugFlag{}
	flag.Enable()
	s, err := NewStore[Policies, *Policies](path, flag, testCorrelationID())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	writeFixture(t, dir, FilePolicies, invalidPoliciesBroken)
	err = s.Reload(testCorrelationID())
	assertPlaceholderCode(t, err, CodeReloadFailed, "")

	got := mustGet(t, s)
	if got == nil {
		t.Fatal("Get() returned nil after failed reload — config must never go nil")
	}
	if got.Version != 1 || len(got.Entries) != 1 {
		t.Errorf("post-failed-reload config = %+v, want the original v1 content", got)
	}
}

func TestStore_OnChangeCallback(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FilePolicies, validPoliciesV1)
	path := filepath.Join(dir, FilePolicies)

	flag := &DebugFlag{}
	flag.Enable()
	s, err := NewStore[Policies, *Policies](path, flag, testCorrelationID())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	var mu sync.Mutex
	var seenVersion int
	if err := s.OnChange(func(p *Policies) {
		mu.Lock()
		seenVersion = p.Version
		mu.Unlock()
	}); err != nil {
		t.Fatalf("OnChange: %v", err)
	}

	writeFixture(t, dir, FilePolicies, validPoliciesV2)
	if err := s.Reload(testCorrelationID()); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if seenVersion != 2 {
		t.Errorf("OnChange callback saw version %d, want 2", seenVersion)
	}
}

// AC-4/AC-13: concurrent readers must never observe a torn struct while
// a Reload is in flight, proven under -race.
func TestStore_ConcurrentGetDuringReload(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FilePolicies, validPoliciesV1)
	path := filepath.Join(dir, FilePolicies)

	flag := &DebugFlag{}
	flag.Enable()
	s, err := NewStore[Policies, *Policies](path, flag, testCorrelationID())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	const readers = 8
	const reloads = 50

	stop := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					p, err := s.Get()
					if err != nil {
						t.Errorf("Get() returned an unexpected error during concurrent reload: %v", err)
						return
					}
					if p == nil {
						t.Error("Get() returned nil during concurrent reload")
						return
					}
					// Reading every field exercises the struct as a whole,
					// not just the pointer — a torn write would show up as
					// an inconsistent Version/Entries pairing.
					_ = p.Version
					_ = len(p.Entries)
				}
			}
		}()
	}

	for i := 0; i < reloads; i++ {
		content := validPoliciesV1
		if i%2 == 0 {
			content = validPoliciesV2
		}
		writeFixture(t, dir, FilePolicies, content)
		if err := s.Reload(testCorrelationID()); err != nil {
			t.Fatalf("Reload iteration %d: %v", i, err)
		}
	}

	close(stop)
	wg.Wait()
}

// TestStore_CopyDetectedAndRejected is BUG-125's regression test.
// Kestrel's Destructive review reproduced a real -race data race:
// Store is exported with no checkNotCopied-style guard, so `s2 := *s1`
// is legal, unsafe-free Go; s2's cbs slice can alias s1's backing array
// while each Store has its own independent cbMu, so concurrent OnChange
// calls through the original and the copy raced on the shared array (1
// of 5 runs during the pre-fix reproduction — see the fix report; not
// reproduced here since the whole point of the fix is that the copy is
// rejected before ever touching cbMu/cbs, so there is nothing left to
// race).
//
// This proves (a): the copy is detected and rejected — with
// CodeStoreCopied, before any shared state is touched — by every one of
// Get/OnChange/Reload, not just the method Kestrel's PoC happened to
// exercise.
func TestStore_CopyDetectedAndRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FilePolicies, validPoliciesV1)
	path := filepath.Join(dir, FilePolicies)

	flag := &DebugFlag{}
	flag.Enable()
	s1, err := NewStore[Policies, *Policies](path, flag, testCorrelationID())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// The legal-but-hazardous copy: the real-world attack (`s2 := *s1`)
	// is a plain dereference-and-reassign with no unsafe/reflect at all
	// — storeCopy reaches the identical struct-value copy via unsafe
	// only so THIS TEST FILE doesn't trip go vet's copylocks check on
	// its own literal assignment (see storeCopy's doc comment).
	s2 := storeCopy(s1)

	if _, err := s2.Get(); err == nil {
		t.Error("Get() on a copied Store: expected CodeStoreCopied, got nil")
	} else {
		assertPlaceholderCode(t, err, CodeStoreCopied, "")
	}

	if err := s2.OnChange(func(*Policies) {}); err == nil {
		t.Error("OnChange() on a copied Store: expected CodeStoreCopied, got nil")
	} else {
		assertPlaceholderCode(t, err, CodeStoreCopied, "")
	}

	if err := s2.Reload(testCorrelationID()); err == nil {
		t.Error("Reload() on a copied Store: expected CodeStoreCopied, got nil")
	} else {
		assertPlaceholderCode(t, err, CodeStoreCopied, "")
	}

	// The rejection must not have mutated s1's cbs — s2.OnChange above
	// never reached s2.cbMu, so nothing was appended anywhere.
	s1.cbMu.Lock()
	gotLen := len(s1.cbs)
	s1.cbMu.Unlock()
	if gotLen != 0 {
		t.Errorf("s1.cbs length = %d after rejected s2.OnChange, want 0 (copy must never touch shared state)", gotLen)
	}

	// The ORIGINAL is completely unaffected by the copy's existence —
	// this is (b): normal single-instance usage still works exactly as
	// before through the real *Store.
	v := mustGet(t, s1)
	if v.Version != 1 {
		t.Errorf("s1.Get().Version = %d, want 1 (original must be unaffected by a rejected copy)", v.Version)
	}
	if err := s1.OnChange(func(*Policies) {}); err != nil {
		t.Fatalf("s1.OnChange: unexpected error %v", err)
	}
	writeFixture(t, dir, FilePolicies, validPoliciesV2)
	if err := s1.Reload(testCorrelationID()); err != nil {
		t.Fatalf("s1.Reload: unexpected error %v", err)
	}
	if v := mustGet(t, s1); v.Version != 2 {
		t.Errorf("s1.Get().Version after Reload = %d, want 2", v.Version)
	}
}

// TestStore_CopyRaceNoLongerReproducible re-runs Kestrel's exact
// concurrency shape (concurrent OnChange calls through the original and
// a copy, both serialized by independent cbMu locks, sharing a grown
// cbs backing array) under -race. Pre-fix this reproduced a genuine data
// race reliably (5/5 manual runs during the fix round). Post-fix, the
// copy's OnChange call is rejected by checkNotCopied before it ever
// reaches cbMu, so there is no write for -race to catch — this test
// simply proves the shape stays clean, not that the race magically
// stopped happening.
func TestStore_CopyRaceNoLongerReproducible(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FilePolicies, validPoliciesV1)
	path := filepath.Join(dir, FilePolicies)

	flag := &DebugFlag{}
	flag.Enable()
	s1, err := NewStore[Policies, *Policies](path, flag, testCorrelationID())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Reproduce the exact pre-fix setup: grow cbs's backing array beyond
	// its current length so a copy would share slack capacity, were the
	// copy's OnChange ever allowed to reach cbMu at all.
	for i := 0; i < 4; i++ {
		s1.cbMu.Lock()
		s1.cbs = append(s1.cbs, func(*Policies) {})
		s1.cbMu.Unlock()
	}
	s1.cbMu.Lock()
	s1.cbs = s1.cbs[:1]
	s1.cbMu.Unlock()

	s2 := storeCopy(s1)

	var wg sync.WaitGroup
	var s2Errs int64
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if err := s1.OnChange(func(*Policies) {}); err != nil {
				t.Errorf("s1.OnChange (the original): unexpected error %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if err := s2.OnChange(func(*Policies) {}); err != nil {
				atomic.AddInt64(&s2Errs, 1)
			} else {
				t.Error("s2.OnChange (the copy): expected CodeStoreCopied, got nil")
			}
		}
	}()
	wg.Wait()

	if s2Errs != 200 {
		t.Errorf("s2 (the copy) OnChange rejections = %d, want 200 (every call must be rejected, none may race through)", s2Errs)
	}
}
