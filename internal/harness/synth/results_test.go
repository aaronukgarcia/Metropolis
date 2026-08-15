package synth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFile is a tiny test-local helper writing raw content to path —
// used only by the corrupt-baseline-file test below, which needs
// control over exact bytes rather than going through AppendResult.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

// TestAppendResult_ResultSchema is AC-5's check: results are persisted
// in a form a CI graphing step can consume (JSON keyed by commit hash
// and scale preset), not only printed to stdout — and reading it back
// via LoadLatestBaseline recovers exactly what was written.
func TestAppendResult_ResultSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	rec := PerfRecord{
		CommitHash: "deadbeef",
		Preset:     "1M",
		Result:     PerfResult{Preset: "1M", CitizenCount: OneMillionCitizens, Months: 12, PerMonthTick: 42 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true},
	}
	if err := AppendResult(path, rec); err != nil {
		t.Fatalf("AppendResult: %v", err)
	}

	got, _, corrupt, err := LoadLatestBaseline(path, "1M", nil)
	if err != nil {
		t.Fatalf("LoadLatestBaseline: %v", err)
	}
	if len(corrupt) != 0 {
		t.Fatalf("unexpected corrupt lines: %+v", corrupt)
	}
	if got == nil {
		t.Fatal("LoadLatestBaseline returned nil after a matching record was written")
	}
	if got.PerMonthTick != rec.Result.PerMonthTick {
		t.Fatalf("PerMonthTick = %v, want %v", got.PerMonthTick, rec.Result.PerMonthTick)
	}
}

// TestAppendResult_Appends proves multiple commits accumulate rather
// than overwrite (AC-5's "keyed by commit hash", meaningless if a second
// write erased the first), and that LoadLatestBaseline returns the MOST
// RECENT matching record.
func TestAppendResult_Appends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	older := PerfRecord{CommitHash: "commit1", Preset: "1M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 100 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	newer := PerfRecord{CommitHash: "commit2", Preset: "1M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 110 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	if err := AppendResult(path, older); err != nil {
		t.Fatalf("AppendResult(older): %v", err)
	}
	if err := AppendResult(path, newer); err != nil {
		t.Fatalf("AppendResult(newer): %v", err)
	}

	got, _, corrupt, err := LoadLatestBaseline(path, "1M", nil)
	if err != nil {
		t.Fatalf("LoadLatestBaseline: %v", err)
	}
	if len(corrupt) != 0 {
		t.Fatalf("unexpected corrupt lines: %+v", corrupt)
	}
	if got == nil || got.PerMonthTick != newer.Result.PerMonthTick {
		t.Fatalf("LoadLatestBaseline = %+v, want the most recently appended record (%v)", got, newer.Result.PerMonthTick)
	}
}

// TestAppendResult_RejectsUnmeasuredResult is BUG-055's regression test:
// results_test.go itself used to construct bare PerfResult{} zero-value
// literals (Measured defaulting to false, same as an unset
// PhaseHookCount) and feed them through AppendResult successfully — the
// exact structural gap the finding named, proven here directly rather
// than merely asserted in prose. This test is RED against the pre-fix
// AppendResult (which performed no validation at all and would have
// written this record) and GREEN against the fix, which rejects it
// before any write happens.
func TestAppendResult_RejectsUnmeasuredResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	rec := PerfRecord{CommitHash: "attacker", Preset: "1M", Result: PerfResult{PerMonthTick: 1 * time.Nanosecond}}
	err := AppendResult(path, rec)
	wantCode(t, err, codeUnmeasuredResult)

	// The rejected write must not have created/touched the results file
	// at all — a caller retrying with a legitimate, Measured record
	// later must not find a corrupt/partial artifact of the rejected
	// attempt sitting in front of it.
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("AppendResult rejected the record but still touched %q on disk (stat err: %v)", path, statErr)
	}
}

// TestLoadLatestBaseline_MissingFileIsNotAnError is AC-8's core claim
// applied to the storage layer: a fresh CI cache (no results file at
// all) must not fail the build.
func TestLoadLatestBaseline_MissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.ndjson")

	got, _, corrupt, err := LoadLatestBaseline(path, "1M", nil)
	if err != nil {
		t.Fatalf("LoadLatestBaseline on a missing file should not error, got %v", err)
	}
	if got != nil {
		t.Fatalf("LoadLatestBaseline on a missing file should return nil, got %+v", got)
	}
	if len(corrupt) != 0 {
		t.Fatalf("unexpected corrupt lines: %+v", corrupt)
	}
}

// TestLoadLatestBaseline_NoMatchingPresetIsNotAnError: a results file
// exists but has never recorded THIS preset — also not an error (a
// fresh scale preset has no prior baseline either, AC-8).
func TestLoadLatestBaseline_NoMatchingPresetIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")
	if err := AppendResult(path, PerfRecord{CommitHash: "c1", Preset: "1M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}); err != nil {
		t.Fatalf("AppendResult: %v", err)
	}

	got, _, corrupt, err := LoadLatestBaseline(path, "10M", nil)
	if err != nil {
		t.Fatalf("LoadLatestBaseline: %v", err)
	}
	if got != nil {
		t.Fatalf("LoadLatestBaseline for an unrecorded preset should return nil, got %+v", got)
	}
	if len(corrupt) != 0 {
		t.Fatalf("unexpected corrupt lines: %+v", corrupt)
	}
}

// TestLoadLatestBaseline_CorruptFileIsAnError proves a file that DOES
// exist but has NO recoverable record for the requested preset (every
// line is corrupt) is reported as codeBaselineCorrupt — distinct from
// the "no baseline yet" cases above, which are never errors. This is the
// "genuine corruption, nothing to fall back to" half of BUG-054's
// recovery contract (see TestLoadLatestBaseline_RecoversPastATornLine
// below for the "corrupt line, but a good later record exists" half).
func TestLoadLatestBaseline_CorruptFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")
	if err := writeFile(path, "{not valid json\n"); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	_, _, corrupt, err := LoadLatestBaseline(path, "1M", nil)
	wantCode(t, err, codeBaselineCorrupt)
	if len(corrupt) != 1 {
		t.Fatalf("corrupt lines = %+v, want exactly 1", corrupt)
	}
}

// TestLoadLatestBaseline_UnrelatedPresetCorruptionDoesNotBlockFreshPreset
// is BUG-086's exact live-verified false positive: the corrupt-line
// tracking above is whole-FILE-grained, but the hard-error decision it
// feeds is per-PRESET, so a garbage line anywhere in a shared results
// file used to escalate a totally unrelated preset's legitimate "fresh
// preset, no prior baseline" case (AC-8) into a hard codeBaselineCorrupt
// failure. Here the file has a genuine, valid record for "1M" — proving
// the file's NDJSON format is generally intact — plus one unparseable
// line, and the caller asks for "10M", which has zero records of its
// own. That must recover as the ordinary AC-8 "no prior baseline"
// case (nil baseline, nil anchor, nil error), not an error, even though
// corrupt is still non-empty (GR#17: still reported, never silently
// dropped).
func TestLoadLatestBaseline_UnrelatedPresetCorruptionDoesNotBlockFreshPreset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	other := PerfRecord{CommitHash: "commit1", Preset: "1M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 100 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	if err := AppendResult(path, other); err != nil {
		t.Fatalf("AppendResult(other): %v", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening results file to append a corrupt line: %v", err)
	}
	if _, err := f.WriteString("{not valid json\n"); err != nil {
		t.Fatalf("writing corrupt line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing results file: %v", err)
	}

	baseline, anchor, corrupt, err := LoadLatestBaseline(path, "10M", nil)
	if err != nil {
		t.Fatalf("LoadLatestBaseline: got err %v, want nil — BUG-086: an unrelated preset's corrupt line must not block this preset's legitimate first baseline", err)
	}
	if baseline != nil {
		t.Fatalf("baseline = %+v, want nil for a preset with no records of its own", baseline)
	}
	if anchor != nil {
		t.Fatalf("anchor = %+v, want nil for a preset with no records of its own", anchor)
	}
	if len(corrupt) != 1 {
		t.Fatalf("corrupt lines = %+v, want exactly 1 — still reported per GR#17, just not escalated to a hard error", corrupt)
	}
}

// TestLoadLatestBaseline_AmbiguousCorruptLineStillHardErrors confirms
// BUG-086's fix did not make corruption detection permissive where it
// must still fire: when a corrupt line's preset genuinely cannot be
// ruled out — no OTHER preset's valid record exists anywhere in the
// file to prove the corrupt line belongs elsewhere — the original
// codeBaselineCorrupt behaviour must be preserved exactly as
// TestLoadLatestBaseline_CorruptFileIsAnError above already proves. This
// test names the same scenario explicitly under BUG-086 to document that
// the ambiguous case was a deliberate, considered non-change, not an
// oversight.
func TestLoadLatestBaseline_AmbiguousCorruptLineStillHardErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")
	if err := writeFile(path, "{not valid json\n"); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	baseline, anchor, corrupt, err := LoadLatestBaseline(path, "10M", nil)
	wantCode(t, err, codeBaselineCorrupt)
	if baseline != nil || anchor != nil {
		t.Fatalf("baseline/anchor = %+v/%+v, want nil/nil on a hard error", baseline, anchor)
	}
	if len(corrupt) != 1 {
		t.Fatalf("corrupt lines = %+v, want exactly 1", corrupt)
	}
}

// TestLoadLatestBaseline_TamperedRequestedPresetRecordStillHardErrors is
// BUG-187's exact live-verified attack, reproduced as a regression test:
// BUG-086's fix suppressed codeBaselineCorrupt whenever
// otherPresetRecordSeen was true, without distinguishing genuinely
// unattributable corrupt lines (BUG-086's real target) from corrupt
// entries already successfully attributed to the REQUESTED preset itself
// and rejected by the provenance checks (BUG-073/085/095). Here the file
// has a valid record for an UNRELATED preset ("10M") — proving the file's
// NDJSON format is generally sound and setting otherPresetRecordSeen —
// plus a hand-injected {"preset":"1M","result":{"measured":false}} line
// for the ACTUALLY REQUESTED preset ("1M"). Pre-fix, LoadLatestBaseline
// wrongly returned (nil, nil, corrupt, nil) — "fresh preset, no baseline
// yet" — silently treating a tampered/rejected record for the requested
// preset as if no baseline existed at all (the dangerous BUG-071-family
// direction: laundering a bad result into a pass). This test is RED
// against that behaviour and GREEN against the fix, which must still
// hard-error with codeBaselineCorrupt because the corrupt entry is known,
// not merely presumed, to belong to the requested preset.
func TestLoadLatestBaseline_TamperedRequestedPresetRecordStillHardErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	other := PerfRecord{CommitHash: "commit1", Preset: "10M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 100 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	if err := AppendResult(path, other); err != nil {
		t.Fatalf("AppendResult(other): %v", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening results file to hand-inject a tampered record: %v", err)
	}
	if _, err := f.WriteString(`{"preset":"1M","result":{"measured":false}}` + "\n"); err != nil {
		t.Fatalf("writing tampered record for the requested preset: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing results file: %v", err)
	}

	baseline, anchor, corrupt, err := LoadLatestBaseline(path, "1M", nil)
	wantCode(t, err, codeBaselineCorrupt)
	if baseline != nil || anchor != nil {
		t.Fatalf("baseline/anchor = %+v/%+v, want nil/nil on a hard error — BUG-187: a tampered record for the REQUESTED preset must never be treated as a fresh, baseline-free preset just because an unrelated preset elsewhere in the file happens to be valid", baseline, anchor)
	}
	if len(corrupt) != 1 {
		t.Fatalf("corrupt lines = %+v, want exactly 1 (the tampered requested-preset record)", corrupt)
	}
}

// TestLoadLatestBaseline_RecoversPastATornLine is BUG-054's exact
// live-verified attack, reproduced as a regression test: a valid record,
// then a torn/partial JSON line (simulating a killed perfci.exe or any
// other process leaving a truncated write mid actions/cache round-trip),
// then a THIRD, genuinely newer, perfectly valid record. Pre-fix,
// LoadLatestBaseline aborted the ENTIRE read on the torn line and
// returned codeBaselineCorrupt, permanently hiding the good, later
// baseline — this test is RED against that behaviour (it would see
// err != nil and got == nil) and GREEN against the fix below, which
// skips the bad line, keeps scanning, and recovers the later good
// record while still reporting the corrupt line rather than silently
// discarding it (GR#17).
func TestLoadLatestBaseline_RecoversPastATornLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	older := PerfRecord{CommitHash: "commit1", Preset: "1M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 100 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	if err := AppendResult(path, older); err != nil {
		t.Fatalf("AppendResult(older): %v", err)
	}

	// Simulate a torn write: a process killed mid-flush leaves a
	// truncated JSON object with no closing brace or trailing newline
	// discipline restored, directly appended after the good line.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening results file to append torn line: %v", err)
	}
	if _, err := f.WriteString(`{"commitHash":"commit2","preset":"1M","result":{"perMonthTick"`); err != nil {
		t.Fatalf("writing torn line: %v", err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		t.Fatalf("terminating torn line: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing results file: %v", err)
	}

	// +5% over older: deliberately kept UNDER RegressionThreshold (10%)
	// so this test exercises only the torn-line recovery path, not
	// BUG-083's freeze-on-regression behaviour (LoadLatestBaseline now
	// replays CompareToBaseline against the reconstructed baseline —
	// see TestLoadLatestBaseline_FreezesOnRegressionInsteadOfRatcheting
	// below for that dedicated test, which needs a record that DOES
	// exceed the threshold).
	newer := PerfRecord{CommitHash: "commit3", Preset: "1M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 105 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	if err := AppendResult(path, newer); err != nil {
		t.Fatalf("AppendResult(newer): %v", err)
	}

	got, _, corrupt, err := LoadLatestBaseline(path, "1M", nil)
	if err != nil {
		t.Fatalf("LoadLatestBaseline: got err %v, want nil — a good later record must recover past the torn line", err)
	}
	if got == nil {
		t.Fatal("LoadLatestBaseline returned nil — the good, later baseline was not recovered past the torn line")
	}
	if got.PerMonthTick != newer.Result.PerMonthTick {
		t.Fatalf("PerMonthTick = %v, want the LATER good record's %v (not the older pre-tear record)", got.PerMonthTick, newer.Result.PerMonthTick)
	}
	if len(corrupt) != 1 {
		t.Fatalf("corrupt lines = %+v, want exactly 1 (the torn line) — corruption must still be REPORTED even though recovery succeeded (GR#17)", corrupt)
	}
	if corrupt[0].LineNo != 2 {
		t.Fatalf("corrupt line number = %d, want 2 (the torn line is the 2nd line written)", corrupt[0].LineNo)
	}
}

// TestLoadLatestBaseline_RejectsHandInjectedUnmeasuredRecord is BUG-073's
// regression test, reproducing Destructive-5's exact live-verified
// finding: AppendResult enforces Measured==true before a write happens
// (BUG-055/MET-H308), but that enforcement lived ONLY at the write
// boundary. A record reaching the file by any other route — here, a
// second line appended directly with os.OpenFile, bypassing AppendResult
// entirely, with Measured omitted (Go zero value: false) — was, pre-fix,
// accepted verbatim as the latest baseline with err == nil and zero
// CorruptLine entries. This test is RED against the pre-fix
// LoadLatestBaseline (it would return the hand-injected record with
// PerMonthTick == 0 and no error) and GREEN against the fix, which
// treats an unmeasured record as a CorruptLine and falls back to the
// last genuinely measured one.
func TestLoadLatestBaseline_RejectsHandInjectedUnmeasuredRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	legit := PerfRecord{CommitHash: "commit1", Preset: "1M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 100 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	if err := AppendResult(path, legit); err != nil {
		t.Fatalf("AppendResult(legit): %v", err)
	}

	// Hand-inject a second line DIRECTLY, bypassing AppendResult's
	// Measured guard entirely — simulating a hand edit, a corrupted
	// merge-conflict resolution, or a re-uploaded/edited artifact
	// (exactly the "future second writer" ASM-337 named as open risk).
	fabricated := PerfRecord{CommitHash: "attacker", Preset: "1M", Result: PerfResult{PerMonthTick: 999 * time.Millisecond}} // Measured left false
	data, err := json.Marshal(fabricated)
	if err != nil {
		t.Fatalf("marshalling fabricated record: %v", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening results file to hand-inject a record: %v", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatalf("writing fabricated record: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing results file: %v", err)
	}

	got, _, corrupt, err := LoadLatestBaseline(path, "1M", nil)
	if err != nil {
		t.Fatalf("LoadLatestBaseline: got err %v, want nil — a genuinely measured earlier record should still be recoverable", err)
	}
	if got == nil {
		t.Fatal("LoadLatestBaseline returned nil — the legitimate, Measured=true record should still have been recovered")
	}
	if got.PerMonthTick != legit.Result.PerMonthTick {
		t.Fatalf("LoadLatestBaseline returned PerMonthTick=%v — BUG-073: an unmeasured, hand-injected record (PerMonthTick=%v) was trusted as the latest baseline instead of falling back to the last genuinely measured one", got.PerMonthTick, fabricated.Result.PerMonthTick)
	}
	if len(corrupt) != 1 {
		t.Fatalf("corrupt lines = %+v, want exactly 1 (the unmeasured record) — GR#17 requires this be reported, not silently dropped", corrupt)
	}
	if corrupt[0].LineNo != 2 {
		t.Fatalf("corrupt line number = %d, want 2 (the hand-injected record is the 2nd line)", corrupt[0].LineNo)
	}
}

// TestLoadLatestBaseline_RecoversPastAnOversizedLine is BUG-074's
// regression test, reproducing Destructive-5's exact finding: a single
// NDJSON line over bufio.Scanner's default 64KiB token cap made
// scanner.Scan() return false PERMANENTLY with bufio.ErrTooLong — below
// the per-line json.Unmarshal recovery path, so it never became a
// CorruptLine, and it terminated the scan before a later, perfectly
// good record was ever reached. This test is RED against the pre-fix
// bufio.Scanner-based LoadLatestBaseline (it would return a nil result,
// an EMPTY corrupt list, and a hard codeBaselineCorrupt error — the
// oversized line's failure is invisible as a "line", it just kills the
// whole read) and GREEN against the bufio.Reader-based fix, which has
// no fixed per-line token cap.
func TestLoadLatestBaseline_RecoversPastAnOversizedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	// Build one line comfortably over bufio.MaxScanTokenSize (64*1024)
	// by padding an otherwise-valid-looking JSON object with a huge
	// junk string field — PerfResult.PhaseTimings being an unbounded
	// slice is exactly the reachable path Destructive-5 named for how a
	// line this size could occur in normal operation (a phase-count
	// blowup), so this reproduces the SHAPE of a real oversized record,
	// not an artificial one.
	oversizedJunk := strings.Repeat("x", 100*1024)
	oversizedLine := `{"commitHash":"oversized","preset":"1M","junk":"` + oversizedJunk + `"}` + "\n"
	if err := os.WriteFile(path, []byte(oversizedLine), 0o644); err != nil {
		t.Fatalf("writing oversized line: %v", err)
	}

	// A genuinely later, valid, Measured record.
	newer := PerfRecord{CommitHash: "commit2", Preset: "1M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 77 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	if err := AppendResult(path, newer); err != nil {
		t.Fatalf("AppendResult(newer): %v", err)
	}

	got, _, corrupt, err := LoadLatestBaseline(path, "1M", nil)
	if err != nil {
		t.Fatalf("LoadLatestBaseline: got err %v, want nil — a good later record must recover past the oversized line (BUG-074)", err)
	}
	if got == nil {
		t.Fatal("LoadLatestBaseline returned nil — the good, later baseline was hidden behind the oversized line (BUG-074)")
	}
	if got.PerMonthTick != newer.Result.PerMonthTick {
		t.Fatalf("PerMonthTick = %v, want the later good record's %v", got.PerMonthTick, newer.Result.PerMonthTick)
	}
	// The oversized line does not fail json.Unmarshal (it is valid JSON,
	// just huge and missing the fields this test cares about) — it is
	// not required to appear as a CorruptLine, only to NOT terminate the
	// scan before the later good record. The load succeeding at all,
	// with the right result, is the assertion that matters here.
	_ = corrupt
}

// legacyLastAppendedBaseline reproduces, for THIS TEST ONLY, exactly
// the pre-fix LoadLatestBaseline behaviour Destructive-7 live-verified
// against the real, then-current code: return whatever the LAST record
// for preset was, with no regression/replay check of any kind. Because
// AppendResult ran unconditionally regardless of cmp.Regressed before
// BUG-083's fix, "last appended" and "last measured" were always
// exactly the same value — this helper exists only to give
// TestLoadLatestBaseline_CatchesRatchetByInches an in-repo, re-runnable
// contrast between the broken and fixed behaviour against the IDENTICAL
// fixture file, not as a resurrection of the removed code path.
func legacyLastAppendedBaseline(t *testing.T, path, preset string) *PerfResult {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("legacyLastAppendedBaseline: reading %q: %v", path, err)
	}
	var latest *PerfResult
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec PerfRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("legacyLastAppendedBaseline: unmarshalling line %q: %v", line, err)
		}
		if rec.Preset == preset {
			result := rec.Result
			latest = &result
		}
	}
	return latest
}

// TestLoadLatestBaseline_CatchesRatchetByInches is BUG-083's core
// regression test — it reproduces Destructive-7's exact live-verified
// finding: 30 successive commits, each stepping PerMonthTick to
// baseline*1.09 (9% growth, UNDER RegressionThreshold=10%) computed
// against the immediately preceding value, compounding from 100ms to
// ~1.327s (13.27x) with cmp.Regressed false on every single step.
//
// This is not a single-commit regression test — BUG-083 was explicit
// that a single bad commit does not demonstrate the fix; the whole
// point is that no INDIVIDUAL step ever crosses the 10% line, so only
// a mechanism that stops trusting "last appended" as the comparison
// point (this test's RED half, legacyLastAppendedBaseline) and instead
// freezes at a genuinely-reconstructed last-known-good/anchor (this
// test's GREEN half, the real LoadLatestBaseline) can catch it.
func TestLoadLatestBaseline_CatchesRatchetByInches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	const steps = 30
	const growth = 1.09 // 9% per commit — Destructive-7's exact figure, under RegressionThreshold (10%)
	const start = 100 * time.Millisecond

	// commit1 is the starting baseline itself (100ms, unmultiplied);
	// commit2..commit31 are the 30 successive 9%-growth steps
	// Destructive-7's report describes ("starting baseline
	// PerMonthTick=100ms, 30 successive simulated commits each
	// stepping..."), so 30 GROWTH STEPS happen after the starting
	// point — 1.09^30 ≈ 13.27x, matching the live-verified figure
	// exactly.
	current := start
	for i := 0; i < steps+1; i++ {
		rec := PerfRecord{
			CommitHash: fmt.Sprintf("commit%d", i+1),
			Preset:     "1M",
			Result:     PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: current, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true},
		}
		if err := AppendResult(path, rec); err != nil {
			t.Fatalf("AppendResult(step %d, value=%v): %v", i+1, current, err)
		}
		current = time.Duration(float64(current) * growth)
	}

	// Sanity-check the fixture actually reproduces the live-verified
	// SHAPE (each step individually under threshold, compounding to a
	// large multiple) before asserting anything about the fix.
	if current < 13*start {
		t.Fatalf("test fixture does not reproduce the ratchet shape: final synthetic value = %v, want >= 13x the %v starting point (matching Destructive-7's live-verified 13.27x)", current, start)
	}

	// RED (documented against this exact fixture, not re-executed
	// against removed code): what the pre-fix LoadLatestBaseline would
	// have returned — the fully-ratcheted final value, because it
	// trusted "last appended" unconditionally.
	legacy := legacyLastAppendedBaseline(t, path, "1M")
	if legacy == nil || legacy.PerMonthTick < 13*start {
		t.Fatalf("test setup: legacyLastAppendedBaseline = %+v, want ~13x the starting %v — this documents the bug being fixed, not the fix itself", legacy, start)
	}

	// GREEN: the FIXED LoadLatestBaseline must not return anywhere near
	// that ratcheted figure. See limits.go's CumulativeRegressionThreshold
	// doc comment for the arithmetic: by the 4th commit, cumulative
	// drift from the anchor (fixed at the very first recorded value,
	// 100ms) already exceeds the 20% cumulative ceiling, freezing the
	// reconstructed baseline there — long before the remaining 26
	// commits compound to 13x.
	got, anchor, corrupt, err := LoadLatestBaseline(path, "1M", nil)
	if err != nil {
		t.Fatalf("LoadLatestBaseline: %v", err)
	}
	if len(corrupt) != 0 {
		t.Fatalf("unexpected corrupt lines: %+v", corrupt)
	}
	if got == nil {
		t.Fatal("LoadLatestBaseline returned nil baseline")
	}
	if anchor == nil || anchor.PerMonthTick != start {
		t.Fatalf("anchor = %+v, want the FIRST recorded measurement (%v), unmoved by any of the 30 individually-passing commits (BUG-083: the anchor only moves on an explicit, registry-corroborated AcceptedRegression record — BUG-095)", anchor, start)
	}
	// Generous headroom (2x the anchor) — the assertion that matters is
	// "nowhere near 13x", not a tightly tuned exact figure.
	const driftCeiling = 2 * start
	if got.PerMonthTick >= driftCeiling {
		t.Fatalf("LoadLatestBaseline reconstructed baseline = %v — BUG-083 NOT fixed: the ratchet-by-inches drift was not stopped (want < %v, chosen to be nowhere near the 13.27x live-verified failure, not a precisely tuned boundary)", got.PerMonthTick, driftCeiling)
	}
	if got.PerMonthTick >= legacy.PerMonthTick {
		t.Fatalf("fixed baseline (%v) is not smaller than the legacy last-appended baseline (%v) — the fix should freeze well short of the fully-ratcheted figure", got.PerMonthTick, legacy.PerMonthTick)
	}

	// Prove the fix actually CATCHES the drift going forward, not just
	// reconstructs a smaller number: comparing the fully-drifted final
	// measurement against the reconstructed baseline+anchor must report
	// a genuine regression — the exact CI signal Destructive-7 found
	// completely absent across all 30 steps.
	cmp := CompareToBaseline(got, anchor, *legacy)
	if !cmp.Regressed {
		t.Fatalf("CompareToBaseline(reconstructed baseline, anchor, fully-drifted current) = %+v, want Regressed=true — the whole point of freezing the baseline is that the drifted value now fails a real comparison instead of silently becoming the new normal", cmp)
	}
}

// TestLoadLatestBaseline_FreezesOnSingleRegressionInsteadOfRatcheting
// is the simpler, single-commit half of BUG-083: even one genuine
// (over-RegressionThreshold) regression must not become the next
// baseline just because AppendResult still records it for history
// (AC-5) — this is also the property that fixes .github/workflows/
// ci.yml's `if: always()` interaction for free: a run that reds out CI
// (exit 1) still gets its measurement cached, but that measurement
// must never be trusted as the next comparison point.
func TestLoadLatestBaseline_FreezesOnSingleRegressionInsteadOfRatcheting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	good := PerfRecord{CommitHash: "commit1", Preset: "1M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 100 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	if err := AppendResult(path, good); err != nil {
		t.Fatalf("AppendResult(good): %v", err)
	}
	// +50%: comfortably over RegressionThreshold. Still appended (AC-5
	// history/graphing is unaffected by this fix — only what becomes
	// the reconstructed BASELINE changes), simulating exactly what
	// ci.yml's perf-smoke job does today: AppendResult runs before the
	// exit code is returned, and the `if: always()` cache-save step
	// persists it regardless of the job's own pass/fail result.
	regressed := PerfRecord{CommitHash: "commit2", Preset: "1M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 150 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	if err := AppendResult(path, regressed); err != nil {
		t.Fatalf("AppendResult(regressed): %v", err)
	}

	got, anchor, corrupt, err := LoadLatestBaseline(path, "1M", nil)
	if err != nil {
		t.Fatalf("LoadLatestBaseline: %v", err)
	}
	if len(corrupt) != 0 {
		t.Fatalf("unexpected corrupt lines: %+v", corrupt)
	}
	if got == nil || got.PerMonthTick != good.Result.PerMonthTick {
		t.Fatalf("LoadLatestBaseline = %+v, want the FROZEN pre-regression value (%v) — BUG-083: a regressed record must never become the next baseline just because it was appended", got, good.Result.PerMonthTick)
	}
	if anchor == nil || anchor.PerMonthTick != good.Result.PerMonthTick {
		t.Fatalf("anchor = %+v, want it to also stay at the pre-regression value — a genuine regression must not move the anchor either", anchor)
	}
}

// TestAppendResult_RejectsImplausibleNegativeValues is BUG-085's write-
// boundary regression test: a hand-crafted record with a negative
// CitizenCount, Months, or PerMonthTick — values a real RunPerf
// measurement can never structurally produce — must be rejected before
// any write happens, the same shape as BUG-055's Measured check.
func TestAppendResult_RejectsImplausibleNegativeValues(t *testing.T) {
	cases := []struct {
		name   string
		result PerfResult
	}{
		{"negative CitizenCount", PerfResult{CitizenCount: -1, Months: 3, PerMonthTick: 10 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}},
		{"negative Months", PerfResult{CitizenCount: 1000, Months: -5, PerMonthTick: 10 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}},
		{"negative PerMonthTick", PerfResult{CitizenCount: 1000, Months: 3, PerMonthTick: -1 * time.Hour, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "perf-results.ndjson")
			rec := PerfRecord{CommitHash: "attacker", Preset: "1M", Result: tc.result}

			err := AppendResult(path, rec)
			wantCode(t, err, codeImplausibleResult)

			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatalf("AppendResult rejected the implausible record but still touched %q on disk (stat err: %v)", path, statErr)
			}
		})
	}
}

// TestLoadLatestBaseline_RejectsHandInjectedImplausibleRecord is
// BUG-085's read-boundary regression test, mirroring BUG-073's
// Measured-recheck pattern: a hand-injected record bypassing
// AppendResult entirely (Measured=true, so BUG-073's check alone would
// accept it) but carrying a negative CitizenCount/Months/PerMonthTick —
// live-verified: PerfResult{CitizenCount: -1, Months: -5,
// PerMonthTick: -1h, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true} was accepted as the trusted
// baseline with err == nil and zero CorruptLine entries before this
// fix. RED against the pre-BUG-085 LoadLatestBaseline (would return
// the fabricated negative-valued record); GREEN against the fix, which
// treats it as a CorruptLine and falls back to the last genuinely
// plausible record.
func TestLoadLatestBaseline_RejectsHandInjectedImplausibleRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	legit := PerfRecord{CommitHash: "commit1", Preset: "1M", Result: PerfResult{CitizenCount: 1000, Months: 3, PerMonthTick: 100 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	if err := AppendResult(path, legit); err != nil {
		t.Fatalf("AppendResult(legit): %v", err)
	}

	fabricated := PerfRecord{CommitHash: "attacker", Preset: "1M", Result: PerfResult{CitizenCount: -1, Months: -5, PerMonthTick: -1 * time.Hour, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	data, err := json.Marshal(fabricated)
	if err != nil {
		t.Fatalf("marshalling fabricated record: %v", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening results file to hand-inject a record: %v", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatalf("writing fabricated record: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing results file: %v", err)
	}

	got, _, corrupt, err := LoadLatestBaseline(path, "1M", nil)
	if err != nil {
		t.Fatalf("LoadLatestBaseline: got err %v, want nil — the legitimate earlier record should still be recoverable", err)
	}
	if got == nil || got.PerMonthTick != legit.Result.PerMonthTick {
		t.Fatalf("LoadLatestBaseline returned %+v — BUG-085: a hand-injected record with a structurally impossible negative value was trusted as the latest baseline instead of falling back to the last genuinely plausible one", got)
	}
	if len(corrupt) != 1 {
		t.Fatalf("corrupt lines = %+v, want exactly 1 (the implausible record) — GR#17 requires this be reported, not silently dropped", corrupt)
	}
}

// TestAppendResult_RejectsUnjustifiedAcceptedRegression is BUG-083's
// write-boundary check on its own override mechanism: AcceptedRegression
// with no AcceptedReason is exactly as untrustworthy as an unmeasured
// record — cmd/perfci's accept path always pairs it with a required,
// registry-sourced reason (BUG-095, accepted.go), but AppendResult (the
// actual write boundary any caller must go through) re-enforces that
// itself rather than trusting the CLI to have done so.
func TestAppendResult_RejectsUnjustifiedAcceptedRegression(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")
	rec := PerfRecord{
		CommitHash:         "commit1",
		Preset:             "1M",
		Result:             PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 200 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true},
		AcceptedRegression: true,
		// AcceptedReason deliberately left empty.
	}
	err := AppendResult(path, rec)
	wantCode(t, err, codeUnjustifiedAcceptedRegression)

	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("AppendResult rejected the unjustified override but still touched %q on disk (stat err: %v)", path, statErr)
	}
}

// TestLoadLatestBaseline_HonorsRegistryCorroboratedAcceptedRegression is
// BUG-083's "how does a legitimate, intended slowdown ever become the new
// baseline" answer, exercised at the storage layer, UPDATED for BUG-095: a
// record explicitly marked AcceptedRegression (with a reason) becomes BOTH
// the reconstructed baseline AND the reset cumulative anchor ONLY when its
// (Preset, CommitHash) is ALSO present in the git-committed AcceptedRegistry
// passed in — the deliberate, visible, now-corroborated escape hatch from
// BUG-083's freeze, as opposed to an ordinary regressed record (see
// TestLoadLatestBaseline_FreezesOnSingleRegressionInsteadOfRatcheting),
// which must NOT do this, and as opposed to an UNCORROBORATED accepted
// record (see TestLoadLatestBaseline_RejectsUncorroboratedAcceptedRegression
// below — BUG-095's direct attack reproduction), which also must NOT.
func TestLoadLatestBaseline_HonorsRegistryCorroboratedAcceptedRegression(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	original := PerfRecord{CommitHash: "commit1", Preset: "1M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 100 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	if err := AppendResult(path, original); err != nil {
		t.Fatalf("AppendResult(original): %v", err)
	}

	accepted := PerfRecord{
		CommitHash:         "commit2",
		Preset:             "1M",
		Result:             PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 300 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}, // +200%, a genuine regression
		AcceptedRegression: true,
		AcceptedReason:     "engine.core gained a real phase hook this commit; the slowdown is expected and reviewed",
	}
	if err := AppendResult(path, accepted); err != nil {
		t.Fatalf("AppendResult(accepted): %v", err)
	}

	registry := AcceptedRegistry{
		{Preset: "1M", CommitHash: "commit2"}: "engine.core gained a real phase hook this commit; the slowdown is expected and reviewed",
	}

	got, anchor, corrupt, err := LoadLatestBaseline(path, "1M", registry)
	if err != nil {
		t.Fatalf("LoadLatestBaseline: %v", err)
	}
	if len(corrupt) != 0 {
		t.Fatalf("unexpected corrupt lines: %+v", corrupt)
	}
	if got == nil || got.PerMonthTick != accepted.Result.PerMonthTick {
		t.Fatalf("LoadLatestBaseline = %+v, want the registry-corroborated accepted regression (%v) to become the new baseline", got, accepted.Result.PerMonthTick)
	}
	if anchor == nil || anchor.PerMonthTick != accepted.Result.PerMonthTick {
		t.Fatalf("anchor = %+v, want the accepted record to ALSO reset the cumulative anchor (an accept resets both reference points)", anchor)
	}
}

// TestLoadLatestBaseline_RejectsUncorroboratedAcceptedRegression is
// BUG-095's core regression test — the attacker's own reproduction,
// exactly as live-verified: a hand-crafted PerfRecord written directly via
// AppendResult (bypassing cmd/perfci's real accept flow entirely), with
// AcceptedRegression=true and a plausible-sounding, non-empty
// AcceptedReason (so it clears every check that existed BEFORE this fix),
// but with NO corresponding entry in the AcceptedRegistry passed to
// LoadLatestBaseline. RED against the pre-BUG-095 LoadLatestBaseline
// (which honoured AcceptedRegression on the record's own say-so and would
// have reset both baseline and anchor to the forged 6ms figure — the
// live-verified consequence being a genuine, unregressed 19ms run then
// reporting Regressed=true at a 216% delta against the forged anchor).
// GREEN against the fix: the forged record is reported (CorruptLine) and
// then replayed as an ORDINARY, non-accepted measurement — since it is
// itself a (fabricated) regression against the real history, it must not
// even advance the step-to-step baseline, let alone the anchor.
func TestLoadLatestBaseline_RejectsUncorroboratedAcceptedRegression(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	// A real, 5-record history at a stable ~18-20ms PerMonthTick —
	// mirroring Destructive-9's live reproduction fixture.
	genuine := []time.Duration{18, 19, 20, 19, 20}
	for i, ms := range genuine {
		rec := PerfRecord{
			CommitHash: fmt.Sprintf("genuine%d", i+1),
			Preset:     "1M",
			Result:     PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: ms * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true},
		}
		if err := AppendResult(path, rec); err != nil {
			t.Fatalf("AppendResult(genuine %d): %v", i+1, err)
		}
	}

	// The forged record: AcceptedRegression=true, a reason that reads as
	// legitimate, PerMonthTick collapsed to 6ms — written directly via
	// AppendResult, exactly as an attacker (or a second, non-cmd/perfci
	// writer) would, with NO registry entry backing it.
	forged := PerfRecord{
		CommitHash:         "attacker-commit",
		Preset:             "1M",
		Result:             PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 6 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true},
		AcceptedRegression: true,
		AcceptedReason:     "reviewed by aaron, approved cumulative drift",
	}
	if err := AppendResult(path, forged); err != nil {
		t.Fatalf("AppendResult(forged): %v", err)
	}

	// Empty registry: nobody has ever committed an acceptance entry for
	// this commit.
	got, anchor, corrupt, err := LoadLatestBaseline(path, "1M", AcceptedRegistry{})
	if err != nil {
		t.Fatalf("LoadLatestBaseline: %v", err)
	}
	if len(corrupt) != 1 {
		t.Fatalf("corrupt lines = %+v, want exactly 1 (the uncorroborated accepted-regression record) — GR#17 requires this be reported, not silently trusted or silently dropped", corrupt)
	}
	if got == nil || got.PerMonthTick == 6*time.Millisecond {
		t.Fatalf("LoadLatestBaseline baseline = %+v — BUG-095: the forged AcceptedRegression record moved the baseline to its own attacker-chosen value with no registry corroboration", got)
	}
	if anchor == nil || anchor.PerMonthTick == 6*time.Millisecond {
		t.Fatalf("LoadLatestBaseline anchor = %+v — BUG-095: the forged AcceptedRegression record moved the ANCHOR to its own attacker-chosen value with no registry corroboration; this is the exact mechanism that turned a genuine 19ms run into a reported 216%% regression", anchor)
	}

	// The killer assertion: a genuine, unregressed 19ms run — identical in
	// shape to the real history — must still compare as NOT regressed
	// against the reconstructed baseline/anchor, proving the forged
	// record did not poison future comparisons either.
	genuineFollowUp := PerfResult{CitizenCount: OneMillionCitizens, Months: 3, PerMonthTick: 19 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}
	cmp := CompareToBaseline(got, anchor, genuineFollowUp)
	if cmp.Regressed {
		t.Fatalf("CompareToBaseline(reconstructed baseline/anchor, genuine unregressed 19ms run) = %+v, want Regressed=false — BUG-095: a real, unregressed commit must never be forced to fail because of an uncorroborated forged acceptance record", cmp)
	}
}

// TestLoadLatestBaseline_RejectsHandInjectedUnjustifiedAcceptedRegression
// is the read-boundary half of the AcceptedRegression provenance check
// (mirroring BUG-073/BUG-085's write-then-read enforcement shape): a
// record bypassing AppendResult entirely, hand-set to
// AcceptedRegression=true with no AcceptedReason, must not be trusted
// as an accepted override at read time either.
func TestLoadLatestBaseline_RejectsHandInjectedUnjustifiedAcceptedRegression(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	legit := PerfRecord{CommitHash: "commit1", Preset: "1M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 100 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	if err := AppendResult(path, legit); err != nil {
		t.Fatalf("AppendResult(legit): %v", err)
	}

	fabricated := PerfRecord{
		CommitHash:         "attacker",
		Preset:             "1M",
		Result:             PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 999 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true},
		AcceptedRegression: true,
		// AcceptedReason deliberately left empty — bypassing
		// AppendResult's write-boundary check.
	}
	data, err := json.Marshal(fabricated)
	if err != nil {
		t.Fatalf("marshalling fabricated record: %v", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening results file to hand-inject a record: %v", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatalf("writing fabricated record: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing results file: %v", err)
	}

	got, anchor, corrupt, err := LoadLatestBaseline(path, "1M", nil)
	if err != nil {
		t.Fatalf("LoadLatestBaseline: got err %v, want nil — the legitimate earlier record should still be recoverable", err)
	}
	if got == nil || got.PerMonthTick != legit.Result.PerMonthTick {
		t.Fatalf("LoadLatestBaseline = %+v, want the last genuinely legitimate record (%v) — an unjustified AcceptedRegression must not be honoured", got, legit.Result.PerMonthTick)
	}
	if anchor == nil || anchor.PerMonthTick != legit.Result.PerMonthTick {
		t.Fatalf("anchor = %+v, want it unmoved by the unjustified override attempt", anchor)
	}
	if len(corrupt) != 1 {
		t.Fatalf("corrupt lines = %+v, want exactly 1 (the unjustified override) — GR#17 requires this be reported, not silently dropped", corrupt)
	}
}

// TestAppendResult_RejectsZeroValuedCitizenCountAndMonths is ASM-374's
// write-boundary regression test: a hand-crafted Measured=true record
// with a zero CitizenCount or Months — structurally impossible from a
// real RunPerf call — must be rejected before any write, exactly like
// the negative-value cases BUG-085 already covered. Pre-ASM-374 these
// zero-valued records sailed through ImplausibleReason (it checked `< 0`
// only) and could seed a poisoned baseline.
func TestAppendResult_RejectsZeroValuedCitizenCountAndMonths(t *testing.T) {
	cases := []struct {
		name   string
		result PerfResult
	}{
		{"zero CitizenCount", PerfResult{CitizenCount: 0, Months: 3, PerMonthTick: 10 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}},
		{"zero Months", PerfResult{CitizenCount: 1000, Months: 0, PerMonthTick: 10 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "perf-results.ndjson")
			rec := PerfRecord{CommitHash: "attacker", Preset: "1M", Result: tc.result}

			err := AppendResult(path, rec)
			wantCode(t, err, codeImplausibleResult)

			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatalf("AppendResult rejected the implausible record but still touched %q on disk (stat err: %v)", path, statErr)
			}
		})
	}
}

// TestLoadLatestBaseline_LineOverMaxBytesIsCorruptAndRecoverable is
// ASM-355's regression test: the BUG-074 fix removed bufio.Scanner's
// 64KiB token cap entirely by switching to bufio.Reader.ReadString, which
// reads a line of ANY length — so a single multi-GiB line would OOM the
// reader rather than fail gracefully. readResultsLine re-adds a GENEROUS
// finite ceiling (maxResultsLineBytes, 1 MiB) with its own CorruptLine
// path. This test proves two things: (1) a line OVER the ceiling is
// reported as a CorruptLine (never silently trusted as a baseline, never
// read unbounded), and (2) a good, later record is still recovered past
// it — the same BUG-054 recovery contract as a torn line.
func TestLoadLatestBaseline_LineOverMaxBytesIsCorruptAndRecoverable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "perf-results.ndjson")

	// One line strictly longer than maxResultsLineBytes, padded with a
	// huge junk field — mirroring the SHAPE of a PhaseTimings blowup that
	// a real oversized record would take — followed by a genuinely valid,
	// measured record.
	oversized := `{"commitHash":"oversized","preset":"1M","junk":"` + strings.Repeat("x", maxResultsLineBytes+4096) + `"}` + "\n"
	if err := os.WriteFile(path, []byte(oversized), 0o644); err != nil {
		t.Fatalf("writing over-ceiling line: %v", err)
	}

	newer := PerfRecord{CommitHash: "commit2", Preset: "1M", Result: PerfResult{CitizenCount: MinSyntheticCitizens, Months: 1, PerMonthTick: 77 * time.Millisecond, PhaseHookCount: PhaseHookCountInHeadlessPath(), Measured: true}}
	if err := AppendResult(path, newer); err != nil {
		t.Fatalf("AppendResult(newer): %v", err)
	}

	got, _, corrupt, err := LoadLatestBaseline(path, "1M", nil)
	if err != nil {
		t.Fatalf("LoadLatestBaseline: got err %v, want nil — a good later record must recover past the over-ceiling line (ASM-355)", err)
	}
	if got == nil || got.PerMonthTick != newer.Result.PerMonthTick {
		t.Fatalf("LoadLatestBaseline = %+v, want the later good record (%v) — the over-ceiling line must not hide it", got, newer.Result.PerMonthTick)
	}
	if len(corrupt) != 1 {
		t.Fatalf("corrupt lines = %+v, want exactly 1 (the over-ceiling line) — GR#17 requires it be reported, not silently dropped", corrupt)
	}
	if corrupt[0].LineNo != 1 {
		t.Fatalf("corrupt line number = %d, want 1 (the over-ceiling line is the 1st line)", corrupt[0].LineNo)
	}
}
