package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/firms"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FEAT-1972079927 "money circulation inc2" wires firms-pay-construction per
// Aaron's 2026-08-31 rulings (all recorded on the BOW item's comments):
//
//   - Materials are GENERIC TONNES (already true of engine.build's §34
//     materials bill — data/buildings.json's constructionMaterials
//     quantity, unchanged by this increment).
//   - A builders'-merchant firm (B&Q/Jewsons-type industrial/trade
//     supplier) AUTO-PLACES, deterministically, the first time the city
//     has an "Industry & Farms zone grouping" — engine.build's
//     IndustryAndFarmsPresent() query (zone.go/build.go) — present: at
//     least one industry-type zone (manufacturing/heavy_industry/mining)
//     AND at least one farming zone. Checked once per successful zone
//     command (handleGameplay's KindZone branch), never per-tick.
//   - If a merchant exists, construction materials are sourced LOCALLY
//     (money stays in-city: engine.finance's SettleConstructionSourced
//     credits AcctFirms, the same aggregate account every other firm
//     revenue lands in). If none exists yet, materials are IMPORTED (money
//     leaks to AcctExternal, pricier in spirit — Baseline One has no
//     separate import markup mechanism yet, a documented simplification).
//   - Settlement runs on a NET-90 commercial/B2B billing cycle
//     (COMMERCIAL_PAYMENT_TERM_TICKS=90, 1 tick=1 day): cost accrues every
//     day a build order draws materials, and is paid in one lump at the
//     90-tick boundary — never per-draw, matching the PAYE-is-monthly /
//     commercial-is-net-90 cadence split Aaron confirmed.
//
// --- Auto-placement (the "reuse the auto-junction/orphan-connect
// pattern" instruction) ---
//
// Those webconsole-side features (contiguous roads/junctions) are
// TypeScript dogfood UI, not this Go engine — there is no shared code to
// import. The PATTERN this increment reuses is the same shape: a
// deterministic, STATE-DERIVED trigger (never a random roll, never a
// counter that can drift from the state it describes) that fires exactly
// once and is idempotent against being re-checked every time the
// triggering state might have changed. Here that state is engine.build's
// own zoneState map (IndustryAndFarmsPresent), and idempotence is
// buildersMerchantFirmID != 0 (compose.go's simState field) — a second
// zone command that keeps the grouping present is a no-op, never a second
// firm.
//
// --- Ledger scale (see moneycirc.go's "Ledger scale vs real-world scale"
// doc comment for the general convention this mirrors) ---
//
// The real, citable UK figure is recorded for magnitude/traceability, but
// is NOT posted directly: at typical Baseline One build volumes (a single
// zone's materials bill is 60-400 tonnes, data/buildings.json), even the
// already-1000x-scaled-down monthly-figure treatment
// (monthlyConsumptionSpendMicropounds's own convention) would still
// overdraft the £10 seed treasury by one to two orders of magnitude
// (150,000 micropounds/tonne x 400 tonnes = £60). Construction cost is a
// per-tonne ACCUMULATING quantity, not a flat per-capita monthly figure,
// so the flat-placeholder treatment (mirroring
// baselineOneMonthlyRentMicropounds's own documented deviation from a
// naive real-value/ledgerScaleDivisor division) is used instead: a small
// toy-scale per-tonne price, empirically sized to keep a full zone's
// materials bill within a few pence of baseline-one's toy treasury.
// TODO(BUG-452): once initialTreasury/seedCitizenCount are real-derived,
// revisit this constant alongside every other flat placeholder in
// moneycirc.go.
const (
	// commercialPaymentTermTicksReal is the real-world grounding for the
	// NET-90 business-to-business trade-credit convention (Aaron's
	// 2026-08-31 ruling): 90 calendar days, standard UK commercial
	// trade-credit terms. 1 simulation tick = 1 day (build.go's
	// daysPerTick), so this IS the tick count directly — no scaling.
	commercialPaymentTermTicksReal = 90

	// COMMERCIAL_PAYMENT_TERM_TICKS is the named constant Aaron's ruling
	// calls for verbatim: every commercial/B2B settlement (construction,
	// and any future inter-firm trade) accrues and settles on this cycle.
	// Exported-style ALL_CAPS name preserved exactly as ruled, despite
	// Go's lower/exported convention — this is a domain constant name
	// Aaron specified, not a Go identifier style choice.
	commercialPaymentTermTicks = commercialPaymentTermTicksReal

	// constructionMaterialPricePerTonneRealMicropounds is the real-world-
	// grounded UK builders'-merchant blended price for generic
	// construction materials — approximately £150/tonne (2024 order-of-
	// magnitude: UK aggregate/ready-mix runs £25-65/tonne at the
	// quarry/plant, ONS/Mineral Products Association; a builders'-merchant
	// counter price for the BLENDED basket a real merchant actually
	// stocks — aggregate, timber, brick, insulation — sits materially
	// higher than raw aggregate alone). Real-world-grounded, balance-pass
	// adjustable (GR#15) — recorded for magnitude/traceability; NOT posted
	// directly (see this file's doc comment).
	constructionMaterialPricePerTonneRealMicropounds = 150_000_000

	// constructionMaterialLedgerPriceMicropoundsPerTonne is the LEDGER-
	// facing per-tonne price accrueAndSettleConstruction actually charges:
	// a flat toy-scale placeholder (see this file's doc comment for why a
	// naive ledgerScaleDivisor division still overdrafts), sized so a full
	// zone's materials bill (60-400 tonnes) settles as a few pence against
	// baseline-one's £10 seed treasury rather than instantly overdrafting
	// it.
	constructionMaterialLedgerPriceMicropoundsPerTonne = 100

	// buildersMerchantStaffCount is the auto-placed merchant's founding
	// staff count (RegisterFirm's staff argument) — a Startup-stage
	// headcount (firms.StageStartup's own §45 band is 1-5 staff), sized at
	// the top of that band for a builders' merchant (a real-world
	// small trade counter typically runs a handful of staff).
	buildersMerchantStaffCount = 5

	// buildersMerchantName is the auto-placed firm's display name and the
	// RegisterFirm purpose-string seed for its deterministic FirmID (see
	// firms.go's firmIDForLocked: "stagefirm:"+name) — a fixed, singular
	// name because Baseline One auto-places at most one merchant per city.
	buildersMerchantName = "Builders Merchant"

	// buildersMerchantPremises is the ZoneClass tag RegisterFirm records
	// on the merchant's Premises (firms.Premises.ZoneClass) — a plain
	// descriptive label, not a build.ZoneType (the merchant is not itself
	// zoned/built through engine.build's queue; it is auto-placed as a
	// firms-registry record only, mirroring freight's chain-stage
	// RegisterFirm usage which carries the same kind of descriptive tag).
	buildersMerchantPremises = "industrial"
)

// maybeAutoPlaceBuildersMerchant is FEAT-1972079927 inc2's deterministic,
// state-derived auto-placement trigger: the first time engine.build
// reports an Industry&Farms zone grouping present, register exactly one
// builders'-merchant firm via engine.firms' RegisterFirm (the same
// dependency-inversion seam engine.freight already uses to register a
// chain-stage as a firm — see firms.go's doc comment). Idempotent: a
// second call after the merchant already exists (buildersMerchantFirmID
// != 0) is a deliberate no-op, whether or not the grouping is still
// present (a later demolition never un-places the merchant — a documented
// Baseline One simplification, matching engine.build's own general
// "demolition never dissolves other modules' derived state" posture, e.g.
// registerLeisureVenues's leisureVenuesRegistered set only ever
// contracts on ITS OWN structure's demolition, never on a different
// module's).
func (st *simState) maybeAutoPlaceBuildersMerchant() error {
	if st.buildersMerchantFirmID != 0 {
		return nil // already placed — idempotent (AC: "auto-places... once")
	}
	if st.buildAPI == nil || st.firms == nil {
		return nil // dependencies not wired (e.g. a stub-engine test path)
	}
	present, err := st.buildAPI.IndustryAndFarmsPresent()
	if err != nil {
		return errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "build", "op": "IndustryAndFarmsPresent"})
	}
	if !present {
		return nil
	}
	startup, err := st.firms.RegisterFirm(buildersMerchantName, buildersMerchantStaffCount, buildersMerchantPremises)
	if err != nil {
		return errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "firms", "op": "RegisterFirm", "firm": buildersMerchantName})
	}
	st.buildersMerchantFirmID = firms.FirmID(startup.ID)
	return nil
}

// hasBuildersMerchant reports whether the deterministic auto-placement
// trigger has fired yet this run — the "sourced locally if one exists,
// else imported" fact accrueAndSettleConstruction reads at the moment
// each tonne is drawn.
func (st *simState) hasBuildersMerchant() bool {
	return st.buildersMerchantFirmID != 0
}

// totalMaterialsDrawn sums BuildOrder.MaterialsDrawn across the entire
// build queue (every order, complete or not) — the cumulative-since-
// genesis tonnage engine.build has drawn through engine.logistics.
// engine.build's Tick exposes no per-tick delta directly (only this
// cumulative snapshot via Queue()), so accrueAndSettleConstruction diffs
// this against materialsDrawnCumulative to find THIS tick's newly-drawn
// tonnage.
func (st *simState) totalMaterialsDrawn() int64 {
	var total int64
	for _, o := range st.buildAPI.Queue() {
		total = num.SatAdd(total, o.MaterialsDrawn)
	}
	return total
}

// accrueAndSettleConstruction is FEAT-1972079927 inc2's daily accrual +
// NET-90 settlement step, called once per day-tick from buildHook.
// ApplyEffect (right after buildAPI.Tick advances the queue):
//
//  1. Diff totalMaterialsDrawn() against the last-seen cumulative total to
//     find how many tonnes were newly drawn THIS tick (zero on a tick
//     where no order drew anything — the common case).
//  2. Price that delta at the ledger-facing per-tonne rate and add it to
//     whichever accrual bucket matches today's sourcing (local merchant
//     present vs imported) — the sourcing fact is read ONCE per tick, at
//     the moment of accrual, never re-derived at settlement time (a
//     merchant placed mid-cycle does not retroactively re-source
//     already-accrued tonnage).
//  3. At every COMMERCIAL_PAYMENT_TERM_TICKS boundary (tick > 0 and
//     tick%90==0), settle BOTH buckets in one call each via
//     finance.SettleConstructionSourced (local -> AcctFirms, external ->
//     AcctExternal) and zero them — accrue-then-pay-in-one-lump, never
//     per-draw, per Aaron's net-90 ruling.
//
// A settlement that fails (e.g. the toy treasury cannot cover it) is
// logged loudly (GR#1) and its bucket is still zeroed — matching
// PostWages/CollectTax's existing "attempt, log on failure, move on"
// pattern elsewhere in this package; carrying a failed settlement forward
// would let one bad tick's debt silently double up against the NEXT
// cycle's genuinely-new accrual, which is exactly the double-count this
// increment's tests guard against.
// dayTick is the 1-based day-tick number that is completing on THIS call
// (buildHook.ApplyEffect passes clock.Tick()+1 — see its own comment for
// why the +1 is needed): dayTick==90 is the first NET-90 boundary,
// dayTick==180 the second, and so on.
func (st *simState) accrueAndSettleConstruction(dayTick int64) error {
	if st.buildAPI == nil || st.finance == nil {
		return nil
	}

	drawnNow := st.totalMaterialsDrawn()
	delta := num.SatSub(drawnNow, st.materialsDrawnCumulative)
	st.materialsDrawnCumulative = drawnNow

	if delta > 0 {
		cost, _ := num.SafeMul(delta, constructionMaterialLedgerPriceMicropoundsPerTonne)
		if st.hasBuildersMerchant() {
			st.constructionAccrualLocal = num.SatAdd(st.constructionAccrualLocal, cost)
		} else {
			st.constructionAccrualExternal = num.SatAdd(st.constructionAccrualExternal, cost)
		}
	}

	if dayTick <= 0 || dayTick%commercialPaymentTermTicks != 0 {
		return nil
	}

	if st.constructionAccrualLocal > 0 {
		amount := st.constructionAccrualLocal
		st.constructionAccrualLocal = 0 // zeroed regardless of the settle outcome (see doc comment)
		if settled, err := st.finance.SettleConstructionSourced(finance.Money(amount), true); err != nil {
			_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "op": "SettleConstructionSourced.local", "cause": err.Error()})
		} else {
			st.constructionSettledLocal = num.SatAdd(st.constructionSettledLocal, int64(settled))
		}
	}
	if st.constructionAccrualExternal > 0 {
		amount := st.constructionAccrualExternal
		st.constructionAccrualExternal = 0
		if settled, err := st.finance.SettleConstructionSourced(finance.Money(amount), false); err != nil {
			_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "finance", "op": "SettleConstructionSourced.external", "cause": err.Error()})
		} else {
			st.constructionSettledExternal = num.SatAdd(st.constructionSettledExternal, int64(settled))
		}
	}
	st.syncMoneyFromLedger()
	return nil
}
