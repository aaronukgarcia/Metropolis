package data

import "fmt"

// This file is §22's own: the Development-Point unlock-tree catalogue
// (twelve per-category progression trees, each covering all thirteen
// §4 milestone tiers), loaded from data/unlock_trees.json (FEAT-029 /
// data.unlocktrees). See docs/design/unlock-trees-schema.md for the
// field reference and docs/planning/acceptance/data.unlocktrees.md for
// the acceptance criteria.
//
// The schema is structural rather than a flat list: each tree carries
// an id (the stable lowercase category slug, e.g. "roads") plus its
// display name, and its nodes are a per-category array keyed by "nodes"
// — NOT a flat "category" field on each node. That mirrors the file
// (the old skeleton's flat {"category": ...} shape was wrong against
// the committed data) and lets Validate enforce per-tree invariants
// (every tier covered, cross-tree prereq edges resolving) that a flat
// list could not express.

// milestoneTierMin / milestoneTierMax are §4's milestone-ladder bounds
// (1 = Wilderness .. 13 = Centopolis). They are the structural tier
// domain this file's trees and external_world.go's capacityByEra /
// availableFromTier values share — a fixed domain from the spec, not a
// hardcoded count of anything the data file decides (GR#15). The
// category COUNT (12) and node COUNT (183) are never hardcoded here:
// the former is derived from meta.categories, the latter is never a
// loader concern at all.
const (
	milestoneTierMin = 1
	milestoneTierMax = 13
)

// knownNodeKinds is the node "kind" enum: "unlock" is a real §10
// content node (carrying specRef/description/dpCost/prereqTier), "none"
// is an explicit no-op node standing in for a tier where §10 names no
// signature content for that category (ASM-481's no-op convention —
// see the file's meta.noopConvention).
var knownNodeKinds = map[string]bool{"unlock": true, "none": true}

// UnlockTrees is data/unlock_trees.json's top-level schema (§22 + §4):
// the twelve Development-Point progression trees plus the file's own
// provenance metadata (spec citations, the declared category list, and
// the placeholder / no-op / floor disclosures).
type UnlockTrees struct {
	// Comment carries the file's top-level "$comment" provenance note
	// (spec sections, the transcription rule, and the id-convention
	// statement).
	Comment string `json:"$comment,omitempty"`

	Version int `json:"version"`

	// Meta is the "meta" object: SpecCitations (the § sections this file
	// transcribes), Categories (the declared category display-name list,
	// the derivation source for Validate's category-count check — GR#15),
	// and the placeholder/no-op/floor disclosures.
	Meta UnlockTreesMeta `json:"meta"`

	// Trees is the twelve per-category progression trees, in the file's
	// declared category order.
	Trees []UnlockTree `json:"trees"`
}

// UnlockTreesMeta is the "meta" object: provenance and the declared
// category list. Categories is the data-derived source of the expected
// tree count — Validate compares len(Trees) against it rather than a
// hardcoded 12 (GR#15).
type UnlockTreesMeta struct {
	SpecCitations []string `json:"specCitations"`

	// Categories is the declared category display-name list ("Roads",
	// "Electricity", ...), in order. It is both the derivation source for
	// the expected tree count and the membership domain each tree's Name
	// must fall inside.
	Categories []string `json:"categories"`

	PlaceholderDisclosure string `json:"placeholderDisclosure,omitempty"`
	NoopConvention        string `json:"noopConvention,omitempty"`
	FloorRule             string `json:"floorRule,omitempty"`
}

// UnlockTree is one category's progression tree: its slug id (the
// stable lookup key data.catalogue's unlock.dp field may reference), its
// display name, and its nodes.
type UnlockTree struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	Nodes []UnlockNode `json:"nodes"`
}

// UnlockNode is one tree node: a §10 Service & Feature Inventory item
// (kind "unlock") or an explicit no-op placeholder for a tier the
// category has no signature content at (kind "none").
type UnlockNode struct {
	// ID is the stable lowercase slug (buildingIDPattern) — the globally
	// unique key prereqNodeIds and data.catalogue reference.
	ID   string `json:"id"`
	Name string `json:"name"`

	// Kind is "unlock" (real §10 content) or "none" (deliberate no-op,
	// ASM-481). Domain: knownNodeKinds.
	Kind string `json:"kind"`

	// Tier is the §4 milestone tier this node unlocks at (1..13).
	Tier int `json:"tier"`

	// SpecRef cites §10's (and, where §4 gates it, §4's) literal wording
	// — the file's "nothing invented" traceability anchor. Required for
	// kind "unlock", empty for kind "none".
	SpecRef string `json:"specRef,omitempty"`

	// Description is the node's §10-derived one-line description.
	// Required for kind "unlock", empty for kind "none".
	Description string `json:"description,omitempty"`

	// DPCost is the Development-Point cost (a placeholder v1 shape: the
	// node's own tier — see meta.placeholderDisclosure). Required and
	// positive for kind "unlock", zero for kind "none".
	DPCost int `json:"dpCost,omitempty"`

	// PrereqTier is the milestone tier the node's prerequisite is
	// satisfied at (placeholder v1 shape: the node's own tier). Required
	// and in-range for kind "unlock", zero for kind "none".
	PrereqTier int `json:"prereqTier,omitempty"`

	// Note documents a kind "none" node's absence rationale (ASM-481).
	// Required for kind "none", empty for kind "unlock".
	Note string `json:"note,omitempty"`

	// PrereqNodeIDs lists the node ids this node depends on — the edges
	// of the unlock DAG. May be cross-category (e.g. a gas peaker
	// requiring the gas network). Every entry must resolve to an existing
	// node id, and the graph must be acyclic.
	PrereqNodeIDs []string `json:"prereqNodeIds,omitempty"`
}

// unlockNodeRef locates a node (tree index, node index) so validation
// errors can name it deterministically.
type unlockNodeRef struct {
	tree int
	node int
}

// Validate implements Validator. It enforces, in file order (already
// deterministic — JSON arrays decode preserving order):
//
//   - a non-empty meta.categories list and a tree count that matches it
//     (the "12 categories" check, derived from data — GR#15), with a
//     name bijection between the declared categories and the trees;
//   - per-tree and per-node required fields (id/name/kind/tier, plus
//     kind-specific fields), with node ids globally unique;
//   - every tree covering all thirteen milestone tiers (each tier
//     present at least once);
//   - every prereqNodeIds edge resolving to an existing node id and the
//     resulting graph being acyclic.
func (t *UnlockTrees) Validate() error {
	if err := requireVersion(t.Version); err != nil {
		return err
	}

	if len(t.Meta.Categories) == 0 {
		return fieldErr("meta.categories", "required, must be non-empty")
	}
	if len(t.Trees) != len(t.Meta.Categories) {
		return fieldErr("trees",
			fmt.Sprintf("must have exactly %d trees (matching meta.categories), got %d",
				len(t.Meta.Categories), len(t.Trees)))
	}

	metaNames := make(map[string]bool, len(t.Meta.Categories))
	for _, name := range t.Meta.Categories {
		if name == "" {
			return fieldErr("meta.categories", "entries must be non-empty")
		}
		if metaNames[name] {
			return fieldErr("meta.categories", fmt.Sprintf("duplicate category name %q", name))
		}
		metaNames[name] = true
	}

	// seenTreeNames maps a tree's display name to the tree index that
	// first claimed it, for the duplicate-name message.
	seenTreeNames := make(map[string]int, len(t.Trees))
	// nodeIndex maps a node id to its (tree, node) location, proving
	// global uniqueness and later resolving prereqNodeIds edges.
	nodeIndex := make(map[string]unlockNodeRef)

	for ti := range t.Trees {
		tree := &t.Trees[ti]
		prefix := fmt.Sprintf("trees[%d]", ti)
		idPrefix := prefix
		if tree.ID != "" {
			idPrefix = fmt.Sprintf("%s(id=%s)", prefix, tree.ID)
		}

		if err := requireNonEmptyString(prefix+".id", tree.ID); err != nil {
			return err
		}
		if !buildingIDPattern.MatchString(tree.ID) {
			return fieldErr(idPrefix+".id", fmt.Sprintf("must match %s, got %q", buildingIDPattern.String(), tree.ID))
		}
		if err := requireNonEmptyString(idPrefix+".name", tree.Name); err != nil {
			return err
		}
		if !metaNames[tree.Name] {
			return fieldErr(idPrefix+".name", fmt.Sprintf("must match a meta.categories entry, got %q", tree.Name))
		}
		if first, dup := seenTreeNames[tree.Name]; dup {
			return fieldErr(idPrefix+".name", fmt.Sprintf("duplicate tree name (first seen at trees[%d])", first))
		}
		seenTreeNames[tree.Name] = ti

		if len(tree.Nodes) == 0 {
			return fieldErr(idPrefix+".nodes", "required, must be non-empty")
		}

		for ni := range tree.Nodes {
			n := &tree.Nodes[ni]
			nprefix := fmt.Sprintf("%s.nodes[%d]", idPrefix, ni)

			if err := requireNonEmptyString(nprefix+".id", n.ID); err != nil {
				return err
			}
			if !buildingIDPattern.MatchString(n.ID) {
				return fieldErr(nprefix+".id", fmt.Sprintf("must match %s, got %q", buildingIDPattern.String(), n.ID))
			}
			if first, dup := nodeIndex[n.ID]; dup {
				return fieldErr(nprefix+".id",
					fmt.Sprintf("duplicate node id (first seen at trees[%d].nodes[%d])", first.tree, first.node))
			}
			nodeIndex[n.ID] = unlockNodeRef{ti, ni}

			if err := requireNonEmptyString(nprefix+".name", n.Name); err != nil {
				return err
			}
			if !knownNodeKinds[n.Kind] {
				return fieldErr(nprefix+".kind", fmt.Sprintf("must be one of unlock/none, got %q", n.Kind))
			}
			if n.Tier < milestoneTierMin || n.Tier > milestoneTierMax {
				return fieldErr(nprefix+".tier",
					fmt.Sprintf("must be a milestone tier %d-%d, got %d", milestoneTierMin, milestoneTierMax, n.Tier))
			}

			switch n.Kind {
			case "unlock":
				if err := requireNonEmptyString(nprefix+".specRef", n.SpecRef); err != nil {
					return err
				}
				if err := requireNonEmptyString(nprefix+".description", n.Description); err != nil {
					return err
				}
				if n.DPCost <= 0 {
					return fieldErr(nprefix+".dpCost", fmt.Sprintf("must be positive, got %d", n.DPCost))
				}
				if n.PrereqTier < milestoneTierMin || n.PrereqTier > milestoneTierMax {
					return fieldErr(nprefix+".prereqTier",
						fmt.Sprintf("must be a milestone tier %d-%d, got %d", milestoneTierMin, milestoneTierMax, n.PrereqTier))
				}
			case "none":
				if err := requireNonEmptyString(nprefix+".note", n.Note); err != nil {
					return err
				}
			}
		}
	}

	// Every declared category is represented by a tree (the reverse half
	// of the name bijection above).
	for _, name := range t.Meta.Categories {
		if _, ok := seenTreeNames[name]; !ok {
			return fieldErr("trees", fmt.Sprintf("meta.categories entry %q has no matching tree", name))
		}
	}

	// Every tree covers all thirteen milestone tiers (at least one node
	// per tier — the real file carries multiple nodes at some tiers, so
	// "at least once", never "exactly once").
	for ti := range t.Trees {
		tree := &t.Trees[ti]
		covered := make(map[int]bool, milestoneTierMax)
		for ni := range tree.Nodes {
			covered[tree.Nodes[ni].Tier] = true
		}
		for tier := milestoneTierMin; tier <= milestoneTierMax; tier++ {
			if !covered[tier] {
				return fieldErr(
					fmt.Sprintf("trees[%d](id=%s).nodes", ti, tree.ID),
					fmt.Sprintf("must cover milestone tier %d (missing)", tier),
				)
			}
		}
	}

	return validatePrereqGraph(t, nodeIndex)
}

// validatePrereqGraph resolves every node's prereqNodeIds edges against
// the globally-unique node index and then checks the resulting directed
// graph for cycles. It runs after the per-node and per-tree structural
// checks, so every id it touches is already known valid.
func validatePrereqGraph(t *UnlockTrees, nodeIndex map[string]unlockNodeRef) error {
	// adj maps a prerequisite node id to the ids of the nodes that
	// depend on it (edge prereq -> dependent), built in file order so the
	// DFS below is deterministic.
	adj := make(map[string][]string)
	for ti := range t.Trees {
		tree := &t.Trees[ti]
		for ni := range tree.Nodes {
			n := &tree.Nodes[ni]
			for _, dep := range n.PrereqNodeIDs {
				if _, ok := nodeIndex[dep]; !ok {
					return fieldErr(
						fmt.Sprintf("trees[%d](id=%s).nodes[%d](id=%s).prereqNodeIds", ti, tree.ID, ni, n.ID),
						fmt.Sprintf("references unknown node %q", dep),
					)
				}
				adj[dep] = append(adj[dep], n.ID)
			}
		}
	}

	const (
		white = 0 // unseen
		grey  = 1 // on the current DFS path
		black = 2 // fully explored
	)
	color := make(map[string]int, len(nodeIndex))

	// dfs walks from id; owner is the node whose prereqNodeIds edge led
	// here (itself for a DFS root), so a detected back edge can be named.
	var dfs func(id, owner string) (string, bool)
	dfs = func(id, owner string) (string, bool) {
		switch color[id] {
		case grey:
			return owner, true
		case black:
			return "", false
		}
		color[id] = grey
		for _, next := range adj[id] {
			if cycleOwner, ok := dfs(next, id); ok {
				return cycleOwner, true
			}
		}
		color[id] = black
		return "", false
	}

	for ti := range t.Trees {
		tree := &t.Trees[ti]
		for ni := range tree.Nodes {
			id := tree.Nodes[ni].ID
			if owner, ok := dfs(id, id); ok {
				ref := nodeIndex[owner]
				ownerTree := t.Trees[ref.tree]
				return fieldErr(
					fmt.Sprintf("trees[%d](id=%s).nodes[%d](id=%s).prereqNodeIds", ref.tree, ownerTree.ID, ref.node, owner),
					"prereq graph is cyclic",
				)
			}
		}
	}

	return nil
}
