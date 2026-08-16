package worklife

import (
	"errors"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// loadTestFile loads the real data/worklife.json via ResolveDataDir so
// every expected figure is read from data, never hardcoded in the test
// (GR#15/AC-19).
func loadTestFile(t *testing.T) WorklifeFile {
	t.Helper()
	dir, err := data.ResolveDataDir("corr-worklife-test")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	f, err := LoadWorklife(dir, "corr-worklife-test")
	if err != nil {
		t.Fatalf("LoadWorklife: %v", err)
	}
	return f
}

// newTestAPI builds a WorkScheduleAPI from cfg with a fixed seed.
func newTestAPI(t *testing.T, cfg WorklifeFile) *WorkScheduleAPI {
	t.Helper()
	api, err := New(cfg, 12345, "corr-worklife-test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return api
}

// patternDef returns the loaded PatternDef for kind.
func patternDef(t *testing.T, f WorklifeFile, kind PatternKind) PatternDef {
	t.Helper()
	for _, p := range f.Patterns {
		if PatternKind(p.ID) == kind {
			return p
		}
	}
	t.Fatalf("pattern %q not found in loaded data", kind)
	return PatternDef{}
}

// policyDef returns the loaded WorkingWeekPolicyDef for id.
func policyDef(t *testing.T, f WorklifeFile, id string) WorkingWeekPolicyDef {
	t.Helper()
	for _, p := range f.WorkingWeekPolicies {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("policy %q not found in loaded data", id)
	return WorkingWeekPolicyDef{}
}

// assertCode asserts err is a registry-sourced *errs.E carrying exactly code
// (GR#7 — the returned error's registry code matches, not merely that an
// error exists).
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("want *errs.E, got %T (%v)", err, err)
	}
	if e.Code != code {
		t.Fatalf("error code = %s, want %s (message: %s)", e.Code, code, e.Display())
	}
}

// fakePolicies is a test fake for the PoliciesAPI seam. It is safe for
// concurrent use (the race test toggles it while queries read).
type fakePolicies struct {
	mu     sync.Mutex
	effect WorkingWeekEffect
	active bool
	err    error
}

func (f *fakePolicies) ActiveWorkingWeek(string) (WorkingWeekEffect, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.effect, f.active, f.err
}

func (f *fakePolicies) set(effect WorkingWeekEffect, active bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.effect = effect
	f.active = active
}

// fakeWellbeing is a test fake for the WellbeingAPI seam; it records the
// last balance pushed per worker ID. Safe for concurrent use.
type fakeWellbeing struct {
	mu       sync.Mutex
	balances map[uint64]float64
}

func (f *fakeWellbeing) PushWorkLifeBalance(workerID uint64, balance float64, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.balances == nil {
		f.balances = map[uint64]float64{}
	}
	f.balances[workerID] = balance
	return nil
}

func (f *fakeWellbeing) balance(workerID uint64) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.balances[workerID]
}
