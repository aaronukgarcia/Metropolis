package main

// BUG-1213 (FEAT-083 leg 3): frame-capture proof on the REAL boot path that
// F2's PROFIT & LOSS shows a household consumption line that changes between
// month closes. Keys go through routeKeyInput, frames are the composed frame.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

const plW, plH = 140, 40

func plFrame(w *skeletonWiring) string {
	buf := core.NewBuffer(plW, plH)
	composeDraw(w)(buf, nil)
	var sb strings.Builder
	for y := 0; y < plH; y++ {
		sb.WriteString(strings.TrimRight(bufferRowString(buf, plW, y), " "))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func plAdvance(t *testing.T, w *skeletonWiring, n int64) {
	t.Helper()
	want := w.engine.TicksCompleted() + uint64(n)
	if err := w.transport.SendCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: n},
	}); err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	waitForTicks(t, w, want, 60*time.Second)
	needle := fmt.Sprintf("TICK %d ", want)
	for i := 0; i < 100 && !strings.Contains(plFrame(w), needle); i++ {
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
}

var (
	// Whole-pound figures with thousands commas and the minus before the pound
	// sign: "-£530,161" (FEAT-9995385625).
	plConsumptionRe = regexp.MustCompile(`Household consumption\s+(-?£[0-9,]+)(?:\s+(-?£[0-9,]+))?`)
	plNetRe         = regexp.MustCompile(`Net treasury change\s+(-?£[0-9,]+)(?:\s+(-?£[0-9,]+))?`)
)

func plF2(w *skeletonWiring) string {
	ev := tcell.NewEventKey(tcell.KeyF2, 0, tcell.ModNone)
	routeKeyInput(w, core.InputMsg{Kind: core.KeyInput, Key: tcell.KeyF2, Rune: 0, Raw: ev})
	time.Sleep(100 * time.Millisecond)
	return plFrame(w)
}

// The F2 frame shows the treasury statement with a Net line and a separate
// City economy block carrying Household consumption; both lines change between
// month closes, and the third close's previous-month columns are the second's.
func TestBUG1213_F2ProfitAndLoss_NetAndConsumptionChangeBetweenMonthCloses(t *testing.T) {
	prev := bootSecondsPerMonthAt1x
	bootSecondsPerMonthAt1x = func(string) (int64, error) { return 1000000, nil }
	t.Cleanup(func() { bootSecondsPerMonthAt1x = prev })
	w, err := bootCore("bug1213-pl", registry.NewRegistry())
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	t.Cleanup(w.shutdown)
	pressKey(w, ' ')
	waitForPaused(t, w, true, 5*time.Second)

	var out strings.Builder
	var cons, net, consPrev, netPrev [3]string
	for i := 0; i < 3; i++ {
		plAdvance(t, w, 30)
		frame := plF2(w)
		fmt.Fprintf(&out, "===== F2 after finance close %d | engine ticks completed %d =====\n%s\n", i+1, w.engine.TicksCompleted(), frame)
		if !strings.Contains(frame, "City economy (not treasury money):") {
			t.Fatalf("close %d: no City economy block on F2:\n%s", i+1, frame)
		}
		cm := plConsumptionRe.FindStringSubmatch(frame)
		nm := plNetRe.FindStringSubmatch(frame)
		if cm == nil || nm == nil {
			t.Fatalf("close %d: Household consumption line %v or Net treasury change line %v missing on F2:\n%s", i+1, cm, nm, frame)
		}
		// The consumption line is in the City economy block, below the Net line.
		if strings.Index(frame, "Household consumption") < strings.Index(frame, "Net treasury change") {
			t.Fatalf("close %d: consumption is not in the City economy block under the Net line:\n%s", i+1, frame)
		}
		cons[i], consPrev[i], net[i], netPrev[i] = cm[1], cm[2], nm[1], nm[2]
	}
	if cons[2] == cons[1] || cons[1] == "" {
		t.Fatalf("consumption did not change between closes 2 and 3: %v", cons)
	}
	if net[0] == net[1] || net[1] == net[2] {
		t.Fatalf("Net did not change between closes: %v", net)
	}
	if consPrev[2] != cons[1] || netPrev[2] != net[1] {
		t.Fatalf("close 3 previous-month columns = consumption %q net %q, want close 2's %q and %q", consPrev[2], netPrev[2], cons[1], net[1])
	}
	if dir := os.Getenv("METRO_EVIDENCE_DIR"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, "leg3-f2-pnl.txt"), []byte(out.String()), 0o644); err != nil {
			t.Fatalf("write evidence: %v", err)
		}
	}
	t.Log(out.String())
}
