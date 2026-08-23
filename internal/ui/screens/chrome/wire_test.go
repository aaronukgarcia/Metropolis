package chrome

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestDecodeFiguresPatch checks the happy path of the "chrome.topbar" wire
// schema (AC-1): all six fields decode field-for-field.
func TestDecodeFiguresPatch(t *testing.T) {
	f, err := decodeFiguresPatch(mustFiguresPatch(t, "Aug 2026", 14, 2, 123456, 50000, "AA"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Date != "Aug 2026" || f.ClockCycle != 14 || f.Speed != 2 || f.Money != 123456 || f.Population != 50000 || f.Rating != "AA" {
		t.Fatalf("decoded figures = %+v", f)
	}
}

// TestDecodeFiguresPatchInvalidJSON checks that invalid JSON is rejected with
// an error, never silently decoded into zero figures.
func TestDecodeFiguresPatchInvalidJSON(t *testing.T) {
	if _, err := decodeFiguresPatch(json.RawMessage("{not json")); err == nil {
		t.Fatal("decodeFiguresPatch(invalid JSON) returned nil error")
	}
}

// TestDecodeFiguresPatchUnsupportedVersion checks that a patch carrying an
// unrecognised schemaVersion is rejected (last-known-good figures stand),
// never guessed at. The patch carries a FULL six-key figures object, so the
// ONLY thing wrong with it is the version -- a version-99 patch rejected by the
// r3/r5 content guards (because its figures subset is incomplete) would not
// prove the r2 schemaVersion check is load-bearing (round-5 attacker finding).
func TestDecodeFiguresPatchUnsupportedVersion(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"schemaVersion": 99, "figures": map[string]any{"date": "Jan Y1", "clockCycle": 3, "speed": 1, "money": 1234, "population": 50000, "rating": "1000/1000"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeFiguresPatch(raw); err == nil {
		t.Fatal("decodeFiguresPatch(schemaVersion 99) returned nil error")
	}
}

// TestDecodeFiguresPatchEmptyOrPartialFigures checks BUG-324 rounds 2, 3 and
// 4's attacker findings: a structurally-valid patch whose figures carry EMPTY
// or PARTIAL content must be rejected (keeping last-known-good), never decoded
// cleanly and applied to blank the bar. The engine publisher always fills all
// six figures, so the content guard is a KEY-PRESENCE check on the RAW
// figures JSON -- an empty object, a missing figures field, a partial subset,
// or a null value are all by construction not a real engine delta. A subset
// carrying BOTH identity strings but no numerics (round-3's repro) must be
// rejected too: the round-2 Date==""||Rating=="" guard proved only that the
// two strings are present, not that all six fields are. And an all-six-keys
// patch whose date/rating is WHITESPACE-ONLY or control-char (round-4's repro)
// must be rejected as well -- whitespace-only is the same class as empty.
func TestDecodeFiguresPatchEmptyOrPartialFigures(t *testing.T) {
	cases := []struct {
		name  string
		patch string
	}{
		{"empty figures object", `{"schemaVersion":1,"figures":{}}`},
		{"missing figures field", `{"schemaVersion":1}`},
		{"null figures", `{"schemaVersion":1,"figures":null}`},
		{"partial (date only)", `{"schemaVersion":1,"figures":{"date":"HACKED"}}`},
		{"partial (money/pop only)", `{"schemaVersion":1,"figures":{"money":999,"population":42}}`},
		{"partial (date+rating only)", `{"schemaVersion":1,"figures":{"date":"Jan Y1","rating":"1000/1000"}}`},
		{"null date value", `{"schemaVersion":1,"figures":{"date":null,"clockCycle":0,"speed":0,"money":10,"population":64,"rating":"1000/1000"}}`},
		{"empty date", `{"schemaVersion":1,"figures":{"date":"","clockCycle":0,"speed":0,"money":10,"population":64,"rating":"1000/1000"}}`},
		{"empty rating", `{"schemaVersion":1,"figures":{"date":"Jan Y1","clockCycle":0,"speed":0,"money":10,"population":64,"rating":""}}`},
		{"whitespace date", `{"schemaVersion":1,"figures":{"date":"   ","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"tab date", `{"schemaVersion":1,"figures":{"date":"\t","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"whitespace rating", `{"schemaVersion":1,"figures":{"date":"Jan Y1","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"   "}}`},
		// Round-5: control characters outside the whitespace set. TrimSpace
		// trims only unicode.IsSpace; NUL/SOH and other C0/C1 controls (plus
		// zero-width format chars) must be rejected as the same class as empty.
		// The wire text uses JSON escapes (\u0000, \u0001, \u200b, \u007f) so the control char is
		// present in the DECODED string -- the content check must reject it, not
		// json.Unmarshal (a literal control byte in the JSON document would be
		// invalid JSON and rejected for the wrong reason).
		{"NUL date", `{"schemaVersion":1,"figures":{"date":"\u0000","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"SOH date", `{"schemaVersion":1,"figures":{"date":"\u0001","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"double NUL date", `{"schemaVersion":1,"figures":{"date":"\u0000\u0000","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"space then NUL date", `{"schemaVersion":1,"figures":{"date":" \u0000","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"NUL then space date", `{"schemaVersion":1,"figures":{"date":"\u0000 ","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"embedded NUL date", `{"schemaVersion":1,"figures":{"date":"Jan\u0000 Y1","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"NUL rating", `{"schemaVersion":1,"figures":{"date":"Jan Y1","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"\u0000"}}`},
		{"SOH rating", `{"schemaVersion":1,"figures":{"date":"Jan Y1","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"\u0001"}}`},
		{"NUL padded rating", `{"schemaVersion":1,"figures":{"date":"Jan Y1","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"\u0000   "}}`},
		{"zero-width-space date", `{"schemaVersion":1,"figures":{"date":"\u200b","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"DEL date", `{"schemaVersion":1,"figures":{"date":"\u007f","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		// Round-6 (combining marks + non-printable runes): the r5 guard rejected
		// Cc/Cf only, so a patch of COMBINING MARKS (Mn/Mc/Me -- printable, so
		// IsControl=false, Cf=false) decoded cleanly and rendered a BLANK
		// date/rating segment. The identity-string test now enforces "every
		// rune renders as visible text": !unicode.IsPrint (covers Cc/Cf AND
		// unassigned AND Zl/Zp) or a combining mark (the printable-but-invisible
		// category) is rejected. The wire text uses JSON escapes so the runes
		// are present in the DECODED string -- the content check must reject
		// them, not the JSON decoder.
		{"combining marks date (r5 attacker's exact repro)", `{"schemaVersion":1,"figures":{"date":"\u0301\u0302\u0303","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"combining marks rating", `{"schemaVersion":1,"figures":{"date":"Jan Y1","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"\u0304\u0305\u0307"}}`},
		{"embedded combining mark date", `{"schemaVersion":1,"figures":{"date":"Jan\u0301Y1","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"combining mark rating + space", `{"schemaVersion":1,"figures":{"date":"Jan Y1","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000\u0301"}}`},
		{"line separator date", `{"schemaVersion":1,"figures":{"date":"Jan\u2028Y1","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"paragraph separator date", `{"schemaVersion":1,"figures":{"date":"Jan\u2029Y1","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"unassigned codepoint date", `{"schemaVersion":1,"figures":{"date":"\u0378","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		// Round-7 (printable-but-BLANK runes): unicode.IsPrint admits U+2800
		// BRAILLE PATTERN BLANK and the Unicode filler codepoints, all Print
		// with a zero glyph, so a date/rating of nothing but these decodes
		// cleanly and renders a BLANK segment (the same class rounds 4-6 were
		// meant to close). The wire text uses JSON escapes so the runes are
		// present in the DECODED string; the denylist in isBlankIdentityString
		// must reject them, not the JSON decoder.
		{"braille-pattern-blank date (r7 attacker repro)", `{"schemaVersion":1,"figures":{"date":"\u2800","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"hangul-filler date (r7 attacker repro)", `{"schemaVersion":1,"figures":{"date":"\u3164","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"choseong-filler date", `{"schemaVersion":1,"figures":{"date":"\u115f","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
		{"halfwidth-hangul-filler rating", `{"schemaVersion":1,"figures":{"date":"Jan Y1","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"\uffa0"}}`},
		{"braille-pattern-blank embedded date", `{"schemaVersion":1,"figures":{"date":"Jan\u2800Y1","clockCycle":3,"speed":1,"money":1234,"population":50000,"rating":"1000/1000"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeFiguresPatch(json.RawMessage(tc.patch)); err == nil {
				t.Fatalf("decodeFiguresPatch(%s) returned nil error -- empty/partial figures must be rejected", tc.patch)
			}
		})
	}
}

// TestDecodeFiguresPatchFullPatchPasses checks the content guard's other
// half: a genuinely complete patch -- every field populated, including both
// identity strings -- still decodes. Guards that cannot be satisfied by the
// real publisher would be over-blocking.
func TestDecodeFiguresPatchFullPatchPasses(t *testing.T) {
	f, err := decodeFiguresPatch(mustFiguresPatch(t, "Jan Y1", 0, 0, 10, 64, "1000/1000"))
	if err != nil {
		t.Fatalf("full patch rejected: %v", err)
	}
	if f.Date != "Jan Y1" || f.Rating != "1000/1000" {
		t.Fatalf("decoded figures = %+v", f)
	}
}

// TestDecodeFiguresPatchVisibleUnicodePasses checks the round-6 hardening's
// other half: a patch whose identity strings carry genuinely VISIBLE non-ASCII
// content (a precomposed accented letter -- Lc, printable, not a combining
// mark) still decodes. The stricter "every rune renders as visible text"
// predicate must not over-block a future localized date string: the invariant
// is visibility, not ASCII-ness (the engine today emits ASCII-only, but a
// localised month name is the same class of visible content).
func TestDecodeFiguresPatchVisibleUnicodePasses(t *testing.T) {
	f, err := decodeFiguresPatch(mustFiguresPatch(t, "Août 2026", 0, 0, 10, 64, "1000/1000"))
	if err != nil {
		t.Fatalf("visible-unicode patch rejected: %v", err)
	}
	if f.Date != "Août 2026" {
		t.Fatalf("decoded figures = %+v", f)
	}
}

// TestApplyFiguresPatchMalformedKeepsLastKnownGood checks AC-1's safety half:
// a malformed patch is dropped and the top bar keeps its last-known-good
// figures -- it never renders a corrupted/partial state (GR#1).
func TestApplyFiguresPatchMalformedKeepsLastKnownGood(t *testing.T) {
	c := NewChrome("test", widgets.DefaultPalette, Effects{})
	c.ApplyFiguresPatch(mustFiguresPatch(t, "Aug 2026", 14, 2, 123456, 50000, "AA"))
	before := c.Figures()

	c.ApplyFiguresPatch(json.RawMessage("{bad"))

	if after := c.Figures(); after != before {
		t.Fatalf("malformed patch changed figures: before=%+v after=%+v", before, after)
	}
}
