package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// attack_bug725_round_test.go — INDEPENDENT DESTRUCTIVE ROUND
// (opus-round-bug725, attacker != author) against BUG-725's over-length
// handoff-cursor self-correction. Round verdict: ACCEPT with fixes — the
// round's P2 (full-stream read repeated every caught-up month) and two P3s
// (a discarded DeathHandoff read error; the intakeLocked comment
// mis-attributing which entity has no early return) are closed in
// compose.go/api.go/errors.go; TestAttackBUG725_FullStreamReadRunsOncePerLoad
// below is this round's perf finding, adapted from a log-only defect-pin
// into an assertion that the fix (simState.handoffCursorCheckDone) holds.

const b725Seed = uint64(725901)

// b725Entries returns every MET-G5452 entry in the recent ring whose
// correlation id matches cid, for the given correlation id.
func b725Entries(cid string) []errs.Entry {
	var out []errs.Entry
	for _, e := range errs.Recent() {
		if e.Code == deathservices.ErrCorruptHandoffCursor && e.CorrelationID == cid {
			out = append(out, e)
		}
	}
	return out
}

// TestAttackBUG725_BoundaryLenVsLenPlusOne is attack angle 2: the
// detection predicate must NOT fire on a legitimately caught-up cursor
// equal to the stream length (which would re-read the whole stream every
// single month: a per-month O(stream) cost AND an unnecessary duplicate
// storm), and MUST fire at len+1 — the smallest impossible value, not
// just the huge/MaxInt64 shapes the author's own test covers.
func TestAttackBUG725_BoundaryLenVsLenPlusOne(t *testing.T) {
	cid := errs.NewCorrelationID()
	api := buildGuaranteedDeathCitizensAPI(t, b725Seed)
	e := core.NewEngine(core.WithWorldSeed(b725Seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	for i := 0; i < 2; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	ds := comp.DeathServices()
	st := comp.state
	// Drive the composition's OWN correlation id so the ring entries the
	// production path writes are attributable.
	prodCID := st.cid

	handoff, err := st.citizens.DeathHandoff(cid)
	if err != nil {
		t.Fatalf("DeathHandoff: %v", err)
	}
	L := int64(len(handoff))
	if L == 0 {
		t.Fatal("fixture: no deaths realised")
	}
	cursor, released, _ := rr2State(t, ds, cid)
	if cursor != L {
		t.Fatalf("fixture: expected a caught-up cursor %d, got %d", L, cursor)
	}

	// --- CASE cursor == len: an extra intake call on a caught-up module
	// must be a pure no-op: no reset, no MET-G5452, no re-intake.
	beforeEntries := len(b725Entries(prodCID))
	if err := st.intakeDeathServices(3); err != nil {
		t.Fatalf("intakeDeathServices (caught up): %v", err)
	}
	c2, r2, _ := rr2State(t, ds, cid)
	if c2 != L || r2 != released {
		t.Fatalf("cursor==len was NOT treated as caught-up: cursor %d->%d, released %d->%d", L, c2, released, r2)
	}
	if got := len(b725Entries(prodCID)); got != beforeEntries {
		t.Fatalf("cursor==len raised %d new MET-G5452 entries — a legitimately caught-up cursor must never be reported corrupt", got-beforeEntries)
	}

	// --- CASE cursor == len+1: push the cursor exactly one past the end
	// by handing IntakeFromHandoff a single ALREADY-INTAKEN death (the
	// dedup skips it, so nothing but the cursor moves).
	dup := []citizens.RealisedDeath{{CitizenID: handoff[0].CitizenID, DeathMonth: handoff[0].DeathMonth}}
	if _, ierr := ds.IntakeFromHandoff(dup, cid); ierr != nil && !deathservices.IsDuplicateDeath(ierr) {
		t.Fatalf("IntakeFromHandoff(dup): %v", ierr)
	}
	c3, r3, _ := rr2State(t, ds, cid)
	if c3 != L+1 {
		t.Fatalf("setup: wanted cursor len+1=%d, got %d", L+1, c3)
	}
	if r3 != released {
		t.Fatalf("setup: the duplicate was APPLIED (released %d -> %d) — dedup did not absorb it", released, r3)
	}

	beforeEntries = len(b725Entries(prodCID))
	if err := st.intakeDeathServices(4); err != nil {
		t.Fatalf("intakeDeathServices (len+1): %v", err)
	}
	c4, r4, _ := rr2State(t, ds, cid)
	if c4 != L {
		t.Fatalf("cursor==len+1 did not self-correct back to the stream length: got %d want %d", c4, L)
	}
	if r4 != released {
		t.Fatalf("the reset-to-0 re-read DOUBLE-COUNTED bodies: released %d -> %d (stream length %d) — the dedup did not absorb the replay", released, r4, L)
	}
	ents := b725Entries(prodCID)
	if len(ents) <= beforeEntries {
		t.Fatalf("cursor==len+1 raised NO MET-G5452 — the smallest impossible cursor is undetected")
	}
	last := ents[len(ents)-1]
	if last.Ctx["direction"] != "over_length" {
		t.Fatalf("MET-G5452 direction = %v, want over_length (ctx=%v)", last.Ctx["direction"], last.Ctx)
	}
	for _, k := range []string{"handoffCursor", "streamLength", "clampedTo"} {
		if _, ok := last.Ctx[k]; !ok {
			t.Fatalf("MET-G5452 over_length ctx missing %q: %v", k, last.Ctx)
		}
	}
	t.Logf("boundary held: cursor==%d no-op, cursor==%d reset+re-read to %d, released steady at %d, ctx=%v", L, L+1, c4, r4, last.Ctx)
}

// TestAttackBUG725_CorruptRestoreIsDeterministic is attack angle 5: two
// identical runs through the same corrupt (over-length cursor) restore must
// produce byte-identical composition state digests.
func TestAttackBUG725_CorruptRestoreIsDeterministic(t *testing.T) {
	_, compA, _ := rr2Composition(t, b725Seed, 2)
	root := t.TempDir()
	if err := compA.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	rewriteDeathServicesCursor(t, root, 1<<40)

	run := func() ([32]byte, int64, int64) {
		api := buildGuaranteedDeathCitizensAPI(t, b725Seed)
		e := core.NewEngine(core.WithWorldSeed(b725Seed), core.WithPoolSize(1))
		comp, err := Wire(e, &Deps{Citizens: api})
		if err != nil {
			t.Fatalf("Wire: %v", err)
		}
		if err := comp.Load(root); err != nil {
			t.Fatalf("Load: %v", err)
		}
		for i := 0; i < 3; i++ {
			advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
		}
		cid := errs.NewCorrelationID()
		cur, rel, _ := rr2State(t, comp.DeathServices(), cid)
		return comp.StateDigest(), cur, rel
	}
	d1, c1, r1 := run()
	d2, c2, r2 := run()
	if d1 != d2 {
		t.Fatalf("corrupt-restore runs diverged: digest %x vs %x", d1, d2)
	}
	if c1 != c2 || r1 != r2 {
		t.Fatalf("corrupt-restore runs diverged: cursor %d/%d released %d/%d", c1, c2, r1, r2)
	}
	t.Logf("corrupt restore deterministic: digest=%x cursor=%d released=%d", d1[:8], c1, r1)
}

// TestAttackBUG725_FullStreamReadRunsOncePerLoad is attack angle (perf),
// adapted from the round's original log-only defect-pin
// (TestAttackBUG725_FullStreamReadRunsEveryCaughtUpMonth, which merely
// logged "24 caught-up calls, no once-per-load guard, cost recurs for the
// life of the city") into an assertion that the P2 fix
// (simState.handoffCursorCheckDone, compose.go) actually holds: the
// once-per-load gate flips true on the FIRST caught-up call and every
// subsequent caught-up call in the same load's lifetime is a true no-op
// against it — driving 24 more caught-up months raises no new MET-G5452
// entries and never flips the gate back to false (only a fresh
// Composition.Load/LoadAt may do that).
func TestAttackBUG725_FullStreamReadRunsOncePerLoad(t *testing.T) {
	api := buildGuaranteedDeathCitizensAPI(t, b725Seed)
	e := core.NewEngine(core.WithWorldSeed(b725Seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{Citizens: api})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	for i := 0; i < 2; i++ {
		advanceInChunks(t, e, int64(core.DailyTicksPerMonth))
	}
	st := comp.state
	if st.handoffCursorCheckDone {
		t.Fatal("fixture: handoffCursorCheckDone already true before any caught-up intake call ran")
	}
	handoff, err := st.citizens.DeathHandoff(st.cid)
	if err != nil {
		t.Fatalf("DeathHandoff: %v", err)
	}
	if len(handoff) == 0 {
		t.Fatal("fixture: no deaths realised")
	}

	// A caught-up module: the FIRST of these calls takes the empty-page
	// branch and performs the one-time full-stream check, flipping the
	// gate. Every one thereafter must find it already flipped and skip
	// the read entirely — the once-per-load guarantee the P2 fix adds.
	beforeEntries := len(b725Entries(st.cid))
	for i := 0; i < 24; i++ {
		if err := st.intakeDeathServices(int64(10 + i)); err != nil {
			t.Fatalf("intakeDeathServices (call %d): %v", i, err)
		}
		if !st.handoffCursorCheckDone {
			t.Fatalf("handoffCursorCheckDone still false after caught-up call %d — the once-per-load gate never fired", i)
		}
	}
	if got := len(b725Entries(st.cid)); got != beforeEntries {
		t.Fatalf("24 caught-up calls against an in-range cursor raised %d new MET-G5452 entries — the once-per-load check must run at most once and find nothing wrong every time", got-beforeEntries)
	}

	// A fresh Load re-opens exactly one more check window (save_wire.go's
	// reset), not an unbounded one.
	root := t.TempDir()
	if err := comp.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	api2 := buildGuaranteedDeathCitizensAPI(t, b725Seed)
	e2 := core.NewEngine(core.WithWorldSeed(b725Seed), core.WithPoolSize(1))
	comp2, err := Wire(e2, &Deps{Citizens: api2})
	if err != nil {
		t.Fatalf("Wire 2: %v", err)
	}
	if err := comp2.Load(root); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if comp2.state.handoffCursorCheckDone {
		t.Fatal("a freshly loaded composition started with handoffCursorCheckDone already true — Load must reset the gate")
	}
	t.Logf("once-per-load gate held: 24 caught-up calls after the first flip raised 0 new MET-G5452 entries against a %d-entry stream; a fresh Load re-opens exactly one more check", len(handoff))
}
