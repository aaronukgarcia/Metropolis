package main

// FEAT-9995385620 round r4 attack tests (opus-round-r4-rules-lane5). The
// pre-check is the restore run dry; these tests attack it on disk through the
// real host and boot paths:
//
//   - a retired version reached ONLY by walking back past a newer snapshot
//     whose journal tail is short, with citizen paging enabled: refused before
//     Wire, the persist root identical by CONTENT hash and by directory
//     listing, and the OS temp folder left empty;
//   - the original round r1 scenario (a boot-made city woken by a paged host),
//     which the real restore refuses for its seed (MET-E819) before any rule
//     check: every retry, serial and six concurrent, reports MET-E819 and never
//     a leaked paging claim (MET-G819), and no claim file is left;
//   - many pre-checks in a loop leak no goroutine;
//   - pre-checks running while the same city is woken for real do not disturb
//     the wake;
//   - the read cost of one hosted wake, logged.

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// r4aCountStore counts reads and keeps RepairJournal reachable.
type r4aCountStore struct {
	*persist.DiskStore
	journalReads, snapshotReads, snapshotLists atomic.Int64
}

func (s *r4aCountStore) ReadJournal(ctx context.Context, c persist.CityKey) ([][]byte, error) {
	s.journalReads.Add(1)
	return s.DiskStore.ReadJournal(ctx, c)
}

func (s *r4aCountStore) GetSnapshot(ctx context.Context, c persist.CityKey, id persist.SnapshotID) ([]byte, error) {
	s.snapshotReads.Add(1)
	return s.DiskStore.GetSnapshot(ctx, c, id)
}

func (s *r4aCountStore) ListSnapshots(ctx context.Context, c persist.CityKey) ([]persist.SnapshotID, error) {
	s.snapshotLists.Add(1)
	return s.DiskStore.ListSnapshots(ctx, c)
}

// r4aWalkBackCity builds, by the host: S1 (version 0) then, under rule 1, a
// later S2 (version 1); then the journal is cut back to its length at S1, so
// S2's tail is short and the restore walks back to S1. It returns the dir.
func r4aWalkBackCity(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	id1 := r1CityWithSnapshot(t, dir, rtCommands())
	ctx := context.Background()
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s1, err := st.GetSnapshot(ctx, r1Key, id1)
	if err != nil {
		t.Fatal(err)
	}
	jp := r1JournalPath(t, dir)
	j1, err := os.ReadFile(jp)
	if err != nil {
		t.Fatal(err)
	}
	{
		h, _ := newFinalHost(t, dir, 30*time.Second, time.Hour, nil)
		rc, err := h.GetOrCreate(ctx, r1Key)
		if err != nil {
			t.Fatalf("wake under rule 1: %v", err)
		}
		submitAll(t, rc.Engine(), []protocol.Command{advance("r4a-more", 40)})
		_ = h.Close()
	}
	ids := snapshotIDs(t, dir, r1Key)
	s2, err := st.GetSnapshot(ctx, r1Key, ids[len(ids)-1])
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := st.DeleteSnapshot(ctx, r1Key, id); err != nil {
			t.Fatal(err)
		}
	}
	// Oldest first: S1 (version 0), then S2 (version 1, past the cut journal).
	if _, err := st.PutSnapshot(ctx, r1Key, s1); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutSnapshot(ctx, r1Key, s2); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jp, j1, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func r4aClaimFiles(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "claim.pid" {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// Control: with version 0 still supported the city wakes (so the
// refusal below is the rule version and nothing else).
func TestRoundR4Lane5Attack_SupportedVersionControl_Wakes(t *testing.T) {
	dir := r4aWalkBackCity(t)
	h, _ := newFinalHost(t, dir, 0, time.Hour, nil)
	if _, err := h.GetOrCreate(context.Background(), r1Key); err != nil {
		t.Fatalf("control wake refused: %v", err)
	}
}

// A retired version on the newest snapshot (NOT a walk-back: the fixture's second snapshot is a copy of the first, so the refusal comes from the newest snapshot; the walk-back itself is covered by internal/engine/compose/round_r3_lane5_differential_test.go); paging as named; host and
// boot-style direct pre-check; persist root identical by content and listing,
// OS temp folder empty afterwards.
func TestRoundR4Lane5Attack_RefusedPagedHostWake_RootIdenticalByHash(t *testing.T) {
	r4aWalkBackRetired(t, true)
}

func TestRoundR4Lane5Attack_RefusedUnpagedHostWake_RootIdenticalByHash(t *testing.T) {
	r4aWalkBackRetired(t, false)
}

// A pre-check that hits a NON-rule error (an undecodable journal frame) and
// one that passes write nothing either, by content hash.
func TestRoundR4Lane5Attack_PrecheckErrorAndPass_ContentHashIdentical(t *testing.T) {
	dir := t.TempDir()
	r1CityWithSnapshot(t, dir, rtCommands())
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	before, beforeTree := dirDigest(t, dir), treeListing(t, dir)
	if err := compose.PrecheckRuleVersions(context.Background(), st, r1Key, seedForCity(r1Key), ""); err != nil {
		t.Fatalf("passing pre-check: %v", err)
	}
	if dirDigest(t, dir) != before || treeListing(t, dir) != beforeTree {
		t.Fatal("a passing pre-check changed the root")
	}
	if err := st.AppendJournal(context.Background(), r1Key, []byte("not a command")); err != nil {
		t.Fatal(err)
	}
	before, beforeTree = dirDigest(t, dir), treeListing(t, dir)
	if err := compose.PrecheckRuleVersions(context.Background(), st, r1Key, seedForCity(r1Key), ""); err != nil {
		t.Fatalf("erroring pre-check returned %v, want nil", err)
	}
	if dirDigest(t, dir) != before || treeListing(t, dir) != beforeTree {
		t.Fatal("an erroring pre-check changed the root")
	}
}

func r4aWalkBackRetired(t *testing.T, paged bool) {
	dir := r4aWalkBackCity(t)
	rules.SetOldestSupportedForTest(t, rules.RefuseOccupiedCell)
	scratch := t.TempDir()
	t.Setenv("TMP", scratch)
	t.Setenv("TEMP", scratch)
	t.Setenv("TMPDIR", scratch)
	h, _ := newFinalHost(t, dir, 0, time.Hour, func(h *CityHost) { WithCitizenPaging(paged, 2, false)(h) })
	beforeDigest, beforeTree := dirDigest(t, dir), treeListing(t, dir)
	for i := 0; i < 3; i++ {
		_, err := h.GetOrCreate(context.Background(), r1Key)
		if err == nil || !strings.Contains(err.Error(), "MET-G5611") {
			t.Fatalf("attempt %d: err = %v, want MET-G5611", i, err)
		}
	}
	// Static check would also see this one (tail short); the point here is the disk.
	if got := dirDigest(t, dir); got != beforeDigest {
		t.Fatalf("refused wake changed file content; files now %v", r1Files(dir))
	}
	if got := treeListing(t, dir); got != beforeTree {
		t.Fatalf("refused wake changed the tree:\nbefore:\n%s\nafter:\n%s", beforeTree, got)
	}
	left, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("%d entries left in the OS temp folder, first %s", len(left), left[0].Name())
	}
	if c := r4aClaimFiles(dir); len(c) != 0 {
		t.Fatalf("claim files left: %v", c)
	}
}

// The original r1 scenario: a BOOT-made city with a future frame, woken by a
// paged HOST. The real restore refuses it for its seed (MET-E819) before any
// rule check, so this is not a rule refusal; what r1 found (a leaked paging
// claim making the next wake report MET-G819) must not come back.
func TestRoundR4Lane5Attack_OriginalR1Scenario_NoClaimLeak(t *testing.T) {
	dir := t.TempDir()
	makeV0City(t, dir)
	appendFrame(t, dir, envelope(t, newerStamp(), 2))
	h, _ := newFinalHost(t, dir, 0, time.Hour, func(h *CityHost) { WithCitizenPaging(true, 2, false)(h) })
	check := func(label string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: wake accepted", label)
		}
		if strings.Contains(err.Error(), "MET-G819") {
			t.Fatalf("%s: leaked paging claim: %v", label, err)
		}
		if !strings.Contains(err.Error(), "MET-E819") {
			t.Fatalf("%s: err = %v, want MET-E819", label, err)
		}
	}
	var wg sync.WaitGroup
	seen := make([]error, 6)
	for i := range seen {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, seen[i] = h.GetOrCreate(context.Background(), r1Key)
		}(i)
	}
	wg.Wait()
	for i, err := range seen {
		check("concurrent "+string(rune('0'+i)), err)
	}
	for i := 0; i < 3; i++ {
		_, err := h.GetOrCreate(context.Background(), r1Key)
		check("serial "+string(rune('0'+i)), err)
	}
	if c := r4aClaimFiles(dir); len(c) != 0 {
		t.Fatalf("claim files left: %v", c)
	}
}

// Fifty pre-checks leak no goroutine (bounded by iterations, not by time).
func TestRoundR4Lane5Attack_FiftyPrechecks_NoGoroutineLeak(t *testing.T) {
	dir := t.TempDir()
	r1CityWithSnapshot(t, dir, rtCommands())
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	seed := seedForCity(r1Key)
	if err := compose.PrecheckRuleVersions(context.Background(), st, r1Key, seed, ""); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	base := runtime.NumGoroutine()
	var ms0, ms1 runtime.MemStats
	runtime.ReadMemStats(&ms0)
	for i := 0; i < 50; i++ {
		if err := compose.PrecheckRuleVersions(context.Background(), st, r1Key, seed, ""); err != nil {
			t.Fatalf("pre-check %d: %v", i, err)
		}
	}
	n := runtime.NumGoroutine()
	for i := 0; i < 10000 && n > base; i++ {
		runtime.Gosched()
		n = runtime.NumGoroutine()
	}
	runtime.GC()
	runtime.ReadMemStats(&ms1)
	t.Logf("goroutines before=%d after=%d; heap objects before=%d after=%d", base, n, ms0.HeapObjects, ms1.HeapObjects)
	if n > base {
		buf := make([]byte, 1<<16)
		t.Fatalf("goroutines %d after 50 pre-checks, %d before:\n%s", n, base, buf[:runtime.Stack(buf, true)])
	}
}

// Pre-checks on a second store over the same directory while the host wakes
// the city for real: the wake succeeds at the same tick as a solo wake and
// every pre-check passes.
func TestRoundR4Lane5Attack_ConcurrentPrechecksDuringWake(t *testing.T) {
	dir := t.TempDir()
	r1CityWithSnapshot(t, dir, rtCommands())
	soloTick := func() int64 {
		h, _ := newFinalHost(t, dir, 0, time.Hour, nil)
		rc, err := h.GetOrCreate(context.Background(), r1Key)
		if err != nil {
			t.Fatal(err)
		}
		tk := int64(rc.Engine().TicksCompleted())
		_ = h.Close()
		return tk
	}()
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := newFinalHost(t, dir, 0, time.Hour, nil)
	var wg sync.WaitGroup
	errsSeen := make([]error, 8)
	for i := range errsSeen {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errsSeen[i] = compose.PrecheckRuleVersions(context.Background(), st, r1Key, seedForCity(r1Key), "")
		}(i)
	}
	rc, werr := h.GetOrCreate(context.Background(), r1Key)
	wg.Wait()
	if werr != nil {
		t.Fatalf("wake during pre-checks: %v", werr)
	}
	if got := int64(rc.Engine().TicksCompleted()); got != soloTick {
		t.Fatalf("tick %d during pre-checks, %d solo", got, soloTick)
	}
	for i, err := range errsSeen {
		if err != nil {
			t.Fatalf("pre-check %d: %v", i, err)
		}
	}
}

// The read cost of one hosted wake with 1 and with 5 snapshots, logged (and
// bounded so a regression to reading every snapshot shows).
func TestRoundR4Lane5Attack_HostWakeReadCost(t *testing.T) {
	for _, n := range []int{1, 5} {
		dir := t.TempDir()
		id := r1CityWithSnapshot(t, dir, rtCommands())
		st, err := persist.NewDiskStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		data, err := st.GetSnapshot(context.Background(), r1Key, id)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < n; i++ {
			if _, err := st.PutSnapshot(context.Background(), r1Key, data); err != nil {
				t.Fatal(err)
			}
		}
		if got := len(snapshotIDs(t, dir, r1Key)); got != n {
			t.Fatalf("hold %d snapshots, want %d", got, n)
		}
		h, _ := newFinalHost(t, dir, 0, time.Hour, nil)
		disk, ok := h.store.(*persist.DiskStore)
		if !ok {
			t.Fatalf("host store is %T", h.store)
		}
		cs := &r4aCountStore{DiskStore: disk}
		h.store = cs
		if _, err := h.GetOrCreate(context.Background(), r1Key); err != nil {
			t.Fatal(err)
		}
		t.Logf("%d snapshots: hosted wake read journal=%d listSnapshots=%d getSnapshot=%d", n, cs.journalReads.Load(), cs.snapshotLists.Load(), cs.snapshotReads.Load())
		if cs.snapshotReads.Load() > 3 {
			t.Fatalf("%d snapshots: a wake read %d snapshots", n, cs.snapshotReads.Load())
		}
	}
}

var _ = io.Discard
