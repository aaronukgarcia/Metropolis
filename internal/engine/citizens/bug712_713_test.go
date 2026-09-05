package citizens

import (
	"bytes"
	"encoding/gob"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ===========================================================================
// BUG-712: pageOrder was write-only dead state — seeded by
// seedPageBookkeepingLocked and never read anywhere else, while pageList was
// already the real LRU order (BUG-664 round-2 P2). Deleted outright.
// ===========================================================================

// TestBug712PageOrderFieldDeleted is a defensive regression: pageOrder must
// never come back as a CitizensAPI field. TestCitizensAPIFieldsAllClassified
// (participant_test.go) already fails loudly if ANY new/reintroduced field
// is neither "covered" nor "excluded", but this test names the specific
// field BUG-712 removed so a revert is caught by name, not just by the
// generic parity sweep.
func TestBug712PageOrderFieldDeleted(t *testing.T) {
	ct := reflect.TypeOf((*CitizensAPI)(nil)).Elem()
	for i := 0; i < ct.NumField(); i++ {
		if ct.Field(i).Name == "pageOrder" {
			t.Fatal("CitizensAPI.pageOrder was reintroduced — BUG-712 deleted this " +
				"write-only dead field (seeded by seedPageBookkeepingLocked, read by " +
				"nothing; pageList is the real LRU order)")
		}
	}
}

// ===========================================================================
// BUG-713: PageStore double-cache. evictOverBudgetLocked used to Store the
// victim shard (which left it resident in PageStore's OWN internal cache,
// at PageStore's own independent maxResident ceiling) and then nil it out
// of CitizensAPI's cold[] — two independent LRUs over the same shard set,
// so real resident memory could reach ~2x the configured budget, and a
// shard reachable through PageStore.resident's stale copy would shadow a
// corrupt/missing disk file on the next Load.
// ===========================================================================

// residentOnlyInPageStore counts, for a paging-enabled api, how many shard
// indices are resident in PageStore's OWN internal resident map while being
// EVICTED (nil) from CitizensAPI's own cold[] — the actual double-cache
// leak BUG-713 named: a shard CitizensAPI itself no longer counts as
// resident, still silently occupying a second, independent slot inside
// PageStore, invisible to CitizensAPI's own residentCount/maxResidentShards
// ceiling. (A shard resident in BOTH caches at once is normal and expected
// while it is genuinely resident — that is exactly what Load's
// makeResidentLocked is for — so this deliberately only counts the "cold
// says gone, PageStore still has it" case, never plain overlap.) Must
// always be 0 post-fix: evictOverBudgetLocked's Forget call drops
// PageStore's copy in the SAME step that nils cold[victim].
func residentOnlyInPageStore(api *CitizensAPI) int {
	api.pagingMu.Lock()
	defer api.pagingMu.Unlock()
	leaked := 0
	for i := range api.cold {
		if api.cold[i] != nil {
			continue
		}
		api.pages.mu.Lock()
		_, inPageStore := api.pages.resident[i]
		api.pages.mu.Unlock()
		if inPageStore {
			leaked++
		}
	}
	return leaked
}

// TestBug713SingleCacheSingleCeiling (RED-PROOF): heavily churns a
// paging-enabled CitizensAPI across far more distinct shards than the
// residency budget, then asserts (a) CitizensAPI's own resident count
// settles to exactly maxResident, and (b) PageStore's OWN internal
// resident count never exceeds maxResident either — proving there is one
// real cache, one real ceiling, not two independent caches each allowed to
// hold up to maxResident (the pre-fix "2x budget" defect).
func TestBug713SingleCacheSingleCeiling(t *testing.T) {
	const maxResident = 4
	const n = 200 // spans well beyond 256/... shards to force heavy churn
	api := pagedAPI(t, 0xB713A, n, 1, t.TempDir(), maxResident)

	// Touch every citizen once more, forcing continuous eviction/reload
	// churn across many distinct shards.
	for i := 1; i <= n; i++ {
		if _, ok := api.coldRecord(uint64(i)); !ok {
			t.Fatalf("citizen %d unreachable mid-churn", i)
		}
	}

	api.mu.RLock()
	ciResident := 0
	for _, s := range api.cold {
		if s != nil {
			ciResident++
		}
	}
	ciResidentCount := api.residentCount
	api.mu.RUnlock()

	if ciResident > maxResident {
		t.Fatalf("CitizensAPI's own resident shard count = %d, want <= %d", ciResident, maxResident)
	}
	if ciResidentCount > maxResident {
		t.Fatalf("CitizensAPI.residentCount = %d, want <= %d", ciResidentCount, maxResident)
	}

	psResident := api.pages.ResidentCount()
	if psResident > maxResident {
		t.Fatalf("PageStore's OWN resident count = %d, want <= %d — the pre-fix "+
			"double-cache defect let PageStore hold its own independent copy at "+
			"its own ceiling, so real resident memory could reach ~2x the budget",
			psResident, maxResident)
	}

	if leaked := residentOnlyInPageStore(api); leaked > 0 {
		t.Fatalf("%d shard(s) are EVICTED from CitizensAPI's cold[] but still "+
			"resident inside PageStore's own internal cache — a leaked second, "+
			"independent copy invisible to CitizensAPI's own residency ceiling "+
			"(the pre-fix double-cache defect: Store used to leave the evicted "+
			"shard's pointer alive in PageStore.resident)", leaked)
	}
}

// TestBug713PostEvictionReadServesFromDisk (RED-PROOF, rewritten under the
// round ACCEPT on BUG-712/BUG-713, F1) evicts a shard, CORRUPTS its on-disk
// page file directly, then proves THREE things a looser assertion could
// miss:
//
//   - (Forget red-proof) PageStore's OWN internal resident map no longer
//     holds the victim shard's pointer at all. This test's paging budget
//     (maxResident=2) matches PageStore's own maxResident (see pagedAPI),
//     so — WITHOUT evictOverBudgetLocked's Forget call — PageStore's own
//     independent LRU would still be holding the exact same shard resident
//     at the time this assertion runs (49 other citizens is not remotely
//     enough churn to pressure a SEPARATE maxResident=2 ceiling on
//     PageStore's own map into evicting it too), so asserting directly on
//     api.pages.resident here is a REAL red-proof of Forget: removing the
//     Forget call turns this specific assertion red, not just green-by-
//     coincidence via PageStore's own eviction happening to run anyway.
//   - (F1) the corrupted page produces the distinct, registry-sourced
//     ErrPageDecodeCorrupt from PageStore.Load itself — never a bare
//     (nil, false, nil) miss indistinguishable from "never persisted".
//   - (F1) going through the REAL production path (coldRecord ->
//     shardAt -> loadShardLocked) latches c.pageFault, and every
//     subsequent already-error-returning mutation entrypoint (AdvanceDayTick
//     here) REFUSES with that same registry error rather than silently
//     continuing to compute over a city missing a shard's worth of
//     citizens with no error at all (the BUG-687-class defect the round
//     named: population 200 -> 199, PopulationHash changed, no registry
//     error).
func TestBug713PostEvictionReadServesFromDisk(t *testing.T) {
	dir := t.TempDir()
	const maxResident = 2
	api := pagedAPI(t, 0xB713B, 50, 1, dir, maxResident)

	// Touch many other citizens so target's shard is evicted while proving
	// which shard index it lives in.
	const target = uint64(1)
	shard := shardIndexFor(t, api, target)
	for i := 2; i <= 50; i++ {
		_, _ = api.coldRecord(uint64(i))
	}

	api.mu.RLock()
	stillResident := api.cold[shard] != nil
	api.mu.RUnlock()
	if stillResident {
		t.Fatalf("shard %d never got evicted by churning 49 other citizens over a "+
			"budget of %d — test setup assumption broken", shard, maxResident)
	}

	// Forget red-proof: PageStore's OWN cache must not still be holding
	// the evicted shard's pointer (see doc comment above for why this
	// specific setup makes the assertion a real red-proof, not a
	// coincidental pass).
	api.pages.mu.Lock()
	_, stillInPageStore := api.pages.resident[shard]
	api.pages.mu.Unlock()
	if stillInPageStore {
		t.Fatalf("shard %d is evicted from CitizensAPI's cold[] but PageStore's OWN "+
			"resident cache still holds it — Forget was not called (or was removed): "+
			"the corrupted-disk read below would then be served from this stale "+
			"in-memory copy instead of actually hitting disk, silently hiding the "+
			"corruption entirely", shard)
	}

	// Corrupt the page file on disk: truncate it to garbage bytes so any
	// real decode attempt fails.
	path := api.pages.pathFor(shard)
	if err := os.WriteFile(path, []byte("not a valid gob payload"), 0o644); err != nil {
		t.Fatalf("corrupting page file: %v", err)
	}

	// F1: PageStore.Load itself must surface the distinct decode-corruption
	// error, never a bare miss.
	loaded, ok, loadErr := api.pages.Load(shard, "bug713-corrupt-direct")
	if loadErr == nil {
		t.Fatalf("Load of a corrupted page file returned no error (ok=%v, loaded=%+v) "+
			"— corruption is indistinguishable from a never-persisted shard again",
			ok, loaded)
	}
	if ok || loaded != nil {
		t.Fatalf("Load returned ok=%v loaded=%+v alongside a non-nil error — a "+
			"decode failure must never hand back a shard", ok, loaded)
	}
	var le *errs.E
	if !errors.As(loadErr, &le) || le.Code != ErrPageDecodeCorrupt {
		t.Fatalf("Load's error is not ErrPageDecodeCorrupt: %#v", loadErr)
	}

	// F1: the real production path must never resurrect the target citizen
	// from a stale in-memory copy — the corrupted shard is fail-safe
	// substituted empty, so the specific citizen is gone.
	if rec, ok := api.coldRecord(target); ok {
		t.Fatalf("citizen %d was still reachable (record %+v) after its page file "+
			"was corrupted on disk — the read was served from a STALE in-memory "+
			"copy instead of actually hitting the (corrupted) disk file", target, rec)
	}

	// F1: the fault must no longer be silent — the poisoned CitizensAPI
	// refuses any further mutation/tick with the SAME registry error,
	// rather than quietly computing over a city short one shard's worth of
	// citizens with zero indication anything went wrong.
	if _, _, tickErr := api.AdvanceDayTick("bug713-post-corruption"); tickErr == nil {
		t.Fatal("AdvanceDayTick succeeded after a corrupt page silently substituted " +
			"an empty shard — c.pageFault should have refused it (F1)")
	} else {
		var te *errs.E
		if !errors.As(tickErr, &te) || te.Code != ErrPageDecodeCorrupt {
			t.Fatalf("AdvanceDayTick's refusal error is not ErrPageDecodeCorrupt: %#v", tickErr)
		}
	}
}

// shardIndexFor returns the cold shard index the given citizen id lives in,
// using the real accessor (never a private det.ShardForEntity re-derivation)
// so this test tracks whatever sharding scheme the package actually uses.
func shardIndexFor(t *testing.T, api *CitizensAPI, id uint64) int {
	t.Helper()
	api.mu.RLock()
	defer api.mu.RUnlock()
	for i, s := range api.cold {
		if s == nil {
			continue
		}
		if s.rowOf(id) >= 0 {
			return i
		}
	}
	// The shard may already be paged out by the time this runs in some
	// call orderings — fall back to asking the paging layer to load it,
	// which is exactly the production shardAt path.
	for i := 0; i < numColdShards; i++ {
		s := api.shardAt(i)
		if s != nil && s.rowOf(id) >= 0 {
			return i
		}
	}
	t.Fatalf("citizen %d not found in any shard", id)
	return -1
}

// ===========================================================================
// BUG-713 P3: page files carry no city identity — stamp with world seed,
// refuse a mismatch.
// ===========================================================================

// TestBug713PageWorldMismatchRefused (RED-PROOF): a page file written by one
// PageStore (world seed A) must be refused by a DIFFERENT PageStore (world
// seed B) pointed at the same directory/shard index — never silently
// decoded and handed back as if it belonged to world B.
func TestBug713PageWorldMismatchRefused(t *testing.T) {
	dir := t.TempDir()

	psA := NewPageStore(dir, 4, 0xAAAA)
	s := newColdShard(0)
	s.append(mkRecord(1, 0))
	if err := psA.Store(7, s); err != nil {
		t.Fatalf("Store (world A): %v", err)
	}
	// Evict A's own in-memory copy so the next Load is forced to actually
	// decode the on-disk file rather than short-circuiting on residency.
	psA.Forget(7)

	psB := NewPageStore(dir, 4, 0xBBBB)
	loaded, ok, err := psB.Load(7, "bug713-mismatch")
	if err == nil {
		t.Fatalf("Load under a mismatched world seed should have been refused, "+
			"got ok=%v loaded=%+v err=nil", ok, loaded)
	}
	if ok {
		t.Fatal("Load reported ok=true alongside a non-nil error — a mismatch must never hand back a shard")
	}
	if loaded != nil {
		t.Fatalf("Load returned a non-nil shard (%+v) on a world mismatch — must be nil", loaded)
	}

	// The SAME seed must load cleanly.
	psC := NewPageStore(dir, 4, 0xAAAA)
	loadedC, okC, errC := psC.Load(7, "bug713-match")
	if errC != nil {
		t.Fatalf("Load under the MATCHING world seed should succeed, got err: %v", errC)
	}
	if !okC || loadedC == nil || loadedC.count() != 1 || loadedC.ids[0] != 1 {
		t.Fatalf("matching-seed load corrupt: ok=%v loaded=%+v", okC, loadedC)
	}
}

// TestBug713ShardIndexMismatchRefused (RED-PROOF, F2) reproduces the
// attacker's exact shape: copy a legitimately-written page file to a
// DIFFERENT shard's path on disk (e.g. an operator/attacker renaming
// shard-005.page to shard-009.page, or a page directory reorganised by
// hand). Pre-fix, coldShardWire carried no shard identity of its own, so
// Load(9, ...) would decode shard 5's citizens and adopt them WHOLESALE as
// shard 9's data — corrupting det.ShardForEntity's invariant that a citizen
// ID always resolves to exactly one shard. Post-fix, the stamped
// ShardIndex disagrees with the path's own shard argument and Load refuses.
func TestBug713ShardIndexMismatchRefused(t *testing.T) {
	dir := t.TempDir()

	ps := NewPageStore(dir, 4, 0)
	victim := newColdShard(0)
	victim.append(mkRecord(555, 0))
	if err := ps.Store(5, victim); err != nil {
		t.Fatalf("Store(5, ...): %v", err)
	}
	ps.Forget(5) // force the next Load to hit disk, not the in-memory cache

	// The attacker's exact move: copy shard 5's page file onto shard 9's
	// path, byte for byte.
	data, err := os.ReadFile(ps.pathFor(5))
	if err != nil {
		t.Fatalf("reading shard 5's page file: %v", err)
	}
	if err := os.WriteFile(ps.pathFor(9), data, 0o644); err != nil {
		t.Fatalf("copying shard 5's page file onto shard 9's path: %v", err)
	}

	loaded, ok, loadErr := ps.Load(9, "bug713-shard-mismatch")
	if loadErr == nil {
		t.Fatalf("Load(9, ...) of a page file stamped shard=5 succeeded with no "+
			"error (ok=%v loaded=%+v) — shard 5's citizens were adopted wholesale "+
			"as shard 9's data", ok, loaded)
	}
	if ok || loaded != nil {
		t.Fatalf("Load returned ok=%v loaded=%+v alongside a non-nil error — a "+
			"shard-index mismatch must never hand back a shard", ok, loaded)
	}
	var e *errs.E
	if !errors.As(loadErr, &e) || e.Code != ErrPageShardIndexMismatch {
		t.Fatalf("Load's error is not ErrPageShardIndexMismatch: %#v", loadErr)
	}

	// Loading the file back at its OWN, correct shard index must still
	// succeed cleanly.
	loaded5, ok5, err5 := ps.Load(5, "bug713-shard-match")
	if err5 != nil {
		t.Fatalf("Load(5, ...) of shard 5's own file at its own path should "+
			"succeed, got err: %v", err5)
	}
	if !ok5 || loaded5 == nil || loaded5.count() != 1 || loaded5.ids[0] != 555 {
		t.Fatalf("matching shard-index load corrupt: ok=%v loaded=%+v", ok5, loaded5)
	}
}

// TestBug713UnstampedShardIndexAcceptedAtItsOwnPath proves the
// compatibility carve-out for F2, mirroring TestBug713UnstampedPageAcceptedByAnyWorld:
// a page file written before the shard-index stamp existed
// (ShardIndexStamped == false, the gob zero value for a field absent from
// an old encoding) is accepted when loaded at ITS OWN original path —
// decode-and-ignore for old records, never treated as a mismatch just
// because it carries no stamp yet.
func TestBug713UnstampedShardIndexAcceptedAtItsOwnPath(t *testing.T) {
	dir := t.TempDir()

	// Hand-build a legacy-shaped wire record (ShardIndexStamped left at its
	// gob zero value) and write it directly, bypassing Store (which always
	// stamps) so this exercises exactly what a pre-F2 page file looks like
	// on disk.
	w := coldShardWire{IDs: []uint64{77}, BirthDelta: []int16{0}, Sexes: []uint8{0}}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(w); err != nil {
		t.Fatalf("encoding legacy-shaped wire record: %v", err)
	}
	ps := NewPageStore(dir, 4, 0)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(ps.pathFor(3), buf.Bytes(), 0o644); err != nil {
		t.Fatalf("writing legacy page file: %v", err)
	}

	loaded, ok, err := ps.Load(3, "bug713-legacy-shard")
	if err != nil {
		t.Fatalf("Load of an unstamped legacy page should not be refused, got err: %v", err)
	}
	if !ok || loaded == nil || loaded.count() != 1 || loaded.ids[0] != 77 {
		t.Fatalf("legacy page load corrupt: ok=%v loaded=%+v", ok, loaded)
	}
}

// TestBug713UnstampedPageAcceptedByAnyWorld proves the compatibility carve-
// out: a page file written before this stamp existed (WorldSeed == 0, the
// gob zero value for a field absent from an old encoding) is accepted by a
// PageStore with ANY worldSeed rather than being treated as a mismatch —
// decode-and-ignore for old records (BUG-712's own convention, applied here
// to BUG-713 P3's new field).
func TestBug713UnstampedPageAcceptedByAnyWorld(t *testing.T) {
	dir := t.TempDir()

	// worldSeed 0 means "don't stamp" (mirrors "don't verify"): this Store
	// call writes a page with WorldSeed left at its gob zero value, exactly
	// what a pre-BUG-713 page file looks like on disk.
	psWriter := NewPageStore(dir, 4, 0)
	s := newColdShard(0)
	s.append(mkRecord(42, 0))
	if err := psWriter.Store(3, s); err != nil {
		t.Fatalf("Store: %v", err)
	}

	psReader := NewPageStore(dir, 4, 0xDEADBEEF)
	loaded, ok, err := psReader.Load(3, "bug713-legacy")
	if err != nil {
		t.Fatalf("Load of an unstamped legacy page should not be refused, got err: %v", err)
	}
	if !ok || loaded == nil || loaded.count() != 1 || loaded.ids[0] != 42 {
		t.Fatalf("legacy page load corrupt: ok=%v loaded=%+v", ok, loaded)
	}
}
