package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// FEAT-019's end-to-end proof: F7 — the Projections screen — is reachable
// (registered + F7-switchable) and renders REAL, non-empty content, not
// "no data" and not a field of spaces.
//
// Deliberately an end-to-end assertion through the same bootCore the
// shipped binary uses, for the same reason bug323_viewport_view_test.go and
// feat209_census_screen_test.go are: engine.projections could hold a real
// horizon and ui.screen.proj could draw it, each unit-tested green, while
// the registered "f7.projections" view joining them was missing — only a
// test that boots the real composition and looks at the rendered BUFFER can
// fail when that join is removed. It asserts on GLYPHS, never on "the
// subscription succeeded": the "horizon: N months" label is drawn only
// when the projections screen actually holds a patch carrying horizonMonths
// (the no-data branch draws the literal "no data" instead).
//
// Honest scope note: engine.projections is a curve-provider REGISTRY and no
// producer module that registers a curve provider is composed into simState
// yet, so the "f7.projections" patch carries the data-sourced horizon (72
// months) and no curves/crossings/rateOutlook — the screen therefore
// renders its header and no curve panes. The horizon label is the one
// real, end-to-end glyph available today, and it is what these tests pin;
// TestFEAT019_ProjectionsScreen_NoCurvesUntilProducersComposed pins the
// empty-curves gap itself so a future producer composition is a deliberate,
// observed change rather than a silent one.

// bootForProjectionsScreenTest boots a real composition and returns the
// wiring.
func bootForProjectionsScreenTest(t *testing.T) *skeletonWiring {
	t.Helper()
	reg := registry.NewRegistry()
	w, err := bootCore("feat019-"+t.Name(), reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	t.Cleanup(w.shutdown)
	if w.projectionsScreen == nil {
		t.Fatal("bootCore did not construct w.projectionsScreen (FEAT-019's F7 screen did not take)")
	}
	return w
}

// TestFEAT019_ProjectionsSubscription_IsAcceptedAndDelivers proves the
// engine side directly: bootCore primes "f7.projections" through
// primeScreenSubscription (which FAILS THE BOOT if the Subscribe is
// rejected or no Delta arrives), so a successful bootCore already proves
// acceptance + delivery — but only if the priming call is present, which
// this pins by observing the screen's own state: HorizonMonths must report
// the real data-sourced horizon (72), not an empty/placeholder value.
func TestFEAT019_ProjectionsSubscription_IsAcceptedAndDelivers(t *testing.T) {
	w := bootForProjectionsScreenTest(t)

	months, ok := w.projectionsScreen.HorizonMonths()
	if !ok {
		t.Fatal("projectionsScreen.HorizonMonths() reported ok=false after boot — the subscription was not primed/delivered; a registered-but-empty view is not a fix (FEAT-019)")
	}
	if months != 72 {
		t.Fatalf("projectionsScreen.HorizonMonths() = %d, want 72 (engine.projections' embedded horizon.json) — the published patch carried no real horizon", months)
	}
}

// TestFEAT019_ProjectionsScreen_RendersNonEmptyGlyphs is the rendered-glyph
// proof: switch to F7 and draw it into a buffer, then assert the flattened
// text carries the projections header's own data-bearing glyphs (the
// "horizon: 72 months" label, present only when a patch carrying
// horizonMonths flowed) rather than the "no data" placeholder or a field of
// spaces.
func TestFEAT019_ProjectionsScreen_RendersNonEmptyGlyphs(t *testing.T) {
	w := bootForProjectionsScreenTest(t)

	// F7 -> projections, through the real chrome-global routing path.
	routeKeyInput(w, keyMsg(tcell.KeyF7, 0))
	if got := w.screens.ActiveID(); got != screenIDProjections {
		t.Fatalf("ActiveID() after F7 = %q, want %q — F7 is not wired as a chrome global", got, screenIDProjections)
	}

	const width, height = 120, 30
	back := core.NewBuffer(width, height)
	w.screens.ActiveDraw()(back, &core.ViewModels{})
	text := bufferText(back)
	t.Logf("F7 at %dx%d through a real bootCore:\n%s", width, height, text)

	counts := countGlyphs(text)
	if counts[' '] == width*height {
		t.Fatalf("F7 rendered completely blank at %dx%d — a registered screen whose view publishes nothing renders as an empty field", width, height)
	}

	// The header title "F7 Projections" is drawn unconditionally; the
	// horizon label is the data-bearing half, drawn ONLY on the data path
	// (the no-data branch renders "F7 Projections — no data" instead).
	if !strings.Contains(text, "F7 Projections") {
		t.Fatalf("F7's rendered output lacks the projections header — the screen drew nothing recognisable:\n%s", text)
	}
	if !strings.Contains(text, "horizon: 72 months") {
		t.Fatalf("F7's rendered output lacks the horizon label \"horizon: 72 months\", which appears only when the view delivered a real horizonMonths (no data renders \"no data\") — the view is empty:\n%s", text)
	}
	if strings.Contains(text, "no data") {
		t.Fatalf("F7's rendered output contains \"no data\", which means the view never delivered a patch (the subscription succeeded but nothing flowed):\n%s", text)
	}
}

// TestFEAT019_ProjectionsScreen_NoCurvesUntilProducersComposed pins the
// honest data-source gap: no producer module that registers a curve
// provider is composed into simState yet, so the screen's curve list is
// empty after a real boot. This is NOT a placeholder to be silently
// deleted — when engine.capexport/education/social/spiral/policies are
// composed and start registering providers, this assertion is expected to
// fail, and that failure is the SIGNAL that the F7 curve surface just
// turned on (update it deliberately then, alongside the compose-side key
// list in projections_publish.go, never by loosening it now).
func TestFEAT019_ProjectionsScreen_NoCurvesUntilProducersComposed(t *testing.T) {
	w := bootForProjectionsScreenTest(t)

	curves, have := w.projectionsScreen.Curves()
	if !have {
		t.Fatal("projectionsScreen.Curves() reported have=false after boot — the patch did not deliver (FEAT-019)")
	}
	if len(curves) != 0 {
		t.Fatalf("projectionsScreen.Curves() = %d curves after boot, want 0 — no producer module that registers a curve provider is composed into simState yet; a non-empty list here means a producer was composed without this test being updated to pin the new surface", len(curves))
	}
}
