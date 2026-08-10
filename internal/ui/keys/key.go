package keys

import (
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

// Key is this package's own, tcell-independent representation of one
// keystroke: either a single rune (the common case — letters, digits,
// punctuation) or one of the closed set of named specials below. Grammar
// state (Register/Feed) is keyed by [Key.Token], never by a raw
// tcell.Key/rune pair directly, so a caller driving this package from a
// non-terminal source (a test, a future non-tcell front end) never needs
// tcell at all — only FromTcellEvent does.
type Key struct {
	// Rune is valid when Special == "". ' ' (space) is carried as a rune,
	// not a named special.
	Rune rune
	// Special names a non-rune key from namedSpecials below; "" means
	// this Key is a plain rune.
	Special string
}

// namedSpecials is the closed, positively-stated set of named keys this
// package recognises — deliberately the SAME set
// internal/harness/uitest/keyscript.go documents for its scripted-key
// DSL, independently re-declared rather than imported. This is NOT
// weakness-pattern-#2's "value duplicated across a module boundary that
// needs a drift test": the two sets serve different consumers for
// different reasons (uitest's is a test-script grammar; this one is a
// live-input/keymap-token grammar) and GR#20 gives no import route
// between internal/ui/keys and internal/harness/uitest regardless (a
// harness importing the thing it tests is fine; the reverse — production
// code importing a test harness — is not). Kept identical by convention,
// not by shared code; a real front-end key this package doesn't yet name
// is folded into a future addition here, independently of uitest's own
// grammar evolving.
var namedSpecials = map[tcell.Key]string{
	tcell.KeyEsc:        "Esc",
	tcell.KeyEnter:      "Enter",
	tcell.KeyTab:        "Tab",
	tcell.KeyBackspace:  "Backspace",
	tcell.KeyBackspace2: "Backspace",
	tcell.KeyLeft:       "Left",
	tcell.KeyRight:      "Right",
	tcell.KeyUp:         "Up",
	tcell.KeyDown:       "Down",
	tcell.KeyHome:       "Home",
	tcell.KeyEnd:        "End",
	tcell.KeyPgUp:       "PgUp",
	tcell.KeyPgDn:       "PgDn",
	tcell.KeyDelete:     "Delete",
	tcell.KeyF1:         "F1",
	tcell.KeyF2:         "F2",
	tcell.KeyF3:         "F3",
	tcell.KeyF4:         "F4",
	tcell.KeyF5:         "F5",
	tcell.KeyF6:         "F6",
	tcell.KeyF7:         "F7",
	tcell.KeyF8:         "F8",
	tcell.KeyF9:         "F9",
	tcell.KeyF10:        "F10",
	tcell.KeyF11:        "F11",
	tcell.KeyF12:        "F12",
}

// namedByToken is namedSpecials inverted, for ParseKeyToken.
var namedByToken = func() map[string]tcell.Key {
	m := make(map[string]tcell.Key, len(namedSpecials))
	for k, name := range namedSpecials {
		if _, exists := m[name]; !exists {
			m[name] = k
		}
	}
	return m
}()

// KeyEsc/KeyEnter etc. are the package-level Key values for every named
// special, convenient for tests and for a caller wiring RegisterGlobal
// without going through the tcell layer at all.
var (
	KeyEsc       = Key{Special: "Esc"}
	KeyEnter     = Key{Special: "Enter"}
	KeyTab       = Key{Special: "Tab"}
	KeyBackspace = Key{Special: "Backspace"}
	KeyLeft      = Key{Special: "Left"}
	KeyRight     = Key{Special: "Right"}
	KeyUp        = Key{Special: "Up"}
	KeyDown      = Key{Special: "Down"}
	KeyHome      = Key{Special: "Home"}
	KeyEnd       = Key{Special: "End"}
	KeyPgUp      = Key{Special: "PgUp"}
	KeyPgDn      = Key{Special: "PgDn"}
	KeyDelete    = Key{Special: "Delete"}
)

// KeyRune constructs a plain-rune Key — the common case for a leader
// mnemonic token ("b", "5", etc).
func KeyRune(r rune) Key { return Key{Rune: r} }

// FromTcellEvent translates a real *tcell.EventKey into a Key (AC-20's
// integration seam — this is the ONE conversion point between tcell and
// this package's own grammar state). A tcell.KeyRune event becomes a
// plain-rune Key; a recognised named key becomes its Special; anything
// else (an unmapped function/control key) becomes a Key whose Special is
// the literal tcell.Key name-ish fallback "Key<N>", so Feed still gets a
// stable, comparable token rather than silently dropping the event —
// such a token simply never matches any registered path or keymap
// binding unless one is deliberately registered for it.
func FromTcellEvent(ev *tcell.EventKey) Key {
	if ev == nil {
		return Key{}
	}
	if ev.Key() == tcell.KeyRune {
		return Key{Rune: ev.Rune()}
	}
	if name, ok := namedSpecials[ev.Key()]; ok {
		return Key{Special: name}
	}
	return Key{Special: unknownSpecialToken(ev.Key())}
}

func unknownSpecialToken(k tcell.Key) string {
	return "Key" + itoa(int(k))
}

// itoa is a tiny local decimal formatter so this file doesn't need
// strconv just for unknownSpecialToken's rare fallback path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Token returns Key's canonical string form: a bare rune ("b", "5") or a
// "<Name>" bracketed special ("<Esc>"). This is the string this package
// uses everywhere a mnemonic path token, a keymap binding key, or a
// Continuation's key label is needed — one representation, so a path
// registered as []string{"b","r","s"} and a Key{Rune:'b'} fed later
// compare equal via this method, never via ad hoc formatting at each call
// site.
func (k Key) Token() string {
	if k.Special != "" {
		return "<" + k.Special + ">"
	}
	return string(k.Rune)
}

// ParseKeyToken is Token's inverse: turns a mnemonic-path/keymap token
// string back into a Key. Used by Register (so callers may pass plain
// strings, e.g. []string{"b","r","s"}) and by keymap.go when loading a
// JSON profile. Mirrors internal/harness/uitest's parseToken grammar
// (see namedSpecials' doc comment for why that is a deliberate,
// independent re-declaration rather than an import).
func ParseKeyToken(tok string) (Key, bool) {
	if strings.HasPrefix(tok, "<") && strings.HasSuffix(tok, ">") && len(tok) >= 3 {
		name := tok[1 : len(tok)-1]
		if name == "Space" {
			return Key{Rune: ' '}, true
		}
		if _, ok := namedByToken[name]; ok {
			return Key{Special: name}, true
		}
		return Key{}, false
	}
	r, size := utf8.DecodeRuneInString(tok)
	if r == utf8.RuneError || size != len(tok) {
		return Key{}, false
	}
	return Key{Rune: r}, true
}

// IsDigit reports whether k is a plain rune '0'-'9' — the input AC-5's
// count-prefix accumulation recognises.
func (k Key) IsDigit() bool {
	return k.Special == "" && k.Rune >= '0' && k.Rune <= '9'
}

// Digit returns k's numeric value; only meaningful when IsDigit is true.
func (k Key) Digit() int { return int(k.Rune - '0') }
