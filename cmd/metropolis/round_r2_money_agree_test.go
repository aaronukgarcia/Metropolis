package main

import (
	"math"
	"math/rand"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// Round r2 attack: the engine rounding helper and the UI formatter agree for
// seeded random values, a dense window around zero, and the edge table.
func TestRoundR2_MoneyHelperAndFormatterAgree(t *testing.T) {
	check := func(u int64) {
		want := widgets.FormatWholePounds(det.WholePoundsRounded(u))
		if got := widgets.FormatPounds(u); got != want {
			t.Fatalf("units %d: formatter %q helper %q", u, got, want)
		}
	}
	for u := int64(-3000); u <= 3000; u++ {
		check(u)
	}
	rng := rand.New(rand.NewSource(20261118))
	for i := 0; i < 100_000; i++ {
		check(int64(rng.Uint64()))
	}
	table := map[int64]int64{
		math.MaxInt64: 9223372036854776, math.MinInt64: -9223372036854776,
		499: 0, 500: 1, 501: 1, -499: 0, -500: -1, -501: -1,
		1499: 1, 1500: 2, -1499: -1, -1500: -2, 0: 0,
	}
	for in, want := range table {
		if got := det.WholePoundsRounded(in); got != want {
			t.Fatalf("WholePoundsRounded(%d) = %d, want %d", in, got, want)
		}
		check(in)
	}
	for _, in := range []int64{math.MaxInt64 - 1, math.MinInt64 + 1, math.MaxInt64 - 500, math.MinInt64 + 500} {
		check(in)
	}
	for u := int64(-499); u <= 499; u++ {
		if s := widgets.FormatSignedPounds(u); s != "£0" {
			t.Fatalf("units %d signed %q", u, s)
		}
	}
	if widgets.FormatSignedPounds(500) != "+£1" || widgets.FormatSignedPounds(-500) != "-£1" {
		t.Fatalf("signed at 500: %q %q", widgets.FormatSignedPounds(500), widgets.FormatSignedPounds(-500))
	}
}
