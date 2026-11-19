package widgets

import (
	"strconv"
	"strings"
)

// UnitsPerPound is the scale of the engine's money unit as the UI needs it:
// money is stored as int64 units with 1,000 units to the pound (BUG-452
// rebase, 2026-09-01; see internal/foundation/det/money.go
// MicropoundsPerPound). The UI may not import internal/engine, so this is the
// ONE UI-side copy of the constant (GR#3); every screen package uses this
// instead of its own. A test in cmd/metropolis (which may import the engine)
// pins it equal to the engine's value, so a future rescale cannot leave the
// screen silently wrong.
const UnitsPerPound int64 = 1_000

// FormatPounds renders int64 money units as whole pounds with a thousands
// comma and no pence: 123485323000 units -> "£123,485,323". A negative figure
// has its minus BEFORE the pound sign ("-£1,234"). Rounding is half away from
// zero to the nearest whole pound (1,499 units -> "£1", 1,500 -> "£2",
// -1,500 -> "-£2"); a figure that rounds to zero is "£0", never "-£0".
// Integer arithmetic only. The magnitude is held in uint64 so math.MinInt64
// (whose absolute value has no int64 form) never wraps or prints a doubled
// minus (SEC-142).
func FormatPounds(units int64) string {
	neg := units < 0
	var mag uint64
	if neg {
		mag = uint64(-(units + 1)) + 1
	} else {
		mag = uint64(units)
	}
	per := uint64(UnitsPerPound)
	pounds := mag / per
	if mag%per >= per/2 {
		pounds++
	}
	return signedPounds(neg && pounds != 0, pounds)
}

// FormatWholePounds renders a figure that is already in whole pounds (the
// chrome top line's treasury, which the publisher has converted) in the same
// style as FormatPounds: "£26,851,123", "-£5".
func FormatWholePounds(pounds int64) string {
	if pounds < 0 {
		return signedPounds(true, uint64(-(pounds+1))+1)
	}
	return signedPounds(false, uint64(pounds))
}

// FormatSignedPounds is FormatPounds with an explicit plus on a positive
// figure ("+£20", "-£75"; a figure that rounds to zero is "£0"), for a residue row where the direction of the
// money must never be a guess.
func FormatSignedPounds(units int64) string {
	s := FormatPounds(units)
	if s == "£0" {
		return s // rounds to zero: no sign from either side
	}
	if units >= 0 {
		return "+" + s
	}
	return s
}

func signedPounds(neg bool, pounds uint64) string {
	digits := strconv.FormatUint(pounds, 10)
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	b.WriteString("£")
	lead := len(digits) % 3
	if lead > 0 {
		b.WriteString(digits[:lead])
	}
	for i := lead; i < len(digits); i += 3 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// FormatMoneyText reformats a pre-rendered money TEXT that arrives from the
// engine or from data (the build catalogue's CostRaw, "£15000.00") in the same
// style. It is deliberately strict: only an optional leading minus, an
// optional pound sign, digits and an optional ".dd" pence part are accepted
// (pence round half away from zero: .50 or more rounds the pound up). Any
// other text ("20k", "3B", "", "£1.5", "£1,000") is returned UNCHANGED with
// ok=false so a caller or a test can count what was not understood; this is
// the one parser, so no screen parses money strings itself.
func FormatMoneyText(raw string) (string, bool) {
	s := raw
	neg := false
	if rest, ok := strings.CutPrefix(s, "-"); ok {
		neg, s = true, rest
	}
	s = strings.TrimPrefix(s, "£")
	whole, pence := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		whole, pence = s[:i], s[i+1:]
		if len(pence) != 2 || !allDigits(pence) {
			return raw, false
		}
	}
	if whole == "" || !allDigits(whole) {
		return raw, false
	}
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	if len(whole) > 18 {
		return raw, false
	}
	p, err := strconv.ParseUint(whole, 10, 64)
	if err != nil {
		return raw, false
	}
	if pence != "" && pence >= "50" {
		p++
	}
	return signedPounds(neg && p != 0, p), true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
