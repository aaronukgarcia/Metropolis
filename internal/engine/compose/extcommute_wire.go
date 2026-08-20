package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/extcommute"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-207 (docs/planning/icd/engine.extcommute-compose.md): the
// composition-root adapters that map extcommute's contract-first
// CitizensSeam/TrafficSeam/FinanceSeam onto the real engine.citizens/
// engine.traffic/engine.finance APIs. extcommute never imports those engine
// packages directly (GR#20/GR#25) — this file is the ONE place the mapping
// happens, mirroring the FEAT-167 pattern of a compose-owned bridge file per
// integration.

// --- CitizensSeam adapter (ICD §3/§4) -------------------------------------

// extCommuteCitizensSeam adapts *citizens.CitizensAPI onto
// extcommute.CitizensSeam. The employment write is a pure numeric cast
// (ICD §3): extCommuteEmploymentStatesIdentical (checked once, at Wire
// time, before this adapter is ever exercised) proves
// extcommute.EmploymentState and citizens.EmploymentState agree on every
// constant, so citizens.EmploymentState(state) below is the identity
// function, never a translation table.
type extCommuteCitizensSeam struct {
	api *citizens.CitizensAPI
	cid string
}

// TotalPopulation implements extcommute.CitizensSeam.
func (s *extCommuteCitizensSeam) TotalPopulation() int {
	return s.api.TotalPopulation(s.cid)
}

// CitizenExists implements extcommute.CitizensSeam.
func (s *extCommuteCitizensSeam) CitizenExists(id uint64) bool {
	_, ok := s.api.CitizenAt(id, s.cid)
	return ok
}

// ApplyLifeEventEmployment implements extcommute.CitizensSeam: routes the
// employment-state write through citizens' existing LifeEventEmployment
// command surface (ICD engine.citizens-offmap.md §4 — no new CitizensAPI
// method). The sector is always SectorNone: extcommute never knows or
// restores a prior local job/sector for an off-map-assigned or -released
// citizen (matches extcommute/types.go's ApplyLifeEventEmployment doc
// comment).
func (s *extCommuteCitizensSeam) ApplyLifeEventEmployment(citizenID uint64, state extcommute.EmploymentState) error {
	return s.api.ApplyLifeEventCommand(citizens.LifeEventCommand{
		CorrelationID: s.cid,
		Kind:          citizens.LifeEventEmployment,
		CitizenID:     citizenID,
		Employment:    citizens.EmploymentState(state),
		Sector:        citizens.SectorNone,
	})
}

// extCommuteEmploymentStatesIdentical is FEAT-207's Wire-time cross-check
// (ICD engine.extcommute-compose.md §3/§11 "identity-map conformance"):
// extcommute.EmploymentState's six constants must stay numerically
// identical to citizens.EmploymentState's, because
// extCommuteCitizensSeam.ApplyLifeEventEmployment's cast
// (citizens.EmploymentState(state)) is the identity function, not a
// translation table. Checked once per Wire call — mirrors the
// idNamespaceRangesDisjoint FEAT-169 destructive-review pattern already in
// compose.go — so a future renumbering on either side fails loudly at
// construction instead of silently mislabelling a citizen's employment
// state. Reuses ErrModuleFailed (MET-G801, already registered in
// data/errors.json) rather than minting a new registry code for a
// wire-time-only assertion.
//
// KNOWN LIMITATION (round-verdict hardening item, 2026-08-19): the pairs
// slice below is a hardcoded 6-entry literal list, not a reflective
// enumeration — Go has no const reflection, so this function cannot
// discover "every constant either package declares" at runtime, only
// compare the six names it was written to know about. It therefore
// structurally CANNOT detect a 7th employment state added to only one
// side (e.g. a future EmploymentApprentice added to citizens.EmploymentState
// but never mirrored here and in extcommute.EmploymentState): the loop
// below would simply never compare it, and Wire would succeed even though
// the two enums have silently diverged in cardinality. Adding a new
// employment state is therefore a THREE-place change — both packages'
// const blocks AND this pairs literal — and none of the three is enforced
// by the compiler; a future editor must remember to update all three by
// discipline, the same way idNamespaceRangesDisjoint's own constants are
// hand-maintained. This assertion only guards against the ORIGINAL bug
// class it was built for (two enums that both declare N values but
// disagree on one's numeric assignment), not an N-vs-N+1 cardinality
// drift.
func extCommuteEmploymentStatesIdentical(cid string) error {
	pairs := []struct {
		name string
		ext  extcommute.EmploymentState
		cit  citizens.EmploymentState
	}{
		{"None", extcommute.EmploymentNone, citizens.EmploymentNone},
		{"Student", extcommute.EmploymentStudent, citizens.EmploymentStudent},
		{"Employed", extcommute.EmploymentEmployed, citizens.EmploymentEmployed},
		{"Unemployed", extcommute.EmploymentUnemployed, citizens.EmploymentUnemployed},
		{"Retired", extcommute.EmploymentRetired, citizens.EmploymentRetired},
		{"OffMap", extcommute.EmploymentOffMap, citizens.EmploymentOffMap},
	}
	for _, p := range pairs {
		if uint8(p.ext) != uint8(p.cit) {
			return errs.New(ErrModuleFailed, cid, map[string]any{
				"module":     "extcommute",
				"cause":      "EmploymentState identity drift",
				"state":      p.name,
				"extcommute": uint8(p.ext),
				"citizens":   uint8(p.cit),
			})
		}
	}
	return nil
}

// --- TrafficSeam stub (ICD §3/§12 open decision 2) ------------------------

// extCommuteFreeFlowCongestion is the documented free-flow placeholder
// extCommuteTrafficSeamStub returns: engine.traffic is not wired into
// compose yet (no engine.traffic construction anywhere in Wire today), and
// even once it is, engine.traffic exposes no per-channel Congestion query
// (docs/planning/icd/engine.extcommute-compose.md §12 open decision 2 —
// LinkTravelTime/CommuteHours/AccessMinutes/CommuteMinutes/
// ActiveTravelShare/AddTripDemand/RegisterTrip/AddDemand/AdvanceTick/
// DailyAssignment exist, Congestion does not). Per the ICD's honest-gating
// rule this is a documented STUB — free-flow (0.0, no congestion, no
// capacity reduction) — never a fabricated dynamic congestion figure. It
// unblocks the two-cap model's transport check (pool capacity + transport
// capacity) without inventing traffic dynamics; replacing it with a real
// engine.traffic-backed adapter is FEAT-206's gate (a per-channel
// congestion query landing in engine.traffic first).
const extCommuteFreeFlowCongestion = 0.0

// extCommuteTrafficSeamStub implements extcommute.TrafficSeam as the
// documented free-flow stub described above.
type extCommuteTrafficSeamStub struct{}

// Congestion implements extcommute.TrafficSeam.
func (extCommuteTrafficSeamStub) Congestion(channel string) (float64, error) {
	return extCommuteFreeFlowCongestion, nil
}

// --- FinanceSeam adapter (ICD §3/§4/§12 open decision 3) ------------------

// The five FinanceSeam verbs have no verb-matching FinanceAPI surface
// (ICD §12 open decision 3): engine.finance exposes a double-entry
// Post(Transaction{Entries: []Entry{Account, Side, Amount, Category}}), not
// RecordOffMapWage/RemoveOffMapWage/RecordBusinessRates/RecordCorpShare/
// RecordWageLeakage. These four categories are this adapter's translation
// table — compose-local finance.Category values (Category is just a string
// type; no change to internal/engine/finance is needed to mint new tag
// values, so no edit lands outside this package's writable scope).
const (
	finCatExtCommuteOffMapWage    finance.Category = "extcommute.offmap_wage"
	finCatExtCommuteWageLeakage   finance.Category = "extcommute.wage_leakage"
	finCatExtCommuteBusinessRates finance.Category = "extcommute.business_rates_stub"
	finCatExtCommuteCorpShare     finance.Category = "extcommute.corp_share_stub"
)

// extCommuteFinanceSeam adapts *finance.FinanceAPI onto
// extcommute.FinanceSeam. monthFn supplies the current simulation month for
// Transaction.Month (informational only — finance.validateLocked never
// checks it; the ledger's own conservation accounting keys off
// BeginMonth/tickTxns, not this field).
type extCommuteFinanceSeam struct {
	api     *finance.FinanceAPI
	cid     string
	monthFn func() int64
}

// month returns the current simulation month, or 0 if monthFn is unset (a
// defensive default — never called with a nil monthFn in production Wire).
func (s *extCommuteFinanceSeam) month() int64 {
	if s.monthFn == nil {
		return 0
	}
	return s.monthFn()
}

// post is the shared two-entry balanced-transaction helper every verb below
// uses: debit one account, credit another, same amount, one category.
//
// BUG-308 fix 2: every FinanceSeam verb above funnels through this one
// choke point, so the non-negative-amount seam check lives here ONCE
// rather than duplicated five times. A negative amount posted through
// Post's debit/credit pair would REVERSE the credit flow (the debit
// account gains money instead of losing it, and vice versa) — silently
// breaking money conservation (GR#16) rather than erroring. Per GR#16's
// boundary discipline, reject loudly at the seam instead of posting a
// sign-flipped transaction: extcommute.EmploymentState math or a future
// caller bug could otherwise drain the treasury/external account with no
// trace beyond an inverted ledger line.
func (s *extCommuteFinanceSeam) post(debit, credit finance.AccountID, amount int64, cat finance.Category, desc string) error {
	if amount < 0 {
		return errs.New(ErrInvalidWireAmount, s.cid, map[string]any{
			"amount": amount,
			"verb":   string(cat),
		})
	}
	_, err := s.api.Post(finance.Transaction{
		Month:       s.month(),
		Description: desc,
		Entries: []finance.Entry{
			{Account: debit, Side: finance.SideDebit, Amount: finance.Money(amount), Category: cat},
			{Account: credit, Side: finance.SideCredit, Amount: finance.Money(amount), Category: cat},
		},
	})
	return err
}

// RecordOffMapWage implements extcommute.FinanceSeam: an out-commuter's
// off-map wage is new money entering the city economy from outside
// (AcctExternal, RoleExternal — unconstrained, never overdraft-checked)
// credited to the household wealth pool (AcctHouseholds, RoleMoney — a
// credit only ever adds, so it can never trigger the overdraft check
// regardless of AcctHouseholds' starting balance). Income-tax-eligible only
// (AC-12): this verb never touches AcctFirms/business-rates/corp-share
// categories.
func (s *extCommuteFinanceSeam) RecordOffMapWage(citizenID uint64, poolID string, wageMicropounds int64) error {
	return s.post(finance.AcctExternal, finance.AcctHouseholds, wageMicropounds, finCatExtCommuteOffMapWage,
		"extcommute off-map wage: citizen "+itoa64(citizenID)+" pool "+poolID)
}

// RemoveOffMapWage implements extcommute.FinanceSeam: the compensating
// inverse of RecordOffMapWage (extcommute.go's Assign rollback path — the
// ONLY production caller, invoked immediately after RecordOffMapWage in the
// same Assign call if the citizens-seam flip fails). Debiting AcctHouseholds
// here is safe by construction: it reverses exactly the amount RecordOffMapWage
// just credited, with no other mutation of that account possible in between
// (Assign holds a's write lock across both calls).
func (s *extCommuteFinanceSeam) RemoveOffMapWage(citizenID uint64, poolID string, wageMicropounds int64) error {
	return s.post(finance.AcctHouseholds, finance.AcctExternal, wageMicropounds, finCatExtCommuteOffMapWage,
		"extcommute off-map wage rollback: citizen "+itoa64(citizenID)+" pool "+poolID)
}

// RecordBusinessRates implements extcommute.FinanceSeam. Never called by
// extcommute for off-map employment (AC-12/A6c — the fiscal-thinness
// property: an off-map job yields income tax but no business rates). This
// is a proof stub: it is implemented so a caller COULD post one (and a
// destructive test can prove extcommute never exercises this path), routed
// AcctExternal -> AcctTreasury (mirrors RecordOffMapWage's safe direction:
// a credit-only write to the receiving account).
func (s *extCommuteFinanceSeam) RecordBusinessRates(citizenID uint64, amountMicropounds int64) error {
	return s.post(finance.AcctExternal, finance.AcctTreasury, amountMicropounds, finCatExtCommuteBusinessRates,
		"extcommute business rates (proof stub, never called for off-map employment): citizen "+itoa64(citizenID))
}

// RecordCorpShare implements extcommute.FinanceSeam. Never called by
// extcommute for off-map employment (AC-12/A6c), for the same reason and
// with the same safe posting direction as RecordBusinessRates.
func (s *extCommuteFinanceSeam) RecordCorpShare(citizenID uint64, amountMicropounds int64) error {
	return s.post(finance.AcctExternal, finance.AcctTreasury, amountMicropounds, finCatExtCommuteCorpShare,
		"extcommute corp share (proof stub, never called for off-map employment): citizen "+itoa64(citizenID))
}

// RecordWageLeakage implements extcommute.FinanceSeam: an in-commuter's
// wage is paid locally but taken home, outside the city economy (F6 — AC-10
// "player sees the leak"). Baseline one tracks no per-firm cash flow in
// engine.finance yet (nothing else in compose posts to AcctFirms), so
// crediting/debiting AcctFirms here would risk a spurious overdraft
// rejection against an unfunded account for a flow this integration does
// not otherwise model. Posted self-balancing on AcctExternal instead (a
// RoleExternal account, never overdraft-checked, and excluded from
// TotalMoneyInCirculation either way) — the leakage is still fully visible
// and drill-through-able via LinesByCategory(finCatExtCommuteWageLeakage)
// (AC-10's requirement), it just never claims a local money-stock effect
// this integration has no real local-wage ledger to back.
func (s *extCommuteFinanceSeam) RecordWageLeakage(poolID string, amountMicropounds int64) error {
	return s.post(finance.AcctExternal, finance.AcctExternal, amountMicropounds, finCatExtCommuteWageLeakage,
		"extcommute wage leakage: pool "+poolID)
}

// itoa64 is a tiny uint64-to-decimal-string helper for the Description
// fields above (avoiding an fmt import for a single %d use, mirroring
// firms.go's own itoa helper for a comparable case).
func itoa64(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
