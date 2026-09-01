package citizens

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// attack_bug483_followups_test.go holds the INDEPENDENT destructive round's
// regressions for BUG-483 (FEAT-087 inc3 follow-ups F1/F2/F3). Each test
// here was written by the attacker (not the builder) against a mutation
// that the builder's own bug483_test.go did NOT catch, or against a
// property the builder claimed but never asserted.
//
// Round r1 findings these pin:
//
//   - F2-GAP (the headline): bug483_test.go's
//     TestRealiseDrained_NegativeDrainLogsWarningOnce is named "...Once"
//     but explicitly declines to assert the once-ness, on the stated
//     grounds that "the ring coalesces repeats of the same code". That
//     reasoning is wrong: errs.Entry carries a Repeat field precisely so
//     coalesced repeats stay countable (log.go, SEC-030/SEC-031(b)).
//     Consequently the whole point of F2's negativeDrainWarned field --
//     fire ONCE per queue, not once per month forever -- had zero test
//     coverage: deleting `&& !q.negativeDrainWarned` from RealiseDrained
//     left the entire package green. TestAttackBUG483_NegativeDrainWarns
//     ExactlyOncePerQueue closes that, and its sibling proves the guard is
//     PER-QUEUE (a second DeathQueue must still get its own alarm).
//   - F3-ALIAS: DeathHandoffSince must return a COPY, never a window into
//     q.handoff (the project's recurring aliasing lesson class).
//   - F3-COPYGUARD: the SEC-020 guard must be present on the new method at
//     BOTH the DeathQueue and CitizensAPI level.
//   - F1: budgetFor must be structurally shared -- a single mutation of the
//     helper must redden BOTH callers.

// --- F2: the once-per-queue alarm guard (the untested field). ---

// negDrain is a DrainCapacity that always returns a negative capacity --
// the exact "buggy FEAT-088 consumer" F2 exists to make visible. It counts
// its own calls so a test can prove RealiseDrained really did consult it
// every month (i.e. the once-only LOG is not an artefact of the drain
// simply never being asked again).
type negDrain struct{ calls int }

func (d *negDrain) MonthlyDrainCapacity(int64) int { d.calls++; return -7 }

// captureErrs installs a temporary NDJSON errs sink for the duration of
// one test and returns a counter over the entries it captured.
//
// Counting firings via errs.Recent() -- which is what bug483_test.go
// does -- CANNOT work for a once-only assertion: the in-memory ring
// coalesces by Code ALONE (log.go, SEC-030/SEC-031(b)), so every
// MET-G5405 in the whole package folds into one slot whose CorrelationID
// and Ctx reflect only the MOST RECENT occurrence, and whose Repeat count
// mixes in every OTHER test's firings of the same code. A Logger sink
// never coalesces (explicitly documented on Entry.Repeat), so one line
// per errs.New call is the only exact, test-isolated count available.
func captureErrs(t *testing.T) func(code, correlationID string) int {
	t.Helper()
	var buf bytes.Buffer
	logger := errs.NewLogger(&buf)
	if err := errs.SetSink(logger); err != nil {
		t.Fatalf("errs.SetSink: %v", err)
	}
	t.Cleanup(func() { _ = errs.SetSink(nil) })

	return func(code, correlationID string) int {
		n := 0
		for _, line := range strings.Split(buf.String(), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var e errs.Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatalf("sink emitted a non-JSON line %q: %v", line, err)
			}
			if e.Code == code && (correlationID == "" || e.CorrelationID == correlationID) {
				n++
			}
		}
		return n
	}
}

// TestAttackBUG483_NegativeDrainWarnsExactlyOncePerQueue is the mutation
// regression the builder's suite was missing. Verified to FAIL (total=12,
// want 1) with `&& !q.negativeDrainWarned` deleted from RealiseDrained,
// and to PASS against the shipped code.
func TestAttackBUG483_NegativeDrainWarnsExactlyOncePerQueue(t *testing.T) {
	const cid = "attack-bug483-once-per-queue"
	count := captureErrs(t)

	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 5, 0)
	d := &negDrain{}
	if err := q.SetDrainCapacity(d, cid); err != nil {
		t.Fatalf("SetDrainCapacity: %v", err)
	}
	for id := uint64(1); id <= 20; id++ {
		mustEnqueue(t, q, id, 100, cid)
	}

	// Twelve consecutive months with the same still-broken consumer.
	for m := int64(200); m < 212; m++ {
		if got := q.RealiseDrained(cfg, false, m, cid); len(got) != 0 {
			t.Fatalf("month %d: negative drain released %d, want 0", m, len(got))
		}
	}

	// The drain really was consulted every month -- so a per-call alarm
	// would genuinely have had twelve opportunities to fire.
	if d.calls != 12 {
		t.Fatalf("MonthlyDrainCapacity called %d times, want 12 (the drain must be consulted every month)", d.calls)
	}
	// Nothing was dropped: the safe behaviour F2 preserves.
	if p := q.Len(cid); p != 20 {
		t.Fatalf("pending=%d after 12 negative-drain months, want 20 (nothing lost)", p)
	}

	switch got := count(ErrNegativeDrainCapacity, cid); got {
	case 0:
		t.Fatalf("no %s entry found -- the alarm never fired at all", ErrNegativeDrainCapacity)
	case 1: // correct: once per queue
	default:
		t.Fatalf("%s fired %d times for ONE DeathQueue over 12 negative-drain months, want exactly 1 -- "+
			"the negativeDrainWarned guard is not suppressing repeats (log spam for as long as "+
			"the FEAT-088 consumer stays broken)", ErrNegativeDrainCapacity, got)
	}
}

// TestAttackBUG483_NegativeDrainGuardIsPerQueueNotGlobal proves the
// suppression is scoped to the DeathQueue instance. A guard accidentally
// promoted to a package-level sync.Once (an easy "simplification") would
// silence a SECOND, independently broken queue entirely -- strictly worse
// than the pre-fix silence this whole item exists to end.
func TestAttackBUG483_NegativeDrainGuardIsPerQueueNotGlobal(t *testing.T) {
	cfg := mkFixedBudgetCfg(t, 5, 0)
	count := captureErrs(t)

	for i, cid := range []string{"attack-bug483-perqueue-a", "attack-bug483-perqueue-b"} {
		q := NewDeathQueue()
		if err := q.SetDrainCapacity(&negDrain{}, cid); err != nil {
			t.Fatalf("queue %d SetDrainCapacity: %v", i, err)
		}
		mustEnqueue(t, q, uint64(i+1), 100, cid)
		q.RealiseDrained(cfg, false, 200, cid)
		q.RealiseDrained(cfg, false, 201, cid)

		if got := count(ErrNegativeDrainCapacity, cid); got != 1 {
			t.Fatalf("queue %d (%s): %s fired %d times, want exactly 1 -- "+
				"each DeathQueue must raise its OWN alarm exactly once", i, cid, ErrNegativeDrainCapacity, got)
		}
	}
	if got := count(ErrNegativeDrainCapacity, ""); got != 2 {
		t.Fatalf("%s fired %d times across TWO independently-broken DeathQueues, want 2 -- "+
			"the suppression must be per-queue, never process-global", ErrNegativeDrainCapacity, got)
	}
}

// TestAttackBUG483_RemovedClampIsObservationallyIdentical is F2's
// dead-code claim under attack: the deleted `if d < 0 { d = 0 }` must have
// changed nothing observable. A negative effective budget is fed straight
// into realiseLocked, so this pins that the negative path and the zero
// path agree on EVERY observable (releases, pending, realised sequence,
// handoff) including at math.MinInt, where a careless `-d` or a
// len()-relative index would overflow or panic.
func TestAttackBUG483_RemovedClampIsObservationallyIdentical(t *testing.T) {
	cfg := mkFixedBudgetCfg(t, 4, 0)

	build := func(cap int) *DeathQueue {
		const cid = "attack-bug483-clamp"
		q := NewDeathQueue()
		if err := q.SetDrainCapacity(DrainCapacityFunc(func(int64) int { return cap }), cid); err != nil {
			t.Fatalf("SetDrainCapacity(%d): %v", cap, err)
		}
		for id := uint64(1); id <= 6; id++ {
			mustEnqueue(t, q, id, 100, cid)
		}
		for m := int64(200); m < 203; m++ {
			q.RealiseDrained(cfg, false, m, cid)
		}
		return q
	}

	zero := build(0)
	for _, cap := range []int{-1, -6, -1000, math.MinInt} {
		neg := build(cap) // must not panic, must not index negatively
		const cid = "attack-bug483-clamp"
		if a, b := neg.Len(cid), zero.Len(cid); a != b {
			t.Fatalf("drain=%d pending=%d, drain=0 pending=%d -- the removed clamp was NOT dead code", cap, a, b)
		}
		if a, b := neg.TotalRealised(cid), zero.TotalRealised(cid); a != b {
			t.Fatalf("drain=%d totalRealised=%d, drain=0 totalRealised=%d", cap, a, b)
		}
		if a, b := len(neg.RealisedSequence(cid)), len(zero.RealisedSequence(cid)); a != b {
			t.Fatalf("drain=%d realisedSequence len=%d, drain=0 len=%d", cap, a, b)
		}
		assertRealisedDeathsEqual(t, "negative-drain handoff vs zero-drain handoff",
			neg.RealisedDeaths(cid), zero.RealisedDeaths(cid))
	}
}

// --- F3: DeathHandoffSince must hand out a copy, and stay guarded. ---

// TestAttackBUG483_DeathHandoffSinceReturnsCopyNotAlias is the project's
// recurring aliasing lesson applied to the new accessor: a caller that
// scribbles on its page must not corrupt the queue's own stream, and two
// overlapping pages must not alias each other.
func TestAttackBUG483_DeathHandoffSinceReturnsCopyNotAlias(t *testing.T) {
	const cid = "attack-bug483-alias"
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 6, 0)
	for id := uint64(1); id <= 6; id++ {
		mustEnqueue(t, q, id, 100, cid)
	}
	q.RealiseDrained(cfg, false, 200, cid)

	before := q.RealisedDeaths(cid)
	if len(before) != 6 {
		t.Fatalf("setup: handoff=%d, want 6", len(before))
	}

	page := q.DeathHandoffSince(2, cid)
	if len(page) != 4 {
		t.Fatalf("DeathHandoffSince(2) len=%d, want 4", len(page))
	}
	// Hostile caller mutates every field of its page.
	for i := range page {
		page[i].CitizenID = 0xDEADBEEF
		page[i].DeathMonth = -1
		page[i].EmergencyFlag = !page[i].EmergencyFlag
	}
	// Appending must not write into the queue's backing array either
	// (the classic cap-sharing alias).
	page = append(page, RealisedDeath{CitizenID: 0xBADF00D})
	_ = page

	assertRealisedDeathsEqual(t, "queue handoff after a caller mutated its page",
		q.RealisedDeaths(cid), before)
	assertRealisedDeathsEqual(t, "a fresh page after a caller mutated an earlier one",
		q.DeathHandoffSince(2, cid), before[2:])

	// Two independently-taken overlapping pages must not alias each other.
	p1 := q.DeathHandoffSince(0, cid)
	p2 := q.DeathHandoffSince(0, cid)
	p1[0].CitizenID = 0xFEEDFACE
	if p2[0].CitizenID == 0xFEEDFACE {
		t.Fatalf("two DeathHandoffSince pages alias the same backing array")
	}
}

// TestAttackBUG483_DeathHandoffSinceCopyguards asserts the SEC-020 guard
// is wired on the NEW method at both levels, in each level's established
// style: CitizensAPI.DeathHandoffSince rejects a copy with an error;
// DeathQueue.DeathHandoffSince, like every other DeathQueue read accessor
// (RealisedDeaths/Len/RealisedSequence), LOGS the rejection rather than
// failing. Verified to FAIL with either checkNotCopied call removed.
func TestAttackBUG483_DeathHandoffSinceCopyguards(t *testing.T) {
	const qcid = "attack-bug483-copyguard-queue"
	count := captureErrs(t)
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 3, 0)
	for id := uint64(1); id <= 3; id++ {
		mustEnqueue(t, q, id, 100, qcid)
	}
	q.RealiseDrained(cfg, false, 200, qcid)

	if n := count(ErrDeathQueueCopied, qcid); n != 0 {
		t.Fatalf("setup: %d unexpected %s entries before the copy attack", n, ErrDeathQueueCopied)
	}
	cp := deathQueueByteCopy(q)
	cp.DeathHandoffSince(0, qcid)
	if n := count(ErrDeathQueueCopied, qcid); n == 0 {
		t.Fatalf("DeathQueue.DeathHandoffSince on a struct copy logged no %s -- "+
			"the SEC-020 guard is missing (every other DeathQueue read accessor logs one)", ErrDeathQueueCopied)
	}

	const acid = "attack-bug483-copyguard-api"
	api, err := NewCitizensAPI(11, acid)
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	apiCopy := citizensByteCopy(api)
	got, err := apiCopy.DeathHandoffSince(0, acid)
	if err == nil {
		t.Fatalf("CitizensAPI.DeathHandoffSince on a struct copy returned nil error")
	}
	if got != nil {
		t.Fatalf("CitizensAPI.DeathHandoffSince on a struct copy returned %d records alongside its error", len(got))
	}
}

// TestAttackBUG483_DeathHandoffSinceHostileCursors hammers the cursor
// arithmetic with the values most likely to produce a negative slice bound
// or an overflow, on both an empty and a populated stream.
func TestAttackBUG483_DeathHandoffSinceHostileCursors(t *testing.T) {
	const cid = "attack-bug483-cursors"
	empty := NewDeathQueue()
	full := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 5, 0)
	for id := uint64(1); id <= 5; id++ {
		mustEnqueue(t, full, id, 100, cid)
	}
	full.RealiseDrained(cfg, false, 200, cid)

	hostile := []int{math.MinInt, math.MinInt + 1, -1 << 40, -6, -1, 0, 1, 4, 5, 6, 1 << 40, math.MaxInt - 1, math.MaxInt}
	for _, q := range []*DeathQueue{empty, full} {
		want := q.RealisedDeaths(cid)
		for _, c := range hostile {
			page := q.DeathHandoffSince(c, cid) // must never panic
			if page == nil {
				t.Fatalf("DeathHandoffSince(%d) returned nil; the contract is an empty non-nil slice", c)
			}
			switch {
			case c <= 0:
				assertRealisedDeathsEqual(t, "non-positive cursor is the whole stream", page, want)
			case c >= len(want):
				if len(page) != 0 {
					t.Fatalf("DeathHandoffSince(%d) on a stream of %d returned %d records, want 0", c, len(want), len(page))
				}
			default:
				assertRealisedDeathsEqual(t, "mid-stream cursor", page, want[c:])
			}
		}
		// No hostile cursor truncated or otherwise disturbed the stream.
		assertRealisedDeathsEqual(t, "stream survives hostile cursors", q.RealisedDeaths(cid), want)
	}
}

// TestAttackBUG483_DeathHandoffSinceConcurrentWithRealisation runs the new
// reader against concurrent realisation under -race, and asserts the
// pure-read claim: a reader can never make the writer lose records, and a
// page is always a consistent prefix-suffix of the stream at some instant.
func TestAttackBUG483_DeathHandoffSinceConcurrentWithRealisation(t *testing.T) {
	const cid = "attack-bug483-concurrent"
	q := NewDeathQueue()
	cfg := mkFixedBudgetCfg(t, 2, 0)
	for id := uint64(1); id <= 200; id++ {
		mustEnqueue(t, q, id, 100, cid)
	}

	var wg sync.WaitGroup
	wg.Add(5)
	go func() {
		defer wg.Done()
		for m := int64(200); m < 300; m++ {
			q.RealiseDrained(cfg, false, m, cid)
		}
	}()
	for r := 0; r < 4; r++ {
		go func() {
			defer wg.Done()
			cursor := 0
			for i := 0; i < 300; i++ {
				page := q.DeathHandoffSince(cursor, cid)
				cursor += len(page)
				_ = q.DeathHandoffSince(-1, cid)
			}
		}()
	}
	wg.Wait()

	// The readers must not have consumed anything: the full cumulative
	// stream is still there, and still equals the realised sequence.
	full := q.RealisedDeaths(cid)
	seq := q.RealisedSequence(cid)
	if len(full) != len(seq) {
		t.Fatalf("handoff=%d realisedSequence=%d after concurrent paging -- a reader mutated the stream", len(full), len(seq))
	}
	for i := range seq {
		if full[i].CitizenID != seq[i] {
			t.Fatalf("handoff diverged from realisedSequence at %d after concurrent paging", i)
		}
	}
	assertRealisedDeathsEqual(t, "paging from 0 after concurrency equals the full stream",
		q.DeathHandoffSince(0, cid), full)
}

// --- F1: budgetFor is genuinely the one shared implementation. ---

// TestAttackBUG483_BudgetForIsTheOnlyBudgetImplementation asserts the
// structural claim behaviourally: for a grid of configurations, whatever
// budgetFor returns is EXACTLY what both callers act on. Any residual
// inline copy of the rule in either caller shows up here as a divergence
// between a caller's release count and budgetFor's own answer -- which is
// what makes a single mutation of budgetFor redden both call sites.
func TestAttackBUG483_BudgetForIsTheOnlyBudgetImplementation(t *testing.T) {
	const cid = "attack-bug483-budgetfor"
	for _, ord := range []int{1, 3, 5, 9, 40} {
		for _, emg := range []int{0, 1, 4, 12, 40} {
			for _, emergency := range []bool{false, true} {
				cfg := mkFixedBudgetCfg(t, ord, emg)

				ref := NewDeathQueue()
				got := NewDeathQueue()
				probe := NewDeathQueue()
				for id := uint64(1); id <= 20; id++ {
					mustEnqueue(t, ref, id, 100, cid)
					mustEnqueue(t, got, id, 100, cid)
					mustEnqueue(t, probe, id, 100, cid)
				}

				want := budgetFor(probe, cfg, emergency, cid)
				expect := want
				if expect > 20 {
					expect = 20
				}
				if expect < 0 {
					expect = 0
				}

				ids := EmergencyRealise(ref, cfg, emergency, 200, cid)
				recs := got.RealiseDrained(cfg, emergency, 200, cid)

				if len(ids) != expect {
					t.Fatalf("ord=%d emg=%d emergency=%v: EmergencyRealise released %d, budgetFor said %d (capped %d) -- "+
						"EmergencyRealise is not acting on budgetFor's answer", ord, emg, emergency, len(ids), want, expect)
				}
				if len(recs) != expect {
					t.Fatalf("ord=%d emg=%d emergency=%v: RealiseDrained released %d, budgetFor said %d (capped %d) -- "+
						"RealiseDrained is not acting on budgetFor's answer", ord, emg, emergency, len(recs), want, expect)
				}
				for i := range ids {
					if recs[i].CitizenID != ids[i] {
						t.Fatalf("ord=%d emg=%d emergency=%v: release order diverges at %d", ord, emg, emergency, i)
					}
				}
			}
		}
	}
}
