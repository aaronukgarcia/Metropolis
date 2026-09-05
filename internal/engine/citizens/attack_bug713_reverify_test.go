package citizens

import (
	"errors"
	"os"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ===========================================================================
// Independent RE-VERIFY round on BUG-713's pageFault poison flag
// (attacker: opus-reverify-bug713, NOT the author).
// ===========================================================================

// poisonPagedAPI builds a paging-enabled api, corrupts an evicted shard's
// page file, and forces the faulting Load so c.pageFault is latched.
func poisonPagedAPI(t *testing.T) *CitizensAPI {
	t.Helper()
	dir := t.TempDir()
	api := pagedAPI(t, 0xDEAD01, 200, 1, dir, 2)

	// Drive a full sweep so eviction has actually run and page files exist.
	if pop := api.TotalPopulation("reverify"); pop != 200 {
		t.Fatalf("setup: population %d, want 200", pop)
	}
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
		t.Skip("no evicted-with-page-file shard available")
	}
	if err := os.WriteFile(api.pages.pathFor(victim), []byte("garbage-not-gob"), 0o644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	// A read-only accessor is enough to drive the faulting Load.
	_ = api.TotalPopulation("reverify")
	if api.pageFault.Load() == nil {
		t.Fatalf("pageFault was not latched by the corrupt page — setup assumption broken")
	}
	return api
}

// TestReverifyBug713PoisonLatchesAndRefusesTick confirms the latch itself:
// AdvanceDayTick/AdvanceMonth/SeedColdRecords all refuse with the latched
// registry error once a corrupt page has been read.
func TestReverifyBug713PoisonLatchesAndRefusesTick(t *testing.T) {
	api := poisonPagedAPI(t)

	if _, _, err := api.AdvanceDayTick("reverify"); err == nil {
		t.Fatal("AdvanceDayTick must refuse once a page fault is latched")
	} else {
		var e *errs.E
		if !errors.As(err, &e) || e.Code != ErrPageDecodeCorrupt {
			t.Fatalf("AdvanceDayTick refusal must carry %s, got %v", ErrPageDecodeCorrupt, err)
		}
	}
	if err := api.AdvanceMonth("reverify"); err == nil {
		t.Fatal("AdvanceMonth must refuse once a page fault is latched")
	}
	if err := api.SeedColdRecords([]ColdRecord{mkRecord(999999, 0)}, "reverify"); err == nil {
		t.Fatal("SeedColdRecords must refuse once a page fault is latched")
	}
}

// TestReverifyBug713FindingA_PoisonedSaveIsWritten (FINDING A, P2): the save
// participant's Source() has an error slot in EXACTLY the position
// failIfPageFault would occupy (right beside its existing checkNotCopied
// call, which already returns an error-yielding RecordSource), yet it does
// NOT consult the flag. A save taken after the latch therefore streams the
// REDUCED population happily — the corrupt-page data loss is made DURABLE,
// overwriting a good save with a city that has silently lost citizens. This
// is the BUG-687 shape, one line from being closed.
func TestReverifyBug713FindingA_PoisonedSaveIsWritten(t *testing.T) {
	api := poisonPagedAPI(t)

	p := NewSaveParticipant(api)
	src := p.Source()
	n := 0
	for {
		rec, ok, err := src()
		if err != nil {
			t.Logf("save refused after %d records: %v (finding already closed?)", n, err)
			return
		}
		if !ok {
			break
		}
		if rec.Kind == recCitizensCold {
			n++
		}
	}
	pop := api.TotalPopulation("reverify")
	t.Fatalf("FINDING A: a save taken AFTER the page-fault latch streamed %d cold "+
		"citizen records with NO refusal (live population is %d, down from the seeded "+
		"200) — the corrupt-page loss is written durably over the good save. "+
		"SaveParticipant.Source() should call failIfPageFault beside its existing "+
		"checkNotCopied", n, pop)
}

// TestReverifyBug713FindingB_LatchSurvivesLoad (FINDING B, P1): resetForLoad
// rebuilds the whole cold store from the save being loaded, so a fault
// latched against the PREVIOUS contents is stale by construction — yet
// nothing clears c.pageFault. A player who loads a known-good save after
// hitting a corrupt page gets a city that can never tick again: every
// AdvanceDayTick refuses forever, with no recovery short of restarting the
// process. That is a NEW hard-lock failure mode introduced by the poison
// flag (pre-flag, the city ticked — wrongly, but it ticked).
func TestReverifyBug713FindingB_LatchSurvivesLoad(t *testing.T) {
	api := poisonPagedAPI(t)

	if err := api.resetForLoad(); err != nil {
		t.Fatalf("resetForLoad: %v", err)
	}
	if api.pageFault.Load() == nil {
		return // finding closed
	}
	if _, _, err := api.AdvanceDayTick("reverify"); err != nil {
		t.Fatalf("FINDING B: after resetForLoad rebuilt the cold store from a "+
			"different (good) save, the stale page fault is STILL latched and "+
			"AdvanceDayTick still refuses: %v. resetForLoad must clear pageFault "+
			"— loading a good save is the only recovery a player has", err)
	}
}
