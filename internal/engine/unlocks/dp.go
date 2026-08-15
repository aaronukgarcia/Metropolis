package unlocks

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// SpendDevelopmentPoints unlocks one tree node by spending Development
// Points (AC-7). It requires:
//
//   - the node to exist in data/unlock_trees.json (ErrUnregisteredGate);
//   - the node to be a real "unlock" node (a "none" placeholder is not
//     spendable — ErrUnregisteredGate);
//   - the node to not already be unlocked (ErrNodeAlreadyUnlocked);
//   - sufficient unspent DP balance (ErrInsufficientDP); and
//   - the node's declared milestone-tier prerequisite to be reached
//     (ErrTierPrerequisite).
//
// On success it debits the DP balance and marks the node unlocked. It is
// callable only through this command — the unlocked set is never a
// directly-mutable field (AC-7).
func (u *UnlocksAPI) SpendDevelopmentPoints(nodeID, correlationID string) error {
	if err := u.checkNotCopied("SpendDevelopmentPoints"); err != nil {
		return err
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	n, ok := u.nodes[nodeID]
	if !ok {
		return errs.New(ErrUnregisteredGate, u.correlationID, map[string]any{
			"kind": "node", "ref": nodeID,
		})
	}
	if n.Kind != "unlock" {
		return errs.New(ErrUnregisteredGate, u.correlationID, map[string]any{
			"kind": "node", "ref": nodeID,
		})
	}
	if u.unlockedNodes[nodeID] {
		return errs.New(ErrNodeAlreadyUnlocked, u.correlationID, map[string]any{
			"node": nodeID,
		})
	}
	if current := int(u.tier.Load()); current < n.PrereqTier {
		return errs.New(ErrTierPrerequisite, u.correlationID, map[string]any{
			"node": nodeID, "tier": n.PrereqTier, "current": current,
		})
	}
	cost := int64(n.DPCost)
	if u.dp < cost {
		return errs.New(ErrInsufficientDP, u.correlationID, map[string]any{
			"node": nodeID, "cost": cost, "balance": u.dp,
		})
	}

	u.dp -= cost
	u.dpSpent += cost
	u.unlockedNodes[nodeID] = true
	return nil
}
