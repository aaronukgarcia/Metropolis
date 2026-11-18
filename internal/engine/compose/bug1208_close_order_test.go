package compose

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// pageDirDigest hashes every .page file's name and content under dir.
func pageDirDigest(t *testing.T, dir string) string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range ents {
		if filepath.Ext(e.Name()) == ".page" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		h.Write([]byte(n))
		h.Write(b)
	}
	return string(h.Sum(nil))
}

// TestBug1208_CloseSealsPageStoreBeforeReleasingClaim pins the order inside
// Composition.Close: the page store is sealed FIRST, then the claim is
// released. The seam closeBetweenHook runs between the two steps; at that
// instant nothing may be able to write a page file. With the order swapped
// (release, then seal) the hook runs against a live store and the page
// directory changes, so this test fails.
func TestBug1208_CloseSealsPageStoreBeforeReleasingClaim(t *testing.T) {
	dir := t.TempDir()
	e := core.NewEngine(core.WithWorldSeed(7))
	comp, err := Wire(e, &Deps{CitizenPaging: CitizenPagingOptions{
		Enabled: true, MaxResidentShards: 4, PageDir: dir,
	}})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	seed := func(base uint64) {
		for id := base; id < base+40; id++ {
			// Errors are expected once sealed (store closed); only the
			// directory content is asserted.
			_ = comp.state.citizens.SeedColdRecords([]citizens.ColdRecord{{ID: id}}, "bug1208-order")
		}
	}

	// Control: the same traffic DOES write page files while the store is live.
	seed(1000)
	live := pageDirDigest(t, dir)
	seed(5000)
	if pageDirDigest(t, dir) == live {
		t.Fatal("control failed: seeding across shards at budget 4 wrote no page file on a live store, so the ordering assertion below would prove nothing")
	}

	hookRan := false
	closeBetweenHook = func() {
		hookRan = true
		before := pageDirDigest(t, dir)
		seed(9000) // an abandoned build's paging traffic, at the instant between the two Close steps
		if after := pageDirDigest(t, dir); after != before {
			t.Error("a page file was written between the two Close steps: the store was not sealed before the claim was released")
		}
	}
	t.Cleanup(func() { closeBetweenHook = nil })

	if err := comp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !hookRan {
		t.Fatal("closeBetweenHook never ran")
	}
	if _, err := os.Stat(filepath.Join(dir, pageClaimFileName)); err == nil {
		t.Fatal("the claim file still exists after Close")
	}
}
