package citizens

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// BUG-1208: once a PageStore is closed (the composition released its page
// directory) nothing it is still running may write a page file.

// dirDigest returns a stable name+content digest of every file under dir.
func dirDigest(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatalf("ReadFile %s: %v", n, err)
		}
		h.Write([]byte(n))
		h.Write(b)
	}
	return string(h.Sum(nil))
}

// TestBug1208_ClosedPageStoreWritesNothing holds a paging walk (the abandoned
// snapshot build's shape) inside its first page write, closes the registry's
// paging, releases the walk, and asserts the page directory is byte-identical
// afterwards and the walk ended.
func TestBug1208_ClosedPageStoreWritesNothing(t *testing.T) {
	dir := t.TempDir()
	api := pagedCity(t, 2000, 4, dir)

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	api.pages.beforeStoreHook = func() {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	}

	walkDone := make(chan struct{})
	go func() {
		defer close(walkDone)
		for _, id := range idsOf(2000) {
			api.CitizenAt(id, "bug1208-walk")
		}
	}()

	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the paging walk never reached a page write; the test cannot hold it")
	}

	api.ClosePaging()
	before := dirDigest(t, dir)
	close(release)

	select {
	case <-walkDone:
	case <-time.After(60 * time.Second):
		t.Fatal("the walk did not end after the page store was closed")
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatal("a page file was written (or changed) after ClosePaging returned")
	}
	if err := api.pages.Store(1, newColdShard(0)); err == nil {
		t.Fatal("Store on a closed PageStore returned nil")
	}
	if _, ok, err := api.pages.Load(1, "bug1208"); err == nil || ok {
		t.Fatalf("Load on a closed PageStore = ok %v err %v; want an error", ok, err)
	}
}

// TestBug1208_ClosePagingNoPagingIsNoop: paging off, nil receiver, double
// close are all safe.
func TestBug1208_ClosePagingNoPagingIsNoop(t *testing.T) {
	api, err := NewCitizensAPI(42, "bug1208-noop")
	if err != nil {
		t.Fatal(err)
	}
	api.ClosePaging()
	var nilAPI *CitizensAPI
	nilAPI.ClosePaging()
	p := NewPageStore(t.TempDir(), 2, 0)
	p.Seal()
	p.Seal()
}

// TestBug1208_ClosedStoreFailsLoudNeverEmptyShard is the r1 blocker: after
// ClosePaging, an operation that touches a paged-out shard must FAIL (latch
// the page fault, ErrPageStoreClosed), never proceed with an empty stand-in
// shard. The save stream (what the abandoned build runs) must end in an error
// and must not complete as a short population.
func TestBug1208_ClosedStoreFailsLoudNeverEmptyShard(t *testing.T) {
	dir := t.TempDir()
	const n = 2000
	api := pagedCity(t, n, 4, dir)
	// Page most shards out first (a full walk at a budget of 4).
	for _, id := range idsOf(n) {
		api.CitizenAt(id, "bug1208-prewalk")
	}
	api.ClosePaging()

	src := NewSaveParticipant(api).Source()
	cold := 0
	var gotErr error
	for {
		rec, ok, err := src()
		if err != nil {
			gotErr = err
			break
		}
		if !ok {
			break
		}
		if rec.Kind == recCitizensCold {
			cold++
		}
	}
	if gotErr == nil {
		t.Fatalf("a save stream on a closed paged store completed with %d cold records and no error (empty stand-in shard)", cold)
	}
	if f := api.pageFault.Load(); f == nil || f.Code != ErrPageStoreClosed {
		t.Fatalf("pageFault = %v; want code %s latched", f, ErrPageStoreClosed)
	}
	if err := api.failIfPageFault("bug1208", "test"); err == nil {
		t.Fatal("entrypoints do not refuse after the closed-store fault")
	}
}
