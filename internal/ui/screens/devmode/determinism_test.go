package devmode

// AC-DM16 (GR#21 determinism): opening the console, pausing, inspecting
// an entity, and submitting feedback never retroactively change
// already-committed simulation history, and none of this package's
// production code calls the wall clock directly.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/debug"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// TestNoWallClockUsage mechanically encodes AC-DM16's own grep check
// ("grep -rn time.Now internal/ui/screens/devmode/*.go, excluding
// _test.go, returns no matches") as a real test rather than a
// comment-only claim, so a future edit that reintroduces a wall-clock
// read here fails CI instead of only failing a manual review step.
func TestNoWallClockUsage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	needle := []byte("time.Now(")
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" {
			continue
		}
		if len(name) >= len("_test.go") && name[len(name)-len("_test.go"):] == "_test.go" {
			continue // _test.go files are exempt (fixtures may use it)
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		if bytes.Contains(b, needle) {
			t.Errorf("%s calls time.Now() directly — this package must route every timestamp through an injected Clock seam (AC-DM16/GR#21)", name)
		}
	}
}

// TestOpenPauseInspectFeedback_DoesNotPerturbEngine is AC-DM13's sibling
// on the determinism side: opening the console (which pauses a REAL
// engine.core Engine via the real protocol.KindPause command),
// inspecting an entity, and submitting feedback must not change the
// engine's already-committed tick history. Two snapshots taken before
// and after the whole console session must be byte-identical.
func TestOpenPauseInspectFeedback_DoesNotPerturbEngine(t *testing.T) {
	e := core.NewEngine(core.WithWorldSeed(20260812), core.WithPoolSize(2))
	if err := e.AdvanceTicks("corr-adv", 12); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	var before bytes.Buffer
	if _, err := e.Snapshot(&before, "corr-snap-before"); err != nil {
		t.Fatalf("Snapshot (before): %v", err)
	}

	h := serialize.NewHeader(1, 0, 0, "test")
	inbox := filepath.Join(t.TempDir(), "inbox")
	s := debug.NewState(
		debug.WithHeader(&h),
		debug.WithFeedbackInbox(inbox),
		debug.WithEntityLookup(func(ref string) (any, error) { return map[string]string{"ref": ref}, nil }),
	)

	// Debug must already be on for the REAL s.RequireConsole gate to pass
	// (AC-DM1's rejection-when-off requirement forecloses the "console
	// open is itself the enable trigger" branch AC-DM3 only describes as
	// something an implementation MAY choose to support — see
	// console_test.go's TestOpen_TouchesDebugBeforeOpen for that ordering
	// claim proven in isolation against a stand-in gate). This test's own
	// concern is determinism once the console IS legitimately reachable,
	// so pre-enable exactly as a real :debug on / --debug session would.
	if err := s.Enable(debug.SourceFlag, "corr-pre-enable"); err != nil {
		t.Fatalf("pre-enable: %v", err)
	}

	console := New(
		WithRequireConsole(s.RequireConsole),
		WithEnable(func(cid string) error { return s.Enable(debug.SourcePalette, cid) }),
		WithPause(func(cid string) error {
			cmd := protocol.Command{
				ProtocolVersion: protocol.ProtocolVersion,
				CorrelationID:   protocol.CorrelationID(cid),
				Kind:            protocol.KindPause,
				Payload:         protocol.PausePayload{},
			}
			res := e.HandleCommand(cmd)
			if !res.Accepted {
				t.Fatalf("engine rejected Pause: %+v", res.Error)
			}
			return nil
		}),
		WithInspect(s.InspectEntity),
		WithSubmitFeedback(s.SubmitFeedback),
	)

	if err := console.Open("corr-open"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := console.Inspect("corr-inspect", "citizen:1"); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if err := console.SubmitFeedback("corr-feedback", 12, "determinism smoke"); err != nil {
		t.Fatalf("SubmitFeedback: %v", err)
	}

	clock, err := e.Clock()
	if err != nil {
		t.Fatalf("Clock(): %v", err)
	}
	if !clock.Paused() {
		t.Fatalf("engine clock not paused after console Open")
	}
	if clock.Tick() != 12 {
		t.Fatalf("tick = %d after console session, want unchanged 12", clock.Tick())
	}

	var after bytes.Buffer
	if _, err := e.Snapshot(&after, "corr-snap-after"); err != nil {
		t.Fatalf("Snapshot (after): %v", err)
	}

	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatalf("engine snapshot changed after a console open/inspect/feedback session — devmode must never retroactively perturb committed simulation history (AC-DM16)")
	}
}
