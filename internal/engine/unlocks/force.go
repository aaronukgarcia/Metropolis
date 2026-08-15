package unlocks

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// ForceTarget selects what ForceUnlock force-unlocks: a milestone tier,
// a tree node, or neither (invalid). Exactly one field should be set.
type ForceTarget struct {
	// Tier is the milestone tier to force-reach (1..13). 0 means "no
	// tier".
	Tier int
	// NodeID is the tree node to force-unlock. "" means "no node".
	NodeID string
}

// ForceUnlock is the debug-mode-only cheat path §4 names (its own
// "port testing pre-100k" example): it force-unlocks any milestone tier
// or tree node for testing (AC-11, M0-ENG §3).
//
// It is gated behind the injected debug authorizer (SetDebugGate): with
// no gate wired, or a gate that denies, the call returns ErrDebugRequired
// and mutates nothing — a cheat can never fire silently just because a
// caller forgot to check debug state first. On success it invokes the
// injected sticky-flag callback (SetDebugTouch), which the composition
// root wires to feat.debugmode's serialize.Header.DebugTouched write
// (sticky-forever per M0-ENG §3/§14).
//
// Force-reaching a tier advances the higher-water mark without granting
// the cash/DP/permit awards (this is a test cheat, not an earned
// crossing); force-unlocking a node adds it to the unlocked set,
// bypassing both its DP cost and its tier prerequisite.
func (u *UnlocksAPI) ForceUnlock(target ForceTarget, correlationID string) error {
	if err := u.checkNotCopied("ForceUnlock"); err != nil {
		return err
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	gate := u.debugGate
	if gate == nil {
		return errs.New(ErrDebugRequired, u.correlationID, map[string]any{
			"capability": "force-unlock",
		})
	}
	if err := gate(correlationID); err != nil {
		return err
	}

	if !validForceTarget(target, u) {
		return errs.New(ErrInvalidUnlockTarget, u.correlationID, map[string]any{
			"tier": target.Tier, "node": target.NodeID,
		})
	}

	// SEC-082: invoke the sticky-flag callback (and check its result)
	// BEFORE applying any unlock. A failing flag write must leave the
	// milestone/node state untouched, never "unlock applied but the
	// debug-touched flag didn't persist".
	if u.debugTouch != nil {
		if err := u.debugTouch(); err != nil {
			// The sticky-flag write failed — refuse before any unlock
			// mutation (M0-ENG §3).
			return err
		}
	}

	if target.Tier > 0 {
		if cur := int(u.tier.Load()); target.Tier > cur {
			u.tier.Store(int32(target.Tier))
		}
	}
	if target.NodeID != "" {
		u.unlockedNodes[target.NodeID] = true
	}

	u.debugTouched = true
	return nil
}

// validForceTarget reports whether target names exactly one of a valid
// milestone tier and a valid tree node (u.mu held, but only immutable
// indexes are read).
func validForceTarget(target ForceTarget, u *UnlocksAPI) bool {
	hasTier := target.Tier > 0
	hasNode := target.NodeID != ""
	if hasTier == hasNode {
		return false // neither, or both
	}
	if hasTier {
		_, ok := milestoneAt(target.Tier)
		return ok
	}
	// A force-unlock target must name a REAL "unlock" node, never a
	// kind:"none" no-op placeholder — the same rule SpendDevelopmentPoints
	// enforces (a no-op node has no content to unlock).
	n, ok := u.nodes[target.NodeID]
	return ok && n.Kind == "unlock"
}
