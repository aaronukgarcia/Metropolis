package finance

import "github.com/aaronukgarcia/Metropolis/internal/foundation/errs"

// FEAT-094 CAPEX/OPEX integration: this file folds maintenance,
// staffing wages, raw materials, services, and debt service into a
// composed, drill-through-able OPEX total (US-1/US-3), gives
// maintenance underfunding a real backlog + efficiency consequence
// (US-2), and gives a maintenance/repair event's financial
// classification a policy-driven CAPEX/OPEX split (US-4).
//
// GR#25 coordination gate (recorded here, not just in the acceptance
// doc): this item's escalations name two seams — engine.finance
// consuming engine.maintenance's demand figure and engine.policies'
// repair-strategy value through interfaces — that code.json does NOT
// register as outbound edges of engine.finance today. Per the dispatch
// brief's instruction to "skip+flag unregistered ones", this file
// never imports engine.maintenance or engine.policies: every function
// below takes plain Money/RepairPolicy values from the caller. The
// composition root (once Bill/the Architect register the edges) is
// where a real engine.maintenance.CityDemand()/engine.policies value
// gets translated into these plain arguments — that translation is
// out of scope here.

// OpexBreakdown is the five-component OPEX composition (AC-1): each
// field is independently retrievable and none is folded into another,
// so "maintenance" is never hidden inside "services" (the old single
// CatOpex bucket).
type OpexBreakdown struct {
	Maintenance Money
	StaffWages  Money
	Materials   Money
	Services    Money
	DebtService Money
}

// Total sums the five components with saturating addition (GR#16).
func (b OpexBreakdown) Total() Money {
	total, _ := satAddMoney(b.Maintenance, b.StaffWages)
	total, _ = satAddMoney(total, b.Materials)
	total, _ = satAddMoney(total, b.Services)
	total, _ = satAddMoney(total, b.DebtService)
	return total
}

// OpexBreakdown returns the tick's five-component OPEX composition
// (AC-1/AC-3), each figure a treasury-debit sum over its own category —
// drill-through-able via LinesByCategory.
func (f *FinanceAPI) OpexBreakdown() OpexBreakdown {
	if err := f.checkNotCopied("OpexBreakdown"); err != nil {
		return OpexBreakdown{}
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return OpexBreakdown{
		Maintenance: f.treasuryDebit(CatMaintenance),
		StaffWages:  f.treasuryDebit(CatStaffWages),
		Materials:   f.treasuryDebit(CatMaterials),
		Services:    f.treasuryDebit(CatOpex),
		DebtService: f.treasuryDebit(CatDebtService),
	}
}

// ComposedOpex returns the tick's composed OPEX total — the sum of the
// five named components (AC-2). It equals OpexBreakdown().Total() by
// construction; exposed separately so BudgetBalance and the
// conservation identity read it without building the struct.
func (f *FinanceAPI) ComposedOpex() Money {
	if err := f.checkNotCopied("ComposedOpex"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	total, _ := satAddMoney(f.treasuryDebit(CatMaintenance), f.treasuryDebit(CatStaffWages))
	total, _ = satAddMoney(total, f.treasuryDebit(CatMaterials))
	total, _ = satAddMoney(total, f.treasuryDebit(CatOpex))
	total, _ = satAddMoney(total, f.treasuryDebit(CatDebtService))
	return total
}

// CapexTotal returns the tick's posted capital spend (CatCapex —
// refit/rebuild events, AC-7), distinct from ConstructionTotal
// (new-build spend) and from every OPEX component.
func (f *FinanceAPI) CapexTotal() Money {
	if err := f.checkNotCopied("CapexTotal"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.treasuryDebit(CatCapex)
}

// NetOther returns the tick's TrackedDelta contribution from every
// category OUTSIDE the composed-OPEX family (maintenance, staff wages,
// materials, services, debt service) and outside capex: tax (inflow),
// construction, imports, debt principal, loan, reserve moves,
// investment, wages/spend (net zero — both legs are RoleMoney). Signed
// exactly like MoneyStock().TrackedDelta (inflows positive, outflows
// negative) — unlike ComposedOpex/CapexTotal, which are positive
// outflow magnitudes for consistency with OpexTotal/ConstructionTotal's
// existing accessor convention. The AC-9 conservation identity is
// therefore stated as:
//
//	NetOther() - ComposedOpex() - CapexTotal() == MoneyStock().TrackedDelta
//
// (zero residual) — the doc.go "Money model" section repeats this
// exact formula so the sign convention is never re-derived by a reader.
// Iterates the current tick's transaction log in post order (a fixed
// slice, never map order — GR#21).
func (f *FinanceAPI) NetOther() Money {
	if err := f.checkNotCopied("NetOther"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	var delta Money
	for _, tx := range f.tickTxns {
		for _, e := range tx.Entries {
			if opexFamilyCategory(e.Category) {
				continue
			}
			if f.role[e.Account] != RoleMoney {
				continue
			}
			if e.Side == SideCredit {
				delta, _ = satAddMoney(delta, e.Amount)
			} else {
				delta = satSubMoney(delta, e.Amount)
			}
		}
	}
	return delta
}

// opexFamilyCategory reports whether cat is one of the composed-OPEX
// components or capex — the categories NetOther excludes. A map
// LOOKUP (not a range) is deterministic (GR#21 only bans iterating a
// map in undetermined order, not keyed access).
var opexFamilySet = map[Category]bool{
	CatMaintenance: true,
	CatStaffWages:  true,
	CatMaterials:   true,
	CatOpex:        true,
	CatDebtService: true,
	CatCapex:       true,
}

func opexFamilyCategory(cat Category) bool { return opexFamilySet[cat] }

// SetOpexConfig installs the balance-data-derived config the
// backlog/efficiency and major-drain accessors need (GR#15 — no
// hardcoded Go-literal magnitude). Call once during composition, via
// LoadOpexConfig/LoadDefaultOpexConfig or a test fixture.
func (f *FinanceAPI) SetOpexConfig(cfg OpexConfig) error {
	if err := f.checkNotCopied("SetOpexConfig"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c := cfg
	f.opexCfg = &c
	return nil
}

// OpexConfig returns the currently-installed balance config, and
// whether one has been set (ErrOpexConfigNotSet is returned by the
// accessors that need it rather than this query, which is allowed to
// report "not set" without erroring).
func (f *FinanceAPI) OpexConfig() (OpexConfig, bool) {
	if err := f.checkNotCopied("OpexConfig"); err != nil {
		return OpexConfig{}, false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.opexCfg == nil {
		return OpexConfig{}, false
	}
	return *f.opexCfg, true
}

// PostMaintenance settles the funded portion of the city's maintenance
// demand as the CatMaintenance OPEX component (AC-1/AC-3/AC-4), and
// records the shortfall (demand - funded, when positive) into the
// running maintenance backlog (AC-5). demand and funded are plain
// Money values the caller (the composition root, via
// engine.maintenance's CityDemand once the edge is registered) derives
// — this function never imports engine.maintenance (see the GR#25
// coordination note at the top of this file). Both must be
// non-negative; a negative demand or funded amount is rejected
// (ErrMaintenanceDemandNegative/ErrMaintenanceFundedNegative), never
// silently clamped to zero (AC-11).
func (f *FinanceAPI) PostMaintenance(demand, funded Money) (Money, error) {
	if err := f.checkNotCopied("PostMaintenance"); err != nil {
		return 0, err
	}
	if demand < 0 {
		return 0, errs.New(ErrMaintenanceDemandNegative, f.correlationID, map[string]any{"amount": int64(demand)})
	}
	if funded < 0 {
		return 0, errs.New(ErrMaintenanceFundedNegative, f.correlationID, map[string]any{"amount": int64(funded)})
	}
	if _, err := f.Post(Transaction{
		Description: "maintenance opex (funded allocation)",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: funded, Category: CatMaintenance},
			{Account: AcctExternal, Side: SideCredit, Amount: funded, Category: CatMaintenance},
		},
	}); err != nil {
		return 0, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	shortfall := satSubMoney(demand, funded)
	switch {
	case shortfall > 0:
		f.backlog, _ = satAddMoney(f.backlog, shortfall)
	case shortfall < 0:
		recovered := -shortfall
		if recovered > f.backlog {
			recovered = f.backlog
		}
		f.backlog = satSubMoney(f.backlog, recovered)
	}
	return funded, nil
}

// PostMaterials posts the raw-materials OPEX component (AC-3/AC-4).
func (f *FinanceAPI) PostMaterials(cost Money) (Money, error) {
	if err := f.checkNotCopied("PostMaterials"); err != nil {
		return 0, err
	}
	if cost < 0 {
		return 0, errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "materials", "amount": int64(cost)})
	}
	if _, err := f.Post(Transaction{
		Description: "raw materials opex",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: cost, Category: CatMaterials},
			{Account: AcctExternal, Side: SideCredit, Amount: cost, Category: CatMaterials},
		},
	}); err != nil {
		return 0, err
	}
	return cost, nil
}

// PostStaffWages posts the staffing-wages OPEX component (AC-3/AC-4) —
// the city's own operator/repair-crew wage bill, a distinct category
// from CatWages (household income, MOD-022's PostWages stage) per the
// acceptance doc's Escalations: paying nurses/teachers/repair staff is
// a city operating cost, not the household wage-bill stage.
func (f *FinanceAPI) PostStaffWages(cost Money) (Money, error) {
	if err := f.checkNotCopied("PostStaffWages"); err != nil {
		return 0, err
	}
	if cost < 0 {
		return 0, errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "staffWages", "amount": int64(cost)})
	}
	if _, err := f.Post(Transaction{
		Description: "staffing wages opex (repair/operator crews)",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: cost, Category: CatStaffWages},
			{Account: AcctExternal, Side: SideCredit, Amount: cost, Category: CatStaffWages},
		},
	}); err != nil {
		return 0, err
	}
	return cost, nil
}

// MaintenanceBacklog returns the running maintenance-underfunding
// balance (AC-5): the accumulated shortfall (demand - funded) across
// every PostMaintenance call, floored at zero. Unlike the tick-scoped
// ledger aggregates, this persists across BeginMonth calls.
func (f *FinanceAPI) MaintenanceBacklog() Money {
	if err := f.checkNotCopied("MaintenanceBacklog"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.backlog
}

// MaintenanceEfficiency returns the backlog-driven efficiency factor in
// basis points (10000 = 100%), monotonically decreasing as the backlog
// grows and recovering as it is paid down (AC-5) — the felt consequence
// of underfunding. Requires SetOpexConfig/LoadOpexConfig to have run
// (ErrOpexConfigNotSet otherwise — never a silently-substituted
// default, GR#15).
func (f *FinanceAPI) MaintenanceEfficiency() (BasisPoints, error) {
	if err := f.checkNotCopied("MaintenanceEfficiency"); err != nil {
		return 0, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.opexCfg == nil {
		return 0, errs.New(ErrOpexConfigNotSet, f.correlationID, nil)
	}
	divisor := f.opexCfg.BacklogEfficiencyDivisor
	if divisor <= 0 {
		divisor = 1
	}
	loss := int64(f.backlog) / divisor
	eff := int64(10000) - loss
	min := int64(f.opexCfg.MinEfficiencyBasisPoints)
	if eff < min {
		eff = min
	}
	if eff > 10000 {
		eff = 10000
	}
	return BasisPoints(eff), nil
}

// RepairPolicy classifies a maintenance/repair event's financial
// destination (AC-7): PostMaintenanceSpend routes an auto-repair
// obligation to the OPEX maintenance component and a refit/rebuild
// obligation to the capital total, so the same underlying obligation
// amount lands in a different bucket purely as a function of policy
// (AC-8's "the policy value drives the split, not the total"). This is
// a package-local classification value, not FEAT-091's engine.policies
// [auto]/[manual] type — see the GR#25 coordination note at the top of
// this file for why: the engine.finance -> engine.policies edge this
// AC's real seam needs is not registered in code.json today. The
// composition root is where a real policy value gets translated into
// this local type once that edge lands.
type RepairPolicy uint8

const (
	// RepairPolicyAuto: auto-repair is operating expenditure.
	RepairPolicyAuto RepairPolicy = iota
	// RepairPolicyRefitRebuild: refit/rebuild is a capital event.
	RepairPolicyRefitRebuild
)

// PostMaintenanceSpend posts a maintenance/repair obligation according
// to policy (AC-7/AC-8): RepairPolicyAuto posts as CatMaintenance
// (OPEX), RepairPolicyRefitRebuild posts as CatCapex (capital) via
// PostCapexSpend. amount must be positive for a refit/rebuild — a
// zero-cost "capital event" is not a capital event
// (ErrCapexUnclassified); a zero-cost auto-repair is a legal no-op.
func (f *FinanceAPI) PostMaintenanceSpend(amount Money, policy RepairPolicy) (Money, error) {
	if err := f.checkNotCopied("PostMaintenanceSpend"); err != nil {
		return 0, err
	}
	if amount < 0 {
		return 0, errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "maintenanceSpend", "amount": int64(amount)})
	}
	if policy == RepairPolicyRefitRebuild {
		return f.PostCapexSpend(amount)
	}
	if _, err := f.Post(Transaction{
		Description: "auto-repair maintenance opex",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: amount, Category: CatMaintenance},
			{Account: AcctExternal, Side: SideCredit, Amount: amount, Category: CatMaintenance},
		},
	}); err != nil {
		return 0, err
	}
	return amount, nil
}

// PostCapexSpend posts a refit/rebuild capital event (AC-7): the
// treasury pays the outside world, tagged CatCapex — never routed
// through an OPEX category. cost must be positive; a non-positive cost
// is rejected (ErrCapexUnclassified for zero — "no declared capital
// cost" per AC-11's malformed-input list; ErrNegativeAmount for
// negative).
func (f *FinanceAPI) PostCapexSpend(cost Money) (Money, error) {
	if err := f.checkNotCopied("PostCapexSpend"); err != nil {
		return 0, err
	}
	if cost < 0 {
		return 0, errs.New(ErrNegativeAmount, f.correlationID, map[string]any{"field": "capex", "amount": int64(cost)})
	}
	if cost == 0 {
		return 0, errs.New(ErrCapexUnclassified, f.correlationID, nil)
	}
	if _, err := f.Post(Transaction{
		Description: "refit/rebuild capital spend",
		Entries: []Entry{
			{Account: AcctTreasury, Side: SideDebit, Amount: cost, Category: CatCapex},
			{Account: AcctExternal, Side: SideCredit, Amount: cost, Category: CatCapex},
		},
	}); err != nil {
		return 0, err
	}
	return cost, nil
}
