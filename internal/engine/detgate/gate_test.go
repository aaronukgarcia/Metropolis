package detgate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// gateSeed is the fixed seed TestDeterminismGate runs under (AC-1/AC-9:
// a mechanical, reproducible check, never randomised per run).
const gateSeed uint64 = 20260809

// gateMonths is master doc §1.2 point 5's CI determinism gate size: "same
// seed, 120 months, twice".
const gateMonths = 120

// TestDeterminismGate is the CI-facing check (AC-4: wired into
// .github/workflows/ci.yml's determinism-gate job): same seed, 120
// months, run twice at POOL-SIM=1 (AC-1), then again at POOL-SIM=14
// (AC-2), all against the stub-everything engine.core.Engine (AC-3 — see
// package doc.go, engine.core has zero registered PhaseHooks today, the
// walking-skeleton property). A hash mismatch fails this test with a
// message naming exactly which two runs disagreed (AC-7) and states the
// GR#21 auto-P0 severity (AC-8) so a human triaging red CI does not have
// to know the rule from memory.
//
// grep -rn "time.Now" this file returns no matches (AC-9) — nothing here
// reads the wall clock.
func TestDeterminismGate(t *testing.T) {
	report, err := RunGate("test-determinism-gate", gateSeed, gateMonths, []RunSpec{
		{Label: "run1-pool1", WorkerCount: 1},
		{Label: "run2-pool1", WorkerCount: 1},
		{Label: "pool14", WorkerCount: 14},
	})
	if err != nil {
		t.Fatalf("RunGate(seed=%d, months=%d): %v", gateSeed, gateMonths, err)
	}

	if !report.Verdict {
		t.Fatalf(
			"DETERMINISM GATE RED — GR#21 (docs/golden-rules-detail.md Rule #21): "+
				"this is AUTOMATICALLY P0 and blocks every other merge until green "+
				"(reverting the offending commit is always an acceptable first response). "+
				"Mismatches: %v",
			report.Mismatches,
		)
	}

	for _, r := range report.Runs {
		if r.Hash == "" {
			t.Fatalf("run %q (POOL-SIM=%d) produced an empty hash", r.Label, r.WorkerCount)
		}
	}
}

// TestDeterminismGate_NegativeControl_CorruptedBytesFailGate is the
// required negative control: a determinism gate that can never fail is
// no gate at all. It takes one real Snapshot from a real Engine run,
// deliberately corrupts a byte of it (simulating a nondeterministic
// second run that produced different bytes), hashes both, and asserts
// evaluate — the exact comparison logic RunGate's verdict is built on —
// reports Verdict=false with a mismatch line naming both runs.
//
// grep -rn "time.Now" this file returns no matches (AC-9).
func TestDeterminismGate_NegativeControl_CorruptedBytesFailGate(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(42), core.WithPoolSize(1))
	if err := e.AdvanceTicks("negctl-advance", 30); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	var buf bytes.Buffer
	if _, err := e.Snapshot(&buf, "negctl-snapshot"); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	clean := sha256.Sum256(buf.Bytes())
	cleanHash := hex.EncodeToString(clean[:])

	corrupted := append([]byte(nil), buf.Bytes()...)
	if len(corrupted) == 0 {
		t.Fatal("snapshot produced zero bytes; cannot corrupt an empty run")
	}
	corrupted[0] ^= 0xFF // flip a bit — "corrupt one run's bytes"
	corruptedSum := sha256.Sum256(corrupted)
	corruptedHash := hex.EncodeToString(corruptedSum[:])

	if cleanHash == corruptedHash {
		t.Fatal("sanity check failed: corrupting a byte did not change the sha256 digest")
	}

	verdict, mismatches := evaluate(42, 1, []RunResult{
		{Label: "clean", WorkerCount: 1, Hash: cleanHash},
		{Label: "corrupted", WorkerCount: 1, Hash: corruptedHash},
	})

	if verdict {
		t.Fatal("GATE BUG: a corrupted-bytes run compared equal to the clean run — a gate that can't fail is no gate")
	}
	if len(mismatches) != 1 {
		t.Fatalf("expected exactly one mismatch line, got %d: %v", len(mismatches), mismatches)
	}
}

// TestRunGate_RejectsTooFewRuns proves RunGate refuses to construct a
// GateReport that could never fail (fewer than two runs) rather than
// silently reporting Verdict=true for a single-run "comparison."
func TestRunGate_RejectsTooFewRuns(t *testing.T) {
	if _, err := RunGate("test-too-few", gateSeed, 1, []RunSpec{{Label: "only", WorkerCount: 1}}); err == nil {
		t.Fatal("expected RunGate to reject fewer than two RunSpecs, got nil error")
	}
}

// TestRunGate_RejectsInvalidMonths proves RunGate rejects a non-positive
// month count rather than silently running a zero-tick gate.
func TestRunGate_RejectsInvalidMonths(t *testing.T) {
	specs := []RunSpec{{Label: "a", WorkerCount: 1}, {Label: "b", WorkerCount: 1}}
	if _, err := RunGate("test-invalid-months", gateSeed, 0, specs); err == nil {
		t.Fatal("expected RunGate to reject months <= 0, got nil error")
	}
}
