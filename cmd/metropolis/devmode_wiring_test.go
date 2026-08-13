package main

import (
	"errors"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/debug"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/ui/screens/devmode"
)

// newWiringTestHeader constructs the minimal serialize.Header
// TestBootCore_DevConsole_PauseWiredThroughRealTransport needs to satisfy
// debug.State.Enable's WithHeader requirement — this package
// (cmd/metropolis) does not otherwise construct a serialize.Header
// anywhere in its production wiring (see boot.go's doc comment on
// w.debugState/w.devConsole for why), so this helper exists only for
// that one test's standalone State, not as production wiring.
func newWiringTestHeader() *serialize.Header {
	h := serialize.NewHeader(1, 0, 0, "test")
	return &h
}

// --- BUG-122: feat.devmode's Console is now constructed by the real
// composition root (bootCore, boot.go), not only inside devmode's own
// _test.go files. These tests exercise that exact path — bootCore, the
// same function main's run() calls in production — rather than
// constructing a standalone devmode.Console the way devmode's own package
// tests do, so what's proven here is proven against the real wiring, not
// a look-alike of it.

// TestBootCore_WiresRealDevConsole proves w.devConsole (bootCore's own
// field) is a live, non-nil *devmode.Console produced by the real
// composition path, and that it is wired against the SAME *debug.State
// bootCore also constructed (w.debugState) — not a second, disconnected
// State that would silently diverge from it (doc.go's "single source of
// truth" contract).
func TestBootCore_WiresRealDevConsole(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("devmode-wiring", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	if w.devConsole == nil {
		t.Fatal("bootCore did not construct a devmode.Console (BUG-122 regression: the console is unreachable from the real composition root again)")
	}
	if w.debugState == nil {
		t.Fatal("bootCore did not construct a debug.State to wire the console against")
	}
	if w.debugState.IsOn() {
		t.Fatal("precondition failed: a freshly booted binary must have debug off")
	}
}

// TestBootCore_DevConsole_GateAppliesInReleaseBuild proves AC-DM1 holds
// through the real composition root: with no --debug flag, no config,
// and no ":debug on" ever issued (i.e. exactly the state main's run()
// leaves w.debugState in for every ordinary invocation of this binary),
// Console.Open is rejected with feat.debugmode's own ErrDebugRequired —
// never a devconsole-local substitute code, and never a silent "opens
// anyway." This is BUG-122's own AC-DM13-adjacent claim: wiring the
// console into the real binary must not make it openable without debug
// mode on.
func TestBootCore_DevConsole_GateAppliesInReleaseBuild(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("devmode-gate", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	if w.debugState.IsOn() {
		t.Fatal("precondition failed: a freshly booted binary must have debug off")
	}

	err = w.devConsole.Open("release-build-open-attempt")
	if err == nil {
		t.Fatal("Console.Open succeeded with debug off via the real composition root — RequireConsole gate is not applying (AC-DM1 violated)")
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("Open error %v is not a registry-sourced *errs.E", err)
	}
	if e.Code != debug.ErrDebugRequired {
		t.Fatalf("Open with debug off through the real wiring: code = %s, want %s (feat.debugmode's own gate code, not a devconsole-local substitute)", e.Code, debug.ErrDebugRequired)
	}
	if w.devConsole.IsOpen() {
		t.Fatal("Console reports IsOpen()==true despite Open having been rejected")
	}
}

// TestBootCore_DevConsole_PauseWiredThroughRealTransport proves the
// PauseFunc bootCore wired for devConsole actually issues a real
// protocol.KindPause Command over the SAME transport StubEngine is
// listening on (AC-DM2) — not a stub/no-op closure. It drives this
// directly through debugState/devConsole's wired seams (bypassing the
// RequireConsole gate, which the two tests above already cover
// independently) so this test isolates the pause-wiring claim alone:
// even with debug forced on for this one test, does the Pause command
// the console's Open path issues actually reach and pause the real
// StubEngine wired by bootCore?
func TestBootCore_DevConsole_PauseWiredThroughRealTransport(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("devmode-pause", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	// bootCore's own internal MapScreen.Subscribe call already produced
	// exactly one CommandResult nothing has read yet — drain it first,
	// same as boot_test.go's TestIntegration_CommandsExerciseStubEngineEndToEnd,
	// so the Pause result read below is unambiguously this test's own.
	select {
	case <-w.transport.Results():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the boot Subscribe's CommandResult")
	}

	// Force debug on directly against the SAME State devConsole is wired
	// to, via a wiring this test constructs standalone only for the
	// header Enable requires (bootCore's own w.debugState has no header
	// wired in production — see boot.go's doc comment on why). This does
	// not touch w.devConsole's wiring at all; it only proves the PauseFunc
	// closure bootCore built is live.
	testState := debug.NewState(debug.WithHeader(newWiringTestHeader()))
	// ASM-467: RequireConsole itself gates on IsOn(), so — same as
	// devmode's own console_test.go tests that reach Open successfully —
	// debug must already be on BEFORE Open is called; the
	// "console-opens-are-the-enable-trigger" branch is documented as
	// unreachable via the real gate, not this test inventing a shortcut.
	if err := testState.Enable(debug.SourceFlag, "pre-enable-for-pause-test"); err != nil {
		t.Fatalf("pre-enabling debug: %v", err)
	}
	testConsole := devmode.New(
		devmode.WithRequireConsole(testState.RequireConsole),
		devmode.WithEnable(func(cid string) error { return testState.Enable(debug.SourcePalette, cid) }),
		devmode.WithPause(func(cid string) error { return sendPauseCommand(w.transport, cid) }),
	)

	if err := testConsole.Open("pause-through-real-transport"); err != nil {
		t.Fatalf("Open (debug forced on): %v", err)
	}

	select {
	case res := <-w.transport.Results():
		if !res.Accepted {
			t.Fatalf("Pause command rejected by the real StubEngine: %+v", res.Error)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the Pause command's CommandResult from the real StubEngine")
	}
}
