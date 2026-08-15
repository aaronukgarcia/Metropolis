package chrome

import (
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// RegisterBang registers the `!` global binding (UI-SPEC §3: "`!` jumps to
// top alert") via ui.keys' registered-action model — AC-4. This package
// does not implement its own key handler or dispatch; it asks ui.keys to
// wire a global whose Action.Run calls JumpToTop, so the SAME JumpTo
// mechanism AC-3's drill-through uses is what `!` fires, against whichever
// alert is ranked first at fire time — never a fixed or first-inserted
// alert (AC-5).
//
// The returned error is ui.keys' own rejection (e.g. `!` already registered
// as a global, or a reserved token) — Chrome simply propagates it; it does
// not second-guess the key grammar's ownership of the key vocabulary.
func (c *Chrome) RegisterBang(g *keys.KeyGrammar) error {
	if err := c.checkNotCopied(map[string]any{"method": "RegisterBang"}); err != nil {
		return err
	}
	return g.RegisterGlobal(keys.KeyRune('!'), keys.Action{
		Name: "Jump to top alert",
		Run:  func(keys.ActionArgs) { c.JumpToTop() },
	})
}
