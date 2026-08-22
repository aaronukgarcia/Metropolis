package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// --- MOD-015: -headless dispatches into harness.headless for real ---
// (harness.headless.md AC-1/AC-2). Full behavioural coverage of the
// headless run itself lives in internal/harness/headless's own tests
// (GR#20-respecting: this package only owns flag parsing/dispatch); these
// tests cover run()'s dispatch and required-flag validation.

func TestRun_Headless_MissingRequiredFlags_ReturnsExitCode2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-headless"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run(-headless) with no -seed/-months/-out = %d, want 2", code)
	}
	for _, want := range []string{"-seed", "-months", "-out"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want it to name missing flag %q", stderr.String(), want)
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty on a usage error", stdout.String())
	}
}

func TestRun_Headless_RunsToCompletion(t *testing.T) {
	dir := t.TempDir() + "/snap"
	var stdout, stderr bytes.Buffer
	code := run([]string{"-headless", "-seed", "1", "-months", "1", "-out", dir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(-headless ...) = %d, want 0 (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), dir) {
		t.Errorf("stdout = %q, want it to mention the -out path %q", stdout.String(), dir)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty on a successful headless run", stderr.String())
	}
}

func TestRun_Headless_ZeroMonths_ReturnsExitCode2(t *testing.T) {
	dir := t.TempDir() + "/snap"
	var stdout, stderr bytes.Buffer
	code := run([]string{"-headless", "-seed", "1", "-months", "0", "-out", dir}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run(-headless -months 0) = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "-months") {
		t.Errorf("stderr = %q, want it to mention -months", stderr.String())
	}
}

func TestRun_Version_PrintsBuildIdentity(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(-version) = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "metropolis") {
		t.Errorf("stdout = %q, want it to contain the build identity line", stdout.String())
	}
}

// --- AC-7, exercised through run() itself (not just bootCore) ---

func TestRun_BootFailure_ReturnsExitCode1(t *testing.T) {
	orig := newBootRegistry
	defer func() { newBootRegistry = orig }()
	newBootRegistry = func() *registry.Registry {
		r := registry.NewRegistry()
		// Pre-seed a collision with one of registerSkeletonModules' keys
		// so bootCore fails deterministically, without needing a broken
		// terminal to exercise "a wired component fails to initialize."
		_ = r.Register("harness.stub", nil, wiredModule{name: "harness.stub"})
		return r
	}

	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run() with a colliding registry = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), codeBootFailure) {
		t.Errorf("stderr = %q, want it to mention %s (GR#1: registry code visible)", stderr.String(), codeBootFailure)
	}
}

func TestRun_UnknownFlag_ReturnsExitCode2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-not-a-real-flag"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run(-not-a-real-flag) = %d, want 2", code)
	}
}

// --- isQuitInput: process-lifecycle quit detection, NOT a Command
// translation (see run.go's doc comment) ---

func TestIsQuitInput(t *testing.T) {
	cases := []struct {
		name string
		msg  core.InputMsg
		want bool
	}{
		{"ctrl-c", core.InputMsg{Kind: core.KeyInput, Key: tcell.KeyCtrlC}, true},
		{"escape", core.InputMsg{Kind: core.KeyInput, Key: tcell.KeyEscape}, true},
		{"q rune", core.InputMsg{Kind: core.KeyInput, Key: tcell.KeyRune, Rune: 'q'}, true},
		{"other rune", core.InputMsg{Kind: core.KeyInput, Key: tcell.KeyRune, Rune: 'a'}, false},
		{"resize is never a quit", core.InputMsg{Kind: core.ResizeInput, Width: 120, Height: 30}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isQuitInput(c.msg); got != c.want {
				t.Errorf("isQuitInput(%+v) = %v, want %v", c.msg, got, c.want)
			}
		})
	}
}

// --- Interactive path with a SimulationScreen (headless-capable tcell
// backend, the same pattern internal/ui/core's own render_test.go/
// screen_test.go use) — proves runInteractive actually renders through
// the real RenderLoop/InputLoop, not just bootCore's headless slice.

// TestRunInteractive_RendersAndQuitsOnEscape covers Esc's
// process-lifecycle meaning at IDLE, which is the only state it had
// before FEAT-211 increment 1 made leader sequences reachable from a real
// keyboard. That increment's destructive round (finding 2) showed Esc was
// also keys.KeyGrammar's reserved abort token and could never reach it,
// so OnDelivered now routes Esc to the grammars instead of quitting
// whenever a sequence is pending.
//
// This test is deliberately kept exactly as it was, not weakened: with
// nothing pending — which is the state a freshly booted binary is in, and
// the state this test exercises — Esc must still quit, and that is
// precisely the half of the behaviour the fix must not break. The other
// half (Esc aborts rather than quits while a sequence IS pending, and the
// NEXT Esc then quits) is asserted by
// TestRouting_EscAbortsPendingSequenceThenQuits in
// feat211_inc1_destructive_test.go. Read the two together as one contract.
func TestRunInteractive_RendersAndQuitsOnEscape(t *testing.T) {
	reg := registry.NewRegistry()
	w, err := bootCore("interactive-test", reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	defer w.shutdown()

	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatalf("sim.Init: %v", err)
	}
	sim.SetSize(120, 30)

	done := make(chan struct{})
	go func() {
		runInteractive(w, sim)
		close(done)
	}()

	// Give RenderLoop at least one tick, then ask the SimulationScreen to
	// deliver a quit key the same way a real terminal would.
	sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runInteractive did not return after an injected Escape key")
	}
}
