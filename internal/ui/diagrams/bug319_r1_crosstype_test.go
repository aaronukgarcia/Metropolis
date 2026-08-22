package diagrams

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// ---------------------------------------------------------------------------
// BUG-319, independent round r1 finding: the three topology types share one
// Engine.cache, keyed only on layoutKey(topo.Hash(), buf, opts) — with NO
// type discriminator. ChainTopology{} and SankeyTopology{} (both fully
// empty) each serialise to the empty string and hash identically to
// 0xcbf29ce484222325 (hashString("")). NetworkTopology escaped only by the
// accident of its Mode byte always being written, even when zero-valued.
//
// The fix (types.go): every Hash() now begins with an unconditional, one-
// byte type tag (typeTagChain/typeTagNetwork/typeTagSankey) written before
// any record. These tests prove the fix at three levels: pairwise hash
// distinctness (empty AND adjacent degenerate shapes), and an end-to-end
// cache-entry-count proof that mirrors exactly how the round demonstrated
// the collision.
// ---------------------------------------------------------------------------

// r1-1: the exact case the round reported -- all three EMPTY topologies must
// hash differently from each other.
func TestBUG319r1_EmptyTopologiesHashDistinctlyAcrossTypes(t *testing.T) {
	chain := ChainTopology{}
	network := NetworkTopology{}
	sankey := SankeyTopology{}

	hc, hn, hs := chain.Hash(), network.Hash(), sankey.Hash()
	if hc == hs {
		t.Fatalf("BUG-319 r1: empty ChainTopology and empty SankeyTopology both hash to 0x%x", hc)
	}
	if hc == hn {
		t.Fatalf("BUG-319 r1: empty ChainTopology and empty NetworkTopology both hash to 0x%x", hc)
	}
	if hn == hs {
		t.Fatalf("BUG-319 r1: empty NetworkTopology and empty SankeyTopology both hash to 0x%x", hn)
	}
}

// r1-2: adjacent DEGENERATE-but-non-empty shapes -- one node/flow and
// nothing else, in each type. The round warned the empty case is the one
// that was found; the class of defect hides in the shapes right next to it,
// so this exercises those directly rather than trusting the empty-case fix
// to generalise.
func TestBUG319r1_SingleElementTopologiesHashDistinctlyAcrossTypes(t *testing.T) {
	chain := ChainTopology{Nodes: []ChainNode{{ID: "A", Label: "A"}}}
	network := NetworkTopology{Nodes: []NetworkNode{{ID: "A", Label: "A"}}}
	sankeySrc := SankeyTopology{Sources: []SankeyFlow{{ID: "A", Name: "A", Amount: 0}}}
	sankeySink := SankeyTopology{Sinks: []SankeyFlow{{ID: "A", Name: "A", Amount: 0}}}

	hashes := map[string]uint64{
		"chain":       chain.Hash(),
		"network":     network.Hash(),
		"sankey-src":  sankeySrc.Hash(),
		"sankey-sink": sankeySink.Hash(),
	}
	seen := map[uint64]string{}
	for name, h := range hashes {
		if other, ok := seen[h]; ok {
			t.Fatalf("BUG-319 r1: single-element %q and %q both hash to 0x%x", name, other, h)
		}
		seen[h] = name
	}
}

// r1-3: end-to-end, the way the round proved it -- Chain then Sankey then
// Network, same buffer, same (zero-value) Options, all EMPTY topologies,
// through the real Engine cache. Before the type-tag fix, the Sankey call
// hit the Chain entry (Sankey never ran RenderSankey) and Network was the
// only one to mint its own entry, for a total of 2. The fix requires THREE
// distinct entries -- one per type -- asserted on the entry count directly,
// not inferred from behaviour.
func TestBUG319r1_ChainSankeyNetworkMintThreeCacheEntriesForEmptyTopologies(t *testing.T) {
	e := NewEngine()
	buf := core.NewBuffer(80, 24)
	opts := Options{}

	if _, err := e.Chain(buf, ChainTopology{}, opts); err != nil {
		t.Fatalf("Chain: %v", err)
	}
	if _, err := e.Sankey(buf, SankeyTopology{}, opts); err != nil {
		t.Fatalf("Sankey: %v", err)
	}
	if _, err := e.Network(buf, NetworkTopology{}, opts); err != nil {
		t.Fatalf("Network: %v", err)
	}

	e.mu.Lock()
	n := len(e.cache)
	e.mu.Unlock()
	if n != 3 {
		t.Fatalf("BUG-319 r1: expected 3 cache entries (one per topology type) for the empty-topology "+
			"trio on the same buffer/options, got %d -- a shared entry means one type's cached Result "+
			"is being served to another type's caller", n)
	}
}

// r1-4: the original BUG-319 repro must still pass -- the NUL-forgery
// collision this fix must not reopen. Reproduces the exact scenario from
// TestSEC077_ChainHashNULRecordForgery with the documented collision hash
// noted for traceability (0x28cbac3a36640e2d was the pre-fix collision this
// project's standing rule requires re-confirming, not just re-testing via
// the new type tag).
func TestBUG319r1_OriginalNULForgeryRemainsFixed(t *testing.T) {
	withEdge := ChainTopology{
		Nodes: []ChainNode{{ID: "A", Label: "L"}},
		Edges: []ChainEdge{{ID: "e1", From: "A", To: "A", Figure: "f"}},
	}
	forged := ChainTopology{
		Nodes: []ChainNode{{ID: "A", Label: "L\x00e\x00e1\x00A\x00A\x00f"}},
	}
	if withEdge.Hash() == forged.Hash() {
		t.Fatalf("BUG-319: original NUL-record forgery has regressed -- withEdge and forged both "+
			"hash to 0x%x", withEdge.Hash())
	}
}
