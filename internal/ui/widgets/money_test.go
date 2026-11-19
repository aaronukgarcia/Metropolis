package widgets

import (
	"math"
	"testing"
)

func TestFormatPounds_Table(t *testing.T) {
	cases := []struct {
		units int64
		want  string
	}{
		{0, "£0"}, {1, "£0"}, {499, "£0"}, {500, "£1"}, {999, "£1"}, {1000, "£1"},
		{1499, "£1"}, {1500, "£2"}, {999_499, "£999"}, {999_500, "£1,000"},
		{1_000_000, "£1,000"}, {123_485_323_000, "£123,485,323"},
		{-1, "£0"}, {-499, "£0"}, {-500, "-£1"}, {-999, "-£1"}, {-1000, "-£1"},
		{-1499, "-£1"}, {-1500, "-£2"}, {-999_500, "-£1,000"},
		{-1_000_000, "-£1,000"}, {-123_485_323_000, "-£123,485,323"},
		{math.MaxInt64, "£9,223,372,036,854,776"},
		{math.MinInt64, "-£9,223,372,036,854,776"},
	}
	for _, c := range cases {
		if got := FormatPounds(c.units); got != c.want {
			t.Errorf("FormatPounds(%d) = %q, want %q", c.units, got, c.want)
		}
	}
}

func TestFormatWholePounds_Table(t *testing.T) {
	cases := map[int64]string{
		0: "£0", 5: "£5", 999: "£999", 1000: "£1,000", 26851123: "£26,851,123",
		123485323: "£123,485,323", -5: "-£5", -1234: "-£1,234",
		math.MaxInt64: "£9,223,372,036,854,775,807",
		math.MinInt64: "-£9,223,372,036,854,775,808",
	}
	for in, want := range cases {
		if got := FormatWholePounds(in); got != want {
			t.Errorf("FormatWholePounds(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatSignedPounds(t *testing.T) {
	cases := map[int64]string{20_000: "+£20", -75_000: "-£75", 0: "£0", 499: "£0", -499: "£0", 1_500: "+£2"}
	for in, want := range cases {
		if got := FormatSignedPounds(in); got != want {
			t.Errorf("FormatSignedPounds(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatMoneyText(t *testing.T) {
	good := map[string]string{
		"£15000.00": "£15,000", "£848484.00": "£848,484", "-£1234.50": "-£1,235",
		"£0.49": "£0", "-£0.20": "£0", "£7": "£7", "15000": "£15,000", "£000.00": "£0",
	}
	for in, want := range good {
		got, ok := FormatMoneyText(in)
		if !ok || got != want {
			t.Errorf("FormatMoneyText(%q) = %q,%v want %q,true", in, got, ok, want)
		}
	}
	bad := []string{"", "20k", "3B", "£1.5", "£1,000", "£", "£1.234", "abc", "£-5", " £5", "£12345678901234567890"}
	unchanged := 0
	for _, in := range bad {
		got, ok := FormatMoneyText(in)
		if ok || got != in {
			t.Errorf("FormatMoneyText(%q) = %q,%v want unchanged,false", in, got, ok)
			continue
		}
		unchanged++
	}
	if unchanged != len(bad) {
		t.Fatalf("unchanged count %d of %d", unchanged, len(bad))
	}
}

// FEAT-9995385625 r2: a figure that rounds to zero pounds shows "£0" with no
// sign from either side.
func TestFormatSignedPounds_RoundsToZeroHasNoSign(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "£0"}, {1, "£0"}, {499, "£0"}, {-1, "£0"}, {-499, "£0"},
		{500, "+£1"}, {1_499, "+£1"}, {-500, "-£1"}, {-1_499, "-£1"}, {20_000, "+£20"}, {-75_000, "-£75"},
	}
	for _, c := range cases {
		if got := FormatSignedPounds(c.in); got != c.want {
			t.Errorf("FormatSignedPounds(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
