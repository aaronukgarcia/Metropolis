package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-1197 r2 tests: mid-file journal corruption fails closed (F1), a SIGTERM
// stop abandons an over-deadline build (F3), a late build is never written
// (M2), and a final snapshot is skipped when the engine is ahead of the
// durable journal (partial-tick hypothesis).

func journalPathUnder(t *testing.T, dir string) string {
	t.Helper()
	var jpath string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Base(p) == "journal.dat" {
			jpath = p
		}
		return nil
	})
	if jpath == "" {
		t.Fatalf("journal.dat not found under %s", dir)
	}
	return jpath
}

// F1 at the host: a city whose journal is corrupt mid-file refuses to load
// under MET-P063 and the file is left byte-identical.
func TestBug1197_MidFileCorruptionRefusesToLoadCity(t *testing.T) {
	dir := t.TempDir()
	key := persist.CityKey{TenantID: persistTenantID, CityID: "b1197-corrupt"}
	{
		h, _ := newFinalHost(t, dir, 0, time.Hour, nil)
		rc, err := h.GetOrCreate(context.Background(), key)
		if err != nil {
			t.Fatalf("GetOrCreate: %v", err)
		}
		for i, n := range []int64{1, 2, 3, 4} {
			submitAll(t, rc.Engine(), []protocol.Command{advance("b1197-c"+string(rune('a'+i)), n)})
		}
		_ = h.Close()
	}
	jpath := journalPathUnder(t, dir)
	data, _ := os.ReadFile(jpath)
	// Flip a byte inside the FIRST frame's payload: frames 2..4 stay valid.
	data[6] ^= 0xFF
	if err := os.WriteFile(jpath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	h, _ := newFinalHost(t, dir, 0, time.Hour, nil)
	_, err := h.GetOrCreate(context.Background(), key)
	if err == nil || !strings.Contains(err.Error(), "MET-P063") {
		t.Fatalf("GetOrCreate err = %v, want a MET-P063 refusal", err)
	}
	if after, _ := os.ReadFile(jpath); !bytes.Equal(after, data) {
		t.Fatalf("journal changed (%d -> %d bytes); fail closed must leave it untouched", len(data), len(after))
	}
}

// F3: on the SIGTERM path (CityHost.Close) a build that outlives the deadline
// is abandoned: Close returns near the deadline, not when the build ends.
// The build is held by a channel; the 8 s fallback only bounds a broken
// implementation (it would fail the elapsed assertion instead of hanging).
func TestBug1197_CloseDoesNotWaitForBuildPastDeadline(t *testing.T) {
	dir := t.TempDir()
	key := persist.CityKey{TenantID: persistTenantID, CityID: "b1197-sigterm"}
	h, logs := newFinalHost(t, dir, 300*time.Millisecond, time.Hour, nil)
	rc, err := h.GetOrCreate(context.Background(), key)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	submitAll(t, rc.Engine(), []protocol.Command{advance("b1197-s1", 3)})
	release := make(chan struct{})
	rc.Composition().SetFinalSnapshotBuildHook(func() {
		select {
		case <-release:
		case <-time.After(8 * time.Second):
		}
	})
	start := time.Now()
	_ = h.Close()
	el := time.Since(start)
	close(release)
	if el > 4*time.Second {
		t.Fatalf("Close took %v with a 300 ms deadline: it waited for the in-memory build", el)
	}
	if !strings.Contains(logs.String(), "deadline") {
		t.Fatalf("abandon not logged: %q", logs.String())
	}
	if n := len(snapshotIDs(t, dir, key)); n != 0 {
		t.Fatalf("abandoned build wrote %d snapshots", n)
	}
}

// putCountStore counts PutSnapshot calls.
type putCountStore struct {
	*persist.DiskStore
	puts atomic.Int64
}

func (s *putCountStore) PutSnapshot(ctx context.Context, c persist.CityKey, b []byte) (persist.SnapshotID, error) {
	s.puts.Add(1)
	return s.DiskStore.PutSnapshot(ctx, c, b)
}

// M2: a snapshot whose build finishes AFTER the deadline is never written.
// Channel-controlled, no sleeps: FinalSnapshot returns at the deadline with
// the build parked; the build is then released, FinalSnapshot's Done is
// awaited, and the store must have seen zero PutSnapshot calls.
func TestBug1197_LateBuildAfterDeadlineIsNeverWritten(t *testing.T) {
	dir := t.TempDir()
	key := persist.CityKey{TenantID: persistTenantID, CityID: "b1197-late"}
	h, _ := newFinalHost(t, dir, 0, time.Hour, nil) // host's own final snapshot off
	rc, err := h.GetOrCreate(context.Background(), key)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	submitAll(t, rc.Engine(), []protocol.Command{advance("b1197-l1", 3)})
	disk, _ := persist.NewDiskStore(dir)
	store := &putCountStore{DiskStore: disk}
	release := make(chan struct{})
	rc.Composition().SetFinalSnapshotBuildHook(func() { <-release })
	// The host's city is still ticking nothing (tick-disabled) but its command
	// loop is live; FinalSnapshot needs a quiesced engine, which it is here.
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	res, err := rc.Composition().FinalSnapshot(ctx, store, key)
	if err != nil || res.SkipReason != compose.FinalSkipDeadline || res.Wrote {
		t.Fatalf("FinalSnapshot = %+v err %v, want a deadline skip", res, err)
	}
	close(release)
	<-res.Done
	if n := store.puts.Load(); n != 0 {
		t.Fatalf("PutSnapshot called %d times for a build that finished after the deadline", n)
	}
}

// Partial-tick hypothesis (round r1): the engine is AHEAD of the durable
// journal (an AdvanceTicks(N>1) that fails mid-loop is rejected and never
// journaled, yet its completed ticks stay applied). Modelled by advancing the
// engine directly. A final snapshot at that tick could not be reconciled with
// the journal on restore, so it is skipped and the city restores from the
// journal exactly as it did before BUG-1197.
func TestBug1197_EngineAheadOfJournalSkipsFinalSnapshot(t *testing.T) {
	dir := t.TempDir()
	key := persist.CityKey{TenantID: persistTenantID, CityID: "b1197-partial"}
	h, logs := newFinalHost(t, dir, 20*time.Second, time.Hour, nil)
	rc, err := h.GetOrCreate(context.Background(), key)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	submitAll(t, rc.Engine(), []protocol.Command{advance("b1197-p1", 3)})
	if err := rc.Engine().AdvanceTicks("b1197-p2", 2); err != nil { // applied, never journaled
		t.Fatalf("AdvanceTicks: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if n := len(snapshotIDs(t, dir, key)); n != 0 {
		t.Fatalf("wrote %d snapshots with the engine ahead of the journal, want 0", n)
	}
	if !strings.Contains(logs.String(), "partial-tick") {
		t.Fatalf("skip not logged: %q", logs.String())
	}
	if _, tick, _ := restoreFresh(t, dir, key); tick != 3 {
		t.Fatalf("restore tick = %d, want 3 (the journaled ticks)", tick)
	}
}

type slowOneCityStore struct {
	*persist.DiskStore
	slow string
}

func (s *slowOneCityStore) PutSnapshot(ctx context.Context, c persist.CityKey, b []byte) (persist.SnapshotID, error) {
	if c.CityID == s.slow {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return s.DiskStore.PutSnapshot(ctx, c, b)
}

// Round r1 (kept): N cities under one shared deadline, one slow: the others still snapshot
// and restore exactly; Close returns near the budget, not N x budget.
func TestBug1197_ParallelStopOneSlowCityDoesNotStarveTheRest(t *testing.T) {
	dir := t.TempDir()
	h, logs := newFinalHost(t, dir, 2*time.Second, time.Hour, func(h *CityHost) {
		h.store = &slowOneCityStore{DiskStore: h.store.(*persist.DiskStore), slow: "b1197-par-slow"}
	})
	names := []string{"b1197-par-a", "b1197-par-slow", "b1197-par-b", "b1197-par-c"}
	pre := map[string][32]byte{}
	for _, n := range names {
		rc, err := h.GetOrCreate(context.Background(), persist.CityKey{TenantID: persistTenantID, CityID: n})
		if err != nil {
			t.Fatal(err)
		}
		submitAll(t, rc.Engine(), []protocol.Command{advance(n+"-1", 5)})
		pre[n] = rc.Composition().StateDigest()
	}
	start := time.Now()
	_ = h.Close()
	el := time.Since(start)
	t.Logf("Close took %v; logs=%q", el, logs.String())
	if el > 3500*time.Millisecond {
		t.Fatalf("Close %v exceeds shared budget", el)
	}
	for _, n := range names {
		key := persist.CityKey{TenantID: persistTenantID, CityID: n}
		ids := snapshotIDs(t, dir, key)
		used, tick, post := restoreFresh(t, dir, key)
		t.Logf("%s ids=%d used=%v tick=%d eq=%v", n, len(ids), used, tick, post == pre[n])
		if tick != 5 || post != pre[n] {
			t.Fatalf("%s restore mismatch", n)
		}
		if n != "b1197-par-slow" && (len(ids) != 1 || !used) {
			t.Fatalf("%s fast city did not snapshot", n)
		}
		if n == "b1197-par-slow" && len(ids) != 0 {
			t.Fatalf("slow city wrote a snapshot after deadline")
		}
	}
}

func findPagingClaims(dir string) []string {
	var out []string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && info.Name() == "claim.pid" {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// r3 G1: the SIGTERM give-up path (build outlives the deadline) must still
// release the citizen-paging claim, or the next process's GetOrCreate on the
// same root fails MET-G819 and the container restart-loops.
func TestBug1197_AbandonedSigtermReleasesPagingClaim(t *testing.T) {
	dir := t.TempDir()
	key := persist.CityKey{TenantID: persistTenantID, CityID: "b1197-claim"}
	h, err := newCityHost(dir, hostTickDisabled, time.Hour, 5*time.Millisecond, WithShutdownSnapshotDeadline(300*time.Millisecond), WithCitizenPaging(true, 4, false))
	if err != nil {
		t.Fatal(err)
	}
	h.engineOpts = testEngineOpts()
	h.logw = &syncBuf{}
	rc, err := h.GetOrCreate(context.Background(), key)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	submitAll(t, rc.Engine(), []protocol.Command{advance("b1197-c1", 3)})
	if len(findPagingClaims(dir)) == 0 {
		t.Fatalf("precondition: no claim.pid with paging on")
	}
	release := make(chan struct{})
	rc.Composition().SetFinalSnapshotBuildHook(func() {
		select {
		case <-release:
		case <-time.After(8 * time.Second):
		}
	})
	_ = h.Close()
	claims := findPagingClaims(dir)
	close(release)
	if len(claims) != 0 {
		t.Fatalf("abandoned SIGTERM left claim(s) %v", claims)
	}
	h2, err := newCityHost(dir, hostTickDisabled, time.Hour, 5*time.Millisecond, WithShutdownSnapshotDeadline(0), WithCitizenPaging(true, 4, false))
	if err != nil {
		t.Fatal(err)
	}
	h2.engineOpts = testEngineOpts()
	h2.logw = &syncBuf{}
	defer func() { _ = h2.Close() }()
	if _, err := h2.GetOrCreate(context.Background(), key); err != nil {
		t.Fatalf("next process GetOrCreate on the same root: %v", err)
	}
}
