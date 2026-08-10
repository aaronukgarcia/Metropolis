package uitest

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// namedKeys is the closed, positively-stated set of "<Name>" specials
// ParseScript accepts (doc.go's grammar section) — every entry here maps
// directly to a tcell.Key constant. "Space" is handled separately in
// parseToken (it is a KeyRune carrying ' ', not its own tcell.Key
// constant).
var namedKeys = map[string]tcell.Key{
	"Esc":       tcell.KeyEsc,
	"Enter":     tcell.KeyEnter,
	"Tab":       tcell.KeyTab,
	"Backspace": tcell.KeyBackspace,
	"Left":      tcell.KeyLeft,
	"Right":     tcell.KeyRight,
	"Up":        tcell.KeyUp,
	"Down":      tcell.KeyDown,
	"Home":      tcell.KeyHome,
	"End":       tcell.KeyEnd,
	"PgUp":      tcell.KeyPgUp,
	"PgDn":      tcell.KeyPgDn,
	"Delete":    tcell.KeyDelete,
	"F1":        tcell.KeyF1,
	"F2":        tcell.KeyF2,
	"F3":        tcell.KeyF3,
	"F4":        tcell.KeyF4,
	"F5":        tcell.KeyF5,
	"F6":        tcell.KeyF6,
	"F7":        tcell.KeyF7,
	"F8":        tcell.KeyF8,
	"F9":        tcell.KeyF9,
	"F10":       tcell.KeyF10,
	"F11":       tcell.KeyF11,
	"F12":       tcell.KeyF12,
}

// ParseScript parses script (doc.go's DSL grammar) into a sequence of
// tcell.EventKey values, in order (AC-1, AC-2). A malformed or
// unrecognised token is a parse-time rejection (AC-2b, AC-7): the whole
// call fails with a registry-sourced MET-H100 error naming the offending
// token and its 1-based position, and no events are returned — a script
// with one bad token never produces a partial event sequence a caller
// could accidentally act on.
func ParseScript(script string) ([]*tcell.EventKey, error) {
	tokens := strings.Fields(script)
	events := make([]*tcell.EventKey, 0, len(tokens))
	for i, tok := range tokens {
		ev, err := parseToken(tok)
		if err != nil {
			return nil, errs.New(codeMalformedScriptToken, errs.NewCorrelationID(), map[string]any{
				"token":    tok,
				"position": i + 1,
				"script":   script,
				"cause":    err.Error(),
			})
		}
		events = append(events, ev)
	}
	return events, nil
}

// parseToken resolves one whitespace-delimited token per ParseScript's
// documented grammar. A plain Go error (not a registry error) — the
// caller (ParseScript) wraps it once, with the token's position, rather
// than every recursive/helper call minting its own correlation ID for
// what is ultimately one reported failure.
func parseToken(tok string) (*tcell.EventKey, error) {
	if strings.HasPrefix(tok, "<") {
		if !strings.HasSuffix(tok, ">") || len(tok) < 3 {
			return nil, fmt.Errorf("malformed named-key token %q: expected <Name> form", tok)
		}
		name := tok[1 : len(tok)-1]
		if name == "Space" {
			return tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone), nil
		}
		if k, ok := namedKeys[name]; ok {
			return tcell.NewEventKey(k, 0, tcell.ModNone), nil
		}
		return nil, fmt.Errorf("unknown named key %q — not in the documented <Name> set (doc.go)", name)
	}

	r, size := utf8.DecodeRuneInString(tok)
	if r == utf8.RuneError || size != len(tok) {
		return nil, fmt.Errorf("token %q is not a single valid rune and not a <Name> special", tok)
	}
	return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone), nil
}
