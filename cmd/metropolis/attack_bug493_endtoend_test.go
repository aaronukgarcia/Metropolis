package main

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// BUG-493 end-to-end wiring proof: BUG-490's rejected round found that the
// shipped fix's own unit test only ever called MapScreen.ApplyResult
// directly — never proving a real keypress on a real, wired composition
// reaches it at all (this project's recurring "built but not wired"
// class). This file drives the whole loop through the real engine:
//
//	routeKeyInput('b'/'y') -> mapGrammar action -> sendMapCommand
//	-> router.RegisterResultHandler -> transport.SendCommand
//	-> real engine (compose handleGameplay -> build/world API)
//	-> CommandResult on the results channel -> router drain goroutine
//	-> MapScreen.ApplyResult -> MapScreen.BuildNotice()
//
// and additionally proves BUG-493 item 3's dismiss key ('c') reaches
// MapScreen.DismissBuildNotice through the SAME real routing path.

func waitForBuildNotice(t *testing.T, w *skeletonWiring) string {
	t.Helper()
	for i := 0; i < 200; i++ {
		if s := w.mapScreen.BuildNotice(); s != "" {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	return ""
}

func pressMapKey(t *testing.T, w *skeletonWiring, r rune) {
	t.Helper()
	if got := w.screens.ActiveID(); got != screenIDMap {
		t.Fatalf("precondition: active screen = %q, want %q", got, screenIDMap)
	}
	routeKeyInput(w, keyMsg(tcell.KeyRune, r))
}

// TestAttackBug493_RejectedBuild_ReachesScreen_EndToEnd is reason #1:
// 'b' on a cell that was never bought. engine.build's requireOwned
// rejects; the notice must arrive on the screen without any test-side
// ApplyResult call.
func TestAttackBug493_RejectedBuild_ReachesScreen_EndToEnd(t *testing.T) {
	w := bootForScreenTest(t)
	w.mapScreen.SetViewportSize(40, 20)
	w.mapScreen.MoveCursor(11, 11) // never bought by this test

	if got := w.mapScreen.BuildNotice(); got != "" {
		t.Fatalf("BuildNotice() before any keypress = %q, want empty", got)
	}

	pressMapKey(t, w, 'b')

	notice := waitForBuildNotice(t, w)
	if notice == "" {
		t.Fatal("pressing 'b' on an UNOWNED cell produced no build notice within 2s — BUG-490's rejection feedback is not actually wired end to end")
	}
	assertNoRawToken(t, notice, "unowned-cell build rejection")
}

// TestAttackBug493_RejectedBuy_ReachesScreen_EndToEnd is reason #2, a
// structurally DIFFERENT rejection (engine.world's already-owned
// purchase refusal), proving the Display passthrough (and BUG-493 item
// 2's dedupe) is not accidentally correct for exactly one code. If the
// real engine's already-owned rejection carries the BUG-267-class
// doubled correlation, this also proves item 2's fix reaches the real
// path, not just the unit-level fixture in the map package's own tests.
func TestAttackBug493_RejectedBuy_ReachesScreen_EndToEnd(t *testing.T) {
	w := bootForScreenTest(t)
	w.mapScreen.SetViewportSize(40, 20)
	w.mapScreen.MoveCursor(3, 3)

	// First 'y' buys the tile (accepted). Second 'y' must be rejected:
	// the tile is already owned.
	pressMapKey(t, w, 'y')
	time.Sleep(150 * time.Millisecond)
	pressMapKey(t, w, 'y')

	notice := waitForBuildNotice(t, w)
	if notice == "" {
		t.Fatal("a SECOND 'y' on an already-owned tile produced no build notice within 2s")
	}
	assertNoRawToken(t, notice, "already-owned buy rejection")

	if count := strings.Count(notice, "(correlation:"); count > 1 {
		t.Fatalf("RED (BUG-267-class double-wrap, item 2): real engine notice has %d \"(correlation:\" occurrences, want at most 1: %q", count, notice)
	}
}

// TestAttackBug493_DismissKey_ClearsNotice_EndToEnd is BUG-493 item 3's
// wiring proof: 'c' reaches MapScreen.DismissBuildNotice through the same
// real routeKeyInput path the 'y'/'b' keys use, and does so WITHOUT
// waiting for any subsequent command result.
func TestAttackBug493_DismissKey_ClearsNotice_EndToEnd(t *testing.T) {
	w := bootForScreenTest(t)
	w.mapScreen.SetViewportSize(40, 20)
	w.mapScreen.MoveCursor(21, 21)

	pressMapKey(t, w, 'b') // rejected: unowned cell
	if waitForBuildNotice(t, w) == "" {
		t.Fatal("precondition: no rejection notice appeared")
	}

	pressMapKey(t, w, 'c')

	cleared := false
	for i := 0; i < 200; i++ {
		if w.mapScreen.BuildNotice() == "" {
			cleared = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cleared {
		t.Fatalf("RED: pressing 'c' did not clear the build notice within 2s (still %q) — the dismiss key is not wired end to end", w.mapScreen.BuildNotice())
	}
}

// TestAttackBug493_AcceptClearsStaleNotice_EndToEnd is the "success path
// clears the red line" attack: reject first, then perform an accepted
// command on the same screen and prove the stale rejection is gone.
func TestAttackBug493_AcceptClearsStaleNotice_EndToEnd(t *testing.T) {
	w := bootForScreenTest(t)
	w.mapScreen.SetViewportSize(40, 20)

	w.mapScreen.MoveCursor(23, 23)
	pressMapKey(t, w, 'b')
	if waitForBuildNotice(t, w) == "" {
		t.Fatal("precondition: no rejection notice appeared")
	}

	w.mapScreen.MoveCursor(24, 24)
	pressMapKey(t, w, 'y')

	cleared := false
	for i := 0; i < 200; i++ {
		if w.mapScreen.BuildNotice() == "" {
			cleared = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cleared {
		t.Fatalf("a subsequent ACCEPTED command did not clear the stale rejection notice (still %q)", w.mapScreen.BuildNotice())
	}
}

// TestAttackBug493_RapidFire_NoRaceNoWedge fires many build/dismiss keys
// back to back before any result can be observed. Nothing may panic,
// deadlock, or leave the screen wedged, and the final notice must be a
// real registry string (or empty), never a raw token.
func TestAttackBug493_RapidFire_NoRaceNoWedge(t *testing.T) {
	w := bootForScreenTest(t)
	w.mapScreen.SetViewportSize(40, 20)
	w.mapScreen.MoveCursor(31, 31)

	for i := 0; i < 50; i++ {
		routeKeyInput(w, keyMsg(tcell.KeyRune, 'b'))
		routeKeyInput(w, keyMsg(tcell.KeyRune, 'y'))
		routeKeyInput(w, keyMsg(tcell.KeyRune, 'c'))
	}
	time.Sleep(500 * time.Millisecond)

	if n := w.mapScreen.BuildNotice(); n != "" {
		assertNoRawToken(t, n, "rapid-fire final notice")
	}
}

var rawTokenRe = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

func assertNoRawToken(t *testing.T, s, what string) {
	t.Helper()
	if rawTokenRe.MatchString(s) {
		t.Fatalf("%s Display still contains an unsubstituted registry placeholder: %q (GR#7 / BUG-357 class)", what, s)
	}
	t.Logf("%s player-visible notice (%d chars): %q", what, len(s), s)
	if strings.TrimSpace(s) == "" {
		t.Fatalf("%s produced a blank Display — a rejection the player cannot read is the same as no feedback at all", what)
	}
}
