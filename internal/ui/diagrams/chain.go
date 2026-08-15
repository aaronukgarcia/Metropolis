package diagrams

import (
	"sort"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// Layout constants for chain diagrams.
const (
	nodeH      = 3 // box height: top border, label row, bottom border
	vGap       = 1 // blank rows between vertically stacked nodes
	arrowGap   = 2 // minimum horizontal room between columns for the arrowhead
	leftMargin = 2 // reserved left margin for cyclic/back-edge loops
	topMargin  = 1 // reserved top margin
)

// RenderChain lays out a production chain (boxes and arrows) into buf and
// returns the diagram's region plus one hit per node and edge (AC-2, AC-5).
//
// Layout (layered graph drawing, small n): nodes are assigned a rank by
// longest-path over the DAG; ranks form columns left-to-right; nodes
// within a rank stack top-to-bottom ordered by ID (the deterministic
// tie-break — AC-8). Every edge renders as an arrow annotated with its
// Figure, drawn verbatim.
//
// Cycle-break rule (documented): a cyclic input is detected (Kahn's
// algorithm leaves nodes unprocessed) and flattened to a single rank
// ordered by ID, with cycle-closing edges drawn as left-side loops. Every
// edge is always rendered, cycle or not (AC-2).
//
// Degenerate inputs (AC-7): zero nodes → zero Result, nil error; a node
// with no edges renders alone; an edge referencing a missing node ID →
// MET-U900 with no partial layout returned. A nil buf → zero Result.
func RenderChain(buf *core.Buffer, topo ChainTopology, opts Options) (Result, error) {
	if buf == nil {
		return Result{}, nil
	}
	if err := validateChain(topo); err != nil {
		return Result{}, err
	}
	nodes := sortedBy(topo.Nodes, func(a, b ChainNode) bool { return a.ID < b.ID })
	if len(nodes) == 0 {
		return Result{}, nil
	}
	edges := sortedBy(topo.Edges, func(a, b ChainEdge) bool {
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.ID < b.ID
	})
	idx := make(map[SourceID]int, len(nodes))
	for i, n := range nodes {
		idx[n.ID] = i
	}

	ranks := chainRanks(len(nodes), edges, idx)

	maxRank := 0
	for _, r := range ranks {
		if r > maxRank {
			maxRank = r
		}
	}
	byRank := make([][]int, maxRank+1)
	for i := range nodes {
		byRank[ranks[i]] = append(byRank[ranks[i]], i)
	}

	colWidth := make([]int, maxRank+1)
	for r := range byRank {
		for _, ni := range byRank[r] {
			if w := runeWidth(nodes[ni].Label) + 2; w > colWidth[r] {
				colWidth[r] = w
			}
		}
	}

	// Inter-column gap: room for the longest figure text on an edge leaving
	// that column, plus the arrowhead and a spacer.
	gap := make([]int, maxRank)
	for c := range gap {
		gap[c] = arrowGap
	}
	for _, e := range edges {
		rf := ranks[idx[e.From]]
		if rf < maxRank {
			if need := runeWidth(e.Figure) + 2; need > gap[rf] {
				gap[rf] = need
			}
		}
	}

	x := make([]int, maxRank+1)
	x[0] = leftMargin
	for c := 0; c < maxRank; c++ {
		x[c+1] = x[c] + colWidth[c] + gap[c]
	}

	// Node positions, centered within their column.
	nodeRect := make([]core.Rect, len(nodes))
	for r := 0; r <= maxRank; r++ {
		y := topMargin
		for _, ni := range byRank[r] {
			w := runeWidth(nodes[ni].Label) + 2
			x0 := x[r] + (colWidth[r]-w)/2
			nodeRect[ni] = core.Rect{X: x0, Y: y, W: w, H: nodeH}
			y += nodeH + vGap
		}
	}

	// Chain diagrams are monochrome: boxes, arrows, and figures all use the
	// terminal default style. The palette in opts is reserved for future
	// colour theming and is not consulted here (no spec colour exists for
	// freight/chemical chains).
	boxStyle := tcell.StyleDefault

	var hits []Hit
	for i, n := range nodes {
		widgets.Border(buf, nodeRect[i], widgets.Focused, "", boxStyle)
		drawLabel(buf, nodeRect[i], n.Label, boxStyle)
		hits = append(hits, Hit{Rect: nodeRect[i], ID: n.ID})
	}
	for _, e := range edges {
		src := nodeRect[idx[e.From]]
		dst := nodeRect[idx[e.To]]
		r := drawChainEdge(buf, src, dst, e.Figure, boxStyle)
		hits = append(hits, Hit{Rect: r, ID: e.ID})
	}

	return Result{Region: boundsOfRects(hits), Hits: hits}, nil
}

// validateChain rejects an edge whose From or To references a node ID
// absent from the node set (AC-7), naming the offending edge and the
// missing node ID.
func validateChain(topo ChainTopology) error {
	ids := make(map[SourceID]bool, len(topo.Nodes))
	for _, n := range topo.Nodes {
		ids[n.ID] = true
	}
	for _, e := range topo.Edges {
		if !ids[e.From] {
			return errMissingNode(e.ID, e.From)
		}
		if !ids[e.To] {
			return errMissingNode(e.ID, e.To)
		}
	}
	return nil
}

// chainRanks assigns each node a layer (rank) by longest-path over the DAG.
// Kahn's algorithm provides the topological order and detects cycles: a
// cyclic input (some nodes never reach zero in-degree) is flattened to a
// single rank — the documented cycle-break rule. The queue is re-sorted
// every iteration so the whole pass is deterministic (AC-8).
func chainRanks(n int, edges []ChainEdge, idx map[SourceID]int) []int {
	adj := make([][]int, n)
	indeg := make([]int, n)
	for _, e := range edges {
		from, to := idx[e.From], idx[e.To]
		adj[from] = append(adj[from], to)
		indeg[to]++
	}
	var queue []int
	for i := 0; i < n; i++ {
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	sort.Ints(queue)
	order := make([]int, 0, n)
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		order = append(order, u)
		for _, v := range adj[u] {
			indeg[v]--
			if indeg[v] == 0 {
				queue = append(queue, v)
			}
		}
		sort.Ints(queue)
	}
	ranks := make([]int, n)
	if len(order) != n {
		// Cyclic: flatten to a single rank (documented cycle-break rule).
		return ranks
	}
	for _, u := range order {
		for _, v := range adj[u] {
			if ranks[v] < ranks[u]+1 {
				ranks[v] = ranks[u] + 1
			}
		}
	}
	return ranks
}

// drawChainEdge draws one edge between two boxes and returns the hit rect
// covering the arrow (and its figure annotation). Forward edges (target to
// the right) route horizontally with a vertical elbow; back/cyclic edges
// (target at or left of the source) draw a left-side loop.
func drawChainEdge(buf *core.Buffer, src, dst core.Rect, figure string, style tcell.Style) core.Rect {
	sy := src.Y + nodeH/2
	ty := dst.Y + nodeH/2
	if dst.X > src.X {
		return drawForwardEdge(buf, src, dst, sy, ty, figure, style)
	}
	return drawBackEdge(buf, src, dst, sy, ty, style)
}

func drawForwardEdge(buf *core.Buffer, src, dst core.Rect, sy, ty int, figure string, style tcell.Style) core.Rect {
	col := src.X + src.W
	for _, r := range figure {
		buf.Set(col, sy, r, style)
		col++
	}
	hx := col
	tx := dst.X - 1
	for x := hx; x < tx; x++ {
		buf.Set(x, sy, '─', style)
	}
	if sy != ty {
		lo, hi := sy, ty
		if lo > hi {
			lo, hi = hi, lo
		}
		for y := lo; y <= hi; y++ {
			buf.Set(tx, y, '│', style)
		}
	}
	buf.Set(tx, ty, '→', style)
	return core.Rect{X: src.X + src.W, Y: min(sy, ty), W: tx - (src.X + src.W) + 1, H: abs(sy-ty) + 1}
}

func drawBackEdge(buf *core.Buffer, src, dst core.Rect, sy, ty int, style tcell.Style) core.Rect {
	lx := src.X - 1 // left-side loop column; src.X >= leftMargin = 2, so lx >= 1
	lo, hi := sy, ty
	if lo > hi {
		lo, hi = hi, lo
	}
	buf.Set(lx, sy, '─', style) // stub out of the source's left edge
	for y := lo; y <= hi; y++ {
		buf.Set(lx, y, '│', style)
	}
	buf.Set(lx, sy, '┤', style)      // junction: the stub turns down/up here
	buf.Set(dst.X-1, ty, '→', style) // arrowhead into the target's left edge
	return core.Rect{X: lx, Y: lo, W: dst.X - lx + 1, H: hi - lo + 1}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
