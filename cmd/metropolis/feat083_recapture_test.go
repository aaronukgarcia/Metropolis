package main

// FEAT-083 Baseline One legs 2-4 recapture on main: a scripted player session
// on the REAL boot path (bootCore, routeKeyInput, composeDraw frames, ticks
// via KindAdvanceTicks on the transport). The tester does not fix; the test
// asserts only that frames were produced and the key figures differ as the
// evidence README describes. Frames are written to METRO_EVIDENCE_DIR if set.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

type rcRun struct {
	w   *skeletonWiring
	out strings.Builder
}

func rcNew(t *testing.T) *rcRun {
	t.Helper()
	prev := bootSecondsPerMonthAt1x
	bootSecondsPerMonthAt1x = func(string) (int64, error) { return 1000000, nil }
	t.Cleanup(func() { bootSecondsPerMonthAt1x = prev })
	w, err := bootCore("feat083-recapture", registry.NewRegistry())
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	t.Cleanup(w.shutdown)
	pressKey(w, ' ')
	waitForPaused(t, w, true, 5*time.Second)
	return &rcRun{w: w}
}

func rcKey(w *skeletonWiring, k tcell.Key, r rune) {
	ev := tcell.NewEventKey(k, r, tcell.ModNone)
	routeKeyInput(w, core.InputMsg{Kind: core.KeyInput, Key: k, Rune: r, Raw: ev})
	time.Sleep(60 * time.Millisecond)
}

func (r *rcRun) screen(fkey tcell.Key) string {
	rcKey(r.w, fkey, 0)
	time.Sleep(60 * time.Millisecond)
	return plFrame(r.w)
}

func (r *rcRun) snap(label string, fkey tcell.Key) string {
	f := r.screen(fkey)
	fmt.Fprintf(&r.out, "===== %s | engine ticks completed %d =====\n%s\n", label, r.w.engine.TicksCompleted(), f)
	return f
}

var (
	rcPopRe      = regexp.MustCompile(`(?i)pop[a-z]*[: ]+\s*([0-9][0-9,]*)`)
	rcTreasuryRe = regexp.MustCompile(`Treasury\s+£\s*(-?[0-9,.]+)`)
	rcDwellRe    = regexp.MustCompile(`(?i)dwelling`)
)

func rcSave(t *testing.T, name, body string) {
	dir := os.Getenv("METRO_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func rcPop(t *testing.T, frame string) int {
	t.Helper()
	m := rcPopRe.FindStringSubmatch(frame)
	if m == nil {
		t.Fatalf("no population figure on screen:\n%s", frame)
	}
	n, _ := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
	return n
}

// Legs 2 and 3: control vs build on the same seed; F3 queue to complete, F2
// treasury and P&L.
func TestFEAT083_Recapture_Legs2and3(t *testing.T) {
	ctl := rcNew(t)
	bld := rcNew(t)
	var l2 strings.Builder

	rcKey(bld.w, tcell.KeyF1, 0)
	rcKey(bld.w, tcell.KeyRune, 'y')
	bld.snap("BUILD F3 after F1 y (buy), before b", tcell.KeyF3)
	rcKey(bld.w, tcell.KeyRune, 'b')
	f3 := bld.snap("BUILD F3 after b (queued)", tcell.KeyF3)
	if !rcDwellRe.MatchString(f3) {
		t.Fatalf("F3 shows no dwelling queue row after b:\n%s", f3)
	}
	var ctlTre, bldTre []string
	var f3Last, f2Last string
	for i := 0; i < 3; i++ {
		plAdvance(t, ctl.w, 30)
		plAdvance(t, bld.w, 30)
		cf := ctl.snap(fmt.Sprintf("CONTROL F2 month %d", i+1), tcell.KeyF2)
		f3Last = bld.snap(fmt.Sprintf("BUILD F3 month %d", i+1), tcell.KeyF3)
		f2Last = bld.snap(fmt.Sprintf("BUILD F2 month %d", i+1), tcell.KeyF2)
		if m := rcTreasuryRe.FindStringSubmatch(cf); m != nil {
			ctlTre = append(ctlTre, m[1])
		}
		if m := rcTreasuryRe.FindStringSubmatch(f2Last); m != nil {
			bldTre = append(bldTre, m[1])
		}
	}
	if !strings.Contains(f3Last, "complete") {
		t.Fatalf("F3 queue never reached complete by tick 90:\n%s", f3Last)
	}
	if len(ctlTre) != 3 || len(bldTre) != 3 || ctlTre[2] == bldTre[2] {
		t.Fatalf("treasury control %v vs build %v: expected to differ at tick 90", ctlTre, bldTre)
	}
	if !strings.Contains(strings.ToLower(f2Last), "construction") {
		t.Logf("NOTE: no construction row in build P&L at tick 90 (GAP to report)")
	}

	// Leg 3: P&L at consecutive closes on a run with no player action.
	var cons []string
	l3run := rcNew(t)
	for i := 0; i < 3; i++ {
		plAdvance(t, l3run.w, 30)
		f := l3run.snap(fmt.Sprintf("LEG3 F2 close %d", i+1), tcell.KeyF2)
		if m := plConsumptionRe.FindStringSubmatch(f); m != nil {
			cons = append(cons, m[1])
		}
		if plNetRe.FindStringSubmatch(f) == nil {
			t.Fatalf("no Net treasury change line at close %d", i+1)
		}
	}
	if len(cons) != 3 || cons[1] == cons[2] {
		t.Fatalf("household consumption did not change between closes: %v", cons)
	}
	fmt.Fprintf(&l2, "LEG 2: control treasury at ticks 30/60/90 %v, build %v\n", ctlTre, bldTre)
	l2.WriteString(bld.out.String())
	l2.WriteString("\n---- CONTROL ----\n" + ctl.out.String())
	rcSave(t, "leg2.txt", l2.String())
	rcSave(t, "leg3.txt", "LEG 3: F2 at three consecutive month closes, no player action\n"+l3run.out.String())
}

// Leg 4: control vs build several dwellings early, 12 months; what the player
// sees, on which screens.
func TestFEAT083_Recapture_Leg4(t *testing.T) {
	ctl := rcNew(t)
	trt := rcNew(t)
	for i := 0; i < 6; i++ {
		rcKey(trt.w, tcell.KeyF1, 0)
		rcKey(trt.w, tcell.KeyRune, 'y')
		rcKey(trt.w, tcell.KeyF3, 0)
		rcKey(trt.w, tcell.KeyRune, 'b')
		rcKey(trt.w, tcell.KeyF1, 0)
		rcKey(trt.w, tcell.KeyRight, 0)
	}
	trt.snap("TREATMENT F3 after six build attempts", tcell.KeyF3)
	var cp, tp []int
	var body strings.Builder
	for m := 1; m <= 12; m++ {
		plAdvance(t, ctl.w, 30)
		plAdvance(t, trt.w, 30)
		cf := ctl.snap(fmt.Sprintf("CONTROL F1 month %d", m), tcell.KeyF1)
		tf := trt.snap(fmt.Sprintf("TREATMENT F1 month %d", m), tcell.KeyF1)
		cp = append(cp, rcPop(t, cf))
		tp = append(tp, rcPop(t, tf))
		fmt.Fprintf(&body, "month %2d tick %3d  population control %d  treatment %d\n", m, m*30, cp[m-1], tp[m-1])
	}
	for _, k := range []tcell.Key{tcell.KeyF2, tcell.KeyF3, tcell.KeyF4} {
		ctl.snap(fmt.Sprintf("CONTROL screen %v month 12", k), k)
		trt.snap(fmt.Sprintf("TREATMENT screen %v month 12", k), k)
	}
	head := "LEG 4 population by month (chrome figure)\n" + body.String() + "\n"
	rcSave(t, "leg4.txt", head+"---- TREATMENT ----\n"+trt.out.String()+"\n---- CONTROL ----\n"+ctl.out.String())
	t.Log(head)
	if cp[11] == tp[11] {
		t.Fatalf("population at month 12 identical (%d) control vs treatment: %v vs %v", cp[11], cp, tp)
	}
}
