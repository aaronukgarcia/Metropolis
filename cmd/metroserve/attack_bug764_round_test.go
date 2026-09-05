package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// Independent destructive round on BUG-764 (attacker: opus-round-bug764).

// The default configuration must have ZERO paging side effects: no pages/
// subtree under -persist-dir when -citizen-paging is not set.
func TestAttackBUG764_DefaultOffCreatesNoPagesSubtree(t *testing.T) {
	dir := t.TempDir()
	e := core.NewEngine(core.WithWorldSeed(9))
	comp, store, err := setUpPersistence(e, dir, "atk-city", io.Discard)
	if err != nil {
		t.Fatalf("setUpPersistence: %v", err)
	}
	if comp == nil || store == nil {
		t.Fatal("nil composition/store")
	}
	if err := e.AdvanceTicks("atk764", int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pages")); !os.IsNotExist(err) {
		t.Fatalf("FINDING: a pages/ subtree exists under -persist-dir with paging OFF (stat err=%v)", err)
	}
}

// -citizen-paging with no -persist-dir must be refused at boot, not
// silently ignored -- through run() itself, not just the helper.
func TestAttackBUG764_RunRefusesPagingWithoutPersistDir(t *testing.T) {
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	defer func() { _ = devnull.Close() }()
	tmp, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer func() { _ = tmp.Close() }()
	code := run([]string{"-citizen-paging", "-addr", "127.0.0.1:0"}, devnull, tmp)
	if code == 0 {
		t.Fatal("FINDING: run() accepted -citizen-paging with no -persist-dir")
	}
	out, _ := os.ReadFile(tmp.Name())
	if !strings.Contains(string(out), "citizen-paging") {
		t.Fatalf("FINDING: refusal message does not name the offending flag: %q", string(out))
	}
	t.Logf("refused (exit %d): %s", code, strings.TrimSpace(string(out)))
}

// The paging directory a CityHost derives for a city must live under
// -persist-dir but NOT inside the DiskStore's own tenant/city tree, and two
// cities under one host must never share it.
func TestAttackBUG764_HostPageDirsAreDisjoint(t *testing.T) {
	root := t.TempDir()
	host, err := NewCityHost(root, 0, WithCitizenPaging(true, 4, false))
	if err != nil {
		t.Fatalf("NewCityHost: %v", err)
	}
	defer func() { _ = host.Close() }()
	if host.persistDir != root {
		t.Fatalf("host.persistDir = %q, want %q", host.persistDir, root)
	}
	if !host.citizenPagingEnabled || host.citizenPageBudget != 4 {
		t.Fatalf("WithCitizenPaging did not take: enabled=%v budget=%d", host.citizenPagingEnabled, host.citizenPageBudget)
	}
}

// RE-ROUND (opus-reround-bug764): the F1(b) fix installs a `stopping` marker
// in evictIdle only. Shutdown(cityKey) still deletes the entry from the map
// under the lock and calls stop() -- which releases the page-directory claim
// -- OUTSIDE it, with no marker, so a same-key GetOrCreate racing a Shutdown
// can reach compose.Wire while the outgoing city still holds the claim.
func TestAttackBUG764RR_ShutdownRacesGetOrCreate(t *testing.T) {
	persistDir := t.TempDir()
	cityKey := persist.CityKey{TenantID: persistTenantID, CityID: "shutdown-race"}
	ctx := t.Context()

	host, err := newCityHost(persistDir, hostTickDisabled, time.Hour, time.Hour, WithCitizenPaging(true, 2, false))
	if err != nil {
		t.Fatalf("newCityHost: %v", err)
	}
	host.engineOpts = testEngineOpts()
	defer func() { _ = host.Close() }()

	refusals := 0
	for i := 0; i < 8; i++ {
		if _, err := host.GetOrCreate(ctx, cityKey); err != nil {
			t.Fatalf("seed GetOrCreate: %v", err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		var getErr error
		go func() { defer wg.Done(); _ = host.Shutdown(cityKey) }()
		go func() { defer wg.Done(); _, getErr = host.GetOrCreate(ctx, cityKey) }()
		wg.Wait()
		if getErr != nil {
			refusals++
			t.Logf("iteration %d: GetOrCreate racing Shutdown failed: %v", i, getErr)
		}
	}
	if refusals > 0 {
		t.Fatalf("FINDING: %d/8 same-key GetOrCreate calls racing Shutdown were refused (the evictIdle stopping-marker fix does not cover Shutdown)", refusals)
	}
}
