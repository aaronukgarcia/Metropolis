package citizens

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ===========================================================================
// Independent RE-VERIFY 2 on BUG-713's Finding A / Finding B fixes
// (attacker: opus-reverify2-bug713, NOT the author).
// ===========================================================================

// TestReverify2Bug713RefusedSaveEmitsNoRecords: the Finding A refusal must
// land on the FIRST pull, before a single record is emitted, so a driver
// streaming into a bundle cannot write a half-city. (Bundle-level
// atomicity is the caller's: serialize.CreateBundleDir refuses an
// already-existing directory outright, so a refused save can never
// partially overwrite the previous on-disk one — verified by reading
// savebundle.go, out of this package's own scope.)
func TestReverify2Bug713RefusedSaveEmitsNoRecords(t *testing.T) {
	api := poisonPagedAPI(t)

	src := NewSaveParticipant(api).Source()
	rec, ok, err := src()
	if err == nil {
		t.Fatalf("Source's FIRST pull must refuse once a page fault is latched, got ok=%v rec=%+v", ok, rec)
	}
	if ok {
		t.Fatal("a refusing pull must report ok=false")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrPageDecodeCorrupt {
		t.Fatalf("refusal must carry %s, got %v", ErrPageDecodeCorrupt, err)
	}
	// Pull again: still refusing, still emitting nothing (never a
	// one-shot error that then streams the reduced city anyway).
	for i := 0; i < 3; i++ {
		if _, ok, err := src(); err == nil || ok {
			t.Fatalf("pull %d after the refusal emitted data instead of refusing again (ok=%v err=%v)", i, ok, err)
		}
	}
}

// TestReverify2Bug713RenderedMessageCarriesShard: the P3 fix — every
// refusal after the first must render the real shard number, never the
// literal placeholder.
func TestReverify2Bug713RenderedMessageCarriesShard(t *testing.T) {
	api := poisonPagedAPI(t)

	_, _, err := api.AdvanceDayTick("reverify2")
	if err == nil {
		t.Fatal("AdvanceDayTick must refuse")
	}
	msg := err.Error()
	if strings.Contains(msg, "{shard}") {
		t.Fatalf("the refusal still renders the literal placeholder: %s", msg)
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("not a registry error: %v", err)
	}
	if _, ok := e.Ctx["shard"]; !ok {
		t.Fatalf("the re-wrap dropped the original fault's shard ctx: %+v", e.Ctx)
	}
	if e.Ctx["method"] != "AdvanceDayTick" {
		t.Fatalf("the re-wrap lost this call's own method: %+v", e.Ctx)
	}
	if _, ok := e.Ctx["firstFaultAt"]; !ok {
		t.Fatalf("the re-wrap lost firstFaultAt: %+v", e.Ctx)
	}
}

// TestReverify2Bug713LoadOfCorruptSaveRelatches: clearing the flag in
// resetForLoad must NOT be a laundering hole — if the newly loaded city's
// OWN pages are corrupt, the very first read through them re-latches.
func TestReverify2Bug713LoadOfCorruptSaveRelatches(t *testing.T) {
	api := poisonPagedAPI(t)
	if api.pageFault.Load() == nil {
		t.Fatal("setup: expected a latched fault")
	}

	if err := api.resetForLoad(); err != nil {
		t.Fatalf("resetForLoad: %v", err)
	}
	if api.pageFault.Load() != nil {
		t.Fatal("resetForLoad must clear the stale fault (Finding B)")
	}
	if _, _, err := api.AdvanceDayTick("reverify2"); err != nil {
		t.Fatalf("a freshly loaded city must tick again: %v", err)
	}

	// Now poison the LOADED city's own pages: every shard is resident and
	// empty after the reset, so drive eviction, then corrupt a page file
	// the loaded city itself wrote and read it back.
	_ = api.TotalPopulation("reverify2")
	victim := -1
	api.pagingMu.Lock()
	for i := range api.cold {
		if api.cold[i] == nil {
			if _, err := os.Stat(api.pages.pathFor(i)); err == nil {
				victim = i
				break
			}
		}
	}
	api.pagingMu.Unlock()
	if victim < 0 {
		t.Skip("loaded city evicted nothing to disk in this configuration")
	}
	if err := os.WriteFile(api.pages.pathFor(victim), []byte("garbage-not-gob"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	_ = api.TotalPopulation("reverify2")
	if api.pageFault.Load() == nil {
		t.Fatal("a corrupt page in the LOADED city's OWN pages must re-latch — " +
			"resetForLoad's clear must not become a laundering hole")
	}
	if _, _, err := api.AdvanceDayTick("reverify2"); err == nil {
		t.Fatal("AdvanceDayTick must refuse again after the re-latch")
	}
}
