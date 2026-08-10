package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// wantErrCode fails the test unless err is a *errs.E whose Code is code,
// or the MET-F003 "unregistered code" fallback naming code in its
// Display (mirrors internal/engine/core/commands_test.go's
// wantPlaceholderCode, so these tests stay correct whether or not this
// item's MET-H2xx codes have been registered in data/errors.json by the
// time this runs).
func wantErrCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want code %s", code)
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("err = %v (%T), want *errs.E with code %s", err, err, code)
	}
	if e.Code == code {
		return
	}
	if e.Code == "MET-F003" && strings.Contains(e.Display(), code) {
		return
	}
	t.Fatalf("err code = %s, want %s (or MET-F003 fallback naming it): %v", e.Code, code, err)
}

// --- AC-1/AC-3 (harness.headless.md): a headless run writes an
// int.serializer bundle metctl verify can validate ---

func TestRun_WritesValidBundle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "snap")
	result, err := Run(context.Background(), Config{Seed: 1, Months: 1, OutDir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TicksAdvanced != core.DailyTicksPerMonth {
		t.Errorf("TicksAdvanced = %d, want %d", result.TicksAdvanced, core.DailyTicksPerMonth)
	}

	h, err := serialize.ValidateBundle(dir)
	if err != nil {
		t.Fatalf("ValidateBundle(%q): %v (AC-3: -out must be a valid int.serializer bundle)", dir, err)
	}
	if h.WorldSeed != 1 {
		t.Errorf("bundle WorldSeed = %d, want 1", h.WorldSeed)
	}
	if h.CreatedAtTick != core.DailyTicksPerMonth {
		t.Errorf("bundle CreatedAtTick = %d, want %d", h.CreatedAtTick, core.DailyTicksPerMonth)
	}
}

// --- AC-7 (harness.headless.md): same seed/months/scenario, run twice,
// byte-identical -out snapshots ---

func snapshotSHA256(t *testing.T, dir string) string {
	t.Helper()
	h, err := serialize.ValidateBundle(dir)
	if err != nil {
		t.Fatalf("ValidateBundle(%q): %v", dir, err)
	}
	if len(h.ShardIndex) != 1 {
		t.Fatalf("ShardIndex has %d entries, want 1", len(h.ShardIndex))
	}
	return h.ShardIndex[0].SHA256
}

func TestRun_Determinism_SameSeedMonths_IdenticalSnapshotHash(t *testing.T) {
	dir1 := filepath.Join(t.TempDir(), "snap1")
	dir2 := filepath.Join(t.TempDir(), "snap2")

	if _, err := Run(context.Background(), Config{Seed: 42, Months: 2, OutDir: dir1}); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	if _, err := Run(context.Background(), Config{Seed: 42, Months: 2, OutDir: dir2}); err != nil {
		t.Fatalf("Run 2: %v", err)
	}

	h1 := snapshotSHA256(t, dir1)
	h2 := snapshotSHA256(t, dir2)
	if h1 != h2 {
		t.Fatalf("same (seed, months): shard hash1=%s hash2=%s, want equal", h1, h2)
	}
}

// --- AC-4 (harness.headless.md): scenario-script commands are executed
// via the real protocol.Command path before/around tick advancement ---

func TestRun_Scenario_CommandsExecutedAndCounted(t *testing.T) {
	scenarioPath := filepath.Join(t.TempDir(), "scenario.json")
	scenario := `[
		{"protocolVersion":"1.0","correlationId":"scn-1","kind":"SetSpeed","payload":{"speed":2}},
		{"protocolVersion":"1.0","correlationId":"scn-2","kind":"Pause","payload":{}}
	]`
	if err := os.WriteFile(scenarioPath, []byte(scenario), 0o644); err != nil {
		t.Fatalf("writing scenario file: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "snap")
	result, err := Run(context.Background(), Config{Seed: 1, Months: 1, OutDir: dir, ScenarioPath: scenarioPath})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ScenarioCommands != 2 {
		t.Errorf("ScenarioCommands = %d, want 2", result.ScenarioCommands)
	}
}

// --- AC-8 (harness.headless.md): unreadable/malformed -scenario is a
// registry-sourced, non-zero-exit error, never a panic or partial run ---

func TestRun_Scenario_MissingFile_ReturnsRegistryError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "snap")
	_, err := Run(context.Background(), Config{
		Seed: 1, Months: 1, OutDir: dir,
		ScenarioPath: filepath.Join(t.TempDir(), "does-not-exist.json"),
	})
	wantErrCode(t, err, ErrScenarioReadFailed)
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Errorf("OutDir %q was created despite a scenario load failure -- AC-8 requires no partial run", dir)
	}
}

func TestLoadScenario_MalformedJSON_ReturnsRegistryError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{ not an array"), 0o644); err != nil {
		t.Fatalf("writing malformed scenario: %v", err)
	}
	_, err := LoadScenario("corr-test", path)
	wantErrCode(t, err, ErrScenarioReadFailed)
}

func TestLoadScenario_UnknownCommandKind_ReturnsRegistryError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unknown-kind.json")
	body := `[{"protocolVersion":"1.0","correlationId":"x","kind":"NotARealKind","payload":{}}]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing scenario: %v", err)
	}
	_, err := LoadScenario("corr-test", path)
	wantErrCode(t, err, ErrScenarioReadFailed)
}

// LoadScenario auto-fills an empty CorrelationID rather than rejecting
// it -- documented behaviour (see LoadScenario's doc comment / ASM logged
// against this file), verified directly here so a future change to that
// judgement call is a visible test failure, not a silent behaviour
// change.
func TestLoadScenario_EmptyCorrelationID_IsAutoFilled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-corr.json")
	body := `[{"protocolVersion":"1.0","kind":"Pause","payload":{}}]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing scenario: %v", err)
	}
	cmds, err := LoadScenario("corr-test", path)
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("got %d commands, want 1", len(cmds))
	}
	if cmds[0].CorrelationID == "" {
		t.Error("CorrelationID was not auto-filled for an empty-correlationId scenario command")
	}
}

// --- AC-9 (harness.headless.md): a write failure on -out is a clear,
// non-zero-exit error, never a silent success with no file written ---

func TestRun_OutDirAlreadyExists_ReturnsRegistryError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "snap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("pre-creating OutDir: %v", err)
	}
	_, err := Run(context.Background(), Config{Seed: 1, Months: 1, OutDir: dir})
	wantErrCode(t, err, ErrOutputWriteFailed)
}

// --- AC-5/AC-6 (harness.headless.md): per-phase timing and stub
// invariant reports are emitted every tick, in a fixed field order (AC-11) ---

func TestRun_Report_EmitsPhaseTimingAndInvariantPerTick(t *testing.T) {
	var buf bytes.Buffer
	dir := filepath.Join(t.TempDir(), "snap")
	_, err := Run(context.Background(), Config{Seed: 1, Months: 1, OutDir: dir, Report: &buf})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	dec := json.NewDecoder(&buf)
	var timingLines, invariantLines int
	dailyTickTimingLines := 0
	for {
		var raw map[string]json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			break
		}
		var typ string
		if err := json.Unmarshal(raw["type"], &typ); err != nil {
			t.Fatalf("report line missing/invalid \"type\" field: %v", raw)
		}
		switch typ {
		case reportKindPhaseTiming:
			timingLines++
			var phase string
			_ = json.Unmarshal(raw["phase"], &phase)
			if phase == string(core.PhaseDailyTick) {
				dailyTickTimingLines++
			}
		case reportKindInvariant:
			invariantLines++
			var passed bool
			_ = json.Unmarshal(raw["passed"], &passed)
			if !passed {
				t.Error("invariant report line has passed=false, want true (stub check, MOD-019 not built yet)")
			}
		default:
			t.Errorf("unexpected report line type %q", typ)
		}
	}

	if dailyTickTimingLines != int(core.DailyTicksPerMonth) {
		t.Errorf("daily-tick phaseTiming lines = %d, want %d (one per tick, AC-5)", dailyTickTimingLines, core.DailyTicksPerMonth)
	}
	if invariantLines != int(core.DailyTicksPerMonth) {
		t.Errorf("invariant lines = %d, want %d (one per tick, AC-6)", invariantLines, core.DailyTicksPerMonth)
	}
}

// TestRun_NoReport_IsANoOp confirms Config.Report == nil (the default)
// never allocates a writer or attempts to write anywhere -- Run must
// succeed identically whether or not a caller wants the report stream.
func TestRun_NoReport_IsANoOp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "snap")
	result, err := Run(context.Background(), Config{Seed: 1, Months: 1, OutDir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ReportWriteErr != nil {
		t.Errorf("ReportWriteErr = %v, want nil when Report was never set", result.ReportWriteErr)
	}
}

// --- AC-2 (harness.headless.md, engine.headless.md AC-1/AC-9): Months <=
// 0 is legal library-level behaviour (zero ticks); cmd/metropolis's flag
// layer, not this package, is where "-months is required and must be
// positive" is enforced as a hard usage error -- see
// cmd/metropolis/run_test.go's TestRun_Headless_ZeroMonths_ReturnsExitCode2. ---

func TestRun_ZeroMonths_AdvancesNoTicks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "snap")
	result, err := Run(context.Background(), Config{Seed: 1, Months: 0, OutDir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TicksAdvanced != 0 {
		t.Errorf("TicksAdvanced = %d, want 0", result.TicksAdvanced)
	}
}

// --- SEC-036 / engine.headless.md AC-9: this package drives the engine
// exclusively through NewEngine + RunCommandLoop/HandleCommand over a
// real protocol.Command stream -- no headless-only bypass of
// Engine.AdvanceTicks anywhere in this package's non-test source. ---

func TestPackage_NeverCallsAdvanceTicksDirectly(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join(".", "*.go"))
	if err != nil {
		t.Fatalf("globbing package source: %v", err)
	}
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if strings.Contains(string(data), ".AdvanceTicks(") {
			t.Errorf("%s calls .AdvanceTicks( directly -- engine.headless.md AC-1/AC-2 requires ticks driven exclusively through protocol.Command/RunCommandLoop, never a headless-only bypass", path)
		}
	}
}

// Compile-time assertions that Config/Result fields referenced by
// cmd/metropolis's headless.go exist with the expected types, so a
// signature drift here is caught at build time rather than only at the
// CLI's own (thinner) test coverage.
var (
	_ = Config{Seed: 0, Months: 0, OutDir: "", ScenarioPath: "", PoolSize: 0, Report: nil, CorrelationID: ""}
	_ = Result{Header: serialize.Header{}, TicksAdvanced: 0, ScenarioCommands: 0, ReportWriteErr: nil}
	_ = protocol.ProtocolVersion
)
