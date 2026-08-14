// Package uitest is H-UITEST (MOD-014, module key "ui.harness"): a
// headless test harness that drives ui.core's T-INPUT/T-VIEWS seam with
// scripted key sequences and harness.replay fixtures, captures
// human-diffable cell-buffer snapshots, and asserts UI-SPEC §5's
// per-commit latency budgets in CI. "The UI gets the same regression
// rigour as the sim" (UI-SPEC §5).
//
// Module key: ui.harness (see code.json; GUID 626afaec-ce56-46db-bd7c-b2b45f2b29bb)
// Spec ref:   UI-SPEC §5; M0-ENG §6 point 5
//
// # Latency budgets this package asserts (UI-SPEC §5, transcribed)
//
// AC-10/AC-11 require these as hard CI test failures (t.Fatalf), never a
// logged warning:
//
//   - Keystroke -> echo:            < 10ms  (TestLatencyKeystrokeEcho)
//   - Screen switch:                < 30ms  (TestLatencyScreenSwitch)
//   - Full-terminal diff flush, typical: < 3ms (TestLatencyDiffFlushTypical)
//   - Full-terminal diff flush, worst (on resize): < 8ms (TestLatencyDiffFlushWorst)
//
// UI-SPEC §5 also names a pane-focus (<5ms) and a map-pan (<8ms) budget;
// both are screen-specific interactions with no generic ui.core seam to
// drive headlessly yet (no F-screen owns focus/pan logic as of this
// package's build — MOD-015+ land later sprints). AC-10's own wording is
// "at least" the four above; the other two are OUT OF SCOPE here and
// logged as an assumption (see this item's BOW record) rather than
// silently dropped — a future F-screen's own test package is the natural
// place to add them once there is a real pane-focus/map-pan
// implementation to time.
//
// # Files
//
//   - keyscript.go — ParseScript: the scripted key-sequence DSL (AC-2).
//   - eventsource.go — chanEventSource: a core.EventSource the harness
//     controls directly, the headless stand-in for a real terminal's
//     input stream.
//   - transport.go — fixturePlayback: adapts a harness.replay.UIPlayer
//     into a protocol.Transport-shaped, read-only Results/Events/Deltas
//     source (AC-3), counting forwarded deltas so AC-3b's
//     fixture-exhausted condition can be detected deterministically.
//   - harness.go — Harness: constructs ui.core's InputLoop/ViewStore/
//     ViewsLoop headlessly, injects key events, drives DrawFuncs, and
//     captures the resulting cell buffer (AC-1, AC-4).
//   - snapshot.go — AssertSnapshot: golden-file comparison with a
//     -update mode (AC-5, AC-6, AC-8) and a hostile-name guard (AC-5b).
//   - errors.go — MET-H1xx registry codes (GR#7).
//
// # The scripted key-sequence DSL (AC-2, AC-2b)
//
// A script is a whitespace-separated sequence of tokens. Each token is
// EITHER:
//
//  1. exactly one non-whitespace UTF-8 rune (e.g. "b", "r", "5"), which
//     becomes a KeyRune event carrying that rune; OR
//  2. a named special of the form "<Name>", where Name is one of exactly
//     this documented, closed set (never arbitrary Go format-string
//     syntax, never shell-like expansion):
//     Space, Esc, Enter, Tab, Backspace, Left, Right, Up, Down, Home,
//     End, PgUp, PgDn, Delete, F1, F2, F3, F4, F5, F6, F7, F8, F9, F10,
//     F11, F12.
//
// Any token that is neither of the above — a multi-rune token not in
// "<Name>" form, an unbalanced "<...>", or a "<Name>" not in the list —
// is a PARSE-TIME rejection naming the offending token and its 1-based
// position in the script (MET-H100, AC-2b). It is never silently
// dropped, never interpreted as a literal sequence of its own
// characters, and never partially applied — ParseScript returns no
// events at all on a rejection, so a caller cannot accidentally act on
// the prefix that did parse.
//
// # Copy-safety (SEC-020-class)
//
// Harness holds a sync.Mutex alongside reference fields (buffers,
// channels, a WaitGroup) and follows this codebase's standard
// self-identity pattern (mirrors harness.replay's Recorder/EnginePlayer,
// which mirror internal/protocol/transport.go's InProcTransport.self):
// an atomic.Pointer[Harness] identity check runs BEFORE any mutex is
// touched, rejecting a struct-copied receiver with a registry-sourced
// error (MET-H104) rather than risking a permanent lock-order hang
// (SEC-016) or a torn buffer read. Always use the *Harness NewHarness
// returned; never dereference-and-copy it.
//
// # Snapshot workflow (AC-14 — adding a new screen's snapshot test)
//
//  1. Build a Harness with the screen's real DrawFunc(s):
//     h := uitest.NewHarness(correlationID, onKey, screen.Draw)
//  2. Optionally attach a harness.replay fixture as the delta source:
//     h.AttachFixture(fixture)
//  3. Drive it: h.RunScript("b r s", wantDeltas, timeout) or h.SendKeys(...)
//  4. Render + capture: h.Render(); got := h.Capture()
//  5. Compare against (or create) the golden:
//     uitest.AssertSnapshot(t, got)
//
// The golden file lives at testdata/snapshots/<t.Name()>.golden — plain
// text, one line per buffer row, human-diffable in a PR (AC-5). The
// first run (or any intentional change) is captured with:
//
//	go test ./internal/harness/uitest/... -run <TestName> -update
//
// then the generated file is reviewed like any other diff before commit
// — -update is a deliberate, separate mode, never on by default (AC-6).
//
// # Snapshot names are path segments, not labels (AC-5b, weakness pattern #4)
//
// A snapshot's file name is derived ONLY from t.Name() — never a
// caller-supplied free string — but t.Name() is not, in fact,
// universally safe: Go's testing package does not forbid a subtest named
// ".." or containing "/". So every '/'-delimited segment of t.Name() (Go
// uses "/" as its own subtest-hierarchy separator, which this package
// preserves as nested snapshot directories) is validated with
// [serialize.ValidateShardName] — the SAME function harness.replay's
// fixture names and serialize.ShardMeta.Name are checked with, reused
// rather than reimplemented (weakness pattern #2) — before being joined
// into a path, and the fully resolved path is confirmed to still fall
// under testdata/snapshots/ as defence in depth. A hostile segment is
// rejected outright (MET-H103), never filepath.Clean'd or substituted.
//
// # Determinism (GR#21)
//
// This package never calls time.Now/time.Since on the key-injection,
// delta-consumption, or snapshot-capture path (grep -rn
// "time\.Now|time\.Since" internal/harness/uitest/*.go, excluding
// _test.go, returns matches only inside the latency-budget tests
// themselves, which are explicitly timing UI-SPEC §5's own budgets, not
// gating correctness on wall time). Waiting for an attached fixture's
// deltas (AwaitDeltas) is driven by a real completion signal — a count
// reaching the caller-stated expectation, or the fixture's Delta channel
// closing (exhausted) — never a blind sleep; the same
// poll-a-real-condition idiom internal/ui/core's own test suite uses
// (harness_test.go's waitForCondition). Running the same script against
// the same fixture twice therefore produces byte-identical Capture()
// output both times (AC-9, TestDeterministicCapture).
//
// # Cell-buffer capture scope (assumption, logged)
//
// Capture() renders only each Cell's Rune into the human-diffable golden
// text, not its tcell.Style — a reasonable person could expect full
// style diffing too (colours, bold, etc.), but a plain-rune grid is what
// stays human-diffable as plain text in a PR (AC-5's explicit
// requirement), and this package's own Flush call (via core.Flush) still
// exercises the real styled Cell path end-to-end even though the golden
// text itself only records the rune. Logged as an assumption against
// this item's BOW record.
package uitest
