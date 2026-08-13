package main

import (
	"bytes"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// --- FEAT-035 AC-M1 (mandatory, non-negotiable) ---
//
// This is the end-to-end test FEAT-035's own acceptance doc names as
// "the single most important AC in this file": it invokes
// cmd/metropolis's real run() function -- the actual -headless flag
// parsing and dispatch path, never a hand-assembled headless.Config{}
// literal that bypasses flag parsing -- twice, as two genuinely
// separate calls standing in for two separate process invocations, and
// proves the DebugTouched sticky flag survives BOTH hops:
//
//  1. First hop: run() with -debug set writes a bundle. Its
//     header.json (read back from disk via serialize.ReadHeader, never
//     from an in-memory debug.State) must be DebugTouched.
//  2. Second hop: run() with -in pointed at the first hop's bundle, and
//     WITHOUT -debug, writes a second bundle. Its header.json read back
//     from disk must ALSO be DebugTouched, despite this second run()
//     call never enabling debug at all -- the only thing carrying the
//     flag forward is Engine.Snapshot's MergeDebugTouched merge of the
//     on-disk prior header (internal/harness/headless/run.go's Run,
//     which reads -in via serialize.ReadHeader itself, exactly as this
//     test's own second assertion does independently).
//
// See feat.debugmode.md's AC-M1 "what a lazy implementation looks
// like" section for the shape this test deliberately avoids: it never
// constructs a *debug.State or *serialize.Header by hand and never
// threads DebugTouched between the two hops out of band -- the second
// hop's only source of "this lineage was debug-touched" is the first
// hop's on-disk header.json, read by the real headless.Run code path.
func TestFEAT035_DebugTouched_SurvivesTwoSnapshotReloadHops(t *testing.T) {
	hop1Dir := t.TempDir() + "/hop1"
	var stdout1, stderr1 bytes.Buffer
	code1 := run([]string{
		"-headless", "-seed", "1", "-months", "1", "-out", hop1Dir, "-debug",
	}, &stdout1, &stderr1)
	if code1 != 0 {
		t.Fatalf("hop 1: run(-headless -debug) = %d, want 0 (stderr=%q)", code1, stderr1.String())
	}

	hop1Header, err := serialize.ReadHeader(hop1Dir)
	if err != nil {
		t.Fatalf("hop 1: serialize.ReadHeader(%q): %v", hop1Dir, err)
	}
	if !hop1Header.DebugTouched() {
		t.Fatalf("hop 1: header.json's DebugTouched() = false, want true (this run enabled debug via -debug)")
	}

	// Second hop: a genuinely separate run() call, -in pointed at hop
	// 1's bundle, and -debug deliberately NOT passed -- the only way
	// this hop's own header can end up DebugTouched is the real
	// carry-forward path (headless.Run reading hop1Dir/header.json and
	// merging it via Engine.Snapshot's prior argument).
	hop2Dir := t.TempDir() + "/hop2"
	var stdout2, stderr2 bytes.Buffer
	code2 := run([]string{
		"-headless", "-seed", "1", "-months", "1", "-out", hop2Dir, "-in", hop1Dir,
	}, &stdout2, &stderr2)
	if code2 != 0 {
		t.Fatalf("hop 2: run(-headless -in %s) = %d, want 0 (stderr=%q)", hop1Dir, code2, stderr2.String())
	}

	hop2Header, err := serialize.ReadHeader(hop2Dir)
	if err != nil {
		t.Fatalf("hop 2: serialize.ReadHeader(%q): %v", hop2Dir, err)
	}
	if !hop2Header.DebugTouched() {
		t.Fatalf("hop 2: header.json's DebugTouched() = false, want true (must survive the reload from hop 1's on-disk header, even though hop 2 never passed -debug)")
	}
}

// TestFEAT035_DebugTouched_FalsePassGuard is the negative control AC-M1's
// own "false-pass warning" implies: a run() that neither enables debug
// nor resumes from a prior bundle must write a header that is NOT
// DebugTouched -- proving the carry-forward path above is genuinely
// OR-merge-shaped (AC-S1) rather than a change that marks every
// snapshot DebugTouched unconditionally (which would make the mandatory
// test above pass for the wrong reason).
func TestFEAT035_DebugTouched_FalsePassGuard(t *testing.T) {
	dir := t.TempDir() + "/clean"
	var stdout, stderr bytes.Buffer
	code := run([]string{"-headless", "-seed", "1", "-months", "1", "-out", dir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(-headless) = %d, want 0 (stderr=%q)", code, stderr.String())
	}

	header, err := serialize.ReadHeader(dir)
	if err != nil {
		t.Fatalf("serialize.ReadHeader(%q): %v", dir, err)
	}
	if header.DebugTouched() {
		t.Fatalf("header.json's DebugTouched() = true, want false (neither -debug nor -in was passed -- a run with no debug history must default clean, AC-S2)")
	}
}
