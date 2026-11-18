package main

// BUG-1222 (FEAT-083 leg 3): on the REAL boot path, at the 80x24 size most
// terminals open at, the F2 frame must carry the P&L's Net treasury change line
// and the City economy Household consumption line for the closed month. The
// P&L is one quadrant of a 2x2 grid, so it is short; priority layout keeps
// those lines. Method copied from bug1213_pl_frame_test.go.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

const smallW, smallH = 80, 24

func smallFrame(w *skeletonWiring) string {
	buf := core.NewBuffer(smallW, smallH)
	composeDraw(w)(buf, nil)
	var sb strings.Builder
	for y := 0; y < smallH; y++ {
		sb.WriteString(strings.TrimRight(bufferRowString(buf, smallW, y), " "))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func TestBUG1222_F2At80x24ShowsNetAndHouseholdConsumption(t *testing.T) {
	prev := bootSecondsPerMonthAt1x
	bootSecondsPerMonthAt1x = func(string) (int64, error) { return 1000000, nil }
	t.Cleanup(func() { bootSecondsPerMonthAt1x = prev })
	w, err := bootCore("bug1222-f2", registry.NewRegistry())
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	t.Cleanup(w.shutdown)
	pressKey(w, ' ')
	waitForPaused(t, w, true, 5*time.Second)

	var out strings.Builder
	for i := 1; i <= 2; i++ {
		plAdvance(t, w, 30)
		// plF2 switches to F2; the frame it returns is the 140x40 one, so
		// take the 80x24 frame afterwards.
		plF2(w)
		frame := smallFrame(w)
		out.WriteString("===== F2 at 80x24 after finance close " + string(rune('0'+i)) + " =====\n" + frame + "\n")
		for _, want := range []string{"PROFIT & LOSS", "Net treasury change", "Household consumption", "City economy (not treasury money):"} {
			if !strings.Contains(frame, want) {
				t.Fatalf("close %d: %q missing from the 80x24 F2 frame:\n%s", i, want, frame)
			}
		}
		if cm := plConsumptionRe.FindStringSubmatch(frame); cm == nil {
			t.Fatalf("close %d: Household consumption has no amount at 80x24:\n%s", i, frame)
		}
		if nm := plNetRe.FindStringSubmatch(frame); nm == nil {
			t.Fatalf("close %d: Net treasury change has no amount at 80x24:\n%s", i, frame)
		}
		// The Balance Sheet's treasury, debt and net worth stay on screen.
		for _, want := range []string{"BALANCE SHEET", "Treasury", "Reserves", "Outstanding Debt", "Net Worth:"} {
			if !strings.Contains(frame, want) {
				t.Fatalf("close %d: balance sheet lost %q at 80x24:\n%s", i, want, frame)
			}
		}
	}
	if dir := os.Getenv("METRO_EVIDENCE_DIR"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, "bug1222-f2-80x24.txt"), []byte(out.String()), 0o644); err != nil {
			t.Fatalf("write evidence: %v", err)
		}
	}
	t.Log(out.String())
}
