package attract

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// ATTACK (FIXED, BUG-380 round finding P1, opus-round-bug380): decode-time
// unbounded allocation. applyLoadRecord's BUG-380 backfill loop ran
// i = 2..NextMigrantID with a map insert per iteration, and NextMigrantID
// was an unvalidated uint64 straight off the save file — a corrupt/hostile
// value made Load allocate O(NextMigrantID) map entries before any other
// check ran. Pre-BUG-380 the same field was a plain assignment (no loop at
// all). Fixed by migrantCounterCeiling (participant.go): a NextMigrantID
// beyond the ceiling is refused with ErrMigrantCounterImplausible BEFORE
// the backfill loop, reputation, or any other field is touched.
//
// This test now has TWO regimes:
//  1. Sane-but-large values (100k, 1M — well under the ceiling) are still
//     ACCEPTED and correctly backfilled: the ceiling protects against
//     implausible values, not legitimate large cities.
//  2. An implausible value (1<<40, far beyond any real population) is
//     REFUSED fast, with no meaningful allocation — the actual attack this
//     pins.
func TestAttack380_CorruptNextMigrantIDDrivesDecodeAllocation(t *testing.T) {
	for _, n := range []uint64{100_000, 1_000_000} {
		a, _, _, _ := newAPI(t, validConfig())
		rec := serialize.Record{Kind: recAttractMeta, Data: []byte(fmt.Sprintf(
			`{"reputation":{"hasBaseline":true,"baseline":5,"value":6},"lastAdvancedMonth":9,"hasAdvanced":true,"nextMigrantID":%d}`, n))}
		var ms runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&ms)
		before := ms.HeapAlloc
		start := time.Now()
		if err := NewSaveParticipant(a).Handler()(rec); err != nil {
			t.Fatalf("Handler: %v", err)
		}
		elapsed := time.Since(start)
		runtime.ReadMemStats(&ms)
		a.mu.RLock()
		entries := len(a.migrantAdmittedMonth)
		a.mu.RUnlock()
		t.Logf("nextMigrantID=%d (sane, under migrantCounterCeiling=%d) -> %d map entries built at decode, %.0f ms, heap +%.1f MB",
			n, uint64(migrantCounterCeiling), entries, float64(elapsed.Milliseconds()), float64(ms.HeapAlloc-before)/(1024*1024))
		if uint64(entries) != n-1 {
			t.Fatalf("expected %d entries, got %d", n-1, entries)
		}
	}
}

// TestAttack380_ImplausibleNextMigrantIDRefusesFastNoAllocation is the
// actual DoS closure proof the round asked for by name: a hostile record
// with NextMigrantID=1<<40 (far beyond migrantCounterCeiling) must be
// REFUSED — a registry-sourced ErrMigrantCounterImplausible error, never a
// panic, never a successful decode — in well under a second, with no
// meaningful heap growth (the backfill loop must never start).
func TestAttack380_ImplausibleNextMigrantIDRefusesFastNoAllocation(t *testing.T) {
	const hostile = uint64(1) << 40
	a, _, _, _ := newAPI(t, validConfig())
	rec := serialize.Record{Kind: recAttractMeta, Data: []byte(fmt.Sprintf(
		`{"reputation":{"hasBaseline":true,"baseline":5,"value":6},"lastAdvancedMonth":9,"hasAdvanced":true,"nextMigrantID":%d}`, hostile))}

	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	before := ms.HeapAlloc
	start := time.Now()
	err := NewSaveParticipant(a).Handler()(rec)
	elapsed := time.Since(start)
	runtime.ReadMemStats(&ms)

	isErr(t, err, ErrMigrantCounterImplausible)
	if elapsed > 2*time.Second {
		t.Fatalf("refusal took %s — the ceiling check did not run before the backfill loop (should be sub-millisecond)", elapsed)
	}
	a.mu.RLock()
	entries := len(a.migrantAdmittedMonth)
	nextID := a.nextMigrantID
	a.mu.RUnlock()
	if entries != 0 {
		t.Fatalf("migrantAdmittedMonth has %d entries after a REFUSED record — the backfill loop ran despite the refusal", entries)
	}
	if nextID != 0 {
		t.Fatalf("nextMigrantID = %d after a REFUSED record, want 0 (resetForLoad's zero, untouched by the refused assignment)", nextID)
	}
	grew := int64(ms.HeapAlloc) - int64(before)
	t.Logf("REFUSED in %s, heap delta %d bytes (no allocation loop reached), err=%v", elapsed, grew, err)
	// A generous ceiling: a real backfill loop at even a fraction of 1<<40
	// would allocate gigabytes; a few MB of incidental error-path/registry/
	// GC-measurement noise (observed ~1.2MB in practice) is expected and
	// harmless — this bound exists to catch a multi-GB runaway, not to
	// pin exact incidental allocator noise.
	const maxIncidentalBytes = 8 << 20 // 8 MB
	if grew > maxIncidentalBytes {
		t.Fatalf("heap grew by %d bytes on a refused record — want under %d (the backfill loop must not have run)", grew, maxIncidentalBytes)
	}
}

// ATTACK: quantify the OLD-SAVE backfill's grace extension. A migrant
// admitted at month A, saved at LastAdvancedMonth L, is backfilled at L, so
// its eligibility moves from A+grace to L+grace. Worst case (a migrant
// already past grace at the save point) is a FULL extra grace period.
func TestAttack380_OldSaveBackfillGraceExtension(t *testing.T) {
	for _, l := range []int64{0, 11, 12, 500} {
		a, _, _, _ := newAPI(t, validConfig())
		rec := serialize.Record{Kind: recAttractMeta, Data: []byte(fmt.Sprintf(
			`{"reputation":{"hasBaseline":true,"baseline":5,"value":6},"lastAdvancedMonth":%d,"hasAdvanced":true,"nextMigrantID":3}`, l))}
		if err := NewSaveParticipant(a).Handler()(rec); err != nil {
			t.Fatalf("Handler: %v", err)
		}
		id := migrantIDHighBit | 2
		// First month at/after L on which the migrant is eligible.
		var eligible int64 = -1
		for m := l; m <= l+40; m++ {
			if !a.migrantBelowTenureGrace(id, m) {
				eligible = m
				break
			}
		}
		// Truth for a migrant admitted at month 0 with the field present.
		trueEligible := int64(0) + migrantTenureGraceMonths
		t.Logf("old save with lastAdvancedMonth=%d: backfilled migrant becomes eligible at month %d; a migrant genuinely admitted at month 0 would be eligible at %d -> EXTRA delay %d months",
			l, eligible, trueEligible, eligible-trueEligible)
	}
}

// ATTACK: an ancient save whose record predates nextMigrantID entirely
// decodes NextMigrantID==0; the first post-load mint then returns
// MigrantIDBase+1 -- an id compose's migrantIDsFromCount([+2,+M]) can never
// enumerate. Pre-BUG-380's [+1,+M] range DID cover it.
func TestAttack380_PostLoadFirstMintCanBeBasePlusOne(t *testing.T) {
	a, _, _, _ := newAPI(t, validConfig())
	rec := serialize.Record{Kind: recAttractMeta, Data: []byte(`{"reputation":{"hasBaseline":false,"baseline":0,"value":0},"lastAdvancedMonth":0,"hasAdvanced":false}`)}
	if err := NewSaveParticipant(a).Handler()(rec); err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if got := a.MigrantsAdmitted(); got != 0 {
		t.Fatalf("expected nextMigrantID 0 after an ancient record, got %d", got)
	}
	id := a.mintMigrantID()
	if id != migrantIDHighBit|1 {
		t.Fatalf("expected the first post-load mint to be base+1, got %d", id)
	}
	t.Logf("CONFIRMED: after a record with no nextMigrantID key, the first minted migrant id is base+1 (MigrantsAdmitted()=%d) -- compose's migrantIDsFromCount enumerates [base+2, base+M] and would silently omit this real citizen from both emigration eligibility and the wage/household surface", a.MigrantsAdmitted())
}

// ===================== RE-ROUND (opus-reround-bug380) =====================

// TestReround380_CounterCeilingBoundary — MET-G708's boundary, FIXED
// (opus-reround-bug380 P2): the check is now `>= migrantCounterCeiling`,
// so EXACTLY 100,000,000 is REFUSED too — the round's own finding that a
// `>` check accepted the ceiling itself and would have driven a
// ~1e8-iteration, ~4.4GB backfill. One-below-the-ceiling stays ACCEPTED
// (the ceiling bounds implausible values, not legitimate large cities).
// The 1e8/4.4GB scenario is never actually run (it would OOM this
// process) — the per-entry decode cost is calibrated on a safe 1e6 sample
// and the round's original extrapolation is kept in the log for the
// historical record of what the pre-fix `>` check would have cost.
func TestReround380_CounterCeilingBoundary(t *testing.T) {
	mk := func(n uint64) serialize.Record {
		return serialize.Record{Kind: recAttractMeta, Data: []byte(fmt.Sprintf(
			`{"reputation":{"hasBaseline":true,"baseline":5,"value":6},"lastAdvancedMonth":9,"hasAdvanced":true,"nextMigrantID":%d}`, n))}
	}
	// At or above the ceiling: refused, no state touched.
	for _, n := range []uint64{migrantCounterCeiling, migrantCounterCeiling + 1, 1 << 40} {
		a, _, _, _ := newAPI(t, validConfig())
		start := time.Now()
		err := NewSaveParticipant(a).Handler()(mk(n))
		el := time.Since(start)
		if err == nil {
			t.Fatalf("nextMigrantID=%d was ACCEPTED, want MET-G708 refusal", n)
		}
		isErr(t, err, ErrMigrantCounterImplausible)
		a.mu.RLock()
		entries := len(a.migrantAdmittedMonth)
		next := a.nextMigrantID
		a.mu.RUnlock()
		if entries != 0 || next != 0 {
			t.Fatalf("nextMigrantID=%d refused but state was mutated: %d entries, nextMigrantID=%d (must be fully refused before any assignment)", n, entries, next)
		}
		t.Logf("REFUSED nextMigrantID=%d in %v, 0 entries, counter untouched (%v)", n, el.Round(time.Millisecond), err)
	}

	// NOTE: deliberately NOT testing migrantCounterCeiling-1 with a real
	// decode here — that would actually run a ~1e8-entry backfill (the
	// same ~10s/~4.4GB this test's own extrapolation below describes),
	// which would blow this test's time/memory budget for no extra
	// coverage. TestAttack380_CorruptNextMigrantIDDrivesDecodeAllocation's
	// 100k/1M cases already prove legitimately large-but-sane values are
	// accepted and correctly backfilled; the boundary check above (`>=`)
	// is a one-line comparison whose correctness at migrantCounterCeiling-1
	// follows directly from it being < the ceiling, without needing to pay
	// for the allocation to prove it.

	// Calibrate the per-entry decode cost on a safe sample, and keep the
	// round's original historical extrapolation of what accepting the
	// ceiling itself (the pre-fix `>` behaviour) would have cost.
	const sample = 1_000_000
	a, _, _, _ := newAPI(t, validConfig())
	start := time.Now()
	if err := NewSaveParticipant(a).Handler()(mk(sample)); err != nil {
		t.Fatalf("sample %d refused: %v", sample, err)
	}
	el := time.Since(start)
	a.mu.RLock()
	got := len(a.migrantAdmittedMonth)
	a.mu.RUnlock()
	t.Logf("ACCEPTED nextMigrantID=%d -> %d entries in %v", sample, got, el.Round(time.Millisecond))
	t.Logf("HISTORICAL (pre-fix `>` behaviour, no longer reachable): accepting nextMigrantID=%d itself would have extrapolated to ~%d map entries, ~%.0f s of decode and ~%.1f GB of heap — closed by switching the check to `>=`",
		uint64(migrantCounterCeiling), migrantCounterCeiling-1,
		el.Seconds()*float64(migrantCounterCeiling)/float64(sample),
		45.0*float64(migrantCounterCeiling)/float64(sample)/1024)
}
