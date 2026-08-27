package main

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/router"
	mapscreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/map"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// BUG-359: the map staleness dot must track the map subscription's REAL
// feed staleness, not render permanently "fresh".
//
// The bug was a gate that could not fire (GR#17): FEAT-208 increment 2
// replaced ui.core's ViewsLoop with ui.router as this binary's transport
// consumer, and BUG-323 then removed mapDrawFunc's old vm.Stale read when
// map terrain moved onto router-bound ApplyDelta — leaving nothing calling
// MapScreen.SetStale at all. The staleness indicator (a silent-failure
// signal for a dead map feed) could therefore never light up.
//
// This test drives a REAL router with a REAL Seq gap and renders through
// the REAL mapDrawFunc(ms, func() bool { return r.SubscriptionStale(sub) })
// closure the shipped binary builds (bootCore), asserting the on-screen dot
// GLYPH changes with the router's staleness. It is deliberately an
// end-to-end assertion through the same wiring, not a check of the map's
// internal `stale` field: what the player sees is the dot, and the whole
// bug was that the dot was disconnected from the truth — a test on either
// side's internals in isolation could not fail on that disconnect.
//
// Glyphs mirror render.go's unexported staleGlyphOn/staleGlyphOff — asserted
// by literal rune here because they are the actual pixels drawn.
const (
	bug359StaleGlyphOn  = '●'
	bug359StaleGlyphOff = '○'
)

// bug359CountingReceiver forwards each routed Delta to the map screen and
// counts them, so the test can wait until the router has actually processed
// a sent Delta (and therefore updated its per-subscription staleness) before
// rendering — without racing the router's own goroutine.
type bug359CountingReceiver struct {
	ms *mapscreen.MapScreen
	n  atomic.Int64
}

func (c *bug359CountingReceiver) ApplyDelta(d protocol.Delta) {
	c.ms.ApplyDelta(d)
	c.n.Add(1)
}

func (c *bug359CountingReceiver) count() int64 { return c.n.Load() }

// bug359RenderDotGlyph builds the SAME DrawFunc bootCore builds for F1 —
// mapDrawFunc(ms, staleFn) with staleFn reading the router — draws it into a
// fresh buffer, and returns the rune at the staleness-dot cell (the top-right
// of the chrome-inset screen content rect, render.go's drawStalenessDot).
func bug359RenderDotGlyph(ms *mapscreen.MapScreen, r *router.Router, sub protocol.SubscriptionID) rune {
	draw := mapDrawFunc(ms, func() bool { return r.SubscriptionStale(sub) })
	back := core.NewBuffer(24, 10)
	draw(back, &core.ViewModels{})
	rect := screenContentRect(back)
	cell := back.Get(rect.X+rect.W-1, rect.Y)
	return cell.Rune
}

func bug359WaitForCount(t *testing.T, rcv *bug359CountingReceiver, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rcv.count() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("router never processed %d deltas (got %d)", want, rcv.count())
}

func TestBUG359_StalenessDotTracksRouterStaleness(t *testing.T) {
	tr := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	r := router.New(tr, router.WithCorrelationID("bug359-router"))
	ms := mapscreen.NewMapScreen("bug359-map", widgets.DefaultPalette)

	sub := protocol.SubscriptionID("bug359-f1")
	ms.BindSubscription(sub)
	rcv := &bug359CountingReceiver{ms: ms}
	r.BindSubscription(sub, rcv)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// The dot content is a pure function of router staleness; the patch
	// bytes are irrelevant to it, so any valid JSON serves (the router only
	// requires well-formed JSON before routing a Delta on).
	patch := json.RawMessage(`{}`)

	// 1) In-order first Delta (Seq 1): the feed is FRESH -> hollow dot.
	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 1, Seq: 1, Patch: patch})
	bug359WaitForCount(t, rcv, 1)
	if got := bug359RenderDotGlyph(ms, r, sub); got != bug359StaleGlyphOff {
		t.Fatalf("after in-order Seq 1: dot glyph = %q, want the FRESH glyph %q", got, bug359StaleGlyphOff)
	}

	// 2) Seq jumps 1 -> 5 (a gap of 3: three deltas the router never saw).
	// The feed is now STALE -> filled dot. This is the assertion the bug
	// made impossible: before the fix nothing called SetStale, so the dot
	// stayed the FRESH glyph here.
	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 2, Seq: 5, Patch: patch})
	bug359WaitForCount(t, rcv, 2)
	if got := bug359RenderDotGlyph(ms, r, sub); got != bug359StaleGlyphOn {
		t.Fatalf("after Seq gap 1->5: dot glyph = %q, want the STALE glyph %q -- the staleness dot is disconnected from the feed (BUG-359)", got, bug359StaleGlyphOn)
	}

	// 3) The next in-order Delta (Seq 6) clears staleness -> hollow again,
	// proving the dot tracks the LIVE value each frame, not a latch.
	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 3, Seq: 6, Patch: patch})
	bug359WaitForCount(t, rcv, 3)
	if got := bug359RenderDotGlyph(ms, r, sub); got != bug359StaleGlyphOff {
		t.Fatalf("after recovering in-order Seq 6: dot glyph = %q, want the FRESH glyph %q", got, bug359StaleGlyphOff)
	}
}

// TestBUG359_RouterSubscriptionStaleMirrorsSeqGap is the router-unit half:
// SubscriptionStale is false after in-order deltas, true after a Seq gap, and
// false again after the next in-order delta -- exactly ui.core ViewsLoop's
// `Stale[subID] = gap > 0` discipline it replaced. This isolates the new
// router surface from the render path so a regression points at the right layer.
func TestBUG359_RouterSubscriptionStaleMirrorsSeqGap(t *testing.T) {
	tr := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	r := router.New(tr, router.WithCorrelationID("bug359-router-unit"))
	sub := protocol.SubscriptionID("bug359-unit")
	rcv := &bug359CountingReceiver{ms: mapscreen.NewMapScreen("bug359-unit-map", widgets.DefaultPalette)}
	rcv.ms.BindSubscription(sub)
	r.BindSubscription(sub, rcv)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	patch := json.RawMessage(`{}`)

	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 1, Seq: 1, Patch: patch})
	bug359WaitForCount(t, rcv, 1)
	if r.SubscriptionStale(sub) {
		t.Fatal("SubscriptionStale = true after a single in-order delta, want false")
	}

	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 2, Seq: 5, Patch: patch})
	bug359WaitForCount(t, rcv, 2)
	if !r.SubscriptionStale(sub) {
		t.Fatal("SubscriptionStale = false after a Seq gap, want true")
	}

	tr.SendDelta(protocol.Delta{SubscriptionID: sub, Tick: 3, Seq: 6, Patch: patch})
	bug359WaitForCount(t, rcv, 3)
	if r.SubscriptionStale(sub) {
		t.Fatal("SubscriptionStale = true after the gap was followed by an in-order delta, want false")
	}

	// A subscription the router has never seen is not stale.
	if r.SubscriptionStale(protocol.SubscriptionID("never-seen")) {
		t.Fatal("SubscriptionStale = true for a never-seen subscription, want false")
	}
}
