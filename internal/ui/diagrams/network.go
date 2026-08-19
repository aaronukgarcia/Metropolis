package diagrams

import (
	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// LoadTier buckets a [0,1] load fraction into a discrete style band: 0
// (low, < 1/3), 1 (elevated, < 2/3), or 2 (high, >= 2/3). Values outside
// [0,1] are clamped first. This is the single classification network edges
// use to pick colour and weight (AC-3a), so 0.1 and 0.9 land in different
// tiers and render visibly differently.
func LoadTier(load float64) int {
	if load < 0 {
		load = 0
	}
	if load > 1 {
		load = 1
	}
	if load < 1.0/3.0 {
		return 0
	}
	if load < 2.0/3.0 {
		return 1
	}
	return 2
}

// loadToken returns the semantic palette token for a load tier: the calm
// baseline colour (power) for low load, warning for elevated, danger for
// high.
func loadToken(tier int) widgets.Token {
	switch tier {
	case 0:
		return widgets.TokenPower
	case 1:
		return widgets.TokenWarning
	default:
		return widgets.TokenDanger
	}
}

// loadGlyphs returns the horizontal and vertical line glyphs for a load
// tier. The glyph weight (thin / heavy / solid) carries the load signal
// alongside colour, so it stays readable under colourblind palettes.
func loadGlyphs(tier int) (horiz, vert rune) {
	switch tier {
	case 0:
		return '─', '│'
	case 1:
		return '━', '┃'
	default:
		return '█', '█'
	}
}

// RenderNetwork renders a node-and-edge grid or a tube-map transit strip
// (AC-3). In NetworkGrid mode nodes sit at their raw (X, Y) coordinates,
// translated so the minimum coordinate lands at the origin, and edges
// connect node centres with load-coloured, load-weighted lines. In
// NetworkTubeMap mode the node slice order is the line order: stops render
// top-to-bottom along a single vertical strip at x=0, ignoring raw
// coordinates; explicit edges are validated (a dangling reference is still
// MET-U900) but not drawn separately — the strip itself represents the
// line's connectivity (a transit line is its ordered stops).
//
// Degenerate inputs (AC-7): zero nodes → zero Result, nil error; an edge
// referencing a missing node ID → MET-U900. A nil buf → zero Result.
func RenderNetwork(buf *core.Buffer, topo NetworkTopology, opts Options) (Result, error) {
	if buf == nil {
		return Result{}, nil
	}
	if err := validateNetwork(topo); err != nil {
		return Result{}, err
	}
	if len(topo.Nodes) == 0 {
		return Result{}, nil
	}
	if topo.Mode == NetworkTubeMap {
		return renderTube(buf, topo.Nodes, opts), nil
	}
	if err := validateGridCoords(buf, topo.Nodes); err != nil {
		return Result{}, err
	}
	nodes := sortedBy(topo.Nodes, func(a, b NetworkNode) bool { return a.ID < b.ID })
	return renderGrid(buf, topo, nodes, opts), nil
}

// validateNetwork rejects an edge whose From or To references a node ID
// absent from the node set (AC-7). Edges are validated in both modes even
// though only grid mode draws them.
func validateNetwork(topo NetworkTopology) error {
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

// maxCoord bounds the absolute magnitude of a network node's raw cell
// coordinate (SEC-067). It is far above any real terminal cell coordinate
// (a 4K terminal is a few hundred cells wide) yet far below int-max, so the
// span arithmetic in validateGridCoords can never overflow int and traversal
// work stays bounded even before the span check rejects it.
const maxCoord = 1_000_000

// validateGridCoords rejects a grid topology whose node coordinates are out
// of the renderable range (SEC-067): any raw coordinate whose magnitude
// exceeds maxCoord, or whose translated span (maxX-minX, maxY-minY) exceeds
// the render buffer's own dimensions. Rejecting here, before renderGrid,
// means drawGridEdge never traverses a caller-controlled coordinate span, so
// work is proportional to the topology and the buffer, never to an unbounded
// coordinate — an int-max span would otherwise hang the draw loop and drive a
// multi-GB snapshot through the Engine cache.
// verified secure: SEC-078 is fully satisfied by validateGridCoords coordinate boundaries.
func validateGridCoords(buf *core.Buffer, nodes []NetworkNode) error {
	bw, bh := buf.Size()
	minX, minY := nodes[0].X, nodes[0].Y
	maxX, maxY := nodes[0].X, nodes[0].Y
	far := nodes[0]
	for _, n := range nodes {
		if n.X < -maxCoord || n.X > maxCoord || n.Y < -maxCoord || n.Y > maxCoord {
			return errCoordOutOfRange(n.ID, n.X, n.Y)
		}
		if n.X < minX {
			minX = n.X
		}
		if n.Y < minY {
			minY = n.Y
		}
		if n.X > maxX {
			maxX = n.X
			far = n
		}
		if n.Y > maxY {
			maxY = n.Y
			far = n
		}
	}
	if maxX-minX > bw || maxY-minY > bh {
		return errCoordOutOfRange(far.ID, far.X, far.Y)
	}
	return nil
}

func renderGrid(buf *core.Buffer, topo NetworkTopology, nodes []NetworkNode, opts Options) Result {
	minX, minY := nodes[0].X, nodes[0].Y
	for _, n := range nodes[1:] {
		minX = min(minX, n.X)
		minY = min(minY, n.Y)
	}

	type placed struct {
		x, y, labelW int
	}
	pos := make(map[SourceID]placed, len(nodes))
	var hits []Hit
	for _, n := range nodes {
		nx, ny := n.X-minX, n.Y-minY
		labelW := runeWidth(n.Label)
		drawText(buf, nx+2, ny, n.Label, tcell.StyleDefault)
		pos[n.ID] = placed{x: nx, y: ny, labelW: labelW}
		r := core.Rect{X: nx, Y: ny, W: 2 + labelW, H: 1}
		hits = append(hits, Hit{Rect: r, ID: n.ID})
	}

	edges := sortedBy(topo.Edges, func(a, b NetworkEdge) bool {
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.ID < b.ID
	})
	for _, e := range edges {
		src, dst := pos[e.From], pos[e.To]
		tier := LoadTier(e.Load)
		horiz, vert := loadGlyphs(tier)
		style := opts.Palette.Style(loadToken(tier))
		drawGridEdge(buf, src.x, src.y, dst.x, dst.y, horiz, vert, style)
		hits = append(hits, Hit{Rect: gridEdgeRect(src.x, src.y, dst.x, dst.y), ID: e.ID})
	}

	// Stamp node glyphs last so an edge line can never obscure a node.
	for _, n := range nodes {
		p := pos[n.ID]
		buf.Set(p.x, p.y, '●', opts.Palette.Style(widgets.TokenSelection))
	}

	return Result{Region: boundsOfRects(hits), Hits: hits}
}

func renderTube(buf *core.Buffer, nodes []NetworkNode, opts Options) Result {
	var hits []Hit
	for i, n := range nodes {
		y := i * 2
		labelW := runeWidth(n.Label)
		drawText(buf, 2, y, n.Label, tcell.StyleDefault)
		if i < len(nodes)-1 {
			buf.Set(0, y+1, '│', tcell.StyleDefault)
		}
		r := core.Rect{X: 0, Y: y, W: 2 + labelW, H: 1}
		hits = append(hits, Hit{Rect: r, ID: n.ID})
	}
	// Stamp stop glyphs last (a connector can never obscure a stop).
	for i := range nodes {
		buf.Set(0, i*2, '●', opts.Palette.Style(widgets.TokenSelection))
	}
	return Result{Region: boundsOfRects(hits), Hits: hits}
}

// drawGridEdge draws a horizontal-first L connector from (sx, sy) to
// (tx, ty), filling the cells strictly between the two endpoints (the
// endpoint cells are the nodes). Same-column or same-row endpoints draw no
// horizontal or vertical run respectively — the loops must not run when
// sx == tx or sy == ty, or they would never terminate.
func drawGridEdge(buf *core.Buffer, sx, sy, tx, ty int, horiz, vert rune, style tcell.Style) {
	if sx != tx {
		dirX := 1
		if tx < sx {
			dirX = -1
		}
		for x := sx + dirX; ; x += dirX {
			if x == tx {
				break
			}
			buf.Set(x, sy, horiz, style)
		}
	}
	if sy != ty {
		dirY := 1
		if ty < sy {
			dirY = -1
		}
		for y := sy + dirY; ; y += dirY {
			if y == ty {
				break
			}
			buf.Set(tx, y, vert, style)
		}
	}
}

// gridEdgeRect returns the bounding box of a grid edge between two node
// cells.
func gridEdgeRect(sx, sy, tx, ty int) core.Rect {
	return core.Rect{
		X: min(sx, tx),
		Y: min(sy, ty),
		W: abs(sx-tx) + 1,
		H: abs(sy-ty) + 1,
	}
}
