package widgets

import (
	"math"
	"strings"
	"testing"
)

func TestRoundR1Money_FormatPoundsTable(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "£0"}, {1, "£0"}, {-1, "£0"}, {499, "£0"}, {-499, "£0"}, {500, "£1"}, {-500, "-£1"}, {501, "£1"}, {-501, "-£1"},
		{999, "£1"}, {-999, "-£1"}, {1000, "£1"}, {-1000, "-£1"}, {1499, "£1"}, {-1499, "-£1"}, {1500, "£2"}, {-1500, "-£2"},
		{999499, "£999"}, {-999499, "-£999"}, {999500, "£1,000"}, {-999500, "-£1,000"},
		{123485323000, "£123,485,323"},
		{math.MinInt64, "-£9,223,372,036,854,776"},
		{math.MaxInt64, "£9,223,372,036,854,776"},
		{math.MinInt64 + 1, "-£9,223,372,036,854,776"},
	}
	for _, c := range cases {
		if got := FormatPounds(c.in); got != c.want {
			t.Errorf("FormatPounds(%d) = %q want %q", c.in, got, c.want)
		}
		if strings.Contains(FormatPounds(c.in), "--") || strings.Contains(FormatPounds(c.in), "-£0") {
			t.Errorf("bad sign on %d", c.in)
		}
	}
	for _, v := range []int64{0, 1, -1, -499, 499, 500, -500} {
		t.Logf("FormatSignedPounds(%d) = %q", v, FormatSignedPounds(v))
	}
	if got := FormatSignedPounds(math.MinInt64); got != "-£9,223,372,036,854,776" {
		t.Errorf("signed min: %q", got)
	}
	if got := FormatWholePounds(math.MinInt64); got != "-£9,223,372,036,854,775,808" {
		t.Errorf("whole min: %q", got)
	}
}

func TestRoundR1Money_FormatMoneyTextTable(t *testing.T) {
	long := strings.Repeat("9", 400)
	for _, in := range []string{"£15000.00", "15000", "-£7.50", "£1,234.56", "£", "", " £5 ", "£5.5", "£5.555", "20k", "3B", "£-5", "£€é£", "£" + long, long, "-", "£.50", "£0.49", "-£0.49", "£999999999999999999", "£9999999999999999999"} {
		got, ok := FormatMoneyText(in)
		t.Logf("%q -> %q ok=%v", in, got, ok)
		if !ok && got != in {
			t.Errorf("unchanged contract broken for %q: %q", in, got)
		}
	}
}
