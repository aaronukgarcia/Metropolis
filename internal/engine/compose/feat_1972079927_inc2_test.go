package compose

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/logistics"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079927 inc2 (firms-pay-construction, Aaron's 2026-08-31 ruling):
// composition-root integration tests for the builders'-merchant
// auto-placement trigger and the NET-90 (COMMERCIAL_PAYMENT_TERM_TICKS)
// construction-settlement cadence, driven entirely through the real
// gameplay-command seam (e.HandleCommand) and the real daily buildHook —
// never a direct call into moneycirc_inc2.go's unexported methods from
// outside the package's own test harness.

// wireInc2TestEngine builds a real composed engine with a generously
// provisioned logistics stock (so a build order's materials draw
// completes in a single day-tick, giving this file's tests one clean,
// countable delta rather than a smear across many ticks) and purchases
// the tile under buyCell so every zone/build command against a cell in
// that same tile is pre-authorised (AC-3's ownership gate).
func wireInc2TestEngine(t *testing.T, seed uint64, buyCell protocol.CellRef) (*core.Engine, *Composition) {
	t.Helper()
	cid := "inc2-test"
	logisticsAPI, err := logistics.LoadDefault(cid)
	if err != nil {
		t.Fatalf("logistics.LoadDefault: %v", err)
	}
	if _, err := logisticsAPI.Provision(build.DefaultDistrict, market.ConstructionMaterials, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("Provision: %v", err)
	}

	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{CorrelationID: cid, Logistics: logisticsAPI})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("inc2-buy"),
		Kind: protocol.KindBuy, Payload: protocol.BuyPayload{Cell: buyCell},
	}); !res.Accepted {
		t.Fatalf("Buy rejected: %+v", res.Error)
	}
	return e, comp
}

func zoneCell(t *testing.T, e *core.Engine, corr string, cell protocol.CellRef, zoneType string) {
	t.Helper()
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID(corr),
		Kind: protocol.KindZone, Payload: protocol.ZonePayload{Cell: cell, ZoneType: zoneType},
	}); !res.Accepted {
		t.Fatalf("Zone(%s) rejected: %+v", zoneType, res.Error)
	}
}

// TestFEAT1972079927_Inc2_MerchantAutoPlacesOnIndustryAndFarms proves the
// deterministic, state-derived auto-placement trigger: no merchant exists
// until BOTH an industry-type zone and a farming zone are laid down (in
// either order component alone), it fires exactly once, and it is a real
// firms.FirmsAPI registration (queryable via Firm/Firms), not a bare flag.
//
// PROOF THIS CAN FAIL: temporarily deleting the
// maybeAutoPlaceBuildersMerchant call from handleGameplay's KindZone
// branch pins buildersMerchantFirmID at 0 forever and this test's
// "merchant placed" assertion fails — verified by hand during development
// (see this file's git history), then reverted.
func TestFEAT1972079927_Inc2_MerchantAutoPlacesOnIndustryAndFarms(t *testing.T) {
	buyCell := protocol.CellRef{X: 3, Y: 3}
	e, comp := wireInc2TestEngine(t, 501, buyCell)

	if comp.state.buildersMerchantFirmID != 0 {
		t.Fatalf("buildersMerchantFirmID = %d before any zoning, want 0", comp.state.buildersMerchantFirmID)
	}

	// Farming ALONE: no merchant yet (no industry zone present).
	zoneCell(t, e, "inc2-zone-farming", protocol.CellRef{X: 4, Y: 4}, "farming")
	if comp.state.buildersMerchantFirmID != 0 {
		t.Fatalf("buildersMerchantFirmID = %d after farming-only zoning, want 0 (no industry zone yet)", comp.state.buildersMerchantFirmID)
	}

	// Now the industry half completes the grouping — the merchant places.
	zoneCell(t, e, "inc2-zone-manufacturing", protocol.CellRef{X: 5, Y: 5}, "manufacturing")
	firmID := comp.state.buildersMerchantFirmID
	if firmID == 0 {
		t.Fatal("buildersMerchantFirmID still 0 after farming+manufacturing zoning — auto-placement trigger did not fire")
	}
	firm, err := comp.state.firms.Firm(firmID)
	if err != nil {
		t.Fatalf("firms.Firm(%d): %v (auto-placed FirmID does not resolve to a real registered firm)", firmID, err)
	}
	if firm.Name != buildersMerchantName {
		t.Fatalf("auto-placed firm Name = %q, want %q", firm.Name, buildersMerchantName)
	}

	// Idempotence: a further zone command that keeps the grouping present
	// must NOT place a second merchant.
	zoneCell(t, e, "inc2-zone-farming-2", protocol.CellRef{X: 6, Y: 6}, "farming")
	if comp.state.buildersMerchantFirmID != firmID {
		t.Fatalf("buildersMerchantFirmID changed from %d to %d on a later zone command — auto-placement is not idempotent", firmID, comp.state.buildersMerchantFirmID)
	}
}

// dwellingMaterialsTonnes mirrors data/buildings.json's
// zones[dwelling].materialsBill.constructionMaterials (100) — the exact
// tonnage a single dwelling build order draws, used to compute this
// file's expected accrual/settlement amounts against ground truth rather
// than an approximate inequality.
const dwellingMaterialsTonnesInc2 = 100

// expectedConstructionCost is the ledger-facing cost
// accrueAndSettleConstruction charges for tonnes tonnes of construction
// material, mirroring moneycirc_inc2.go's own per-tonne pricing exactly
// (constructionMaterialLedgerPriceMicropoundsPerTonne).
func expectedConstructionCost(tonnes int64) int64 {
	return tonnes * constructionMaterialLedgerPriceMicropoundsPerTonne
}

// TestFEAT1972079927_Inc2_ExternalSourcing_AccrueThenSettleAtNinety
// proves the NET-90 cadence end to end for the imported (no local
// merchant) branch: a dwelling's materials draw (100 tonnes, fulfilled in
// a single day-tick against the generously-provisioned logistics stock)
// accrues into constructionAccrualExternal and sits there — NOT settled —
// through tick 89, then settles in exactly one lump at tick 90, zeroing
// the accrual and crediting constructionSettledExternal by exactly the
// expected cost. No merchant is ever placed in this test, so every tonne
// must route external.
//
// PROOF THIS CAN FAIL: temporarily changing the settlement guard from
// `tick%commercialPaymentTermTicks != 0` to `tick%30 != 0` (the monthly
// cadence, not net-90) makes the "still accrued, not yet settled at tick
// 89" assertion pass but the settlement fires THREE times by tick 90
// instead of once, and constructionSettledExternal ends up 3x the
// expected amount — verified by hand during development, then reverted.
func TestFEAT1972079927_Inc2_ExternalSourcing_AccrueThenSettleAtNinety(t *testing.T) {
	buyCell := protocol.CellRef{X: 3, Y: 3}
	e, comp := wireInc2TestEngine(t, 502, buyCell)

	// Never zone an Industry&Farms grouping in this test — no merchant.
	zoneCell(t, e, "inc2-ext-zone", buyCell, "dwelling")
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("inc2-ext-build"),
		Kind: protocol.KindBuild, Payload: protocol.BuildPayload{Cell: buyCell, BuildingType: "dwelling"},
	}); !res.Accepted {
		t.Fatalf("Build rejected: %+v", res.Error)
	}

	// Day 1: the build queue draws the full 100-tonne materials bill in
	// one Tick (the provisioned stock is far larger than the bill), so the
	// entire order's cost accrues on this single day.
	if err := e.AdvanceTicks(errsCorr("inc2-ext-day1"), 1); err != nil {
		t.Fatalf("AdvanceTicks(day1): %v", err)
	}
	wantCost := expectedConstructionCost(dwellingMaterialsTonnesInc2)
	if comp.state.constructionAccrualExternal != wantCost {
		t.Fatalf("constructionAccrualExternal after day 1 = %d, want %d (100 tonnes drawn in one tick)", comp.state.constructionAccrualExternal, wantCost)
	}
	if comp.state.constructionSettledExternal != 0 {
		t.Fatalf("constructionSettledExternal after day 1 = %d, want 0 (net-90 has not elapsed)", comp.state.constructionSettledExternal)
	}

	// Advance to day 89 (88 more ticks): still accrued, still unsettled.
	if err := e.AdvanceTicks(errsCorr("inc2-ext-day89"), 88); err != nil {
		t.Fatalf("AdvanceTicks(to day89): %v", err)
	}
	if comp.state.constructionAccrualExternal != wantCost {
		t.Fatalf("constructionAccrualExternal at day 89 = %d, want %d (still accrued)", comp.state.constructionAccrualExternal, wantCost)
	}
	if comp.state.constructionSettledExternal != 0 {
		t.Fatalf("constructionSettledExternal at day 89 = %d, want 0 (net-90 boundary not yet reached)", comp.state.constructionSettledExternal)
	}

	// Day 90: the net-90 boundary — settle in one lump.
	if err := e.AdvanceTicks(errsCorr("inc2-ext-day90"), 1); err != nil {
		t.Fatalf("AdvanceTicks(day90): %v", err)
	}
	if comp.state.constructionAccrualExternal != 0 {
		t.Fatalf("constructionAccrualExternal at day 90 = %d, want 0 (settled and zeroed)", comp.state.constructionAccrualExternal)
	}
	if comp.state.constructionSettledExternal != wantCost {
		t.Fatalf("constructionSettledExternal at day 90 = %d, want %d", comp.state.constructionSettledExternal, wantCost)
	}
	if comp.state.constructionSettledLocal != 0 {
		t.Fatalf("constructionSettledLocal = %d, want 0 (no merchant was ever placed in this test)", comp.state.constructionSettledLocal)
	}
}

// TestFEAT1972079927_Inc2_LocalSourcing_CreditsFirms proves the local
// (merchant-present) branch settles to AcctFirms (in-city) rather than
// AcctExternal: place the merchant FIRST (Industry&Farms grouping), then
// build a dwelling elsewhere in the same owned tile — its materials draw
// must accrue and settle as LOCAL, never external.
func TestFEAT1972079927_Inc2_LocalSourcing_CreditsFirms(t *testing.T) {
	buyCell := protocol.CellRef{X: 3, Y: 3}
	e, comp := wireInc2TestEngine(t, 503, buyCell)

	// Place the merchant before the dwelling build order exists.
	zoneCell(t, e, "inc2-loc-farming", protocol.CellRef{X: 10, Y: 10}, "farming")
	zoneCell(t, e, "inc2-loc-manufacturing", protocol.CellRef{X: 11, Y: 11}, "manufacturing")
	if comp.state.buildersMerchantFirmID == 0 {
		t.Fatal("builders' merchant did not auto-place — test setup invalid")
	}

	zoneCell(t, e, "inc2-loc-zone-dwelling", buyCell, "dwelling")
	if res := e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID("inc2-loc-build"),
		Kind: protocol.KindBuild, Payload: protocol.BuildPayload{Cell: buyCell, BuildingType: "dwelling"},
	}); !res.Accepted {
		t.Fatalf("Build rejected: %+v", res.Error)
	}

	if err := e.AdvanceTicks(errsCorr("inc2-loc-day1"), 1); err != nil {
		t.Fatalf("AdvanceTicks(day1): %v", err)
	}
	wantCost := expectedConstructionCost(dwellingMaterialsTonnesInc2)
	if comp.state.constructionAccrualLocal != wantCost {
		t.Fatalf("constructionAccrualLocal after day 1 = %d, want %d", comp.state.constructionAccrualLocal, wantCost)
	}
	if comp.state.constructionAccrualExternal != 0 {
		t.Fatalf("constructionAccrualExternal after day 1 = %d, want 0 (a merchant exists — nothing should route external)", comp.state.constructionAccrualExternal)
	}

	// BUG-548 (2026-09-05): stop 1 tick short of the day-90 settlement so
	// the settlement's OWN effect on AcctFirms can be isolated from that
	// same day's OTHER legs (financeHook's monthly wage/consumption
	// postings also land on day 90 — commercialPaymentTermTicks=90 is an
	// exact multiple of DailyTicksPerMonth=30). AcctFirms now also pays
	// PRIVATE-sector wages every month (PostWagesFromFirms), so a plain
	// "balance grew" check across the whole 89-day gap no longer isolates
	// this test's subject.
	if err := e.AdvanceTicks(errsCorr("inc2-loc-to89"), 88); err != nil {
		t.Fatalf("AdvanceTicks(to day89): %v", err)
	}
	firmsBeforeDay90 := ledgerBalance(comp.state.finance, finance.AcctFirms)

	if err := e.AdvanceTicks(errsCorr("inc2-loc-day90"), 1); err != nil {
		t.Fatalf("AdvanceTicks(day90): %v", err)
	}
	if comp.state.constructionSettledLocal != wantCost {
		t.Fatalf("constructionSettledLocal at day 90 = %d, want %d", comp.state.constructionSettledLocal, wantCost)
	}
	if comp.state.constructionSettledExternal != 0 {
		t.Fatalf("constructionSettledExternal = %d, want 0 (this order was sourced locally throughout)", comp.state.constructionSettledExternal)
	}
	firmsAfterDay90 := ledgerBalance(comp.state.finance, finance.AcctFirms)

	// day 90's OTHER AcctFirms legs (financeHook's own wage debit and
	// consumption-spend credit) are read straight off the ledger (GR#15 —
	// derive from data, never assume a figure) rather than re-derived by
	// formula, since financeHook's own BeginMonth call resets the
	// category log AFTER the (earlier-phase) construction settlement
	// posts — LinesByCategory(CatConstruction) can no longer see the
	// settlement itself by the time this test reads it, but it CAN still
	// see financeHook's own subsequent postings, which is exactly what's
	// needed to net them out of the balance delta below.
	var otherFirmsDelta int64
	for _, entry := range comp.state.finance.LinesByCategory(finance.CatWages) {
		if entry.Account != finance.AcctFirms {
			continue
		}
		if entry.Side == finance.SideDebit {
			otherFirmsDelta = num.SatSub(otherFirmsDelta, int64(entry.Amount))
		} else {
			otherFirmsDelta = num.SatAdd(otherFirmsDelta, int64(entry.Amount))
		}
	}
	for _, entry := range comp.state.finance.LinesByCategory(finance.CatSpend) {
		if entry.Account != finance.AcctFirms {
			continue
		}
		if entry.Side == finance.SideCredit {
			otherFirmsDelta = num.SatAdd(otherFirmsDelta, int64(entry.Amount))
		} else {
			otherFirmsDelta = num.SatSub(otherFirmsDelta, int64(entry.Amount))
		}
	}
	// The commercial (sales) + industrial (corp) tax legs also debit
	// AcctFirms directly (finance/stages.go's CollectTax) on the same
	// day's consumption spend.
	for _, cat := range []finance.Category{finance.CatTaxSales, finance.CatTaxCorp} {
		for _, entry := range comp.state.finance.LinesByCategory(cat) {
			if entry.Account != finance.AcctFirms {
				continue
			}
			if entry.Side == finance.SideDebit {
				otherFirmsDelta = num.SatSub(otherFirmsDelta, int64(entry.Amount))
			} else {
				otherFirmsDelta = num.SatAdd(otherFirmsDelta, int64(entry.Amount))
			}
		}
	}
	gotDelta := firmsAfterDay90 - firmsBeforeDay90
	wantDelta := otherFirmsDelta + wantCost
	if gotDelta != wantDelta {
		t.Fatalf("AcctFirms day-90 delta = %d, want %d (otherFirmsDelta=%d + settlement wantCost=%d) — local construction settlement must land IN-CITY", gotDelta, wantDelta, otherFirmsDelta, wantCost)
	}
}

// errsCorr is a tiny local helper so this file does not need to import
// foundation/errs just for a correlation-ID string literal in
// AdvanceTicks calls above (which accepts any string, not a distinct
// type) — kept distinct from errs.NewCorrelationID's random generator
// because these tests want stable, greppable correlation IDs per step.
func errsCorr(s string) string { return s }
