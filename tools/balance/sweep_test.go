package balance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/harness/synth"
)

const scenarioJSON = `{
  "name": "t",
  "parameters": {
    "secondsPerMonthAt1x": [240, 480],
    "months": [600],
    "citizenCount": [1000],
    "sprawl": [0.5],
    "networkShape": ["grid"]
  },
  "seeds": [1, 2],
  "target": {"metric": "realHoursToMilestone", "band": [80, 150]}
}`

func mustScenario(t *testing.T, body string) *Scenario {
	t.Helper()
	var s Scenario
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("unmarshal scenario: %v", err)
	}
	s.raw = []byte(body)
	if err := s.Validate(); err != nil {
		t.Fatalf("validate scenario: %v", err)
	}
	return &s
}

// fakeRunner is the test cellRunner: deterministic by default (returns
// SimulatedMonths = cfg.Months), overridable per test.
type fakeRunner struct {
	fn func(ctx context.Context, cfg CellConfig, seed uint64) (Measurement, error)
}

func (f fakeRunner) runCell(ctx context.Context, cfg CellConfig, seed uint64) (Measurement, error) {
	if f.fn == nil {
		return Measurement{SimulatedMonths: cfg.Months, TicksAdvanced: cfg.Months * 30}, nil
	}
	return f.fn(ctx, cfg, seed)
}

func runSweep(t *testing.T, scn *Scenario, r cellRunner, workers int) (SweepResult, []byte) {
	t.Helper()
	var buf bytes.Buffer
	sr := NewSweepRunner(r, Options{WorkerCount: workers, CommitHash: "test-commit", Version: "test-version"})
	res, err := sr.Run(context.Background(), scn, &buf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res, buf.Bytes()
}

func recordsJSON(res SweepResult) []byte {
	b, _ := json.Marshal(res.Records)
	return b
}

func TestRealHoursArithmetic(t *testing.T) {
	// AC-5: simulated months × secondsPerMonthAt1x ÷ 3600, exact.
	if got := realHours(600, 480); got != 80.0 {
		t.Fatalf("realHours(600,480) = %v, want 80", got)
	}
	if got := realHours(300, 960); got != 80.0 {
		t.Fatalf("realHours(300,960) = %v, want 80", got)
	}
	if got := realHours(0, 480); got != 0.0 {
		t.Fatalf("realHours(0,480) = %v, want 0", got)
	}
	if got := realHours(1500, 240); got != 100.0 {
		t.Fatalf("realHours(1500,240) = %v, want 100", got)
	}
}

func TestSweepDeterministic(t *testing.T) {
	// ICD §11 test 1: two identical runs at a FIXED worker count produce
	// byte-identical full output (meta + records) ...
	scn := mustScenario(t, scenarioJSON)
	_, a := runSweep(t, scn, fakeRunner{}, 2)
	_, b := runSweep(t, scn, fakeRunner{}, 2)
	if !bytes.Equal(a, b) {
		t.Fatalf("fixed-worker-count runs diverged:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}

	// ... and the RESULT TABLE (records) is byte-identical across worker
	// counts too (results merge in ascending order, never completion order),
	// while the recorded WorkerCount provenance legitimately differs.
	ra, _ := runSweep(t, scn, fakeRunner{}, 1)
	rb, _ := runSweep(t, scn, fakeRunner{}, 4)
	if !bytes.Equal(recordsJSON(ra), recordsJSON(rb)) {
		t.Fatalf("records diverged across worker counts:\n--- 1 ---\n%s\n--- 4 ---\n%s", recordsJSON(ra), recordsJSON(rb))
	}
	if ra.WorkerCount == rb.WorkerCount {
		t.Fatalf("expected WorkerCount provenance to differ (1 vs 4), both %d", ra.WorkerCount)
	}
	if ra.WorkerCount != 1 || rb.WorkerCount != 4 {
		t.Fatalf("WorkerCount = %d/%d, want 1/4", ra.WorkerCount, rb.WorkerCount)
	}
}

func TestSweepProposesInBand(t *testing.T) {
	// ICD §11 test 2: a fixture scenario with a known target band asserts the
	// harness proposes the config whose real-hours lands in the band.
	scn := mustScenario(t, scenarioJSON)
	res, _ := runSweep(t, scn, fakeRunner{}, 2)

	if res.TotalCells != 4 {
		t.Fatalf("TotalCells = %d, want 4", res.TotalCells)
	}
	prop := Proposal(scn.Target, res.Records)
	if len(prop) != 2 {
		t.Fatalf("proposal size = %d, want 2 (the spm=480 cells)", len(prop))
	}
	for _, r := range prop {
		if r.Cell.Config[paramSecondsPerMonthAt1x] != "480" {
			t.Fatalf("proposed config spm = %q, want 480", r.Cell.Config[paramSecondsPerMonthAt1x])
		}
		if r.RealHours == nil || *r.RealHours != 80.0 {
			t.Fatalf("proposed realHours = %v, want 80", r.RealHours)
		}
	}
}

func TestEveryCellAccountedFor(t *testing.T) {
	// AC-2: every attempted (config, seed) cell has exactly one terminal
	// record, even when a cell crashes — the crashed cell is present and
	// classified, never dropped.
	scn := mustScenario(t, scenarioJSON)
	boom := &cellError{category: CauseHeadlessExitNonzero, err: errors.New("boom")}
	r := fakeRunner{fn: func(_ context.Context, cfg CellConfig, seed uint64) (Measurement, error) {
		if cfg.SecondsPerMonthAt1x == 240 && seed == 1 {
			return Measurement{}, boom
		}
		return Measurement{SimulatedMonths: cfg.Months}, nil
	}}
	res, _ := runSweep(t, scn, r, 2)

	if len(res.Records) != res.TotalCells {
		t.Fatalf("records = %d, total cells = %d — a cell was silently dropped", len(res.Records), res.TotalCells)
	}
	var crashed *CellResult
	for i := range res.Records {
		if res.Records[i].Status == StatusCrashed {
			crashed = &res.Records[i]
		}
	}
	if crashed == nil {
		t.Fatal("no crashed record found — the engineered crash was absent")
	}
	if crashed.Cell.Seed != 1 || crashed.Cell.Config[paramSecondsPerMonthAt1x] != "240" {
		t.Fatalf("crashed cell identity wrong: %+v", crashed.Cell)
	}
}

func TestFailureCausesDistinct(t *testing.T) {
	// AC-3: two different injected failure modes produce two distinct cause
	// values, never one generic "failed" string.
	scn := mustScenario(t, scenarioJSON)
	r := fakeRunner{fn: func(_ context.Context, cfg CellConfig, seed uint64) (Measurement, error) {
		switch {
		case cfg.SecondsPerMonthAt1x == 240 && seed == 1:
			return Measurement{}, &cellError{category: CauseTimeout, err: context.DeadlineExceeded}
		case cfg.SecondsPerMonthAt1x == 240 && seed == 2:
			return Measurement{}, &cellError{category: CauseHeadlessExitNonzero, err: errors.New("exit 1")}
		default:
			return Measurement{SimulatedMonths: cfg.Months}, nil
		}
	}}
	res, _ := runSweep(t, scn, r, 1)

	causes := map[uint64]CauseCategory{}
	for _, rec := range res.Records {
		if rec.Status == StatusTimedOut || rec.Status == StatusCrashed {
			causes[rec.Cell.Seed] = rec.Cause
		}
	}
	if causes[1] != CauseTimeout || causes[2] != CauseHeadlessExitNonzero {
		t.Fatalf("causes not distinct: %v", causes)
	}
	if causes[1] == causes[2] {
		t.Fatal("both failure modes recorded the identical cause")
	}
}

func TestRetriesAdditive(t *testing.T) {
	// AC-4: a cell that fails once then succeeds on retry shows BOTH attempt
	// records, ordered, never just the final one.
	body := strings.Replace(scenarioJSON, `"target"`, `"retries": 1, "target"`, 1)
	scn := mustScenario(t, body)

	flaky := &flakyRunner{attempts: map[string]int{}}
	res, _ := runSweep(t, scn, flaky, 1)

	if len(res.Records) != res.TotalCells+1 {
		t.Fatalf("records = %d, want %d (4 cells + 1 retry)", len(res.Records), res.TotalCells+1)
	}
	var attempts []CellResult
	for _, rec := range res.Records {
		if rec.Cell.Seed == 1 && rec.Cell.Config[paramSecondsPerMonthAt1x] == "240" {
			attempts = append(attempts, rec)
		}
	}
	if len(attempts) != 2 {
		t.Fatalf("expected 2 attempt records for the flaky cell, got %d", len(attempts))
	}
	if attempts[0].Attempt != 0 || attempts[0].Status != StatusCrashed {
		t.Fatalf("attempt 0 = %+v, want crashed", attempts[0])
	}
	if attempts[1].Attempt != 1 || attempts[1].Status != StatusCompleted {
		t.Fatalf("attempt 1 = %+v, want completed", attempts[1])
	}
}

type flakyRunner struct {
	mu       sync.Mutex
	attempts map[string]int
}

func (f *flakyRunner) runCell(_ context.Context, cfg CellConfig, seed uint64) (Measurement, error) {
	key := fmt.Sprintf("%v/%d", cfg.SecondsPerMonthAt1x, seed)
	f.mu.Lock()
	f.attempts[key]++
	n := f.attempts[key]
	f.mu.Unlock()
	if cfg.SecondsPerMonthAt1x == 240 && seed == 1 && n == 1 {
		return Measurement{}, &cellError{category: CauseHeadlessExitNonzero, err: errors.New("first attempt boom")}
	}
	return Measurement{SimulatedMonths: cfg.Months}, nil
}

func TestOutOfDomainRejected(t *testing.T) {
	// AC-8: a negative secondsPerMonthAt1x is rejected per-cell with the
	// requested (out-of-domain) value preserved, never clamped.
	body := `{
	  "name": "t",
	  "parameters": {
	    "secondsPerMonthAt1x": [-5],
	    "months": [600],
	    "citizenCount": [1000],
	    "sprawl": [0.5],
	    "networkShape": ["grid"]
	  },
	  "seeds": [1, 2],
	  "target": {"metric": "realHoursToMilestone", "band": [80, 150]}
	}`
	scn := mustScenario(t, body) // Validate passes: -5 is a JSON number (shape OK)
	res, _ := runSweep(t, scn, fakeRunner{}, 1)

	if len(res.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(res.Records))
	}
	for _, rec := range res.Records {
		if rec.Status != StatusRejected {
			t.Fatalf("status = %q, want rejected", rec.Status)
		}
		if rec.Cause != CauseInvalidParameter {
			t.Fatalf("cause = %q, want invalid-parameter", rec.Cause)
		}
		if rec.Cell.Config[paramSecondsPerMonthAt1x] != "-5" {
			t.Fatalf("requested value = %q, want -5 (preserved, not clamped)", rec.Cell.Config[paramSecondsPerMonthAt1x])
		}
		if rec.ErrorCode != codeCellOutOfDomain {
			t.Fatalf("errorCode = %q, want %q", rec.ErrorCode, codeCellOutOfDomain)
		}
	}
}

func TestMalformedScenarioRejected(t *testing.T) {
	// AC-7 / ICD §11 test 3: malformed scenarios are rejected at load time
	// with a registry-sourced code, never silently defaulted to an empty grid.
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing required dimension",
			body: `{"parameters":{"secondsPerMonthAt1x":[480],"months":[600],"citizenCount":[1000],"sprawl":[0.5]},"seeds":[1],"target":{"metric":"realHoursToMilestone","band":[80,150]}}`,
			want: codeScenarioInvalid,
		},
		{
			name: "empty parameter range",
			body: `{"parameters":{"secondsPerMonthAt1x":[],"months":[600],"citizenCount":[1000],"sprawl":[0.5],"networkShape":["grid"]},"seeds":[1],"target":{"metric":"realHoursToMilestone","band":[80,150]}}`,
			want: codeScenarioInvalid,
		},
		{
			name: "empty seed set",
			body: `{"parameters":{"secondsPerMonthAt1x":[480],"months":[600],"citizenCount":[1000],"sprawl":[0.5],"networkShape":["grid"]},"seeds":[],"target":{"metric":"realHoursToMilestone","band":[80,150]}}`,
			want: codeScenarioInvalid,
		},
		{
			name: "wrong-shaped growth curve",
			body: `{"parameters":{"secondsPerMonthAt1x":[480],"months":[600],"citizenCount":[1000],"sprawl":[0.5],"networkShape":["grid"],"growthCurve":["not-an-object"]},"seeds":[1],"target":{"metric":"realHoursToMilestone","band":[80,150]}}`,
			want: codeScenarioInvalid,
		},
		{
			name: "unsupported metric",
			body: `{"parameters":{"secondsPerMonthAt1x":[480],"months":[600],"citizenCount":[1000],"sprawl":[0.5],"networkShape":["grid"]},"seeds":[1],"target":{"metric":"moneyFlow","band":[80,150]}}`,
			want: codeScenarioInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s Scenario
			if err := json.Unmarshal([]byte(tc.body), &s); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			s.raw = []byte(tc.body)

			var buf bytes.Buffer
			sr := NewSweepRunner(fakeRunner{}, Options{WorkerCount: 1})
			_, err := sr.Run(context.Background(), &s, &buf)
			if err == nil {
				t.Fatal("expected a load-time validation error, got nil")
			}
			if got := codeOf(err); got != tc.want {
				t.Fatalf("code = %q, want %q", got, tc.want)
			}
			if buf.Len() != 0 {
				t.Fatal("malformed scenario must not create or run any grid (results written)")
			}
		})
	}
}

func TestScenarioReadFailed(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadScenario(filepath.Join(dir, "does-not-exist.json"))
	if err == nil {
		t.Fatal("expected an error for a missing scenario file")
	}
	if got := codeOf(err); got != codeScenarioReadFailed {
		t.Fatalf("code = %q, want %q", got, codeScenarioReadFailed)
	}
}

func TestInterruptedSweepNotYetRun(t *testing.T) {
	// AC-9: a truncated results file re-reads with absent cells reported as
	// not-yet-run, distinct from completed and from crashed.
	scn := mustScenario(t, scenarioJSON)
	_, full := runSweep(t, scn, fakeRunner{}, 1)

	// Keep the meta line + the first 2 record lines, drop the rest — a torn
	// partial write.
	lines := strings.Split(strings.TrimRight(string(full), "\n"), "\n")
	partial := strings.Join(lines[:3], "\n") + "\n"

	res, err := readResults(strings.NewReader(partial), scn)
	if err != nil {
		t.Fatalf("readResults: %v", err)
	}
	var notYetRun, completed int
	for _, rec := range res.Records {
		switch rec.Status {
		case StatusNotYetRun:
			notYetRun++
		case StatusCompleted:
			completed++
		}
	}
	if completed == 0 || notYetRun == 0 {
		t.Fatalf("expected a mix of completed (%d) and not-yet-run (%d) records", completed, notYetRun)
	}
	if completed+notYetRun != res.TotalCells {
		t.Fatalf("reconciled records = %d, want %d cells", completed+notYetRun, res.TotalCells)
	}
}

func TestProvenance(t *testing.T) {
	// AC-12: two runs under different (mocked) commit identifiers produce
	// distinguishable provenance.
	scn := mustScenario(t, scenarioJSON)
	var b1, b2 bytes.Buffer
	sr1 := NewSweepRunner(fakeRunner{}, Options{WorkerCount: 1, CommitHash: "commit-aaa", Version: "v1"})
	sr2 := NewSweepRunner(fakeRunner{}, Options{WorkerCount: 1, CommitHash: "commit-bbb", Version: "v1"})
	if _, err := sr1.Run(context.Background(), scn, &b1); err != nil {
		t.Fatalf("run1: %v", err)
	}
	if _, err := sr2.Run(context.Background(), scn, &b2); err != nil {
		t.Fatalf("run2: %v", err)
	}
	if !strings.Contains(b1.String(), `"commitHash":"commit-aaa"`) {
		t.Fatalf("run1 provenance missing: %s", b1.String())
	}
	if !strings.Contains(b2.String(), `"commitHash":"commit-bbb"`) {
		t.Fatalf("run2 provenance missing: %s", b2.String())
	}
	if b1.String() == b2.String() {
		t.Fatal("two runs under different commits produced identical output")
	}
	if !strings.Contains(b1.String(), `"scenarioHash"`) || !strings.Contains(b1.String(), `"workerCount"`) {
		t.Fatalf("run1 missing scenarioHash/workerCount provenance: %s", b1.String())
	}
}

func TestNoBaselineComparisonReportsMissingBaseline(t *testing.T) {
	// AC-14: surfacing a synth-derived comparison reports the missing baseline
	// (BUG-034) explicitly, never as a pass.
	cmp := synth.CompareToBaseline(nil, nil, synth.PerfResult{CitizenCount: 1, Months: 1, Measured: true})
	if cmp.HasBaseline {
		t.Fatal("expected HasBaseline=false for a nil baseline")
	}
	if !strings.Contains(cmp.Message, "no prior baseline to compare") {
		t.Fatalf("missing-baseline message = %q", cmp.Message)
	}
}

func TestConcurrentFanOut(t *testing.T) {
	// AC-15: concurrent fan-out across the worker pool completes every cell
	// with no data race (exercised under -race).
	scn := mustScenario(t, scenarioJSON)
	var mu sync.Mutex
	seen := map[string]int{}
	r := fakeRunner{fn: func(_ context.Context, cfg CellConfig, seed uint64) (Measurement, error) {
		key := fmt.Sprintf("%v/%d", cfg.SecondsPerMonthAt1x, seed)
		mu.Lock()
		seen[key]++
		mu.Unlock()
		return Measurement{SimulatedMonths: cfg.Months}, nil
	}}
	res, _ := runSweep(t, scn, r, 8)

	mu.Lock()
	total := 0
	for _, n := range seen {
		total += n
	}
	mu.Unlock()
	if total != res.TotalCells {
		t.Fatalf("runner saw %d invocations, want %d", total, res.TotalCells)
	}
	if len(res.Records) != res.TotalCells {
		t.Fatalf("records = %d, want %d", len(res.Records), res.TotalCells)
	}
}

func TestConcurrentSweepsAreDeterministic(t *testing.T) {
	// AC-15: four concurrent sweeps of the same scenario, each with its own
	// worker pool, must all produce byte-identical output (and no data race
	// under -race).
	scn := mustScenario(t, scenarioJSON)
	var mu sync.Mutex
	outputs := make([][]byte, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			var buf bytes.Buffer
			sr := NewSweepRunner(fakeRunner{}, Options{WorkerCount: 2, CommitHash: "c", Version: "v"})
			if _, err := sr.Run(context.Background(), scn, &buf); err != nil {
				t.Errorf("run %d: %v", n, err)
				return
			}
			mu.Lock()
			outputs[n] = buf.Bytes()
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	for i := 1; i < len(outputs); i++ {
		if !bytes.Equal(outputs[0], outputs[i]) {
			t.Fatalf("concurrent sweep %d diverged from sweep 0", i)
		}
	}
}

func TestScenarioDataDriven(t *testing.T) {
	// AC-6 / GR#15: the sweep runs the values FROM the scenario file, not a
	// compiled-in grid — changing the scenario's list changes what runs.
	body := `{
	  "name": "t",
	  "parameters": {
	    "secondsPerMonthAt1x": [111, 222, 333],
	    "months": [600],
	    "citizenCount": [1000],
	    "sprawl": [0.5],
	    "networkShape": ["grid"]
	  },
	  "seeds": [7],
	  "target": {"metric": "realHoursToMilestone", "band": [1, 999999]}
	}`
	scn := mustScenario(t, body)
	res, _ := runSweep(t, scn, fakeRunner{}, 1)

	if res.TotalCells != 3 {
		t.Fatalf("TotalCells = %d, want 3 (one per scenario spm value)", res.TotalCells)
	}
	got := map[string]bool{}
	for _, rec := range res.Records {
		got[rec.Cell.Config[paramSecondsPerMonthAt1x]] = true
		if rec.Cell.Seed != 7 {
			t.Fatalf("seed = %d, want 7 (from the scenario data)", rec.Cell.Seed)
		}
	}
	for _, want := range []string{"111", "222", "333"} {
		if !got[want] {
			t.Fatalf("scenario spm value %q not run — grid is not data-driven", want)
		}
	}
}

// TestLoadScenarioRoundTrip writes a scenario to disk and loads it back, so
// LoadScenario's read path (and its registry error) are exercised end-to-end.
func TestLoadScenarioRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scenario.json")
	if err := os.WriteFile(path, []byte(scenarioJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	scn, err := LoadScenario(path)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if scn.hash() != sha256Sum([]byte(scenarioJSON)) {
		t.Fatal("scenario hash does not match file content")
	}
}
