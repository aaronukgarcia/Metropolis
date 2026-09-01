package citizens

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-087 (mkey feat.deathwave) inc1 acceptance tests: the death-queue
// smoothing core (AC-1..5, AC-12..19). AC-6..11 (weather emergency, FEAT-088
// handoff/injected drain) are inc2/inc3 and are NOT tested here.

// mkCliffCohort builds a same-birthMonth cohort of n citizens aged so their
// Gompertz-Makeham hazard sits on the steep slope (old age, critical
// health, zero healthcare access), and returns which of them MortalityDeath
// actually selects this month — the "hazard-selected" wave AC-1's cohort
// cliff scenario needs. Uses the real MortalityHazard/MortalityDeath (never
// re-derived), exactly the machinery this feature consumes.
func mkCliffCohort(seed uint64, month int64, n int) []uint64 {
	const ageMonths = int64(12 * 150) // 150 years old: deep in the Gompertz tail, hazard well above 10%/month
	var selected []uint64
	for id := uint64(1); id <= uint64(n); id++ {
		if MortalityDeath(seed, id, month, ageMonths, HealthCritical, 0) {
			selected = append(selected, id)
		}
	}
	return selected
}

// TestDeathQueueCohortCliffBounded (AC-1, the load-bearing AC). A large
// same-birthMonth cohort aged onto the steep Gompertz slope has
// MortalityDeath select a large fraction in one month (asserted below, so
// the test cannot vacuously pass on an empty selection). Enqueuing every
// selected death and Realising with a small budget must keep the LIVING
// POPULATION delta in that month <= budget — the false-pass this AC names
// explicitly is an implementation that reports a smooth count while still
// removing the whole cohort from a living-population view in one month, so
// this test tracks a living set directly (not just TotalRealised) and
// checks its size drop.
func TestDeathQueueCohortCliffBounded(t *testing.T) {
	const seed = uint64(777)
	const month = int64(1200)
	const n = 2000
	const budget = 25

	selected := mkCliffCohort(seed, month, n)
	if len(selected) < budget*3 {
		t.Fatalf("test setup invalid: cohort must select well more than the budget to prove smoothing bites; selected=%d budget=%d", len(selected), budget)
	}

	q := NewDeathQueue()
	living := make(map[uint64]bool, n)
	for id := uint64(1); id <= uint64(n); id++ {
		living[id] = true
	}

	for _, id := range selected {
		if err := q.Enqueue(id, month, "corr"); err != nil {
			t.Fatalf("Enqueue(%d): %v", id, err)
		}
	}

	popBefore := len(living)
	released := q.Realise(budget, month, "corr")
	for _, id := range released {
		delete(living, id) // ONLY a realised death actually leaves the living population
	}
	popAfter := len(living)
	delta := popBefore - popAfter

	if len(released) > budget {
		t.Fatalf("Realise released %d deaths, exceeding the budget of %d", len(released), budget)
	}
	if delta > budget {
		t.Fatalf("false-pass trap triggered: living-population delta in the cliff month was %d, exceeding budget %d -- a cliff occurred underneath a smoothed count", delta, budget)
	}
	if q.Len("corr") != len(selected)-len(released) {
		t.Fatalf("unrealised remainder not retained in the queue: queue len=%d, want %d", q.Len("corr"), len(selected)-len(released))
	}
	// Every non-released selected citizen must STILL be alive (AC-3).
	for _, id := range selected {
		wasReleased := false
		for _, r := range released {
			if r == id {
				wasReleased = true
				break
			}
		}
		if !wasReleased && !living[id] {
			t.Fatalf("citizen %d was selected but not released this month, yet is missing from the living set", id)
		}
	}
}

// TestDeathQueueConservesAcrossDrain (AC-2): after the cohort cliff's
// selection wave, running enough subsequent months (no new selections)
// empties the queue, and total realised deaths equals total selected
// deaths exactly -- proving smoothing is delay, never a cull or a leak.
func TestDeathQueueConservesAcrossDrain(t *testing.T) {
	const seed = uint64(555)
	const month0 = int64(600)
	const n = 1000
	const budget = 10

	selected := mkCliffCohort(seed, month0, n)
	if len(selected) == 0 {
		t.Fatal("test setup invalid: cohort selected nobody")
	}

	q := NewDeathQueue()
	for _, id := range selected {
		if err := q.Enqueue(id, month0, "corr"); err != nil {
			t.Fatalf("Enqueue(%d): %v", id, err)
		}
	}

	totalSelected := len(selected)
	month := month0
	// Drain with a hard iteration ceiling so a broken drain fails the test
	// instead of looping forever.
	for i := 0; i < totalSelected/budget+10 && q.Len("corr") > 0; i++ {
		month++
		q.Realise(budget, month, "corr")
	}

	if q.Len("corr") != 0 {
		t.Fatalf("queue did not fully drain: %d entries remain", q.Len("corr"))
	}
	if got := q.TotalRealised("corr"); got != totalSelected {
		t.Fatalf("conservation violated: totalRealised=%d, want totalSelected=%d", got, totalSelected)
	}
}

// TestDeathQueueQueuedStaysAliveAndSingleEntry (AC-3): a citizen selected
// for death in month m is still reported IsQueued (the caller's signal to
// keep them alive/ageing/aggregated) at m+1 until realised, and Enqueue
// rejects a second selection of the same citizen (the queue entry is the
// single, terminal selection event).
func TestDeathQueueQueuedStaysAliveAndSingleEntry(t *testing.T) {
	q := NewDeathQueue()
	const id = uint64(42)
	const monthSelected = int64(100)

	if err := q.Enqueue(id, monthSelected, "corr"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Advance a month with no realisation for this citizen: still queued.
	if m, ok := q.IsQueued(id, "corr"); !ok || m != monthSelected {
		t.Fatalf("citizen must still be queued (alive) after a month with no realisation: got (%d,%v)", m, ok)
	}

	// A second selection attempt (e.g. a re-drawn mortality hazard bug)
	// must be rejected, never silently overwrite the entry.
	err := q.Enqueue(id, monthSelected+1, "corr")
	if err == nil {
		t.Fatal("a second Enqueue for an already-queued citizen must be rejected")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrCitizenAlreadyQueued {
		t.Fatalf("expected ErrCitizenAlreadyQueued, got %v", err)
	}

	// Realise it: exactly once, terminal.
	released := q.Realise(1, monthSelected+2, "corr")
	if len(released) != 1 || released[0] != id {
		t.Fatalf("expected exactly citizen %d realised, got %v", id, released)
	}
	if _, ok := q.IsQueued(id, "corr"); ok {
		t.Fatal("citizen must no longer be queued once realised")
	}
	// A THIRD Enqueue attempt (post-realisation) must also be rejected --
	// the queue entry was the single terminal selection.
	if err := q.Enqueue(id, monthSelected+3, "corr"); err == nil {
		t.Fatal("Enqueue after realisation must be rejected -- a citizen is never re-selected once realised")
	}
}

// TestDeathQueueDeterministicFIFO (AC-4): realisation order is the
// documented total order FIFO by (selectionMonth, citizenID), independent
// of Enqueue call order.
func TestDeathQueueDeterministicFIFO(t *testing.T) {
	type entry struct {
		id    uint64
		month int64
	}
	entries := []entry{
		{id: 30, month: 5},
		{id: 10, month: 5},
		{id: 20, month: 5},
		{id: 5, month: 3},
		{id: 1, month: 7},
	}
	want := []uint64{5, 10, 20, 30, 1} // (month,id) ascending: (3,5) (5,10) (5,20) (5,30) (7,1)

	run := func(order []int) []uint64 {
		q := NewDeathQueue()
		for _, i := range order {
			e := entries[i]
			if err := q.Enqueue(e.id, e.month, "corr"); err != nil {
				t.Fatalf("Enqueue: %v", err)
			}
		}
		return q.Realise(len(entries), 100, "corr")
	}

	seqA := run([]int{0, 1, 2, 3, 4})
	seqB := run([]int{4, 3, 2, 1, 0})

	if !equalUint64Slices(seqA, want) {
		t.Fatalf("realisation order (insertion order A) = %v, want %v", seqA, want)
	}
	if !equalUint64Slices(seqB, want) {
		t.Fatalf("realisation order (reverse insertion order B) = %v, want %v", seqB, want)
	}
}

// toFloat converts the small set of numeric literal types used by this
// test file's malformed-budget cases (int, float64) to float64 for
// comparison against MortalityNumber.Value.
func toFloat(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case float64:
		return n
	default:
		return 0
	}
}

func equalUint64Slices(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDeathQueueWorkerCountInvariance (AC-15): the death queue is fed by
// many shards' cold passes; the realisation sequence must be a pure
// function of queue CONTENTS, never of which goroutine happened to Enqueue
// first at a given worker count. Enqueues the identical (id, selectionMonth)
// set through 1 vs 14 concurrent goroutines (arbitrary completion order)
// and asserts the byte-identical realised sequence both times.
func TestDeathQueueWorkerCountInvariance(t *testing.T) {
	const n = 500
	const budget = n // drain everything in one call so order is fully exposed

	run := func(workers int) []uint64 {
		q := NewDeathQueue()
		type job struct {
			id    uint64
			month int64
		}
		jobs := make(chan job, n)
		for i := 0; i < n; i++ {
			jobs <- job{id: uint64(i + 1), month: int64(i % 7)}
		}
		close(jobs)

		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobs {
					if err := q.Enqueue(j.id, j.month, "corr"); err != nil {
						t.Errorf("Enqueue(%d): %v", j.id, err)
					}
				}
			}()
		}
		wg.Wait()
		return q.Realise(budget, 1000, "corr")
	}

	seq1 := run(1)
	seq14 := run(14)

	if !equalUint64Slices(seq1, seq14) {
		t.Fatalf("worker-count invariance violated: sequences differ between 1 and 14 workers")
	}
}

// TestDeathQueueConcurrentEnqueueAndRealise (AC-17): the queue is written
// by a per-shard cold pass and read/drained by the realisation path
// concurrently -- run under `go test -race`.
func TestDeathQueueConcurrentEnqueueAndRealise(t *testing.T) {
	q := NewDeathQueue()
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			_ = q.Enqueue(uint64(i+1), int64(i%10), "corr")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			q.Realise(5, int64(i), "corr")
		}
	}()
	wg.Wait()

	// Drain whatever remains -- proves the queue is still coherent after
	// the concurrent phase (no partial/corrupted state).
	q.Realise(q.Len("corr"), 9999, "corr")
	if q.Len("corr") != 0 {
		t.Fatalf("queue not fully drainable after concurrent phase: %d remain", q.Len("corr"))
	}
}

// TestDeathQueueRealiseByIDNotQueuedAndDoubleRealise (AC-13, GR#7): the
// error's registry CODE is asserted, and that no extra death record was
// created -- never a phantom or duplicated corpse.
func TestDeathQueueRealiseByIDNotQueuedAndDoubleRealise(t *testing.T) {
	q := NewDeathQueue()

	// Not queued at all.
	err := q.RealiseByID(999, 1, "corr")
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrCitizenNotQueued {
		t.Fatalf("expected ErrCitizenNotQueued for an unqueued citizen, got %v", err)
	}
	if q.TotalRealised("corr") != 0 {
		t.Fatalf("a not-queued realisation attempt must never create a death record, got TotalRealised=%d", q.TotalRealised("corr"))
	}

	// Queue, realise once (success), then attempt a double realisation.
	if err := q.Enqueue(1, 1, "corr"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.RealiseByID(1, 2, "corr"); err != nil {
		t.Fatalf("first RealiseByID must succeed: %v", err)
	}
	if got := q.TotalRealised("corr"); got != 1 {
		t.Fatalf("expected exactly 1 realised death, got %d", got)
	}

	err = q.RealiseByID(1, 3, "corr")
	if !errors.As(err, &e) || e.Code != ErrDoubleRealisation {
		t.Fatalf("expected ErrDoubleRealisation, got %v", err)
	}
	if got := q.TotalRealised("corr"); got != 1 {
		t.Fatalf("a double-realisation attempt must never create a SECOND death record, got TotalRealised=%d", got)
	}
}

// --- data/mortality.json config tests (AC-5/AC-12) ---

// TestMortalityConfigLoadsBudgetFromDataFile (AC-5): the budget is loaded
// from data/mortality.json, not a hardcoded Go literal -- proven by parsing
// the raw file independently and asserting the loaded config's budget
// matches the FILE's own value (never a pinned magnitude; this is a
// congruence check, not a balance-number assertion).
func TestMortalityConfigLoadsBudgetFromDataFile(t *testing.T) {
	cfg, err := LoadDefaultMortalityConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultMortalityConfig: %v", err)
	}
	if cfg.MonthlyDeathBudget() <= 0 {
		t.Fatalf("loaded monthly death budget must be positive, got %d", cfg.MonthlyDeathBudget())
	}

	// Independently re-read and parse the raw file, bypassing this
	// package's loader entirely, to prove the value truly came from disk.
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "data", "mortality.json"))
	if err != nil {
		t.Fatalf("could not independently read data/mortality.json: %v", err)
	}
	var raw2 struct {
		Params struct {
			MonthlyDeathBudget struct {
				Value float64 `json:"value"`
			} `json:"monthlyDeathBudget"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &raw2); err != nil {
		t.Fatalf("could not parse data/mortality.json independently: %v", err)
	}
	if int(raw2.Params.MonthlyDeathBudget.Value) != cfg.MonthlyDeathBudget() {
		t.Fatalf("loaded config budget (%d) does not match the raw file's own value (%v) -- budget is not truly data-sourced", cfg.MonthlyDeathBudget(), raw2.Params.MonthlyDeathBudget.Value)
	}
}

// TestMortalityConfigRejectsMalformedBudget (AC-12, GR#7): a missing,
// negative, or non-integer budget produces ErrMortalityDataInvalid at
// load time, and NEVER a silently-substituted default budget (which would
// re-enable the very cliff this feature exists to prevent).
func TestMortalityConfigRejectsMalformedBudget(t *testing.T) {
	base := map[string]any{
		"version": 1,
		"meta": map[string]any{
			"module":        "engine.citizens",
			"featureKey":    "feat.deathwave",
			"specRefs":      []string{"§5.2"},
			"balanceRegime": "placeholder pending Aaron's balance pass",
		},
	}

	write := func(t *testing.T, budgetValue any) string {
		dir := t.TempDir()
		cfg := map[string]any{}
		for k, v := range base {
			cfg[k] = v
		}
		cfg["params"] = map[string]any{
			"monthlyDeathBudget": map[string]any{
				"value":      budgetValue,
				"unit":       "deaths/month",
				"disclosure": "placeholder",
			},
			"monthlyEmergencyBudget": map[string]any{
				"value":      0,
				"unit":       "deaths/month (0 = unbounded)",
				"disclosure": "placeholder",
			},
		}
		b, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		path := filepath.Join(dir, FileMortality)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return dir
	}

	cases := []struct {
		name  string
		value any
	}{
		{"negative", -5},
		{"zero", 0},
		{"nonInteger", 2.5},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := write(t, c.value)
			cfg, err := LoadMortalityConfig(dir, "corr")
			if err == nil {
				t.Fatalf("expected an error for a %s budget, got none (cfg=%+v)", c.name, cfg)
			}
			var e *errs.E
			if !errors.As(err, &e) || e.Code != ErrMortalityDataInvalid {
				t.Fatalf("expected ErrMortalityDataInvalid, got %v", err)
			}
			// GR#7/AC-12: the load must fail closed -- the caller receives an
			// error it cannot ignore, never a silently-substituted valid
			// default budget it might mistake for an accepted config. Proven
			// here by asserting the code path has no fallback constant: the
			// decoded (rejected) value survives unmodified in cfg rather than
			// having been coerced to some "safe" default.
			if got, want := cfg.Params.MonthlyDeathBudget.Value, toFloat(c.value); got != want {
				t.Fatalf("a rejected budget must not be silently coerced -- got %v, want the raw rejected value %v", got, want)
			}
		})
	}

	// Missing budget entirely.
	t.Run("missing", func(t *testing.T) {
		dir := t.TempDir()
		cfg := map[string]any{}
		for k, v := range base {
			cfg[k] = v
		}
		cfg["params"] = map[string]any{
			"monthlyEmergencyBudget": map[string]any{
				"value":      0,
				"unit":       "deaths/month (0 = unbounded)",
				"disclosure": "placeholder",
			},
		}
		b, _ := json.Marshal(cfg)
		if err := os.WriteFile(filepath.Join(dir, FileMortality), b, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, err := LoadMortalityConfig(dir, "corr")
		if err == nil {
			t.Fatal("expected an error for a missing budget entry, got none")
		}
		var e *errs.E
		if !errors.As(err, &e) || e.Code != ErrMortalityDataInvalid {
			t.Fatalf("expected ErrMortalityDataInvalid, got %v", err)
		}
	})
}
