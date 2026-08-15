package unlocks

import (
	"sort"
	"strconv"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Gate is the structured unlock requirement [UnlocksAPI.IsUnlocked] and
// [UnlocksAPI.CheckGate] resolve. It is the single shape behind every
// gate check — a milestone tier, a specific DP-tree node, a bare
// development-point flag, and an achievement boolean (AC-1, AC-10).
type Gate struct {
	// MilestoneTier is the §4 milestone tier required (1..13). 0 means
	// "no milestone gate".
	MilestoneTier int

	// NodeID is the data/unlock_trees.json node id that must have been
	// unlocked (via SpendDevelopmentPoints or ForceUnlock). Empty means
	// "no node gate".
	NodeID string

	// RequiresDP is the data.catalogue "developmentPoint" flag's
	// placeholder mapping: at least one Development Point must have been
	// spent anywhere in the trees. See the logged ASM for why a bare
	// boolean (which the catalogue carries, with no node id) is resolved
	// this way rather than to a specific node.
	RequiresDP bool

	// RequiresAchievement is the data.catalogue "achievement" flag:
	// this gate needs an achievement to have been met.
	RequiresAchievement bool

	// AchievementMet is whether that achievement is currently achieved.
	// Consumed as an injected boolean — achievement *detection* is out of
	// scope for this module (AC's Out of scope).
	AchievementMet bool
}

// GateForCatalogue adapts a data.catalogue (buildings.json) UnlockGate
// into a [Gate] this module can check (AC-10). achievementMet is the
// caller-supplied "is the achievement achieved" boolean for entries whose
// unlock gate carries an achievement flag. The catalogue's milestone is a
// "M1".."M13" string; a non-milestone conditional gate ("with sources",
// "first 100 deaths") has an empty milestone and is simply not resolved
// here — the owning event module answers those, not this gate check
// (AC's Out of scope).
func GateForCatalogue(unlock data.UnlockGate, achievementMet bool) Gate {
	return Gate{
		MilestoneTier:       milestoneTierFromString(unlock.Milestone),
		RequiresDP:          unlock.DevelopmentPoint,
		RequiresAchievement: unlock.Achievement,
		AchievementMet:      achievementMet,
	}
}

// milestoneTierFromString parses a data.catalogue milestone string ("M1"
// .. "M13") into its 1-based tier. Returns 0 for an empty or unparsable
// value (the catalogue's conditional-gate case, or data that foundation
// data's own validation already rejects as M1-M13 out of domain).
func milestoneTierFromString(m string) int {
	if m == "" || !strings.HasPrefix(m, "M") {
		return 0
	}
	n, err := strconv.Atoi(m[1:])
	if err != nil {
		return 0
	}
	return n
}

// MilestoneReached reports whether the given milestone tier has been
// reached. It is exactly engine.finance's MilestoneGate interface, so a
// *UnlocksAPI can be wired directly as the finance module's loan-facility
// gate — the "loan-facility uplift" §4 grants is this method turning true
// at a crossing (US-7, AC-4). Tier 0 (the "no milestone" sentinel) is
// never "reached" — a loan facility gated on tier 0 is unavailable.
func (u *UnlocksAPI) MilestoneReached(tier int) bool {
	if err := u.checkNotCopied("MilestoneReached"); err != nil {
		return false
	}
	// Lock-free on purpose (SEC-083): engine.finance.Borrow calls this
	// while HOLDING finance's own lock. Taking u.mu here would create a
	// finance.mu -> unlocks.mu edge that deadlocks against the
	// unlocks.mu -> finance.mu edge on the cash-award / Buy post path.
	// The tier is a monotonic atomic, so a lock-free read is race-safe.
	return tier >= 1 && tier <= int(u.tier.Load())
}

// IsUnlocked is the bool gate check (AC-1). It is the convenience form
// for callers whose gates are already data-validated (e.g. ui.screen.build
// consuming data.catalogue entries): it returns false for an unregistered
// node id or out-of-range tier rather than an error. Use [UnlocksAPI.CheckGate]
// when the difference between "unregistered" and "genuinely not yet
// unlocked" matters (AC-12).
func (u *UnlocksAPI) IsUnlocked(g Gate) bool {
	ok, _ := u.CheckGate(g)
	return ok
}

// CheckGate resolves g against the current state, returning whether the
// gate passes. It is the error-returning gate check AC-12 requires: an
// unregistered node id or an out-of-range milestone tier is returned as a
// registry-sourced ErrUnregisteredGate, never a silent false negative a
// caller could mistake for a genuine gate failure.
func (u *UnlocksAPI) CheckGate(g Gate) (bool, error) {
	if err := u.checkNotCopied("CheckGate"); err != nil {
		return false, err
	}

	if g.MilestoneTier < 0 || g.MilestoneTier > len(milestoneLadder) {
		return false, errs.New(ErrUnregisteredGate, u.correlationID, map[string]any{
			"kind": "milestone tier", "ref": g.MilestoneTier,
		})
	}
	if g.NodeID != "" {
		if _, ok := u.nodes[g.NodeID]; !ok {
			return false, errs.New(ErrUnregisteredGate, u.correlationID, map[string]any{
				"kind": "node", "ref": g.NodeID,
			})
		}
	}

	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.gatePassesLocked(g), nil
}

// gatePassesLocked is CheckGate's resolution without the lock (the caller
// holds u.mu). Inputs are already validated by CheckGate.
func (u *UnlocksAPI) gatePassesLocked(g Gate) bool {
	if g.MilestoneTier > 0 && int(u.tier.Load()) < g.MilestoneTier {
		return false
	}
	if g.NodeID != "" && !u.unlockedNodes[g.NodeID] {
		return false
	}
	if g.RequiresDP && u.dpSpent <= 0 {
		return false
	}
	if g.RequiresAchievement && !g.AchievementMet {
		return false
	}
	return true
}

// IsNodeUnlocked reports whether a tree node has been fully unlocked
// (Development Points spent on it, or force-unlocked). Returns false for
// an unregistered node id (the bool convenience form; see CheckNode for
// the error-returning form).
func (u *UnlocksAPI) IsNodeUnlocked(nodeID string) bool {
	ok, _ := u.CheckNodeUnlocked(nodeID)
	return ok
}

// CheckNodeUnlocked is the error-returning node-unlock query: an
// unregistered node id is ErrUnregisteredGate, otherwise whether the node
// is in the unlocked set.
func (u *UnlocksAPI) CheckNodeUnlocked(nodeID string) (bool, error) {
	if err := u.checkNotCopied("CheckNodeUnlocked"); err != nil {
		return false, err
	}
	if _, ok := u.nodes[nodeID]; !ok {
		return false, errs.New(ErrUnregisteredGate, u.correlationID, map[string]any{
			"kind": "node", "ref": nodeID,
		})
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.unlockedNodes[nodeID], nil
}

// SignatureUnlocks returns the sorted node ids of the §4 signature
// unlocks at the given milestone tier — every "unlock"-kind node whose
// milestone prerequisite is that tier (data/unlock_trees.json's
// prereqTier field). Sorted by id, so the result is deterministic
// (GR#21). Empty (not an error) for a tier with no DP-tree signature
// content; tiers outside 1..13 return nil. This is what AC-19's exit-gate
// test uses to derive "the tier's named signature unlocks" from data
// rather than hardcode them (GR#15).
func (u *UnlocksAPI) SignatureUnlocks(tier int) []string {
	if err := u.checkNotCopied("SignatureUnlocks"); err != nil {
		return nil
	}
	if tier < 1 || tier > len(milestoneLadder) {
		return nil
	}
	var out []string
	for id, n := range u.nodes {
		if n.Kind == "unlock" && n.PrereqTier == tier {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
