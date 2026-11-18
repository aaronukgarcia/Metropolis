package main

// BUG-1224 / FEAT-9995385612 lane 4 (C19): on the REAL boot path the game
// runs the current rules, so a second build order on a cell that already
// holds a queued order is refused, the refusal is visible on F1 (the map
// notice) and F3 (the queue still lists ONE order). Booting pinned to version
// 0 would accept the duplicate and make this test red.
//
// The brief's literal "assert RuleVersion() == rules.Current" is not
// expressible here: boot.go discards the Composition that compose.Wire
// returns, and boot.go is outside this lane. The behaviour below is what that
// assertion protects.

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func bufDump(buf *core.Buffer) string {
	var sb strings.Builder
	for y := 0; y < twH; y++ {
		sb.WriteString(strings.TrimRight(bufferRowString(buf, twW, y), " "))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func bug1224Rows(t *testing.T, w *skeletonWiring, prefix string) int {
	t.Helper()
	buf := drawActive(w)
	n := 0
	for y := 0; y < twH; y++ {
		if strings.Contains(bufferRowString(buf, twW, y), prefix) {
			n++
		}
	}
	return n
}

func TestBUG1224_SecondBuildOnSameCellRefusedShownOnF1AndF3(t *testing.T) {
	w := bootForScreenTest(t)
	// The map cursor cannot move before the first draw: draw one frame first.
	_ = drawActive(w)

	routeKeyInput(w, keyMsg(tcell.KeyRune, 'y'))
	barrier(t, w, "bug1224-barrier-y")
	routeKeyInput(w, keyMsg(tcell.KeyRune, 'b'))
	barrier(t, w, "bug1224-barrier-b1")
	if got := w.mapScreen.BuildNotice(); got != "" {
		t.Fatalf("first build was refused: %q", got)
	}
	routeKeyInput(w, keyMsg(tcell.KeyRune, 'b'))
	barrier(t, w, "bug1224-barrier-b2")

	notice := w.mapScreen.BuildNotice()
	if !strings.Contains(notice, "MET-G510") || !strings.Contains(notice, "already in use") {
		t.Fatalf("map notice = %q, want the MET-G510 already-in-use refusal", notice)
	}
	if !bufferContainsString(drawActive(w), twW, twH, "is already in use") {
		t.Errorf("F1 frame does not show the refusal text")
	}

	// F3: the queue lists exactly ONE order (poll: the view is delta-fed).
	pressF(t, w, tcell.KeyF3, screenIDBuild)
	deadline := time.Now().Add(10 * time.Second)
	rows := 0
	for time.Now().Before(deadline) {
		barrier(t, w, "bug1224-barrier-f3-"+time.Now().Format("150405.000000"))
		if rows = bug1224Rows(t, w, "#"); rows > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if rows != 1 {
		t.Fatalf("F3 queue shows %d order rows, want exactly 1\n%s", rows, bufDump(drawActive(w)))
	}

	// F3's own 'b' at the same cursor cell is refused too and says so.
	routeKeyInput(w, keyMsg(tcell.KeyRune, 'b'))
	barrier(t, w, "bug1224-barrier-f3b")
	if r := w.extra.build.RejectedReason(); !strings.Contains(r, "already in use") {
		t.Fatalf("F3 RejectedReason = %q, want the already-in-use refusal", r)
	}
	if !bufferContainsString(drawActive(w), twW, twH, "REJECTED: ") {
		t.Errorf("F3 frame does not show the REJECTED notice")
	}
	if rows := bug1224Rows(t, w, "#"); rows != 1 {
		t.Fatalf("after the F3 duplicate, queue shows %d rows, want 1", rows)
	}
}
