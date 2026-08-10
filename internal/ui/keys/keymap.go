package keys

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// keymapSchemaVersion is the only Version value LoadKeymap currently
// accepts. Bumping the schema is additive-by-new-version, same
// convention protocol/commands.go documents for the command vocabulary.
const keymapSchemaVersion = 1

// Keymap is the remappable keymap JSON profile's decoded form (AC-11,
// AC-19; schema documented in doc.go). Bindings maps ONE physical key
// token to a MNEMONIC PATH — a whitespace-separated list of tokens using
// the same grammar as a Register() path (e.g. "b r s"), NOT a single
// token. A one-segment value ("b" bound to "b") is the common,
// degenerate case, and is exactly what the shipped default profile uses
// throughout — but the general case is a full path, so a player can
// remap a COMMAND to a different physical key ("ctrl+p": "b r s"), not
// merely swap which physical key stands in for which single mnemonic
// letter. ApplyKeymap validates every target path against the live,
// already-Register()ed action set (AC-11b) — see its doc comment.
type Keymap struct {
	Version  int               `json:"version"`
	Bindings map[string]string `json:"bindings"`
}

// resolve returns the mnemonic PATH physicalTok is bound to, if any, as
// its individual tokens (already whitespace-split — see Keymap's doc
// comment). A one-segment path is the common case; a caller that only
// wants that case can check len(path) == 1.
func (km *Keymap) resolve(physicalTok string) ([]string, bool) {
	if km == nil {
		return nil, false
	}
	raw, ok := km.Bindings[physicalTok]
	if !ok {
		return nil, false
	}
	return strings.Fields(raw), true
}

// ParseKeymap decodes and validates raw keymap JSON against the
// documented schema (doc.go): well-formed JSON, the expected version,
// every physical key a single valid token, and every mnemonic target a
// non-empty whitespace-separated list of valid tokens. It does NOT check
// a target path against a live action registry — that is ApplyKeymap's
// job (AC-11b), since a Keymap can be parsed and inspected before any
// KeyGrammar exists to validate against.
func ParseKeymap(raw []byte) (*Keymap, error) {
	var km Keymap
	if err := json.Unmarshal(raw, &km); err != nil {
		return nil, err
	}
	if km.Version != keymapSchemaVersion {
		return nil, errUnsupportedKeymapVersion(km.Version)
	}
	for phys, target := range km.Bindings {
		if _, ok := ParseKeyToken(phys); !ok {
			return nil, errBadKeymapToken("binding key", phys)
		}
		path := strings.Fields(target)
		if len(path) == 0 {
			return nil, errBadKeymapToken("binding target", target)
		}
		for _, tok := range path {
			if _, ok := ParseKeyToken(tok); !ok {
				return nil, errBadKeymapToken("binding target segment", tok)
			}
		}
	}
	return &km, nil
}

// simpleError is a minimal plain error type shared by this package's
// small, purely-local parse-failure paths (keymap token/version
// validation here, palette argument-grammar validation in palette.go)
// that are always immediately wrapped by an errs.New/errs.Wrap call at
// their one call site — a registry code, not this string, is what a
// caller and GR#7 actually care about; this type exists only so those
// call sites have a non-nil `error` to check and wrap.
type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }

func errUnsupportedKeymapVersion(got int) error {
	return &simpleError{msg: fmt.Sprintf("keymap: unsupported schema version %d (want %d)", got, keymapSchemaVersion)}
}

func errBadKeymapToken(field, tok string) error {
	return &simpleError{msg: "keymap: " + field + " " + tok + " is not a valid key token"}
}

// LoadKeymapFile reads path, parses it (ParseKeymap), and applies it to g
// (ApplyKeymap) — the common case (AC-11). On any failure to read/parse,
// it logs MET-U302 and falls back to leaving g's existing keymap
// (typically none, i.e. identity substitution) untouched rather than
// leaving the grammar unusable (AC-13).
func LoadKeymapFile(path string, g *KeyGrammar) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		_ = errs.Wrap(codeKeymapMalformed, g.correlationID, err, map[string]any{"path": path, "cause": err.Error()})
		return err
	}
	km, err := ParseKeymap(raw)
	if err != nil {
		_ = errs.Wrap(codeKeymapMalformed, g.correlationID, err, map[string]any{"path": path, "cause": err.Error()})
		return err
	}
	g.ApplyKeymap(km) // per-entry rejections (MET-U303) are logged, not fatal to the load
	return nil
}

// KeymapEntryError describes one rejected binding from ApplyKeymap
// (AC-11b) — a well-formed keymap whose entry names a mnemonic PATH that
// does not resolve to an already-registered action.
type KeymapEntryError struct {
	PhysicalKey  string
	MnemonicPath string
}

// ApplyKeymap installs km as g's active physical-key-to-mnemonic-path
// substitution table (AC-11). Every binding's target is validated
// against the LIVE action registry (AC-11b, weakness pattern #4: a
// remapped key reaches a dispatch decision, so its allowed domain is
// stated positively and checked, not merely reachable): the target path
// must resolve to a COMPLETE registered action (Register()ed, terminal
// in the trie) or a registered global — never merely a valid PREFIX of
// some longer registered path. "b" alone validates only if "b" was
// itself Register()ed as an action; if only "b r s" was registered, a
// target of "b" is rejected (it is a prefix, not an action) even though
// "b r s" is fine. This is stricter than checking whether the target's
// first token is merely reachable — that weaker check cannot tell
// "b r zzz" (an invalid tail) from "b r s" (a real action), since both
// start with a valid "b" — and letting an invalid tail through is
// exactly the hole AC-11b exists to close.
//
// A rejected entry is logged individually (MET-U303) and OMITTED from
// what gets applied; every other binding in the same profile still
// loads (AC-11b: "that specific binding is rejected/reported while the
// rest of the profile still loads"). Returns the rejected entries, if
// any (nil if none) — never an error for a partially-valid profile,
// since a well-formed file with one bad entry is not the "malformed
// file" condition AC-13/MET-U302 covers.
func (g *KeyGrammar) ApplyKeymap(km *Keymap) []KeymapEntryError {
	if err := g.checkNotCopied(); err != nil {
		return nil
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	clean := &Keymap{Version: km.Version, Bindings: map[string]string{}}
	var rejected []KeymapEntryError
	for phys, target := range km.Bindings {
		path := strings.Fields(target)
		if g.actionExistsLocked(path) {
			clean.Bindings[phys] = target
			continue
		}
		rejected = append(rejected, KeymapEntryError{PhysicalKey: phys, MnemonicPath: target})
		_ = errs.New(codeKeymapUnknownAction, g.correlationID, map[string]any{
			"key": phys, "path": path,
		})
	}
	g.keymap = clean
	return rejected
}

// actionExistsLocked reports whether path names a currently-reachable,
// COMPLETE action: either a registered global (the single-segment
// special case — globals live in a separate registry from the leader
// trie, but are just as much an "already-registered action"), or a path
// that walks the leader trie all the way to a terminal (Register()ed)
// node. A path that only reaches a non-terminal (prefix) node returns
// false — that is the AC-11b distinction a first-token-only reachability
// check cannot make. Caller must hold mu.
func (g *KeyGrammar) actionExistsLocked(path []string) bool {
	if len(path) == 0 {
		return false
	}
	if len(path) == 1 {
		if _, ok := g.globals[path[0]]; ok {
			return true
		}
	}
	cur := g.root
	for _, tok := range path {
		child, ok := cur.children[tok]
		if !ok {
			return false
		}
		cur = child
	}
	return cur.action != nil
}

// resolveActionLocked walks path from root and returns the terminal
// registeredAction it names, if any (nil, false otherwise). Used by
// Feed's multi-segment shortcut dispatch (grammar.go) — by the time Feed
// calls this, ApplyKeymap has already validated every loaded binding via
// actionExistsLocked, so this should always succeed for a bound path;
// the false case is handled defensively rather than assumed unreachable.
// Caller must hold mu.
func (g *KeyGrammar) resolveActionLocked(path []string) (*registeredAction, bool) {
	cur := g.root
	for _, tok := range path {
		child, ok := cur.children[tok]
		if !ok {
			return nil, false
		}
		cur = child
	}
	if cur.action == nil {
		return nil, false
	}
	return cur.action, true
}
