package diagrams

import (
	"encoding/binary"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// SourceID is the caller's own identifier for a topology element (a node,
// edge, or flow). This package passes SourceIDs through unchanged — it
// never invents, resolves, renumbers, or drops them (AC-5, US-4). The ID a
// caller puts on edge E7 is the ID attached to E7's rendered arrow; the
// value that goes in is the value that comes back, so ui.dash can map a
// cell region back to its source without this package ever being the place
// where source identity is lost.
type SourceID string

// Options carries the render-time knobs every layout function takes. The
// only knob today is the semantic colour palette (from ui.widgets); it is
// passed in by the caller, never fetched or sampled here (AC-1).
type Options struct {
	// Palette supplies the semantic colours (money/water/power/danger/
	// warning) a diagram uses. A zero Palette renders in the terminal's
	// default colour, which is safe but is not the shipped look.
	Palette widgets.Palette
}

// Hit pairs a cell-buffer region with the caller-supplied SourceID of the
// element rendered there. A Result carries one Hit per rendered element
// that has a caller identity (a chain node/edge, a network node/edge, a
// Sankey band), so a dashboard can map "this cell region" back to "this
// source" for drill-through (AC-5, US-4).
type Hit struct {
	Rect core.Rect
	ID   SourceID
}

// Result is the output of a layout call: the bounding region the diagram
// occupies within the buffer, plus the hit-test pairing for every rendered
// element that carries a caller identity.
//
// A zero Result (zero Region, nil Hits) is the documented "empty diagram"
// state for a degenerate-but-valid topology (AC-7): zero nodes, or a nil
// buffer. A non-nil error is only ever returned for malformed input (a
// dangling edge reference, MET-U600); a zero Result is never returned with
// a non-nil error, and no partial/corrupted layout is returned alongside
// an error.
type Result struct {
	Region core.Rect
	Hits   []Hit
}

// ChainNode is one box in a chain diagram (a production step, a freight or
// chemical node — §33/§50).
type ChainNode struct {
	ID    SourceID
	Label string
}

// ChainEdge is one directed arrow in a chain diagram, annotated with a live
// figure in the caller's unit (e.g. "12 t/day"). Figure is rendered
// verbatim; this package does not parse or reformat it (AC-2).
type ChainEdge struct {
	ID     SourceID
	From   SourceID
	To     SourceID
	Figure string
}

// ChainTopology is a production chain: boxes (nodes) and directed arrows
// (edges). It is a directed acyclic graph by default; a cycle is accepted
// and rendered under RenderChain's documented cycle-break rule.
type ChainTopology struct {
	Nodes []ChainNode
	Edges []ChainEdge
}

// NetworkMode selects how a network topology is drawn.
type NetworkMode int

const (
	// NetworkGrid draws each node at its raw (X, Y) cell coordinate,
	// translated so the minimum coordinate lands at the origin, and
	// connects them with load-coloured, load-weighted edges — the
	// power/water/chemical node-and-edge grid schematic (AC-3a).
	NetworkGrid NetworkMode = iota
	// NetworkTubeMap draws the nodes as stops along a single schematic
	// strip in slice order, ignoring raw coordinates and explicit edges —
	// the transit-line tube-map variant (AC-3b). The node slice order IS
	// the line order.
	NetworkTubeMap
)

// NetworkNode is one node in a network schematic. X/Y are its raw cell
// coordinates, used only in NetworkGrid mode; in NetworkTubeMap mode the
// slice order is the line order and X/Y are ignored (AC-3b).
type NetworkNode struct {
	ID    SourceID
	Label string
	X, Y  int
}

// NetworkEdge is one load-coloured edge in a network schematic. Load is a
// caller-supplied fraction in [0,1] driving the edge's colour and weight;
// values outside [0,1] are clamped at render time (AC-3a).
type NetworkEdge struct {
	ID   SourceID
	From SourceID
	To   SourceID
	Load float64
}

// NetworkTopology is a node-and-edge grid (power/water/chemical) or a
// transit line, selected by Mode.
type NetworkTopology struct {
	Nodes []NetworkNode
	Edges []NetworkEdge
	Mode  NetworkMode
}

// SankeyFlow is one (name, amount) money flow into or out of the §54
// Fiscal Circuit budget: a source feeding the budget, or a sink the budget
// feeds.
type SankeyFlow struct {
	ID     SourceID
	Name   string
	Amount float64
}

// SankeyTopology is the §54 Fiscal Circuit shape: a set of sources feeding
// one budget node feeding a set of sinks. Amounts are the caller's money
// figures; this package renders their proportions, never their semantics
// (AC-4).
type SankeyTopology struct {
	Sources []SankeyFlow
	Sinks   []SankeyFlow
}

// hashString hashes a canonical serialisation with FNV-1a. It is a cache
// key, not a digest: two topologies that serialise identically hash
// identically. A collision would only cause a stale cache hit (never a
// panic or a wrong SourceID, because a cached Result is still a well-formed
// Result), which is accepted per AC-6's "keyed on a hash" contract.
func hashString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// writeField appends s to b self-delimited by its own byte length, encoded
// as 8 fixed-width big-endian bytes immediately before the content
// (BUG-319). This replaces the old NUL-separated, variable-arity framing
// that let a Label containing NUL bytes impersonate a whole extra record:
// a length-prefixed field can never be mistaken for a record boundary or
// another field, because the prefix is derived from len(s) at write time —
// never copied from, or influenced by, the caller-controlled bytes of s
// itself. A fixed-width binary prefix (as opposed to a decimal-text length
// followed by a delimiter) needs no reservation of any byte value for
// "end of length", so no character is unavailable to field content either;
// every byte string, including empty strings and strings containing NUL,
// round-trips into a distinct position in the record stream.
//
// Hash is write-only (nothing ever decodes this stream back into fields),
// so what write-time self-delimiting must guarantee is narrower than a full
// wire format: two different (record-type, field...) sequences must never
// serialise to the same byte string. Fixing the field count per record tag
// (3 for "n", 5 for "e", etc. — see each Hash below) plus length-prefixing
// every field gives exactly that: the tag fixes how many length-prefixed
// fields follow, and each length prefix fixes exactly how many content
// bytes follow it before the next field or tag, so the whole stream
// decomposes into records and fields in only one way.
func writeField(b *strings.Builder, s string) {
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(s)))
	b.Write(lenBuf[:])
	b.WriteString(s)
}

// typeTagChain, typeTagNetwork, typeTagSankey are unconditional, one-byte
// discriminators written as the very first byte of each topology's Hash
// stream (BUG-319 round r1 finding). Without a type tag, ChainTopology{}
// and SankeyTopology{} — both fully empty — serialise to the identical
// empty string and collide at hashString("") = 0xcbf29ce484222325; a shared
// Engine.cache keyed only on this hash then serves one type's cached Result
// to the other. NetworkTopology escaped only by the accident of its Mode
// byte always being written (even when zero-valued), which is exactly the
// "protection resting on an accident" pattern this fixes deliberately for
// all three types.
//
// The chosen bytes ('C', 'N', 'S') cannot be confused with any record tag
// ('n', 'e', 's', 'k' — all lowercase) purely by case, but the stronger
// guarantee is positional: the type tag is always the first byte written,
// before any record, in every Hash implementation. A record tag can only
// ever appear at offset 1 or later, so no field content — however it is
// framed — can ever produce a byte that is mistaken for the type tag at
// offset 0.
const (
	typeTagChain   = 'C'
	typeTagNetwork = 'N'
	typeTagSankey  = 'S'
)

// Hash returns a deterministic cache key for the topology (AC-6, BUG-319).
// The serialisation follows slice order, so a reordered-but-semantically-
// equal input re-hashes — a harmless recompute, never a stale result. The
// stream begins with the unconditional typeTagChain byte (BUG-319 round r1
// — see typeTagChain doc), so ChainTopology can never collide with another
// topology type's Hash regardless of Nodes/Edges content. Every record
// after the type tag is a one-byte tag ('n' = node, 'e' = edge) followed by
// a FIXED number of length-prefixed fields (writeField), so no field's
// content — including an embedded NUL or a byte sequence resembling another
// field's framing — can forge a record boundary or smuggle in an extra
// record.
func (t ChainTopology) Hash() uint64 {
	var b strings.Builder
	b.WriteByte(typeTagChain)
	for _, n := range t.Nodes {
		b.WriteByte('n')
		writeField(&b, string(n.ID))
		writeField(&b, n.Label)
	}
	for _, e := range t.Edges {
		b.WriteByte('e')
		writeField(&b, string(e.ID))
		writeField(&b, string(e.From))
		writeField(&b, string(e.To))
		writeField(&b, e.Figure)
	}
	return hashString(b.String())
}

// Hash returns a deterministic cache key for the topology (AC-6, BUG-319).
// See ChainTopology.Hash for the tagged, length-prefixed, fixed-arity
// framing rationale. The stream begins with the unconditional typeTagNetwork
// byte (BUG-319 round r1), written before Mode — NetworkTopology previously
// escaped the cross-type collision only by the accident of Mode always
// being written (even when zero-valued); it now carries the same deliberate
// discriminator as ChainTopology and SankeyTopology rather than relying on
// that accident.
func (t NetworkTopology) Hash() uint64 {
	var b strings.Builder
	b.WriteByte(typeTagNetwork)
	b.WriteByte(byte(t.Mode))
	for _, n := range t.Nodes {
		b.WriteByte('n')
		writeField(&b, string(n.ID))
		writeField(&b, n.Label)
		writeField(&b, strconv.Itoa(n.X))
		writeField(&b, strconv.Itoa(n.Y))
	}
	for _, e := range t.Edges {
		b.WriteByte('e')
		writeField(&b, string(e.ID))
		writeField(&b, string(e.From))
		writeField(&b, string(e.To))
		writeField(&b, strconv.FormatFloat(e.Load, 'g', -1, 64))
	}
	return hashString(b.String())
}

// Hash returns a deterministic cache key for the topology (AC-6, BUG-319).
// See ChainTopology.Hash for the tagged, length-prefixed, fixed-arity
// framing rationale. Amount is formatted with strconv.FormatFloat before
// framing, which folds every NaN bit pattern (any payload, signalling or
// quiet) to the literal text "NaN" and every infinity to "+Inf"/"-Inf" —
// so two distinct NaN representations of "not a real number" still hash
// identically, matching the pre-existing (unaffected) float handling. The
// stream begins with the unconditional typeTagSankey byte (BUG-319 round
// r1 — see typeTagChain doc): SankeyTopology{} and ChainTopology{} were
// previously both the empty string and collided identically.
func (t SankeyTopology) Hash() uint64 {
	var b strings.Builder
	b.WriteByte(typeTagSankey)
	for _, f := range t.Sources {
		b.WriteByte('s')
		writeField(&b, string(f.ID))
		writeField(&b, f.Name)
		writeField(&b, strconv.FormatFloat(f.Amount, 'g', -1, 64))
	}
	for _, f := range t.Sinks {
		b.WriteByte('k')
		writeField(&b, string(f.ID))
		writeField(&b, f.Name)
		writeField(&b, strconv.FormatFloat(f.Amount, 'g', -1, 64))
	}
	return hashString(b.String())
}

// sortedBy returns a copy of s ordered by less. Every placement or draw
// order derived from caller input goes through this (or an explicit sort)
// before use — the mechanical guarantee behind AC-8's "no map-iteration-
// order-dependent placement".
func sortedBy[T any](s []T, less func(a, b T) bool) []T {
	out := append([]T(nil), s...)
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

// runeWidth returns the number of terminal cells s occupies (one cell per
// rune). Used for box and label sizing; a multi-byte rune still occupies a
// single cell.
func runeWidth(s string) int {
	return utf8.RuneCountInString(s)
}
