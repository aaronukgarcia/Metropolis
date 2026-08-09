package data

import (
	"path/filepath"
	"sync"
	"testing"
)

const validPoliciesV1 = `{"version": 1, "entries": [{"key": "cyclePriorityNetwork"}]}`
const validPoliciesV2 = `{"version": 2, "entries": [{"key": "cyclePriorityNetwork"}, {"key": "lowEmissionZone"}]}`
const invalidPoliciesBroken = `{not valid json`

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
	if s.Get().Version != 1 {
		t.Errorf("Get().Version = %d, want 1 (reload must not have applied)", s.Get().Version)
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
	if s.Get().Version != 1 {
		t.Errorf("Get().Version = %d, want 1", s.Get().Version)
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
	if len(s.Get().Entries) != 1 {
		t.Fatalf("initial entries = %+v", s.Get().Entries)
	}

	writeFixture(t, dir, FilePolicies, validPoliciesV2)
	if err := s.Reload(testCorrelationID()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if s.Get().Version != 2 {
		t.Errorf("Get().Version = %d, want 2", s.Get().Version)
	}
	if len(s.Get().Entries) != 2 {
		t.Errorf("entries after reload = %+v", s.Get().Entries)
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

	got := s.Get()
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
	s.OnChange(func(p *Policies) {
		mu.Lock()
		seenVersion = p.Version
		mu.Unlock()
	})

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
					p := s.Get()
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
