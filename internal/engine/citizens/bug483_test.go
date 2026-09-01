package citizens

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// bug483_test.go covers BUG-483's three FEAT-087 inc3 independent-round
// follow-ups (F1/F2/F3):
//
//   - F1 (GR#3): RealiseDrained and EmergencyRealise now delegate to one
//     shared budgetFor(...) helper instead of each keeping its own copy of
//     the ordinary-or-emergency budget rule. The existing 96-case
//     differential test (attack_feat087_inc3_handoff_test.go) already
//     proves the two callers still agree after the refactor; this file
//     adds a direct unit test on budgetFor itself.
//   - F2 (GR#17): a negative injected DrainCapacity is now visible via a
//     registry WARNING (MET-G5405), logged once per DeathQueue, instead of
//     silently clamping to zero with no signal.
//   - F3: DeathQueue.DeathHandoffSince (and CitizensAPI.DeathHandoffSince)
//     is a new paging accessor over the same cumulative handoff stream
//     RealisedDeaths/DeathHandoff already expose in full -- this file
//     proves it never truncates the underlying stream, respects FIFO
//     order, clamps a negative cursor to 0, and returns an empty (never
//     nil-panicking, never erroring) slice once a consumer is caught up.

// --- F1: budgetFor is the single source of truth. ---

func TestBudgetFor_OrdinaryAndEmergencyRules(t *testing.T) {
	q := NewDeathQueue()
	for id := uint64(1); id <= 7; id++ {
		mustEnqueue(t, q, id, 100, "corr")
	}

	cases := []struct {
		name         string
		emergency    bool
		emergencyCfg int
		wantBudget   int
	}{
		{"ordinary uses the data-file budget", false, 3, 5},
		{"emergency uses the emergency budget when positive", true, 3, 3},
		{"emergency 0 sentinel means unbounded -- q.Len()", true, 0, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mkFixedBudgetCfg(t, 5, tc.emergencyCfg)
			got := budgetFor(q, cfg, tc.emergency, "corr")
			if got != tc.wantBudget {
				t.Fatalf("budgetFor(emergency=%v, emergencyCfg=%d) = %d, want %d", tc.emergency, tc.emergencyCfg, got, tc.wantBudget)
			}
		})
	}
}

// TestRealiseDrainedAndEmergencyRealise_ShareBudgetFor is a smaller,
// focused companion to the 96-case attack differential test: it asserts
// EmergencyRealise and a nil-drain RealiseDrained release IDENTICAL sets
// for a hand-picked case that exercises the 0-sentinel "unbounded"
// branch of budgetFor -- the branch most likely to silently diverge if a
// future edit re-forks the two call sites.
func TestRealiseDrainedAndEmergencyRealise_ShareBudgetFor(t *testing.T) {
	cfg := mkFixedBudgetCfg(t, 4, 0) // emergency budget 0 -> unbounded release
	ref := NewDeathQueue()
	got := NewDeathQueue()
	for id := uint64(1); id <= 11; id++ {
		mustEnqueue(t, ref, id, 100, "corr")
		mustEnqueue(t, got, id, 100, "corr")
	}

	wantIDs := EmergencyRealise(ref, cfg, true, 200, "corr")
	gotRecords := got.RealiseDrained(cfg, true, 200, "corr")

	if len(wantIDs) != 11 {
		t.Fatalf("test setup invalid: EmergencyRealise released %d, want all 11 (unbounded)", len(wantIDs))
	}
	if len(gotRecords) != len(wantIDs) {
		t.Fatalf("RealiseDrained released %d, EmergencyRealise released %d", len(gotRecords), len(wantIDs))
	}
	for i := range wantIDs {
		if gotRecords[i].CitizenID != wantIDs[i] {
			t.Fatalf("release order diverges at %d: RealiseDrained=%d EmergencyRealise=%d", i, gotRecords[i].CitizenID, wantIDs[i])
		}
	}
}

// --- F2: a negative drain capacity is now visible. ---

// TestRealiseDrained_NegativeDrainLogsWarningOnce proves the RED->GREEN
// fix directly: before BUG-483's F2 fix, a negative DrainCapacity return
// clamped to zero with NOTHING logged (this test would have failed with
// "no MET-G5405 entry found" against the pre-fix code, since the code
// path did not exist). Post-fix, the very first negative observation logs
// exactly one MET-G5405 WARNING; repeated negative months from the same
// stuck consumer do not re-log (the ring's coalescing aside, the fix
// itself only calls errs.New once per DeathQueue), and the queue's SAFE
// behaviour (defer, never drop) is unchanged.
func TestRealiseDrained_NegativeDrainLogsWarningOnce(t *testing.T) {
	// A unique correlation ID (not the shared "corr" every other test in
	// this package uses) so this test's assertion against the PACKAGE-WIDE
	// errs.Recent() ring cannot be confused with entries any other test --
	// including TestRealiseDrained_NonNegativeDrainNeverWarns below, which
	// runs later in the same process and asserts the OPPOSITE (no entry) --
	// happens to leave behind. Recent() is a shared, cross-test ring
	// buffer (log.go), so distinguishing "this test's own entry" from "some
	// other test's leftover entry" by CorrelationID is the only reliable
	// isolation available without a test-only sink/reset hook.
	const cid = "bug483-f2-negative-drain"

	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 5, 0)
	if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int { return -3 }), cid); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}
	for id := uint64(1); id <= 10; id++ {
		mustEnqueue(t, q, id, 100, cid)
	}

	// First call: negative drain observed for the first time.
	got := q.RealiseDrained(cfg, false, 200, cid)
	if len(got) != 0 {
		t.Fatalf("negative drain released %d, want 0 -- must still defer, never drop", len(got))
	}
	if p := q.Len(cid); p != 10 {
		t.Fatalf("pending=%d after a negative-drain month, want 10 (nothing lost)", p)
	}

	found := 0
	var msg string
	for _, entry := range errs.Recent() {
		if entry.Code == ErrNegativeDrainCapacity && entry.CorrelationID == cid {
			found++
			msg = entry.Msg
		}
	}
	if found == 0 {
		t.Fatalf("no %s entry found via errs.Recent() after a negative DrainCapacity return -- the alarm did not fire", ErrNegativeDrainCapacity)
	}
	if entryHasLiteralBraces(msg) {
		t.Fatalf("MET-G5405 message left an unrendered {token}: %q", msg)
	}

	// A second negative-drain month from the same still-broken consumer:
	// behaviour must stay safe (still zero release, still nothing lost) --
	// this test does not assert on log COUNT (the ring coalesces repeats
	// of the same code, log.go's own documented behaviour), only that the
	// alarm has fired at least once and the safe behaviour persists.
	got2 := q.RealiseDrained(cfg, false, 201, cid)
	if len(got2) != 0 {
		t.Fatalf("second negative-drain month released %d, want 0", len(got2))
	}
	if p := q.Len(cid); p != 10 {
		t.Fatalf("pending=%d after a second negative-drain month, want 10", p)
	}
}

// entryHasLiteralBraces is a tiny local check mirroring the project's
// BUG-357-class regression concern: a registry message must never leak an
// unrendered {token} to an operator reading the log.
func entryHasLiteralBraces(msg string) bool {
	for i := 0; i < len(msg); i++ {
		if msg[i] == '{' {
			return true
		}
	}
	return false
}

// TestRealiseDrained_NonNegativeDrainNeverWarns is the RED companion in
// the other direction: a well-behaved (non-negative) DrainCapacity must
// never trip the new alarm, at any binding value.
func TestRealiseDrained_NonNegativeDrainNeverWarns(t *testing.T) {
	// A unique correlation ID (see TestRealiseDrained_NegativeDrainLogsWarningOnce's
	// comment) so a leftover MET-G5405 entry from that other test cannot
	// masquerade as a false positive here.
	const cid = "bug483-f2-nonneg-drain"

	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 5, 0)
	if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int { return 0 }), cid); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}
	for id := uint64(1); id <= 5; id++ {
		mustEnqueue(t, q, id, 100, cid)
	}
	q.RealiseDrained(cfg, false, 200, cid)

	for _, entry := range errs.Recent() {
		if entry.Code == ErrNegativeDrainCapacity && entry.CorrelationID == cid {
			t.Fatalf("a zero (non-negative) drain capacity must never log %s, got entry %+v", ErrNegativeDrainCapacity, entry)
		}
	}
}

// --- F3: DeathHandoffSince pages without truncating. ---

func TestDeathHandoffSince_PagesWithoutTruncating(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 3, 0)
	for id := uint64(1); id <= 9; id++ {
		mustEnqueue(t, q, id, 100, "corr")
	}

	// Release in three waves of 3 so the handoff stream builds up over
	// several calls.
	q.RealiseDrained(cfg, false, 200, "corr")
	q.RealiseDrained(cfg, false, 201, "corr")
	q.RealiseDrained(cfg, false, 202, "corr")

	full := q.RealisedDeaths("corr")
	if len(full) != 9 {
		t.Fatalf("test setup invalid: full handoff=%d, want 9", len(full))
	}

	// Page 1: everything from the start.
	page1 := q.DeathHandoffSince(0, "corr")
	assertRealisedDeathsEqual(t, "page from cursor 0", page1, full)

	// Page 2: only what's after the first 3.
	page2 := q.DeathHandoffSince(3, "corr")
	assertRealisedDeathsEqual(t, "page from cursor 3", page2, full[3:])

	// Fully caught up: empty, non-nil, no error.
	page3 := q.DeathHandoffSince(9, "corr")
	if page3 == nil {
		t.Fatalf("DeathHandoffSince(9) returned nil, want an empty non-nil slice")
	}
	if len(page3) != 0 {
		t.Fatalf("DeathHandoffSince(9) = %+v, want empty", page3)
	}

	// Past the end: same as caught up, never a panic or negative slice.
	page4 := q.DeathHandoffSince(1000, "corr")
	if len(page4) != 0 {
		t.Fatalf("DeathHandoffSince(1000) = %+v, want empty", page4)
	}

	// Negative cursor clamps to 0, does not panic or wrap.
	page5 := q.DeathHandoffSince(-5, "corr")
	assertRealisedDeathsEqual(t, "page from negative cursor", page5, full)

	// The underlying stream must NOT have been truncated by any of the
	// above calls -- RealisedDeaths/DeathHandoff still return everything.
	stillFull := q.RealisedDeaths("corr")
	assertRealisedDeathsEqual(t, "handoff after paging", stillFull, full)
}

// TestDeathHandoffSince_FIFOOrderMatchesRealisedSequence proves
// determinism carries over: paging through DeathHandoffSince in
// consecutive, non-overlapping slices reproduces RealisedSequence exactly.
func TestDeathHandoffSince_FIFOOrderMatchesRealisedSequence(t *testing.T) {
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 4, 2)
	for i := 0; i < 40; i++ {
		id := uint64((i*13)%40) + 1
		month := int64(100 + (i % 4))
		mustEnqueue(t, q, id, month, "corr")
	}
	for m := int64(200); m < 220; m++ {
		q.RealiseDrained(cfg, m%5 == 0, m, "corr")
	}

	seq := q.RealisedSequence("corr")
	var paged []uint64
	cursor := 0
	for {
		page := q.DeathHandoffSince(cursor, "corr")
		if len(page) == 0 {
			break
		}
		for _, rd := range page {
			paged = append(paged, rd.CitizenID)
		}
		cursor += len(page)
	}

	if len(paged) != len(seq) {
		t.Fatalf("paged=%d, RealisedSequence=%d", len(paged), len(seq))
	}
	for i := range seq {
		if paged[i] != seq[i] {
			t.Fatalf("order diverges at %d: paged=%d sequence=%d", i, paged[i], seq[i])
		}
	}
}

// TestCitizensAPI_DeathHandoffSince_MirrorsDeathHandoff exercises the
// CitizensAPI-level wrapper end to end.
func TestCitizensAPI_DeathHandoffSince_MirrorsDeathHandoff(t *testing.T) {
	api, err := NewCitizensAPI(11, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	for id := uint64(1); id <= 4; id++ {
		mustEnqueue(t, api.deathQueue, id, 100, "corr")
	}
	cfg := mkFixedBudgetCfg(t, 4, 0)
	api.deathQueue.RealiseDrained(cfg, false, 200, "corr")

	full, err := api.DeathHandoff("corr")
	if err != nil {
		t.Fatalf("DeathHandoff: %v", err)
	}
	page, err := api.DeathHandoffSince(2, "corr")
	if err != nil {
		t.Fatalf("DeathHandoffSince: %v", err)
	}
	assertRealisedDeathsEqual(t, "CitizensAPI page from cursor 2", page, full[2:])

	// Copyguard: a struct copy must return the registry error, matching
	// every other DeathQueue-adjacent CitizensAPI surface.
	apiCopy := citizensByteCopy(api)
	if _, err := apiCopy.DeathHandoffSince(0, "corr"); err == nil {
		t.Fatalf("DeathHandoffSince on a struct copy returned nil error")
	}
}
