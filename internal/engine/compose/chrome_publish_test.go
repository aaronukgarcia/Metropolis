package compose

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	uichrome "github.com/aaronukgarcia/Metropolis/internal/ui/screens/chrome"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// BUG-324's engine-side proof set: "chrome.topbar" is a REAL registered
// view whose first delta carries non-empty, live-state figures.
//
// The distinction this file exists to enforce: it is not enough that
// the Subscribe is accepted. An accepted subscription that publishes a
// zero-valued patch renders as a blank top bar, which is
// indistinguishable from the unregistered-view bug it replaces. So
// every assertion below is on CONTENT, and the end-to-end test decodes
// the delta through ui.screen.chrome's OWN decoder rather than a
// hand-rolled copy.

// TestChromeViewSubscriptionName_MatchesUIScreenConstant guards the two
// independently-maintained copies of "chrome.topbar" (this package's
// chromeViewSubscriptionName and ui/screens/chrome's ViewChrome) against
// drifting apart in VALUE — GR#20/SF-1 requires them independently
// maintained, not silently divergent. A divergence here is exactly the
// bug: the engine would register one name and the screen subscribe to
// another, and the bar would be permanently blank again.
func TestChromeViewSubscriptionName_MatchesUIScreenConstant(t *testing.T) {
	if chromeViewSubscriptionName != uichrome.ViewChrome {
		t.Fatalf("chromeViewSubscriptionName = %q, want %q (ui.screen.chrome's own ViewChrome)", chromeViewSubscriptionName, uichrome.ViewChrome)
	}
}

// TestChromeView_IsRegistered asserts "chrome.topbar" is in the fixed
// viewRegistrationOrder — the registration half of the fix.
func TestChromeView_IsRegistered(t *testing.T) {
	names := RegisteredViewNames()
	for _, n := range names {
		if n == chromeViewSubscriptionName {
			return
		}
	}
	t.Fatalf("RegisteredViewNames() = %v, does not contain %q — the top bar's view is unregistered, so chrome's Subscribe would be rejected and the bar would render permanently empty", names, chromeViewSubscriptionName)
}

// TestChromeView_EndToEnd_FirstDeltaCarriesRealFigures is the core
// proof. It subscribes to "chrome.topbar" against a REAL compose.Wire'd
// engine, decodes the first delta through ui.screen.chrome's own
// ApplyFiguresPatch (the production decoder — a hand-rolled decode here
// could not prove the schema versions agree), and asserts on the
// resulting figures' CONTENT.
func TestChromeView_EndToEnd_FirstDeltaCarriesRealFigures(t *testing.T) {
	_, transport, cancel := wireFinanceTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uichrome.ViewChrome)

	c := uichrome.NewChrome("bug324-compose-test", widgets.DefaultPalette, uichrome.Effects{})
	c.ApplyFiguresPatch(delta.Patch)
	fig := c.Figures()

	// A malformed/unversioned patch leaves chrome's last-known-good
	// figures in place — which for a fresh Chrome is the zero value. So
	// the zero Figures is precisely the "nothing got through" signal,
	// and each field is checked for real content rather than merely for
	// "the call did not error".
	if fig == (uichrome.Figures{}) {
		t.Fatalf("chrome.Figures() after applying the first chrome.topbar delta is the ZERO value — the patch did not decode (schema mismatch?) or the publisher sent nothing. raw patch: %s", string(delta.Patch))
	}
	if fig.Date == "" {
		t.Error("Figures.Date is empty — the bar would render a leading blank")
	}
	if !strings.HasPrefix(fig.Date, "Jan Y1") {
		t.Errorf("Figures.Date = %q, want a genesis date of the documented form \"Jan Y1\" (data/seasonal.json pins month index 0 = January; the year is an ordinal world year)", fig.Date)
	}
	if fig.Population <= 0 {
		t.Errorf("Figures.Population = %d, want the live seeded citizen count (>0) — a zero population is exactly the plausible-looking-zero this fix refuses to publish", fig.Population)
	}
	if fig.Money <= 0 {
		t.Errorf("Figures.Money = %d, want the live treasury in whole pounds (>0 at baseline: initialTreasury is %d micropounds)", fig.Money, initialTreasury)
	}
	if fig.Rating == "" || !strings.HasSuffix(fig.Rating, "/1000") {
		t.Errorf("Figures.Rating = %q, want engine.finance's real 0..1000 credit score rendered on its own declared scale", fig.Rating)
	}
	if fig.ClockCycle < 0 || fig.ClockCycle >= 30 {
		t.Errorf("Figures.ClockCycle = %d, want 0..29 (the 30 logistics day-ticks in a month)", fig.ClockCycle)
	}
}

// TestChromeView_PatchIsSchemaVersioned proves the published patch
// carries the schemaVersion chrome's decoder demands. Without it, a
// silently-unversioned patch would be dropped by ApplyFiguresPatch and
// the bar would stay blank while every "is it registered?" check
// passed.
func TestChromeView_PatchIsSchemaVersioned(t *testing.T) {
	_, transport, cancel := wireFinanceTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uichrome.ViewChrome)

	var probe struct {
		SchemaVersion int             `json:"schemaVersion"`
		Figures       json.RawMessage `json:"figures"`
	}
	if err := json.Unmarshal(delta.Patch, &probe); err != nil {
		t.Fatalf("chrome.topbar patch is not valid JSON: %v (%s)", err, string(delta.Patch))
	}
	if probe.SchemaVersion != chromeWireSchemaVersion {
		t.Errorf("patch schemaVersion = %d, want %d", probe.SchemaVersion, chromeWireSchemaVersion)
	}
	if len(probe.Figures) == 0 {
		t.Error("patch carries no figures object at all")
	}
}

// --- pure-function proofs for the three sourcing decisions ---

// TestChromeDateString_FollowsSeasonalMonthConvention pins the mapping
// data/seasonal.json documents (index 0 = January) and the ordinal
// world year, including the negative-index clamp.
func TestChromeDateString_FollowsSeasonalMonthConvention(t *testing.T) {
	cases := []struct {
		month int64
		want  string
	}{
		{0, "Jan Y1"},
		{7, "Aug Y1"},
		{11, "Dec Y1"},
		{12, "Jan Y2"},
		{25, "Feb Y3"},
		{-1, "Jan Y1"}, // clamped, never a negative modulo or an out-of-range index
	}
	for _, tc := range cases {
		if got := chromeDateString(tc.month); got != tc.want {
			t.Errorf("chromeDateString(%d) = %q, want %q", tc.month, got, tc.want)
		}
	}
}

// TestChromeSpeedFigure_PausedWins proves a paused clock reports speed
// 0 even though engine.core retains the last multiplier — a bar reading
// "speed 4" over a frozen simulation is the confident-wrong-number
// failure this item exists to prevent.
func TestChromeSpeedFigure_PausedWins(t *testing.T) {
	if got := chromeSpeedFigure(true, 4); got != 0 {
		t.Errorf("chromeSpeedFigure(paused=true, speed=4) = %d, want 0", got)
	}
	if got := chromeSpeedFigure(false, 4); got != 4 {
		t.Errorf("chromeSpeedFigure(paused=false, speed=4) = %d, want 4 (the real multiplier, not an ordinal)", got)
	}
}

// assertTreasuryMirror is the invariant setTreasury exists to keep:
// the publish mirror equals the simulation field. A divergence is not a
// crash and not a test error anywhere else in the suite — it is a WRONG
// NUMBER on the player's top bar, and nothing but this check can see it.
func assertTreasuryMirror(t *testing.T, st *simState, when string) {
	t.Helper()
	if got, want := st.treasuryPub.Load(), st.treasury; got != want {
		t.Fatalf("treasury mirror diverged %s: treasuryPub = %d, st.treasury = %d (difference %d micropounds). Some writer assigned st.treasury without going through setTreasury — the top bar is now publishing a stale figure while the simulation runs on the real one",
			when, got, want, want-got)
	}
}

// TestBUG324_TreasuryMirrorTracksEveryWriter_ThroughTheRealComposition is
// the independent round's blocking finding (D1) made mechanical.
//
// setTreasury being the ONLY writer of st.treasury was, before this test,
// enforced by nothing but a comment. An AST scan confirmed it held on the
// day it was written; the round's attack then changed ONE call site to
// assign st.treasury directly and the ENTIRE suite stayed green while the
// bar rendered "money 9" for a £10 treasury. That is the case that will
// actually happen — BUG-333 adds treasury writers imminently — and it is
// specifically NOT covered by deleting the mirror altogether (which loses
// the seed too, and shows a conspicuous "money 0").
//
// So this drives the real composition — real Wire, real command loop
// seam, real ticks across a month boundary so financeHook's wage/tax
// transfer runs, and a real Buy→Zone→Build→Demolish so the compensation
// path runs — and checks the invariant after each stage.
//
// The demolish is load-bearing: financeHook's wages and tax are equal by
// construction, so ticking alone returns the treasury to where it
// started and a NET assertion would be vacuous. The demolish moves the
// treasury for good, which is why the test also asserts it actually
// changed: an invariant that holds because nothing ever wrote proves
// nothing.
//
// Coverage boundary, recorded honestly: this catches any bypassing
// writer whose value differs from the mirror at rest — the reachable
// case, and the exact case the round attacked. It cannot catch a bypass
// that is compensated by a later setTreasury call within the same effect
// (financeHook's wage line is such a spot: bypass it and the tax line's
// setTreasury re-stores the correct total). That residue is a STATIC
// property, and the right instrument for it is an astgate rule banning
// `.treasury =` outside setTreasury — proposed as a follow-up under
// FEAT-214, not a reason to skip this test.
func TestBUG324_TreasuryMirrorTracksEveryWriter_ThroughTheRealComposition(t *testing.T) {
	cid := errs.NewCorrelationID()
	logisticsAPI, err := logistics.LoadDefault(cid)
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	// Same generous provisioning TestGameplay_DemolishCreditsCompensation
	// uses, so the build order completes inside the Tick loop below.
	if _, err := logisticsAPI.Provision(build.DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	e := core.NewEngine(core.WithWorldSeed(7), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CorrelationID: cid, Logistics: logisticsAPI})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	st := comp.state

	// (1) The seed. Wire must seed THROUGH setTreasury, or the bar reads
	// zero until the first write.
	assertTreasuryMirror(t, st, "immediately after Wire (the seed)")
	if st.treasuryPub.Load() != initialTreasury {
		t.Fatalf("treasuryPub after Wire = %d, want the seeded %d — the mirror was not primed by the seed", st.treasuryPub.Load(), initialTreasury)
	}

	// (2) Ticks across a month boundary, so financeHook's wage/tax
	// transfer actually runs (30 logistics day-ticks per month).
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("bug324-mirror-ticks"),
		Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: 35},
	}); !res.Accepted {
		t.Fatalf("AdvanceTicks rejected: %+v", res.Error)
	}
	assertTreasuryMirror(t, st, "after 35 ticks (financeHook's wage/tax transfer)")

	// (3) A real demolish, which moves the treasury permanently.
	cell := protocol.CellRef{X: 3, Y: 3}
	for _, step := range []struct {
		name string
		cmd  protocol.Command
	}{
		{"Buy", protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "bug324-mirror-buy", Kind: protocol.KindBuy, Payload: protocol.BuyPayload{Cell: cell}}},
		{"Zone", protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "bug324-mirror-zone", Kind: protocol.KindZone, Payload: protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"}}},
		{"Build", protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "bug324-mirror-build", Kind: protocol.KindBuild, Payload: protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"}}},
	} {
		if res := e.HandleCommand(step.cmd); !res.Accepted {
			t.Fatalf("%s rejected: %+v", step.name, res.Error)
		}
		assertTreasuryMirror(t, st, "after "+step.name)
	}

	tile := world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}
	local := world.CellLocal{Row: cell.Y, Col: cell.X}
	completed := false
	for i := int64(0); i < 300; i++ {
		if err := st.buildAPI.Tick(i); err != nil {
			t.Fatalf("buildAPI.Tick(%d): %v", i, err)
		}
		if _, ok := st.buildAPI.Structure(tile, local); ok {
			completed = true
			break
		}
	}
	if !completed {
		t.Fatal("build order never completed after 300 ticks — cannot exercise the demolish writer")
	}

	treasuryBeforeDemolish := st.treasury
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("bug324-mirror-demolish"),
		Kind: protocol.KindDemolish, Payload: protocol.DemolishPayload{Cell: cell},
	}); !res.Accepted {
		t.Fatalf("Demolish rejected: %+v", res.Error)
	}
	assertTreasuryMirror(t, st, "after the demolish compensation payout")

	// Non-vacuousness: the treasury must genuinely have moved, or the
	// equality above proves only that nothing wrote.
	if st.treasury == treasuryBeforeDemolish {
		t.Fatalf("treasury is unchanged across the demolish (%d) — this test would pass on a mirror that is never written at all", st.treasury)
	}

	// And the published FIGURE must follow: the whole point of the mirror
	// is what the player reads, so assert through the real publisher, not
	// just the field.
	raw, err := st.buildChromeTopBarPatch()
	if err != nil {
		t.Fatalf("buildChromeTopBarPatch: %v", err)
	}
	var patch chromeTopBarWirePatch
	if err := json.Unmarshal(raw, &patch); err != nil {
		t.Fatalf("unmarshal published patch: %v", err)
	}
	if want := st.treasury / int64(finance.MicropoundsPerPound); patch.Figures.Money != want {
		t.Fatalf("published Money = %d, want %d (st.treasury %d micropounds in whole pounds) — the bar is showing a figure the simulation does not have", patch.Figures.Money, want, st.treasury)
	}
}
