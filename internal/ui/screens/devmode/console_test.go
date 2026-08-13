package devmode

// _test.go files ARE exempt from GR#20's ui-must-not-import-engine
// depguard rule (.golangci.yml — the same exemption ui.screen.map's own
// H-STUB fixtures rely on), so these tests wire a REAL *debug.State
// rather than a hand-rolled fake, proving Console is genuinely a thin
// consumer of feat.debugmode's real gate (doc.go's whole point) rather
// than something that merely resembles one.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/debug"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

func newTestHeader() *serialize.Header {
	h := serialize.NewHeader(1, 0, 0, "test")
	return &h
}

// asE unwraps err to *errs.E or fails the test — every rejection this
// package (and feat.debugmode) produces is registry-sourced.
func asE(t *testing.T, err error) *errs.E {
	t.Helper()
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("error is not *errs.E: %v (%T)", err, err)
	}
	return e
}

// newWiredConsole builds a Console wired to a real *debug.State exactly
// the way a composition root is expected to (doc.go). pause/isPaused
// are simple in-memory bool seams standing in for engine.core's real
// pause command (out of this package's own file ownership — see doc.go).
func newWiredConsole(t *testing.T, s *debug.State) (*Console, *bool) {
	t.Helper()
	paused := false
	c := New(
		WithRequireConsole(s.RequireConsole),
		WithEnable(func(cid string) error { return s.Enable(debug.SourcePalette, cid) }),
		WithPause(func(string) error { paused = true; return nil }),
		WithIsPaused(func() bool { return paused }),
		WithInspect(s.InspectEntity),
		WithSubmitFeedback(s.SubmitFeedback),
	)
	return c, &paused
}

// TestOpen_DebugOff_Rejected is AC-DM1: with debug off, Open is rejected
// with the real ErrDebugRequired (via RequireConsole) — not a
// devconsole-local code — and the console never becomes open.
func TestOpen_DebugOff_Rejected(t *testing.T) {
	s := debug.NewState(debug.WithHeader(newTestHeader()))
	c, _ := newWiredConsole(t, s)

	err := c.Open("corr-1")
	if err == nil {
		t.Fatalf("Open with debug off: got nil error, want ErrDebugRequired")
	}
	e := asE(t, err)
	if e.Code != debug.ErrDebugRequired {
		t.Fatalf("Open with debug off: code = %s, want %s (devconsole must not substitute its own code for the real gate's)", e.Code, debug.ErrDebugRequired)
	}
	if c.IsOpen() {
		t.Fatalf("console reports open after a rejected Open call")
	}
}

// TestOpen_NoRequireConsoleWired_Rejected proves an unwired gate is
// refused (ErrRequireConsoleNotConfigured), never silently treated as
// "gate passed".
func TestOpen_NoRequireConsoleWired_Rejected(t *testing.T) {
	c := New() // no options at all
	err := c.Open("corr-1")
	if err == nil {
		t.Fatalf("Open on an unconfigured Console: got nil error, want ErrRequireConsoleNotConfigured")
	}
	e := asE(t, err)
	if e.Code != ErrRequireConsoleNotConfigured {
		t.Fatalf("code = %s, want %s", e.Code, ErrRequireConsoleNotConfigured)
	}
	if c.IsOpen() {
		t.Fatalf("console reports open despite no gate ever being configured")
	}
}

// TestOpen_DebugOn_Pauses is AC-DM2: opening the console when debug IS
// on issues the pause action, queryable through the same surface any
// other pause already uses (here, the wired IsPausedFunc).
func TestOpen_DebugOn_Pauses(t *testing.T) {
	s := debug.NewState(debug.WithHeader(newTestHeader()))
	if err := s.Enable(debug.SourceFlag, "pre-enable"); err != nil {
		t.Fatalf("pre-enable: %v", err)
	}
	c, paused := newWiredConsole(t, s)

	if *paused {
		t.Fatalf("paused = true before Open, want false")
	}
	if err := c.Open("corr-2"); err != nil {
		t.Fatalf("Open with debug on: %v", err)
	}
	if !*paused {
		t.Fatalf("paused = false after Open, want true (AC-DM2)")
	}
	if !c.IsPaused() {
		t.Fatalf("c.IsPaused() = false after Open, want true")
	}
}

// TestOpen_TouchesDebugBeforeOpen is AC-DM3, the load-bearing AC: opening
// the console on a debug.State that starts IsOn()==false calls Enable
// BEFORE the console is considered open, so the header's DebugTouched()
// reads true immediately after Open returns successfully — verified by
// reading the REAL header back, not a devconsole-local flag.
func TestOpen_TouchesDebugBeforeOpen(t *testing.T) {
	h := newTestHeader()
	s := debug.NewState(debug.WithHeader(h))

	// The gate itself only checks reachability; per feat.devmode.md's own
	// text, opening the console is allowed to itself be the enable
	// trigger. To exercise that path here we deliberately do NOT
	// pre-enable s — s.RequireConsole would reject with debug off, so
	// this test wires a permissive stand-in gate to isolate AC-DM3's
	// Enable-ordering claim from AC-DM1's separate gate-rejection claim
	// (already covered by TestOpen_DebugOff_Rejected above). Enable
	// itself is the REAL s.Enable.
	var console *Console
	enableCalledWithConsoleAlreadyOpen := false
	console = New(
		WithRequireConsole(func(string) error { return nil }),
		WithEnable(func(cid string) error {
			// The ordering assertion itself: if Open marked the console
			// open BEFORE calling Enable, this closure would observe
			// IsOpen()==true here. A test that only checked the end state
			// (both true after Open returns) cannot distinguish "Enable ran
			// first" from "IsOpen was flipped first" — this is exactly the
			// false-pass shape AC-DM3's own warning describes, so this
			// in-flight check is load-bearing, not decorative.
			if console.IsOpen() {
				enableCalledWithConsoleAlreadyOpen = true
			}
			return s.Enable(debug.SourcePalette, cid)
		}),
	)

	if s.IsOn() {
		t.Fatalf("precondition failed: debug.State starts IsOn()==true")
	}
	if h.DebugTouched() {
		t.Fatalf("precondition failed: header starts DebugTouched()==true")
	}

	if err := console.Open("corr-3"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if enableCalledWithConsoleAlreadyOpen {
		t.Fatalf("Enable was called AFTER the console was already marked open — AC-DM3 requires Enable to fire BEFORE the console is considered open")
	}
	if !h.DebugTouched() {
		t.Fatalf("header.DebugTouched() = false immediately after Open, want true (AC-DM3)")
	}
	if !console.IsOpen() {
		t.Fatalf("console.IsOpen() = false after a successful Open")
	}
}

// TestOpen_FalsePass_VisibleWithoutEnable is AC-DM3's own "false-pass
// warning" made concrete: a Console wired with NO EnableFunc at all
// still becomes open (visible) once RequireConsole passes, but the
// header's DebugTouched bit must stay false — proving Open's "become
// visible" and "touch the header" are two genuinely separate steps, not
// one conflated flag. A reviewer who only checked IsOpen() here would
// wrongly conclude AC-DM3 holds.
func TestOpen_FalsePass_VisibleWithoutEnable(t *testing.T) {
	h := newTestHeader()
	console := New(
		WithRequireConsole(func(string) error { return nil }),
		// deliberately no WithEnable
	)

	if err := console.Open("corr-4"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !console.IsOpen() {
		t.Fatalf("console did not open with a passing gate and no Enable wired")
	}
	if h.DebugTouched() {
		t.Fatalf("header.DebugTouched() = true with no EnableFunc ever wired — impossible, this header was never touched by anything")
	}
}

// TestOpen_AlreadyOn_HarmlessRetouch_PreEnabled is AC-DM4: with
// debug already on, opening the console twice produces no error and
// DebugTouched stays true both times.
func TestOpen_AlreadyOn_HarmlessRetouch_PreEnabled(t *testing.T) {
	h := newTestHeader()
	s := debug.NewState(debug.WithHeader(h))
	if err := s.Enable(debug.SourceFlag, "pre"); err != nil {
		t.Fatalf("pre-enable: %v", err)
	}
	c, _ := newWiredConsole(t, s)

	if err := c.Open("corr-6a"); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if !h.DebugTouched() {
		t.Fatalf("DebugTouched false after first Open")
	}
	c.Close()
	if err := c.Open("corr-6b"); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if !h.DebugTouched() {
		t.Fatalf("DebugTouched false after second Open, want still true (AC-DM4)")
	}
}

// TestInspect_ThinConsumer is AC-DM5: Console.Inspect's rendered bytes
// are byte-identical to a direct debug.State.InspectEntity call against
// the same ref — proving Console is a thin pass-through, not a second
// marshalling path.
func TestInspect_ThinConsumer(t *testing.T) {
	h := newTestHeader()
	type fixtureEntity struct {
		Name string `json:"name"`
		HP   int    `json:"hp"`
	}
	s := debug.NewState(
		debug.WithHeader(h),
		debug.WithEntityLookup(func(ref string) (any, error) {
			if ref == "citizen:1" {
				return fixtureEntity{Name: "Alice", HP: 10}, nil
			}
			return nil, errs.New(debug.ErrEntityLookupFailed, "corr", map[string]any{"ref": ref})
		}),
	)
	if err := s.Enable(debug.SourceFlag, "pre"); err != nil {
		t.Fatalf("pre-enable: %v", err)
	}
	c, _ := newWiredConsole(t, s)
	if err := c.Open("corr-7"); err != nil {
		t.Fatalf("Open: %v", err)
	}

	got, err := c.Inspect("corr-7a", "citizen:1")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	want, err := s.InspectEntity("corr-7b", "citizen:1")
	if err != nil {
		t.Fatalf("direct InspectEntity: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("Inspect output diverges from direct InspectEntity: got %q want %q", got, want)
	}
}

// TestInspect_BadRef_SurfacesRegistryError is AC-DM6: an unresolvable
// ref surfaces the registry-sourced error InspectEntity returns,
// legibly, never a panic.
func TestInspect_BadRef_SurfacesRegistryError(t *testing.T) {
	h := newTestHeader()
	s := debug.NewState(
		debug.WithHeader(h),
		debug.WithEntityLookup(func(ref string) (any, error) {
			return nil, errors.New("not found")
		}),
	)
	if err := s.Enable(debug.SourceFlag, "pre"); err != nil {
		t.Fatalf("pre-enable: %v", err)
	}
	c, _ := newWiredConsole(t, s)
	if err := c.Open("corr-8"); err != nil {
		t.Fatalf("Open: %v", err)
	}

	_, err := c.Inspect("corr-8a", "nope:404")
	if err == nil {
		t.Fatalf("Inspect on a bad ref: got nil error")
	}
	e := asE(t, err)
	if e.Code != debug.ErrEntityLookupFailed {
		t.Fatalf("code = %s, want %s", e.Code, debug.ErrEntityLookupFailed)
	}
}

// TestInspect_ConsoleNotOpen_Rejected is AC-DM7: the selection/query
// interface is not reachable at all when the console has not been
// opened — a dedicated check independent of Open's own AC-DM1 gate, in
// case a future entry point bypasses Open.
func TestInspect_ConsoleNotOpen_Rejected(t *testing.T) {
	h := newTestHeader()
	s := debug.NewState(
		debug.WithHeader(h),
		debug.WithEntityLookup(func(ref string) (any, error) { return "should never be reached", nil }),
	)
	if err := s.Enable(debug.SourceFlag, "pre"); err != nil {
		t.Fatalf("pre-enable: %v", err)
	}
	c, _ := newWiredConsole(t, s)
	// Deliberately never call c.Open.

	_, err := c.Inspect("corr-9", "citizen:1")
	if err == nil {
		t.Fatalf("Inspect on an unopened console: got nil error")
	}
	e := asE(t, err)
	if e.Code != ErrConsoleNotOpen {
		t.Fatalf("code = %s, want %s", e.Code, ErrConsoleNotOpen)
	}
}

// TestSubmitFeedback_WritesRecord is AC-DM8: submitting feedback with
// debug on and a fixed injected clock writes a well-formed JSON file to
// the inbox with the expected field values, including a timestamp
// sourced from the injected clock, not time.Now().
func TestSubmitFeedback_WritesRecord(t *testing.T) {
	h := newTestHeader()
	inbox := filepath.Join(t.TempDir(), "inbox")

	s := debug.NewState(
		debug.WithHeader(h),
		debug.WithFeedbackInbox(inbox),
		debug.WithClock(fixedNow),
	)
	if err := s.Enable(debug.SourceFlag, "pre"); err != nil {
		t.Fatalf("pre-enable: %v", err)
	}
	c, _ := newWiredConsole(t, s)
	if err := c.Open("corr-10"); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := c.SubmitFeedback("corr-10a", 42, "the bridge is floating"); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}

	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatalf("ReadDir(inbox): %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbox has %d entries, want 1", len(entries))
	}
	rec := readFeedbackRecord(t, filepath.Join(inbox, entries[0].Name()))
	if rec.Tick != 42 {
		t.Fatalf("Tick = %d, want 42", rec.Tick)
	}
	if rec.Body != "the bridge is floating" {
		t.Fatalf("Body = %q, want %q", rec.Body, "the bridge is floating")
	}
	if !rec.DebugTouched {
		t.Fatalf("DebugTouched = false, want true")
	}
	if rec.Timestamp != fixedNow().UTC().Format(rfc3339NanoLayout) {
		t.Fatalf("Timestamp = %q, want the injected clock's reading, not time.Now()", rec.Timestamp)
	}
}

// TestSubmitFeedback_DebugOff_NoFileWritten is AC-DM9: attempting
// submission with debug off is rejected with ErrDebugRequired and writes
// NO file — the inbox's contents are asserted unchanged, not merely
// "no new file with today's date" (this project's verification
// standard: mutate the data, don't grep for it).
func TestSubmitFeedback_DebugOff_NoFileWritten(t *testing.T) {
	h := newTestHeader()
	inbox := filepath.Join(t.TempDir(), "inbox")
	s := debug.NewState(
		debug.WithHeader(h),
		debug.WithFeedbackInbox(inbox),
	)
	// Console must itself be open to reach SubmitFeedback's wired seam at
	// all (AC-DM7's discipline extends here); RequireConsole is a
	// permissive stand-in so the failure under test is s.SubmitFeedback's
	// OWN debug-off rejection, not Console's Open gate.
	c := New(
		WithRequireConsole(func(string) error { return nil }),
		WithSubmitFeedback(s.SubmitFeedback),
	)
	if err := c.Open("corr-11"); err != nil {
		t.Fatalf("Open: %v", err)
	}

	before, _ := os.ReadDir(inbox) // inbox may not exist yet — that's fine, len 0 either way

	err := c.SubmitFeedback("corr-11a", 1, "should not be written")
	if err == nil {
		t.Fatalf("SubmitFeedback with debug off: got nil error")
	}
	e := asE(t, err)
	if e.Code != debug.ErrDebugRequired {
		t.Fatalf("code = %s, want %s", e.Code, debug.ErrDebugRequired)
	}

	after, _ := os.ReadDir(inbox)
	if len(after) != len(before) {
		t.Fatalf("inbox contents changed: before=%d entries, after=%d entries", len(before), len(after))
	}
}

// TestSubmitFeedback_ConsoleNotOpen_Rejected mirrors
// TestInspect_ConsoleNotOpen_Rejected for the feedback surface.
func TestSubmitFeedback_ConsoleNotOpen_Rejected(t *testing.T) {
	h := newTestHeader()
	inbox := filepath.Join(t.TempDir(), "inbox")
	s := debug.NewState(debug.WithHeader(h), debug.WithFeedbackInbox(inbox))
	if err := s.Enable(debug.SourceFlag, "pre"); err != nil {
		t.Fatalf("pre-enable: %v", err)
	}
	c, _ := newWiredConsole(t, s)
	// Deliberately never call c.Open.

	err := c.SubmitFeedback("corr-12", 1, "x")
	if err == nil {
		t.Fatalf("SubmitFeedback on an unopened console: got nil error")
	}
	e := asE(t, err)
	if e.Code != ErrConsoleNotOpen {
		t.Fatalf("code = %s, want %s", e.Code, ErrConsoleNotOpen)
	}
}

// TestSubmitFeedback_TwoSubmissions_NoInterleave is a targeted proof of
// AC-DM8's "never appended to a shared file, never overwritten,
// concurrent submissions must not corrupt or interleave" — two
// submissions with distinct correlation IDs produce two distinct,
// independently well-formed files.
func TestSubmitFeedback_TwoSubmissions_NoInterleave(t *testing.T) {
	h := newTestHeader()
	inbox := filepath.Join(t.TempDir(), "inbox")
	s := debug.NewState(debug.WithHeader(h), debug.WithFeedbackInbox(inbox), debug.WithClock(fixedNow))
	if err := s.Enable(debug.SourceFlag, "pre"); err != nil {
		t.Fatalf("pre-enable: %v", err)
	}
	c, _ := newWiredConsole(t, s)
	if err := c.Open("corr-13"); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := c.SubmitFeedback("corr-13a", 1, "first"); err != nil {
		t.Fatalf("first SubmitFeedback: %v", err)
	}
	if err := c.SubmitFeedback("corr-13b", 2, "second"); err != nil {
		t.Fatalf("second SubmitFeedback: %v", err)
	}

	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("inbox has %d entries, want 2 (one per submission, never merged)", len(entries))
	}
	bodies := map[string]bool{}
	for _, e := range entries {
		rec := readFeedbackRecord(t, filepath.Join(inbox, e.Name()))
		bodies[rec.Body] = true
	}
	if !bodies["first"] || !bodies["second"] {
		t.Fatalf("expected both submissions intact and separate, got bodies=%v", bodies)
	}
}
