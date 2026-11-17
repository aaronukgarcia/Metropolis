package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-1197 — final snapshot on SIGTERM (CityHost.Close) and idle eviction,
// bounded by a deadline; torn journal tail truncated on open.

// syncBuf is a goroutine-safe log sink so a test can assert on registry
// lines emitted from the stop path.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func advance(id string, n int64) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(id),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: n},
	}
}

// newFinalHost builds a persisted, tick-free host whose stop path takes the
// final snapshot under budget. mutate may swap h.store (before any city is
// built) to inject faults.
func newFinalHost(t *testing.T, dir string, budget time.Duration, idle time.Duration, mutate func(h *CityHost)) (*CityHost, *syncBuf) {
	t.Helper()
	h, err := newCityHost(dir, hostTickDisabled, idle, 5*time.Millisecond, WithShutdownSnapshotDeadline(budget))
	if err != nil {
		t.Fatalf("newCityHost: %v", err)
	}
	h.engineOpts = testEngineOpts()
	logs := &syncBuf{}
	h.logw = logs
	if mutate != nil {
		mutate(h)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h, logs
}

func snapshotIDs(t *testing.T, dir string, key persist.CityKey) []persist.SnapshotID {
	t.Helper()
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	ids, err := st.ListSnapshots(context.Background(), key)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	return ids
}

// restoreFresh restores key from dir into a BRAND-NEW engine + composition
// (BUG-1201 lesson: never restore into a used composition) and returns
// whether a snapshot was used, the restored tick and the digest.
func restoreFresh(t *testing.T, dir string, key persist.CityKey) (bool, int64, [32]byte) {
	t.Helper()
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	opts := append([]core.Option{core.WithWorldSeed(seedForCity(key))}, testEngineOpts()...)
	e := core.NewEngine(opts...)
	comp, err := compose.Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	used, tick, err := compose.RestoreLatestSnapshotOrGenesis(context.Background(), e, comp, st, key)
	if err != nil {
		t.Fatalf("RestoreLatestSnapshotOrGenesis: %v", err)
	}
	return used, tick, comp.StateDigest()
}

// TestBug1197_CloseWritesFinalSnapshotAndRestoresZeroTail: SIGTERM path
// (httpSrv.Shutdown then host.Close). A city 65 ticks in, far from the
// 360-tick cadence, gets a snapshot at tick 65 on Close and restores from it
// exactly, with nothing left to replay.
func TestBug1197_CloseWritesFinalSnapshotAndRestoresZeroTail(t *testing.T) {
	dir := t.TempDir()
	key := persist.CityKey{TenantID: persistTenantID, CityID: "b1197-close"}
	h, logs := newFinalHost(t, dir, 20*time.Second, time.Hour, nil)
	rc, err := h.GetOrCreate(context.Background(), key)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	submitAll(t, rc.Engine(), rtCommands()[:1]) // advance 5
	submitAll(t, rc.Engine(), []protocol.Command{advance("b1197-a2", 60)})
	pre := rc.Composition().StateDigest()
	if n := len(snapshotIDs(t, dir, key)); n != 0 {
		t.Fatalf("precondition: %d snapshots before Close, want 0", n)
	}

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if n := len(snapshotIDs(t, dir, key)); n != 1 {
		t.Fatalf("Close wrote %d snapshots, want 1; log: %s", n, logs.String())
	}
	used, tick, post := restoreFresh(t, dir, key)
	if !used || tick != 65 {
		t.Fatalf("restore used=%v tick=%d, want snapshot at tick 65", used, tick)
	}
	if post != pre {
		t.Fatalf("restored digest %x != pre-close %x (a replayed tail would double-apply)", post, pre)
	}
}

// slowPutStore blocks PutSnapshot until its context ends, standing in for a
// store that cannot finish inside the budget.
type slowPutStore struct {
	*persist.DiskStore
	puts atomic.Int64
}

func (s *slowPutStore) PutSnapshot(ctx context.Context, city persist.CityKey, snap []byte) (persist.SnapshotID, error) {
	s.puts.Add(1)
	<-ctx.Done()
	return "", ctx.Err()
}

// TestBug1197_DeadlineExceededKeepsPreviousSnapshot: the final snapshot
// cannot finish inside the budget; it is abandoned, the previous snapshot
// stays the latest, and the city still restores exactly (older snapshot plus
// a journal tail).
func TestBug1197_DeadlineExceededKeepsPreviousSnapshot(t *testing.T) {
	dir := t.TempDir()
	key := persist.CityKey{TenantID: persistTenantID, CityID: "b1197-deadline"}
	var slow *slowPutStore
	h, logs := newFinalHost(t, dir, 4*time.Second, time.Hour, func(h *CityHost) {
		disk, ok := h.store.(*persist.DiskStore)
		if !ok {
			t.Fatalf("host store is %T", h.store)
		}
		slow = &slowPutStore{DiskStore: disk}
		h.store = slow
	})
	ctx := context.Background()
	rc, err := h.GetOrCreate(ctx, key)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	submitAll(t, rc.Engine(), []protocol.Command{advance("b1197-d1", 4)})
	// A real, earlier snapshot written straight to disk (bypass the slow put).
	earlier, ok, err := rc.Composition().MaybeSnapshotEvery(ctx, slow.DiskStore, key, 4)
	if err != nil || !ok {
		t.Fatalf("earlier snapshot ok=%v err=%v", ok, err)
	}
	submitAll(t, rc.Engine(), []protocol.Command{advance("b1197-d2", 3)})
	pre := rc.Composition().StateDigest()

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if slow.puts.Load() == 0 {
		t.Fatalf("final snapshot never reached PutSnapshot; the test does not exercise the deadline")
	}
	ids := snapshotIDs(t, dir, key)
	if len(ids) != 1 || ids[0] != earlier {
		t.Fatalf("snapshots after abandoned final = %v, want only the earlier %v", ids, earlier)
	}
	if !strings.Contains(logs.String(), "MET-P061") || !strings.Contains(logs.String(), "deadline") {
		t.Fatalf("abandon not logged under MET-P061/deadline: %q", logs.String())
	}
	used, tick, post := restoreFresh(t, dir, key)
	if !used || tick != 7 || post != pre {
		t.Fatalf("restore used=%v tick=%d digestEqual=%v, want snapshot-4 + tail to tick 7 with equal digest", used, tick, post == pre)
	}
}

// TestBug1197_EvictionWritesFinalSnapshot: idle eviction snapshots too, and
// the rebuilt city comes back from it.
func TestBug1197_EvictionWritesFinalSnapshot(t *testing.T) {
	dir := t.TempDir()
	key := persist.CityKey{TenantID: persistTenantID, CityID: "b1197-evict"}
	h, logs := newFinalHost(t, dir, 20*time.Second, 25*time.Millisecond, nil)
	ctx := context.Background()
	rc, err := h.GetOrCreate(ctx, key)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	h.Acquire(key.TenantID, key.CityID)
	submitAll(t, rc.Engine(), []protocol.Command{advance("b1197-e1", 11)})
	pre := rc.Composition().StateDigest()
	h.Release(key.TenantID, key.CityID)
	if !waitForNoCity(h, key, 5*time.Second) {
		t.Fatalf("idle city was not evicted; log: %s", logs.String())
	}
	// The key leaves the map before stop() (and its snapshot) finishes; the
	// stopping marker clears only after.
	for i := 0; i < 5000; i++ {
		h.mu.Lock()
		n := len(h.stopping)
		h.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if n := len(snapshotIDs(t, dir, key)); n != 1 {
		t.Fatalf("eviction wrote %d snapshots, want 1; log: %s", n, logs.String())
	}
	used, tick, post := restoreFresh(t, dir, key)
	if !used || tick != 11 || post != pre {
		t.Fatalf("restore used=%v tick=%d digestEqual=%v", used, tick, post == pre)
	}
}

// failAppendStore fails AppendJournal once armed, to latch the journal-dirty
// gate (BUG-480).
type failAppendStore struct {
	*persist.DiskStore
	fail atomic.Bool
}

func (s *failAppendStore) AppendJournal(ctx context.Context, city persist.CityKey, rec []byte) error {
	if s.fail.Load() {
		return errors.New("injected append failure")
	}
	return s.DiskStore.AppendJournal(ctx, city, rec)
}

// TestBug1197_DirtyLatchExitsWithoutSnapshot: after a failed journal append
// the final snapshot is refused (a snapshot could never be tail-consistent),
// Close still returns, and the earlier journal still restores.
func TestBug1197_DirtyLatchExitsWithoutSnapshot(t *testing.T) {
	dir := t.TempDir()
	key := persist.CityKey{TenantID: persistTenantID, CityID: "b1197-dirty"}
	var fs *failAppendStore
	h, logs := newFinalHost(t, dir, 20*time.Second, time.Hour, func(h *CityHost) {
		fs = &failAppendStore{DiskStore: h.store.(*persist.DiskStore)}
		h.store = fs
	})
	rc, err := h.GetOrCreate(context.Background(), key)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	submitAll(t, rc.Engine(), []protocol.Command{advance("b1197-x1", 6)})
	fs.fail.Store(true)
	if res := rc.Engine().HandleCommand(advance("b1197-x2", 2)); res.Accepted {
		t.Fatalf("command accepted despite injected journal failure")
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if n := len(snapshotIDs(t, dir, key)); n != 0 {
		t.Fatalf("dirty city wrote %d snapshots, want 0", n)
	}
	if !strings.Contains(logs.String(), "journal-dirty") {
		t.Fatalf("dirty skip not logged: %q", logs.String())
	}
	if used, tick, _ := restoreFresh(t, dir, key); used || tick != 6 {
		t.Fatalf("restore used=%v tick=%d, want genesis replay to tick 6", used, tick)
	}
}

// TestBug1197_TrailingCommandSkipsSnapshot: a command journaled after the
// last tick would be applied twice by "snapshot + tail"; the final snapshot
// is skipped and the journal alone restores exactly.
func TestBug1197_TrailingCommandSkipsSnapshot(t *testing.T) {
	dir := t.TempDir()
	key := persist.CityKey{TenantID: persistTenantID, CityID: "b1197-trail"}
	h, logs := newFinalHost(t, dir, 20*time.Second, time.Hour, nil)
	rc, err := h.GetOrCreate(context.Background(), key)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	cmds := rtCommands()
	submitAll(t, rc.Engine(), cmds[:4]) // advance 5, buy, zone, build: ends on a non-tick command
	pre := rc.Composition().StateDigest()
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if n := len(snapshotIDs(t, dir, key)); n != 0 {
		t.Fatalf("wrote %d snapshots with a trailing command, want 0", n)
	}
	if !strings.Contains(logs.String(), "trailing-command") {
		t.Fatalf("skip not logged: %q", logs.String())
	}
	if used, _, post := restoreFresh(t, dir, key); used || post != pre {
		t.Fatalf("genesis restore used=%v digestEqual=%v", used, post == pre)
	}
}

// TestBug1197_TornTailTruncatedOnBuild: a half-written frame left by a crash
// is cut when the city is built, MET-P060 is logged, and a command accepted
// afterwards is still readable (before the fix it landed after the garbage
// and every later record was unreachable).
func TestBug1197_TornTailTruncatedOnBuild(t *testing.T) {
	dir := t.TempDir()
	key := persist.CityKey{TenantID: persistTenantID, CityID: "b1197-torn"}
	{
		h, _ := newFinalHost(t, dir, 0, time.Hour, nil)
		rc, err := h.GetOrCreate(context.Background(), key)
		if err != nil {
			t.Fatalf("GetOrCreate: %v", err)
		}
		submitAll(t, rc.Engine(), []protocol.Command{advance("b1197-t1", 3)})
		_ = h.Close()
	}
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
	f, err := os.OpenFile(jpath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte{0, 0, 0, 40, 'p', 'a', 'r', 't'}); err != nil { // length 40, 4 payload bytes: torn
		t.Fatal(err)
	}
	_ = f.Close()

	h, logs := newFinalHost(t, dir, 0, time.Hour, nil)
	rc, err := h.GetOrCreate(context.Background(), key)
	if err != nil {
		t.Fatalf("GetOrCreate over torn journal: %v", err)
	}
	if !strings.Contains(logs.String(), "MET-P060") {
		t.Fatalf("torn-tail truncation not logged under MET-P060: %q", logs.String())
	}
	submitAll(t, rc.Engine(), []protocol.Command{advance("b1197-t2", 2)})
	pre := rc.Composition().StateDigest()
	_ = h.Close()
	st, _ := persist.NewDiskStore(dir)
	recs, err := st.ReadJournal(context.Background(), key)
	if err != nil || len(recs) != 2 {
		t.Fatalf("journal has %d records (err %v), want 2: the append after the torn tail must be readable", len(recs), err)
	}
	if _, tick, post := restoreFresh(t, dir, key); tick != 5 || post != pre {
		t.Fatalf("restore tick=%d digestEqual=%v, want tick 5", tick, post == pre)
	}
}

var _ = io.Discard
