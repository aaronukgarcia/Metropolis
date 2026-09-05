package finance

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/gdamore/tcell/v2"
)

// BUG-723 round finding F1/F3: the TUI finance screen previously had no
// wirePayrollShortfallView/PayrollShortfallView at all — this file proves
// the Screen-level wiring compose.go's finance_publish.go's
// financePayrollShortfallView is mirrored against, mirroring
// feat143_unlimited_test.go's TestUnlimitedMoney_Signal/
// TestUnlimitedMoney_AbsentClearsHaveFlag/TestRenderMoneyMode_* shapes
// exactly for the equally-optional payrollShortfall section.

// TestPayrollShortfall_Signal proves ApplyDelta decodes a real
// payrollShortfall section into the Screen's PayrollShortfall() accessor.
func TestPayrollShortfall_Signal(t *testing.T) {
	s := New("corr-payroll-shortfall")
	s.BindSubscription("sub-1")

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "payrollShortfall": {"month": 7, "amountMicropounds": 200000, "months": 2}}`),
	})
	got, have := s.PayrollShortfall()
	if !have {
		t.Fatalf("after a payrollShortfall patch: have = false, want true")
	}
	if got.Month != 7 || got.AmountMicropounds != 200000 || got.Months != 2 {
		t.Fatalf("PayrollShortfall() = %+v, want {Month:7 AmountMicropounds:200000 Months:2}", got)
	}

	// Clear: the next patch reports a zero amount, mirroring compose's
	// own "clear the surface" call shape.
	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "payrollShortfall": {"month": 8, "amountMicropounds": 0, "months": 0}}`),
	})
	got, have = s.PayrollShortfall()
	if !have || got.AmountMicropounds != 0 || got.Months != 0 {
		t.Fatalf("after the clearing patch: PayrollShortfall() = (%+v, %v), want ({Month:8 AmountMicropounds:0 Months:0}, true)", got, have)
	}
}

// TestPayrollShortfall_AbsentClearsHaveFlag mirrors
// TestUnlimitedMoney_AbsentClearsHaveFlag: a cycle with no
// payrollShortfall key clears the have-flag rather than keeping a stale
// prior reading.
func TestPayrollShortfall_AbsentClearsHaveFlag(t *testing.T) {
	s := New("corr-payroll-shortfall-absent")
	s.BindSubscription("sub-1")

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "payrollShortfall": {"month": 1, "amountMicropounds": 100000, "months": 1}}`),
	})
	if _, have := s.PayrollShortfall(); !have {
		t.Fatalf("precondition: expected have=true after first patch")
	}

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1}`),
	})
	if _, have := s.PayrollShortfall(); have {
		t.Fatalf("after a patch with no payrollShortfall key: have = true, want false (cleared, not stale)")
	}
}

// TestRenderPayrollShortfall_ActiveShowsWarning mirrors
// TestRenderMoneyMode_UnlimitedShowsIndicator.
func TestRenderPayrollShortfall_ActiveShowsWarning(t *testing.T) {
	buf := core.NewBuffer(80, 4)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 4}
	style := tcell.StyleDefault

	RenderPayrollShortfall(buf, rect, PayrollShortfallView{Month: 3, AmountMicropounds: 200000, Months: 2}, true, style)

	found := false
	for x := 0; x < 80; x++ {
		if buf.Get(x, 0).Rune == 'P' {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("RenderPayrollShortfall(active) drew nothing at row 0, want the shortfall line")
	}
}

// TestRenderPayrollShortfall_ZeroAmountDrawsNothing and
// TestRenderPayrollShortfall_NoSignalDrawsNothing together mirror
// TestRenderMoneyMode_RealDrawsNothing: a healthy month (amount 0) and an
// unpublished signal (have=false) must both render silently — the whole
// point of a status surface is that it says NOTHING when there is nothing
// wrong, never a "£0 shortfall" line every month.
func TestRenderPayrollShortfall_ZeroAmountDrawsNothing(t *testing.T) {
	buf := core.NewBuffer(80, 4)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 4}
	style := tcell.StyleDefault

	RenderPayrollShortfall(buf, rect, PayrollShortfallView{Month: 3, AmountMicropounds: 0, Months: 0}, true, style)

	assertBufferBlank(t, buf, rect, "RenderPayrollShortfall(amount=0)")
}

func TestRenderPayrollShortfall_NoSignalDrawsNothing(t *testing.T) {
	buf := core.NewBuffer(80, 4)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 4}
	style := tcell.StyleDefault

	RenderPayrollShortfall(buf, rect, PayrollShortfallView{Month: 3, AmountMicropounds: 200000, Months: 2}, false, style)

	assertBufferBlank(t, buf, rect, "RenderPayrollShortfall(have=false)")
}

func assertBufferBlank(t *testing.T, buf *core.Buffer, rect core.Rect, label string) {
	t.Helper()
	for y := 0; y < rect.H; y++ {
		for x := 0; x < rect.W; x++ {
			if r := buf.Get(x, y).Rune; r != ' ' {
				t.Fatalf("%s drew %q at (%d,%d), want nothing", label, r, x, y)
			}
		}
	}
}
