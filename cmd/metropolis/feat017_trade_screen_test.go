package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/registry"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// FEAT-017's end-to-end proof: F5 — the Trade & Logistics screen — is
// reachable (registered + F5-switchable) and renders REAL, non-empty
// content on the live data path, not "no data" and not a field of spaces.
//
// Deliberately an end-to-end assertion through the same bootCore the
// shipped binary uses, for the same reason feat209_census_screen_test.go
// and feat022_districts_screen_test.go are: engine.freight could hold a
// real port (data/freight.json seeds 2 berths) and ui.screen.trade could
// draw its panel, each unit-tested green, while the registered "f5.trade"
// view joining them was missing — only a test that boots the real
// composition and looks at the rendered BUFFER can fail when that join is
// removed. It asserts on GLYPHS, never on "the subscription succeeded":
// "berths: 2" is drawn only when the port panel actually holds data (the
// no-data branch draws the literal "no data" in the header instead).
//
// Honest scope note: engine.freight's import/export ledgers are empty at a
// fresh boot (nothing imports/exports before gameplay, and no phase hook
// ticks freight), so the balance-of-trade surface renders its "imports"/
// "exports" sub-labels with no rows; contracts/junctions/safety are absent
// and render "unavailable". The PORT figures are the one real, non-empty
// glyph available today, and they are what these tests pin;
// TestFEAT017_TradeScreen_BalanceEmptyAtSeed pins the empty-balance gap
// itself so a future import/export path is a deliberate, observed change
// rather than a silent one.

// bootForTradeScreenTest boots a real composition and returns the wiring.
func bootForTradeScreenTest(t *testing.T) *skeletonWiring {
	t.Helper()
	reg := registry.NewRegistry()
	w, err := bootCore("feat017-"+t.Name(), reg)
	if err != nil {
		t.Fatalf("bootCore: %v", err)
	}
	t.Cleanup(w.shutdown)
	if w.tradeScreen == nil {
		t.Fatal("bootCore did not construct w.tradeScreen (FEAT-017's F5 screen did not take)")
	}
	if w.composition == nil {
		t.Fatal("bootCore did not retain the *compose.Composition — the F5 data-path test needs it to reach the composed freight module")
	}
	return w
}

// TestFEAT017_TradeSubscription_IsAcceptedAndDelivers proves the engine
// side directly: bootCore primes "f5.trade" through primeScreenSubscription
// (which FAILS THE BOOT if the Subscribe is rejected or no Delta arrives),
// so a successful bootCore already proves acceptance + delivery — but only
// if the priming call is present, which this pins by observing the screen's
// own state: HaveData must be true and the port panel must hold the real
// 2-berth figure, not an empty/placeholder value.
func TestFEAT017_TradeSubscription_IsAcceptedAndDelivers(t *testing.T) {
	w := bootForTradeScreenTest(t)

	if !w.tradeScreen.HaveData() {
		t.Fatal("tradeScreen.HaveData() = false after boot — the subscription was not primed/delivered; a registered-but-empty view is not a fix (FEAT-017)")
	}
	port, have := w.tradeScreen.Port()
	if !have {
		t.Fatal("tradeScreen.Port() reported have=false after boot — the published patch carried no port the screen recognised")
	}
	if !port.Unlocked || port.Berths != 2 {
		t.Fatalf("Port() = %+v, want unlocked=true berths=2 — the published view is empty/placeholder, not the real data/freight.json port", port)
	}
}

// TestFEAT017_TradeScreen_RendersNonEmptyGlyphs is the rendered-glyph
// proof: switch to F5 and draw it into a buffer, then assert the flattened
// text carries the trade screen's own data-bearing glyphs (the port panel's
// "berths: 2" / "crane rate: 60t/hr" / "customs throughput: 1500t/day"
// figures, present only when data flowed) rather than the "no data"
// placeholder or a field of spaces.
func TestFEAT017_TradeScreen_RendersNonEmptyGlyphs(t *testing.T) {
	w := bootForTradeScreenTest(t)

	// F5 -> trade, through the real chrome-global routing path.
	routeKeyInput(w, keyMsg(tcell.KeyF5, 0))
	if got := w.screens.ActiveID(); got != screenIDTrade {
		t.Fatalf("ActiveID() after F5 = %q, want %q — F5 is not wired as a chrome global", got, screenIDTrade)
	}

	const width, height = 120, 30
	back := core.NewBuffer(width, height)
	w.screens.ActiveDraw()(back, &core.ViewModels{})
	text := bufferText(back)
	t.Logf("F5 at %dx%d through a real bootCore:\n%s", width, height, text)

	counts := countGlyphs(text)
	if counts[' '] == width*height {
		t.Fatalf("F5 rendered completely blank at %dx%d — a registered screen whose view publishes nothing renders as an empty field", width, height)
	}

	// The header title "F5 Trade & Logistics" is drawn unconditionally, but
	// the no-data branch appends " — no data". Its absence plus real port
	// figures is the rendered-glyph equivalent of "data flowed".
	if !strings.Contains(text, "F5 Trade & Logistics") {
		t.Fatalf("F5's rendered output lacks the trade header — the screen drew nothing recognisable:\n%s", text)
	}
	if strings.Contains(text, "no data") {
		t.Fatalf("F5's rendered output contains \"no data\", which means the view never delivered a patch (the subscription succeeded but nothing flowed):\n%s", text)
	}

	// The port figures are drawn ONLY on the data path (port present +
	// unlocked). Their presence proves the join fed real figures, not zeros.
	for _, glyph := range []string{
		"berths: 2",
		"crane rate: 60t/hr",
		"operating hours: 16/day",
		"customs throughput: 1500t/day",
		"smuggling risk: 0%",
	} {
		if !strings.Contains(text, glyph) {
			t.Fatalf("F5's rendered output lacks the port figure %q, which appears only when the view delivered a real port (no data renders \"no data\") — the view is empty:\n%s", glyph, text)
		}
	}

	// The balance surface is present-but-empty at seed: its header and both
	// sub-labels render (data path), with no rows beneath.
	if !strings.Contains(text, "Balance of trade (§33)") {
		t.Fatalf("F5's rendered output lacks the balance header \"Balance of trade (§33)\" — the balance surface did not render:\n%s", text)
	}
}

// TestFEAT017_TradeScreen_BalanceEmptyAtSeed pins the honest data-source
// gap at the boot level: no import/export has occurred and no phase hook
// ticks freight, so the balance-of-trade surface is present-but-empty after
// a real boot (both ledgers' byCommodity empty, byArtery empty). This is
// NOT a placeholder to be silently deleted — when a gameplay path starts
// importing/exporting, this assertion is expected to fail, and that failure
// is the SIGNAL that the F5 balance surface just turned on.
func TestFEAT017_TradeScreen_BalanceEmptyAtSeed(t *testing.T) {
	w := bootForTradeScreenTest(t)

	balance, have := w.tradeScreen.Balance()
	if !have {
		t.Fatal("tradeScreen.Balance() reported have=false after boot — the balance surface was not delivered (FEAT-017)")
	}
	if len(balance.Imports.ByCommodity) != 0 || len(balance.Exports.ByCommodity) != 0 {
		t.Fatalf("balance after boot has imports=%d exports=%d byCommodity rows, want 0/0 (nothing imports/exports before gameplay)", len(balance.Imports.ByCommodity), len(balance.Exports.ByCommodity))
	}
	if len(balance.Imports.ByArtery) != 0 || len(balance.Exports.ByArtery) != 0 {
		t.Fatalf("balance after boot has non-empty byArtery rows — engine.freight's trade ledger has no per-artery rollup, so byArtery must stay empty")
	}

	// The three surfaces freight cannot back must report have=false (absent →
	// "unavailable"), never have=true-with-fabricated-rows.
	if _, have := w.tradeScreen.Contracts(); have {
		t.Fatal("tradeScreen.Contracts() reported have=true after boot, want false — freight exposes no contract surface")
	}
	if _, have := w.tradeScreen.Junctions(); have {
		t.Fatal("tradeScreen.Junctions() reported have=true after boot, want false — freight exposes no junction-queue surface")
	}
	if _, have := w.tradeScreen.Safety(); have {
		t.Fatal("tradeScreen.Safety() reported have=true after boot, want false — the §50 safety surface is BLOCKED on the BUG-058 registry edge")
	}
}
