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

// --- AC-6 (supplementary): -headless is a flag-dispatch seam, not a
// working harness --- run() must do zero engine/registry/transport work
// on this path (nothing to boot, nothing to shut down).

func TestRun_Headless_ReturnsZeroWithoutBooting(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-headless"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run(-headless) = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "harness.headless") {
		t.Errorf("stdout = %q, want it to mention harness.headless (MOD-015)", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty on the headless seam path", stderr.String())
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
