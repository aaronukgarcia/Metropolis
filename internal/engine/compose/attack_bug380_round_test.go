package compose

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// attack_bug380_round_test.go — INDEPENDENT DESTRUCTIVE ROUND
// (opus-round-bug380, attacker != author) against BUG-380's tenure-grace
// widening of residentIDs(). Attack angles: save/restore mid-tenure
// differential, determinism of the new wire slice, unbounded growth of
// the never-pruned migrantAdmittedMonth map, and the enumeration's own
// off-by-one claims.

const b380Seed = uint64(4242)

// b380AttractWire decodes the attract participant's emitted JSON so the
// tenure slice can be inspected structurally (not just byte-compared).
func b380AttractWire(t *testing.T, comp *Composition) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(attractStateJSON(t, comp)), &m); err != nil {
		t.Fatalf("unmarshal attract wire: %v", err)
	}
	return m
}

// TestAttack380_SaveRestoreMidTenureDifferential — ATTACK (2). A city
// saved MID-TENURE (migrants admitted, none yet past the 12-month grace)
// must, after restore, behave byte-identically to a never-saved control
// run to the same month: same attract wire (including every
// migrantAdmittedMonths entry) and the same whole-composition StateDigest.
// A dropped/reset tenure map would make restored migrants either instantly
// eligible (departing earlier than the control) or freshly graced
// (departing later) — both visible here.
func TestAttack380_SaveRestoreMidTenureDifferential(t *testing.T) {
	const saveMonths = 6
	const totalMonths = 30

	// Control: never saved.
	eRef, compRef := newTestEngine(t, b380Seed)
	advanceInChunks(t, eRef, totalMonths*int64(core.DailyTicksPerMonth))

	// Subject: save at month 6, restore into a fresh composition, run on.
	eA, compA := newTestEngine(t, b380Seed)
	advanceInChunks(t, eA, saveMonths*int64(core.DailyTicksPerMonth))

	wireAtSave := b380AttractWire(t, compA)
	entries, _ := wireAtSave["migrantAdmittedMonths"].([]any)
	if len(entries) == 0 {
		t.Fatalf("fixture is VACUOUS: no migrants admitted by month %d (wire=%v) — a tenure round-trip cannot be proved with an empty map", saveMonths, wireAtSave)
	}
	t.Logf("save point month %d: %d tenure entries persisted", saveMonths, len(entries))

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clockA, err := eA.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}

	eB, compB := newTestEngine(t, b380Seed)
	if err := compB.LoadAt(dir, clockA.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if got, want := attractStateJSON(t, compB), attractStateJSON(t, compA); got != want {
		t.Fatalf("attract wire did not round-trip at the save point:\n got=%s\nwant=%s", got, want)
	}

	advanceInChunks(t, eB, (totalMonths-saveMonths)*int64(core.DailyTicksPerMonth))

	if got, want := attractStateJSON(t, compB), attractStateJSON(t, compRef); got != want {
		t.Fatalf("restored city DIVERGED from a never-saved control after running past the tenure grace:\n restored=%s\n  control=%s", got, want)
	}
	if got, want := compB.StateDigest(), compRef.StateDigest(); got != want {
		t.Fatalf("StateDigest diverged across the save/restore boundary: restored=%x control=%x", got, want)
	}
}

// TestAttack380_WireDeterministicAndSorted — ATTACK (3), GR#21. Two
// identical-seed runs must emit BYTE-IDENTICAL attract wire (the tenure
// slice's Go map iteration order must never leak), and the emitted slice
// must be strictly ascending by id.
func TestAttack380_WireDeterministicAndSorted(t *testing.T) {
	const months = 48
	run := func() string {
		e, comp := newTestEngine(t, b380Seed)
		advanceInChunks(t, e, months*int64(core.DailyTicksPerMonth))
		return attractStateJSON(t, comp)
	}
	a, b := run(), run()
	if a != b {
		t.Fatalf("two identical-seed %d-month runs emitted DIFFERENT attract wire:\n a=%s\n b=%s", months, a, b)
	}

	var decoded struct {
		MigrantAdmittedMonths []struct {
			ID            uint64 `json:"id"`
			AdmittedMonth int64  `json:"admittedMonth"`
		} `json:"migrantAdmittedMonths"`
		NextMigrantID uint64 `json:"nextMigrantID"`
	}
	if err := json.Unmarshal([]byte(a), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.MigrantAdmittedMonths) == 0 {
		t.Fatalf("VACUOUS: no tenure entries after %d months", months)
	}
	for i := 1; i < len(decoded.MigrantAdmittedMonths); i++ {
		if decoded.MigrantAdmittedMonths[i].ID <= decoded.MigrantAdmittedMonths[i-1].ID {
			t.Fatalf("tenure wire is not strictly ascending by id at index %d: %d after %d",
				i, decoded.MigrantAdmittedMonths[i].ID, decoded.MigrantAdmittedMonths[i-1].ID)
		}
	}
	t.Logf("%d months: %d tenure entries, nextMigrantID=%d, wire bytes=%d",
		months, len(decoded.MigrantAdmittedMonths), decoded.NextMigrantID, len(a))
}

// TestAttack380_TenureMapStaysBoundedToLiveMigrantsPlusOrphanSlack —
// ATTACK (4), INVERTED into a bound test after the round's fix (BUG-380
// round finding P2, opus-round-bug380) and TIGHTENED to an EXACT bound
// after the re-round (opus-reround-bug380 finding P1): pruneMigrantTenure
// (migration.go) deletes an entry the instant applyEmigration itself
// removes a migrant, and sweepDepartedMigrantTenure (migration.go, called
// at the top of every ApplyMigration) additionally checks EVERY tenured
// migrant's liveness via the already-registered engine.attract ->
// engine.citizens CitizenAt edge and prunes any that no longer resolve —
// closing the natural-mortality orphan gap the round-1 fix's
// pruneMigrantTenure alone could not see (that residual was measured
// growing ~linearly, 45 orphans by month 200 on the re-round's own probe,
// TestReround380_OrphanGrowthOverLongRun — NOT the small constant a fixed
// orphanSlack could safely bound). This test now asserts entries ==
// liveMigrants EXACTLY (slack 0) over a 200-month run — not the unbounded,
// grows-forever-with-cumulative-admissions law the pre-round-1 map
// exhibited (388 entries at 389 cumulative admissions, 153 of them — 39.4%
// — already dead), and not the still-open-ended orphan residual the
// round-1-only fix left.
func TestAttack380_TenureMapStaysBoundedToLiveMigrantsPlusOrphanSlack(t *testing.T) {
	const months = 200

	e, comp := newTestEngine(t, b380Seed)
	admitted := uint64(0)
	sawNonZeroAdmissions := false
	for m := int64(1); m <= months; m++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		admitted = comp.state.attract.MigrantsAdmitted()
		if admitted > 0 {
			sawNonZeroAdmissions = true
		}

		wire := attractStateJSON(t, comp)
		var decoded struct {
			MigrantAdmittedMonths []struct {
				ID uint64 `json:"id"`
			} `json:"migrantAdmittedMonths"`
		}
		if err := json.Unmarshal([]byte(wire), &decoded); err != nil {
			t.Fatalf("month %d: unmarshal: %v", m, err)
		}

		// liveMigrants: independently walked via CitizenAt against the
		// real citizen store — never derived from the tenure map itself,
		// so this bound cannot be satisfied by a map that simply never
		// grew in the first place (e.g. a broken recordMigrantAdmission
		// that stopped recording admissions would trivially pass a naive
		// bound; comparing against a fixture-independent live count
		// catches that too).
		live := 0
		for _, id := range migrantIDsFromCount(admitted) {
			if _, ok := comp.state.citizens.CitizenAt(id, comp.state.cid); ok {
				live++
			}
		}

		entries := len(decoded.MigrantAdmittedMonths)
		if entries != live {
			t.Fatalf("month %d: tenure entries=%d != liveMigrants=%d — pruning is not keeping the map in exact sync with the live population (BUG-380 P1 re-round regression)", m, entries, live)
		}
	}
	if !sawNonZeroAdmissions {
		t.Fatalf("VACUOUS: no admissions across %d months", months)
	}
	t.Logf("EXACT BOUND HELD over %d months: tenure entries == liveMigrants at every sampled month, final cumulative admissions=%d", months, admitted)
}

// TestAttack380_MigrantIDsFromCountLowerBoundClaim — ATTACK (6)/(the doc
// claim). migrantIDsFromCount's own doc comment and its test's
// PROOF-THIS-CAN-FAIL both assert the PRE-FIX formula [base+1, base+M]
// "always excludes the true top id (MigrantIDBase+migrants)". That claim is
// arithmetically false: the pre-fix loop ran i=1..migrants inclusive, so it
// ALREADY contained base+migrants. This test pins the true delta — the fix
// removes exactly ONE phantom id at the bottom and changes nothing at the
// top — so the misattribution in the shipped comments is visible to the
// next reader rather than inherited as fact.
func TestAttack380_MigrantIDsFromCountLowerBoundClaim(t *testing.T) {
	for _, migrants := range []uint64{1, 2, 3, 10, 1000} {
		got := migrantIDsFromCount(migrants)

		// Reconstruct the PRE-FIX formula verbatim.
		var pre []uint64
		for i := uint64(1); i <= migrants; i++ {
			pre = append(pre, attract.MigrantIDBase+i)
		}

		inPre := map[uint64]bool{}
		for _, id := range pre {
			inPre[id] = true
		}
		for _, id := range got {
			if !inPre[id] {
				t.Fatalf("migrants=%d: post-fix id %d was NOT in the pre-fix range — the fix ADDS ids, contradicting 'the pre-fix range was a strict superset'", migrants, id)
			}
		}
		if migrants >= 1 {
			top := attract.MigrantIDBase + migrants
			if !inPre[top] {
				t.Fatalf("migrants=%d: the pre-fix range genuinely lacked the top id %d", migrants, top)
			}
		}
		if len(pre)-len(got) != 1 {
			t.Fatalf("migrants=%d: pre-fix had %d ids, post-fix %d — expected EXACTLY one removed (the base+1 phantom)", migrants, len(pre), len(got))
		}
	}
	t.Log("CONFIRMED: the pre-fix range [base+1, base+M] was a strict SUPERSET of the true minted set — it never excluded the most-recently-admitted migrant. The shipped doc comments on migrantIDsFromCount(), liveResidentIDs(), and TestMigrantIDsFromCount_MatchesRealMintedRange's PROOF-THIS-CAN-FAIL that claim an off-by-one at BOTH ends are wrong at the top end.")
}

// ===================== RE-ROUND (opus-reround-bug380) =====================

// TestReround380_PruningIsUndoneByTheOldSaveBackfill — the decisive
// re-round attack on the P2 "fix". pruneMigrantTenure deletes a departed
// migrant's entry, but applyLoadRecord's backfill loop still runs
// i = 2..NextMigrantID and re-inserts an entry for EVERY id the map lacks —
// which, after pruning, is exactly the set of migrants that already
// departed. So a save taken after any emigration has occurred re-inflates
// the map to its full pre-prune size on load, and the loaded city then
// diverges from a never-saved control.
func TestReround380_PruningIsUndoneByTheOldSaveBackfill(t *testing.T) {
	const saveMonths = 30 // well past the first emigration wave (~m13)

	eRef, compRef := newTestEngine(t, b380Seed)
	advanceInChunks(t, eRef, saveMonths*int64(core.DailyTicksPerMonth))

	countEntries := func(comp *Composition) int {
		var d struct {
			MigrantAdmittedMonths []struct {
				ID uint64 `json:"id"`
			} `json:"migrantAdmittedMonths"`
			NextMigrantID uint64 `json:"nextMigrantID"`
		}
		if err := json.Unmarshal([]byte(attractStateJSON(t, comp)), &d); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return len(d.MigrantAdmittedMonths)
	}

	preSave := countEntries(compRef)
	admitted := compRef.state.attract.MigrantsAdmitted()
	if int(admitted)-1 <= preSave {
		t.Fatalf("VACUOUS: no pruning happened by month %d (%d entries vs %d cumulative admissions) — this attack needs departures to have occurred", saveMonths, preSave, admitted-1)
	}
	t.Logf("month %d: %d cumulative admissions, %d tenure entries live (=> %d already pruned)", saveMonths, admitted-1, preSave, int(admitted)-1-preSave)

	dir := t.TempDir()
	if err := compRef.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clock, err := eRef.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	_, compB := newTestEngine(t, b380Seed)
	if err := compB.LoadAt(dir, clock.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	postLoad := countEntries(compB)
	t.Logf("after LoadAt: %d tenure entries (was %d at save)", postLoad, preSave)

	if postLoad != preSave {
		t.Fatalf("PRUNING UNDONE BY LOAD: the save carried %d tenure entries but the restored city holds %d — applyLoadRecord's backfill loop (i=2..NextMigrantID) re-inserted an entry for every PRUNED (departed) migrant at LastAdvancedMonth, so the P2 prune is erased by any save/load round trip and the map returns to growing with CUMULATIVE admissions", preSave, postLoad)
	}
}

// TestReround380_SaveRestoreAfterPruningDifferential — the round-1
// differential re-aimed at a save point AFTER emigration has pruned
// entries (the original saved at month 6, before any departure, so it
// never exercised the prune/backfill interaction).
func TestReround380_SaveRestoreAfterPruningDifferential(t *testing.T) {
	const saveMonths = 30
	const totalMonths = 48

	eRef, compRef := newTestEngine(t, b380Seed)
	advanceInChunks(t, eRef, totalMonths*int64(core.DailyTicksPerMonth))

	eA, compA := newTestEngine(t, b380Seed)
	advanceInChunks(t, eA, saveMonths*int64(core.DailyTicksPerMonth))
	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clockA, err := eA.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	eB, compB := newTestEngine(t, b380Seed)
	if err := compB.LoadAt(dir, clockA.Tick()); err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if got, want := attractStateJSON(t, compB), attractStateJSON(t, compA); got != want {
		t.Fatalf("attract wire did not round-trip at a POST-PRUNE save point:\n got=%s\nwant=%s", got, want)
	}
	advanceInChunks(t, eB, (totalMonths-saveMonths)*int64(core.DailyTicksPerMonth))
	if got, want := attractStateJSON(t, compB), attractStateJSON(t, compRef); got != want {
		t.Fatalf("restored city DIVERGED from a never-saved control (post-prune save point):\n restored=%s\n  control=%s", got, want)
	}
	if got, want := compB.StateDigest(), compRef.StateDigest(); got != want {
		t.Fatalf("StateDigest diverged (post-prune save point): restored=%x control=%x", got, want)
	}
}

// TestReround380_OrphanGrowthOverLongRun — quantifies the natural-mortality
// orphan residual the author's bound test allows 20 slack for. If orphans
// grow with time rather than sitting at a small constant, the slack is
// arbitrary and the bound test will red spuriously (or stop bounding).
func TestReround380_OrphanGrowthOverLongRun(t *testing.T) {
	e, comp := newTestEngine(t, b380Seed)
	st := comp.state
	type row struct{ month, entries, live, orphans int }
	var rows []row
	for m := int64(1); m <= 200; m++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		if m%25 != 0 {
			continue
		}
		var d struct {
			MigrantAdmittedMonths []struct {
				ID uint64 `json:"id"`
			} `json:"migrantAdmittedMonths"`
		}
		if err := json.Unmarshal([]byte(attractStateJSON(t, comp)), &d); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		live, orph := 0, 0
		for _, ent := range d.MigrantAdmittedMonths {
			if _, ok := st.citizens.CitizenAt(ent.ID, st.cid); ok {
				live++
			} else {
				orph++
			}
		}
		rows = append(rows, row{int(m), len(d.MigrantAdmittedMonths), live, orph})
	}
	for _, r := range rows {
		t.Logf("month %3d: entries=%5d live=%5d ORPHANS=%5d", r.month, r.entries, r.live, r.orphans)
	}
	last := rows[len(rows)-1]
	if last.orphans > 20 {
		t.Fatalf("ORPHAN SLACK BLOWN: %d orphans at month %d — the author's bound test allows only 20, so it will red spuriously on any long run; the natural-mortality residual is NOT a small constant", last.orphans, last.month)
	}
}
