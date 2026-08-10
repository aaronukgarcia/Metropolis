package keys

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// ValidMarkID reports whether id is one of the 12 addressable map-mark
// slots, "a" through "l" (AC-7). This package does not itself bind
// "m a".."m l" / "' a".."' l" into the leader tree — the owning screen
// registers those as ordinary two-token mnemonic paths via Register,
// with an Action.Run that calls SetMark/GetMark below ("or equivalent
// 12-slot addressing" per the acceptance doc); this package's own
// contribution is the 12-slot validation and storage, enforced here so
// it cannot be bypassed regardless of how a caller wires the keys.
func ValidMarkID(id string) bool {
	return len(id) == 1 && id[0] >= 'a' && id[0] <= 'l'
}

// SetMark records loc under the 12-slot mark id (AC-7). A 13th distinct
// identifier (anything outside a-l) is rejected (MET-U305) rather than
// silently overwriting an existing slot or expanding the address space.
func (g *KeyGrammar) SetMark(id string, loc any) error {
	if err := g.checkNotCopied(); err != nil {
		return err
	}
	if !ValidMarkID(id) {
		return errs.New(codeInvalidMarkID, g.correlationID, map[string]any{"id": id})
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.marks[id] = loc
	return nil
}

// GetMark retrieves a previously recorded mark. ok is false both for an
// invalid id and for a valid, never-set slot — a caller that needs to
// distinguish "invalid identifier" from "nothing recorded yet" should
// call ValidMarkID first.
func (g *KeyGrammar) GetMark(id string) (any, bool) {
	if err := g.checkNotCopied(); err != nil {
		return nil, false
	}
	if !ValidMarkID(id) {
		return nil, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	loc, ok := g.marks[id]
	return loc, ok
}
