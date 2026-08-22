package fiscal

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestMoneyTimesRateHugeRateNeverNegative asserts moneyTimesRate never lets
// a huge (but finite, non-negative) rate defeat the negative-rate guard by
// wrapping the float64->int64 basis-point conversion into a negative bp
// (SEC-147). The repro is the exact one from the finding: rate*100 lands
// exactly on 2^63, so a bare int64(math.Round(...)) wraps to math.MinInt64
// on amd64. amount=1 is deliberate: with bp landing on math.MinInt64,
// num.SafeMul(1, MinInt64) is a case its own overflow check does NOT flag
// (the product is exactly representable), so before the fix this
// rate/amount pair silently returned a large NEGATIVE money figure from a
// non-negative rate, with no error at all — the guard defeated. After the
// fix (bp routed through num.ClampInt64FromFloat, which saturates a huge
// positive float to math.MaxInt64 rather than wrapping it negative) the
// result must either be rejected outright or be non-negative; it must never
// silently go negative.
func TestMoneyTimesRateHugeRateNeverNegative(t *testing.T) {
	f, _, _ := newTestFiscal(t)

	const hugeRate = 9.223372036854776e16 // rate*100 == exactly 2^63, the bare-cast wrap boundary
	got, err := f.moneyTimesRate(finance.Money(1), hugeRate)
	if err != nil {
		var e *errs.E
		if !errors.As(err, &e) {
			t.Fatalf("moneyTimesRate(1, %v%%) error is %T, want *errs.E", hugeRate, err)
		}
		if e.Code != ErrFiscalOverflow {
			t.Errorf("moneyTimesRate(1, %v%%) error code = %q, want %q", hugeRate, e.Code, ErrFiscalOverflow)
		}
		return
	}
	if got < 0 {
		t.Errorf("moneyTimesRate(1, %v%%) = %d, want >= 0 (negative-rate guard must not be defeated by float wraparound, SEC-147)", hugeRate, int64(got))
	}
}

// TestMoneyTimesRateNormalRateStillWorks is the non-adversarial control: a
// realistic rate must still produce a positive, correctly-scaled result
// after the SEC-147 fix (basis-point conversion routed through
// num.ClampInt64FromFloat).
func TestMoneyTimesRateNormalRateStillWorks(t *testing.T) {
	f, _, _ := newTestFiscal(t)

	got, err := f.moneyTimesRate(finance.Money(1000), 20.0)
	if err != nil {
		t.Fatalf("moneyTimesRate(1000, 20%%): %v", err)
	}
	if got != 200 {
		t.Errorf("moneyTimesRate(1000, 20%%) = %d, want 200", int64(got))
	}
}
