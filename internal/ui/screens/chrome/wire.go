package chrome

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// wireSchemaVersion is the only schemaVersion ApplyFiguresPatch accepts for
// a "chrome.topbar" figures patch. Bumping the schema is additive-by-new-
// version, the same convention protocol/commands.go documents — a patch
// carrying an unrecognised version is rejected (last-known-good figures
// stand), never guessed at.
const wireSchemaVersion = 1

// wireFiguresPatch is the wire shape of a "chrome.topbar" view patch — the
// JSON carried in a protocol.Delta.Patch for the figures subscription. It is
// this package's own package-local copy of the schema (mirroring
// ui.screen.map's package-local wire shape), NOT a type imported from the
// engine: Chrome never imports internal/engine (GR#20, AC-13). The Figures
// field is the package's own exported Figures type, JSON-tagged directly —
// no separate wire mirror struct exists (the fields are exported and tagged
// on the owning type, matching FEAT-042 AC-27's "no hidden wire mirror"
// preference).
type wireFiguresPatch struct {
	SchemaVersion int     `json:"schemaVersion"`
	Figures       Figures `json:"figures"`
}

// errUnsupportedSchemaVersion is decodeFiguresPatch's error for a
// "chrome.topbar" patch whose schemaVersion this package doesn't
// understand.
func errUnsupportedSchemaVersion(got int) error {
	return fmt.Errorf("chrome: unsupported chrome.topbar schemaVersion %d (want %d)", got, wireSchemaVersion)
}

// errEmptyFigures is decodeFiguresPatch's error for a structurally-valid
// "chrome.topbar" patch whose figures carry no usable content — an empty or
// partial field set (BUG-324 round-2 attacker finding) or a subset missing
// one of the six keys the engine publisher always emits (BUG-324 round-3
// attacker finding). Same malformed-patch class as the schemaVersion and
// JSON errors, so it flows through the same ErrMalformedPatch logMalformed
// path.
func errEmptyFigures() error {
	return fmt.Errorf("chrome: chrome.topbar figures patch carries empty/partial figures")
}

// figuresKeys is the canonical six-key set a "chrome.topbar" figures patch
// must carry. The engine publisher (buildChromeTopBarPatch) ALWAYS marshals
// all six fields (none is omitempty), so a patch missing any one — however
// plausible the subset it does carry looks — is by construction not a real
// engine delta, and rejecting it keeps the last-known-good figures on screen
// instead of blanking them.
var figuresKeys = []string{"date", "clockCycle", "speed", "money", "population", "rating"}

// decodeFiguresPatch decodes raw as a "chrome.topbar" figures patch. It
// returns an error for invalid JSON, an unrecognised schemaVersion, or a
// structurally-valid patch whose figures carry no usable content (an empty or
// partial field set); the zero Figures is returned on error and must not be
// applied.
//
// BUG-324 round-2 (independent attacker finding): the pre-r2 guard validated
// only JSON shape + schemaVersion, so a structurally-valid patch with EMPTY
// or PARTIAL figures — `{"schemaVersion":1,"figures":{}}`, `{"schemaVersion":1}`,
// `{"figures":null}`, or a partial figure subset — decoded cleanly and
// REPLACED the whole Figures value, blanking the bar to
// ` | cycle 0/30 | money 0 | pop 0 | rating` — the exact plausible-zero
// empty-bar failure BUG-324 exists to prevent.
//
// BUG-324 round-3 (independent attacker finding): the round-2 guard's
// Date==""||Rating=="" check was necessary but NOT sufficient — a subset
// carrying BOTH identity strings and nothing else
// (`{"schemaVersion":1,"figures":{"date":"Jan Y1","rating":"1000/1000"}}`)
// decoded cleanly and blanked the four numerics to plausible zeros. The
// guard now presence-checks ALL SIX keys on the RAW figures JSON — never the
// typed struct, because decoding into a Figures first would make an absent
// field indistinguishable from a legitimately-zero one, and a zero struct
// re-marshals every key, so a presence check after the typed decode could
// never fire. Money and population can legitimately be 0, so this is a
// KEY-PRESENCE check (each of the six keys present and non-null — a null
// value counts as absent), never a value check. The engine publisher fills
// all six fields and never emits a null, so this cannot reject a genuine
// engine delta.
//
// BUG-324 round-4 (independent attacker finding): the six-key presence loop
// plus a literal `==""` check still let a WHITESPACE-ONLY date/rating through
// — all six keys present, identity strings all spaces (`{"date":"   ", ...}`)
// — which rendered a blank date/rating segment. The identity-string test is
// therefore TrimSpace-based: whitespace-only is the same class as empty (the
// engine's chromeDateString/chromeRatingString never emit spaces-only content,
// so this cannot reject a genuine delta).
//
// BUG-324 round-5 (independent attacker finding): TrimSpace alone was still
// NOT sufficient — it trims only unicode.IsSpace, so a CONTROL character
// outside the whitespace set (NUL, SOH, other C0/C1 controls, zero-width
// format chars) slipped through the presence loop AND the TrimSpace check,
// decoded cleanly, and replaced last-known-good figures (rendered as a
// replacement glyph by the rune sanitizer). The identity-string test is
// therefore BOTH TrimSpace-based AND control-character-rejecting: whitespace-
// only OR any control char is the same class as empty. The engine publisher
// emits neither spaces-only nor control-bearing content, so this cannot reject
// a genuine delta.
//
// BUG-324 round-6 (independent attacker finding): Cc/Cf rejection was still
// NOT sufficient — a printable COMBINING MARK (Mn/Mc/Me, e.g. U+0301) is
// neither control nor format, so a patch of nothing but combining marks
// (`́̂̃`) passed the presence loop AND the identity-string
// test, decoded cleanly, and rendered a BLANK date/rating segment — the same
// blank-segment class rounds 4 and 5 were meant to close, reopened one
// Unicode category deeper. The identity-string test now enforces "every rune
// renders as visible text": unicode.IsPrint (already false for Cc/Cf,
// unassigned, and Zl/Zp) AND no combining mark (the printable-but-invisible
// category). The engine publisher emits ASCII-only identity strings, so this
// cannot reject a genuine delta.
func decodeFiguresPatch(raw json.RawMessage) (Figures, error) {
	// Capture the figures payload as RAW JSON first: schemaVersion validation
	// and the six-key presence check must run against what the wire actually
	// carried, before any typed decode can flatten it.
	var top struct {
		SchemaVersion int             `json:"schemaVersion"`
		Figures       json.RawMessage `json:"figures"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return Figures{}, err
	}
	if top.SchemaVersion != wireSchemaVersion {
		return Figures{}, errUnsupportedSchemaVersion(top.SchemaVersion)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(top.Figures, &keys); err != nil {
		return Figures{}, err
	}
	for _, k := range figuresKeys {
		v, ok := keys[k]
		if !ok || string(v) == "null" {
			return Figures{}, errEmptyFigures()
		}
	}
	var p wireFiguresPatch
	if err := json.Unmarshal(raw, &p); err != nil {
		return Figures{}, err
	}
	// All six keys present — additionally the two identity strings must be
	// usable: not whitespace-only AND not carrying any control character
	// (round-4 whitespace + round-5 control-char findings; see decodeFiguresPatch
	// doc). This is NOT a value-check on money/population (0 is legitimate and
	// never tested): it applies only to the two string fields the engine
	// publisher guarantees non-empty and control-free (chromeDateString /
	// chromeRatingString), so it cannot reject a genuine delta and closes the
	// blank-date-segment residual.
	if isBlankIdentityString(p.Figures.Date) || isBlankIdentityString(p.Figures.Rating) {
		return Figures{}, errEmptyFigures()
	}
	return p.Figures, nil
}

// isBlankIdentityString reports whether a chrome.topbar identity string (date
// or rating) is unusable as top-bar content: empty, whitespace-only, or
// carrying any rune that does not RENDER as visible text — a control (Cc),
// format (Cf), or other non-printable character (unassigned codepoints,
// line/paragraph separators Zl/Zp), or a combining mark (Mn/Mc/Me, which ARE
// printable yet render zero-width over the preceding base). It is the union of
// three attacker findings — round-4 (whitespace-only passes a literal `==""`
// check), round-5 (NUL/SOH and other non-whitespace controls pass
// strings.TrimSpace, which trims only unicode.IsSpace), and round-6 (U+0301
// class combining marks pass a Cc/Cf-only rejection, decode cleanly, and
// render a BLANK date/rating segment — unicode.IsControl covers only Cc and
// Is(unicode.Cf) only format chars, so neither catches a printable combining
// mark). The predicate is therefore the attacker-recommended invariant
// "every rune renders as visible standalone text": unicode.IsPrint already
// excludes Cc, Cf, unassigned and Zl/Zp (it admits only letters, marks,
// numbers, punctuation, symbols and ASCII space), and the explicit Mn/Mc/Me
// rejection closes the one printable-but-invisible category IsPrint admits.
//
// BUG-324 round-7 (independent attacker finding): IsPrint still admits the
// printable-but-BLANK class — runes that are Print yet render nothing at all:
// U+2800 BRAILLE PATTERN BLANK (So) and the Unicode filler codepoints U+3164
// HANGUL FILLER, U+115F/U+1160 Hangul choseong/jungseong fillers and U+FFA0
// HALFWIDTH HANGUL FILLER (Lo/So, Print=true, zero glyph). A date/rating of
// nothing but these decodes cleanly and renders a BLANK segment — the same
// blank-segment class, one category deeper than round 6. The predicate now
// also rejects an explicit denylist of these known blank-but-printable runes,
// while still accepting genuinely visible non-ASCII (a localised month name
// like "Août"), which none of the blank runes resemble.
// A rejected rune anywhere in the string is rejected, not just an
// all-invisible string: the engine publisher emits only ASCII
// (chromeDateString = ASCII month-name+year, chromeRatingString =
// FormatInt+'/1000'), so the stricter test cannot over-block a genuine delta.
//
// blankIdentityRunes is the round-7 denylist: printable-but-BLANK runes that
// unicode.IsPrint admits yet render zero visible glyph (U+2800 BRAILLE PATTERN
// BLANK and the Unicode filler codepoints — HANGUL FILLER U+3164, Hangul
// choseong/jungseong fillers U+115F/U+1160, HALFWIDTH HANGUL FILLER U+FFA0).
// All are unambiguously blank despite passing IsPrint: the Hangul fillers
// U+3164/U+115F/U+1160/U+FFA0 are Unicode category Lo (letters) yet render no
// visible glyph, and U+2800 is a dedicated blank pattern — none carries content
// a real identity string could display, so denylisting them cannot over-block
// the ASCII-only engine publisher.
var blankIdentityRunes = map[rune]bool{
	0x2800: true,
	0x3164: true,
	0x115f: true,
	0x1160: true,
	0xffa0: true,
}

func isBlankIdentityString(s string) bool {
	if strings.TrimSpace(s) == "" {
		return true
	}
	for _, r := range s {
		if !unicode.IsPrint(r) || unicode.Is(unicode.Mn, r) ||
			unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Me, r) ||
			blankIdentityRunes[r] {
			return true
		}
	}
	return false
}

// ApplyFiguresPatch decodes raw as a "chrome.topbar" figures patch and
// replaces the top-bar figures with it (AC-1: each field updates when a new
// delta arrives). A malformed patch (invalid JSON, an unrecognised
// schemaVersion, or empty/partial figures content) is logged via a
// registry-sourced error (ErrMalformedPatch, MET-U953, GR#7) and dropped —
// the top bar keeps its last-known-good figures, and ApplyFiguresPatch never
// panics (mirrors ui.screen.map ApplyPatch's malformed-patch posture).
func (c *Chrome) ApplyFiguresPatch(raw json.RawMessage) {
	if err := c.checkNotCopied(map[string]any{"method": "ApplyFiguresPatch"}); err != nil {
		return
	}

	f, err := decodeFiguresPatch(raw)
	if err != nil {
		c.logMalformed(err)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkNotCopied(map[string]any{"method": "ApplyFiguresPatch"}); err != nil {
		return
	}
	c.figures = f
}

// logMalformed records a registry-sourced ErrMalformedPatch error (GR#7).
// It only reads c.correlationID (immutable after construction), so it is
// safe to call while mu is held or not. It still carries its own
// checkNotCopied guard (rather than relying on ApplyFiguresPatch having
// already validated the receiver): astgate's ratchet is syntactic, and a
// copied receiver must be rejected here too before the error is recorded.
func (c *Chrome) logMalformed(cause error) {
	if err := c.checkNotCopied(map[string]any{"method": "logMalformed"}); err != nil {
		return
	}
	_ = errs.New(ErrMalformedPatch, c.correlationID, map[string]any{
		"cause": cause.Error(),
	})
}
