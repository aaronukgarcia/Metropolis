package finance

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/gdamore/tcell/v2"
)

// BUG-769 (P1, built-but-not-wired layer 2): FinanceAPI.InsolvencyMonths()/
// IsInsolvent() have advanced for real in production since BUG-759's
// financeHook.ApplyEffect call site landed, but nothing on the wire ever
// surfaced either figure — this file proves the Screen-level wiring
// against compose's finance_publish.go's InsolvencyMonths/Insolvent wire
// fields, mirroring bug723_payroll_shortfall_test.go's exact shape for the
// equally-optional insolvency section.

// TestInsolvency_Signal proves ApplyDelta decodes a real
// insolvencyMonths/insolvent pair into the Screen's Insolvency() accessor.
func TestInsolvency_Signal(t *testing.T) {
	s := New("corr-insolvency")
	s.BindSubscription("sub-1")

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "insolvencyMonths": 2, "insolvent": false}`),
	})
	got, have := s.Insolvency()
	if !have {
		t.Fatalf("after an insolvency patch: have = false, want true")
	}
	if got.Months != 2 || got.Insolvent {
		t.Fatalf("Insolvency() = %+v, want {Months:2 Insolvent:false}", got)
	}

	// Escalation to game-over, mirroring compose's own third-consecutive-
	// month call shape.
	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "insolvencyMonths": 3, "insolvent": true}`),
	})
	got, have = s.Insolvency()
	if !have || got.Months != 3 || !got.Insolvent {
		t.Fatalf("after the escalation patch: Insolvency() = (%+v, %v), want ({Months:3 Insolvent:true}, true)", got, have)
	}

	// A (Months:0, Insolvent:false) patch — round finding F3 (opus-round-
	// bug769): this is NOT reachable via an in-place funded month once
	// gameOver has latched true (FinanceAPI.gameOver never clears in
	// RecordMonthResult — see finance/insolvency.go); the ONLY way
	// production reaches this pair is a Load/New Game that resets
	// FinanceAPI's underlying state entirely (finance's resetForLoad
	// participant). This decode-level test still exercises the Screen
	// correctly decoding whatever the wire sends, independent of whether
	// that exact transition is reachable in one running session.
	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "insolvencyMonths": 0, "insolvent": false}`),
	})
	got, have = s.Insolvency()
	if !have || got.Months != 0 || got.Insolvent {
		t.Fatalf("after the reset-shaped patch: Insolvency() = (%+v, %v), want ({Months:0 Insolvent:false}, true)", got, have)
	}
}

// TestInsolvency_VerdictDecodes proves the second BUG-769 increment: the
// insolvencyVerdict wire field (engine.spiral's DecayAPI.EvaluateInsolvency
// verdict, added once feat.compositionroot -> engine.spiral was registered)
// decodes into InsolvencyView.Verdict alongside Months/Insolvent.
func TestInsolvency_VerdictDecodes(t *testing.T) {
	s := New("corr-insolvency-verdict")
	s.BindSubscription("sub-1")

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "insolvencyMonths": 3, "insolvent": true, "insolvencyVerdict": "insolvency"}`),
	})
	got, have := s.Insolvency()
	if !have || got.Verdict != "insolvency" {
		t.Fatalf("Insolvency() = (%+v, %v), want Verdict %q", got, have, "insolvency")
	}

	// A patch that omits insolvencyVerdict (a pre-second-increment server,
	// or the Go side's Valid() guard) decodes Verdict as the empty string,
	// distinct from a real "none" reading — Months/Insolvent still decode.
	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "insolvencyMonths": 0, "insolvent": false}`),
	})
	got, have = s.Insolvency()
	if !have || got.Verdict != "" || got.Months != 0 || got.Insolvent {
		t.Fatalf("Insolvency() = (%+v, %v), want {Months:0 Insolvent:false Verdict:\"\"}", got, have)
	}
}

// TestInsolvency_AbsentClearsHaveFlag mirrors
// TestPayrollShortfall_AbsentClearsHaveFlag: a cycle with no
// insolvencyMonths key clears the have-flag rather than keeping a stale
// prior reading.
func TestInsolvency_AbsentClearsHaveFlag(t *testing.T) {
	s := New("corr-insolvency-absent")
	s.BindSubscription("sub-1")

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "insolvencyMonths": 1, "insolvent": false}`),
	})
	if _, have := s.Insolvency(); !have {
		t.Fatalf("precondition: expected have=true after first patch")
	}

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1}`),
	})
	if _, have := s.Insolvency(); have {
		t.Fatalf("after a patch with no insolvencyMonths key: have = true, want false (cleared, not stale)")
	}
}

// TestRenderInsolvency_WarningShowsLine mirrors
// TestRenderPayrollShortfall_ActiveShowsWarning.
func TestRenderInsolvency_WarningShowsLine(t *testing.T) {
	buf := core.NewBuffer(80, 4)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 4}
	style := tcell.StyleDefault

	RenderInsolvency(buf, rect, InsolvencyView{Months: 2, Insolvent: false}, true, style)

	found := false
	for x := 0; x < 80; x++ {
		if buf.Get(x, 0).Rune == 'I' {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("RenderInsolvency(warning) drew nothing at row 0, want the insolvency-warning line")
	}
}

// TestRenderInsolvency_GameOverShowsLine proves the escalated Insolvent
// branch draws a distinct line from the plain warning.
func TestRenderInsolvency_GameOverShowsLine(t *testing.T) {
	buf := core.NewBuffer(80, 4)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 4}
	style := tcell.StyleDefault

	RenderInsolvency(buf, rect, InsolvencyView{Months: 3, Insolvent: true}, true, style)

	found := false
	for x := 0; x < 80; x++ {
		if buf.Get(x, 0).Rune == 'I' {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("RenderInsolvency(insolvent) drew nothing at row 0, want the game-over line")
	}
}

// TestRenderInsolvency_ZeroMonthsDrawsNothing and
// TestRenderInsolvency_NoSignalDrawsNothing together mirror the
// PayrollShortfall pair: a healthy month (Months=0) and an unpublished
// signal (have=false) must both render silently.
func TestRenderInsolvency_ZeroMonthsDrawsNothing(t *testing.T) {
	buf := core.NewBuffer(80, 4)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 4}
	style := tcell.StyleDefault

	RenderInsolvency(buf, rect, InsolvencyView{Months: 0, Insolvent: false}, true, style)

	assertBufferBlank(t, buf, rect, "RenderInsolvency(months=0)")
}

// TestRenderInsolvency_LatchedZeroMonthsStillShowsGameOver is the round's
// F2 finding (opus-round-bug769), pinned as a permanent regression
// alongside attack_bug769_render_test.go's own copy: FinanceAPI.gameOver
// LATCHES (RecordMonthResult never clears it) while InsolvencyMonths
// resets to 0 on the next met month — a real, reachable state (3 starved
// months then 3 funded months, never a save/load) is Months=0 +
// Insolvent=true. The pre-fix guard (`v.Months <= 0` alone) drew NOTHING
// here; the GAME OVER line must persist.
func TestRenderInsolvency_LatchedZeroMonthsStillShowsGameOver(t *testing.T) {
	buf := core.NewBuffer(80, 4)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 4}
	style := tcell.StyleDefault

	RenderInsolvency(buf, rect, InsolvencyView{Months: 0, Insolvent: true, Verdict: "insolvency"}, true, style)

	found := false
	for x := 0; x < 80; x++ {
		if buf.Get(x, 0).Rune == 'I' {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("RenderInsolvency(Months=0, Insolvent=true) drew nothing — the latched game-over state must still render the GAME OVER line")
	}
}

func TestRenderInsolvency_NoSignalDrawsNothing(t *testing.T) {
	buf := core.NewBuffer(80, 4)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 4}
	style := tcell.StyleDefault

	RenderInsolvency(buf, rect, InsolvencyView{Months: 2, Insolvent: false}, false, style)

	assertBufferBlank(t, buf, rect, "RenderInsolvency(have=false)")
}
