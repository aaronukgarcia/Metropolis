package compose

import (
	"math"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-2326609832 INDEPENDENT destructive round r1 (the policies r1
// attacker, 2026-10-31; verdict row in bow_destructive_verdicts). Written against the builder's
// tree, disjoint from policies_wire_test.go: money first (double enact,
// treasury-short enact rollback, the treasury-short wage-uplift SKIP path the
// builder disclosed as untested), repeal honesty over many cycles, save/load
// resurrection both ways, and hostile strings through the real codec.

// f832DrainTreasury moves the whole treasury balance out to AcctExternal
// (and folds it into moneyDelta exactly as a real outflow would, so the
// same-tick people/money invariant keeps balancing).
func f832DrainTreasury(t *testing.T, st *simState) {
	t.Helper()
	bal := ledgerBalance(st.finance, finance.AcctTreasury)
	if bal <= 0 {
		return
	}
	if err := st.withLedgerDelta(func() error {
		_, err := st.finance.Post(finance.Transaction{
			Description: "attack f832: drain the treasury",
			Entries: []finance.Entry{
				{Account: finance.AcctTreasury, Side: finance.SideDebit, Amount: finance.Money(bal), Category: finance.Category("attack.drain")},
				{Account: finance.AcctExternal, Side: finance.SideCredit, Amount: finance.Money(bal), Category: finance.Category("attack.drain")},
			},
		})
		return err
	}); err != nil {
		t.Fatalf("drain: %v", err)
	}
}

func f832LineCount(f *finance.FinanceAPI) int {
	n := 0
	for _, a := range r2Accounts {
		n += len(f.Lines(a))
	}
	return n
}

// Attack 1a: enact the same policy twice in one tick - the second is
// refused and only ONE cost leaves the treasury.
func TestAttackF832_DoubleEnactSameTickPostsOneCost(t *testing.T) {
	e, comp := wirePoliciesEngine(t, policiesTestSeed)
	fin := comp.state.finance
	before := fin.TotalMoneyInCirculation()
	md0 := comp.state.moneyDelta
	if res := enactOverProtocol(e, "nationalMinimumWage", "citywide", ""); !res.Accepted {
		t.Fatalf("first enact rejected: %+v", res.Error)
	}
	res := enactOverProtocol(e, "nationalMinimumWage", "citywide", "")
	if res.Accepted {
		t.Fatal("second identical enact in the same tick was ACCEPTED (double-spend)")
	}
	if got := int64(before) - int64(fin.TotalMoneyInCirculation()); got != 1_000_000 {
		t.Fatalf("money stock fell by %d, want exactly one cost 1000000", got)
	}
	if got := sumEntries(fin.LinesByCategory(finance.CatOpex), finance.AcctTreasury, finance.SideDebit); got != 1_000_000 {
		t.Fatalf("treasury opex debits %d, want 1000000", got)
	}
	// The published treasury/household mirrors are re-synced at the command
	// (withLedgerDelta), not left stale until the month's finance tick, and
	// the outflow is folded into moneyDelta.
	if comp.state.treasury != ledgerBalance(fin, finance.AcctTreasury) || comp.state.citizenWealth != ledgerBalance(fin, finance.AcctHouseholds) {
		t.Fatalf("money mirrors stale after enact: treasury %d vs ledger %d", comp.state.treasury, ledgerBalance(fin, finance.AcctTreasury))
	}
	if comp.state.moneyDelta != md0-1_000_000 {
		t.Fatalf("moneyDelta %d, want %d (cost folded once)", comp.state.moneyDelta, md0-1_000_000)
	}
	if bp := int64(math.Round(comp.state.policyWageEffect() * 10000)); bp != 500 {
		t.Fatalf("wage effect %d bp after double enact, want exactly 500 (not compounded)", bp)
	}
}

// Attack 1b: enact with the treasury short of the cost (citywide and the
// tax-moving district freeport). Nothing may survive: no enactment, no
// coefficient, no tax multiplier move, no ledger line, money unchanged.
func TestAttackF832_TreasuryShortEnactRollsBackCompletely(t *testing.T) {
	e, comp := wirePoliciesEngine(t, policiesTestSeed)
	st := comp.state
	did := testDistrict(t, comp)
	f832DrainTreasury(t, st)
	fin := st.finance
	before := fin.TotalMoneyInCirculation()
	lines := f832LineCount(fin)
	md := st.moneyDelta
	multBefore, err := st.pol.tax.GetDistrictMultiplier(tax.DistrictID(did), "business-rates")
	if err != nil {
		t.Fatalf("GetDistrictMultiplier: %v", err)
	}
	for _, c := range []struct{ id, kind, district string }{
		{"nationalMinimumWage", "citywide", ""},
		{"freeportTaxFreeHarbour", "district", string(did)},
	} {
		res := enactOverProtocol(e, c.id, c.kind, c.district)
		if res.Accepted {
			t.Fatalf("%s enacted with an empty treasury", c.id)
		}
	}
	if n := len(st.pol.api.ExportState().Enactments); n != 0 {
		t.Fatalf("%d enactments survived the refused enacts", n)
	}
	for _, cs := range st.pol.api.CoefficientState() {
		if cs.Delta != 0 {
			t.Fatalf("coefficient %s left at %v by a refused enact", cs.Key, cs.Delta)
		}
	}
	if m, _ := st.pol.tax.GetDistrictMultiplier(tax.DistrictID(did), "business-rates"); m != multBefore {
		t.Fatalf("freeport district multiplier %v after refused enact, want %v (resetTaxMovesLocked)", m, multBefore)
	}
	if fin.TotalMoneyInCirculation() != before || f832LineCount(fin) != lines || st.moneyDelta != md {
		t.Fatal("a refused enact moved money, posted a line or touched moneyDelta")
	}
	if eff := st.policyWageEffect(); eff != 0 {
		t.Fatalf("wage effect %v after refused enact", eff)
	}
	advanceMonths(t, e, 1) // invariant must hold
}

// Attack 1c (builder-disclosed untested path): the economy.wage.level
// uplift is a treasury -> households PostWages leg. In a month the treasury
// cannot fund it, the leg must SKIP - no money minted, no phantom wage
// taxed - so the enacted city's households end the month exactly where the
// un-enacted twin's (identically drained) do, the treasury never goes
// negative, and double entry still sums to zero.
func TestAttackF832_WageUpliftSkipsWhenTreasuryShort(t *testing.T) {
	eA, cA := wirePoliciesEngine(t, policiesTestSeed)
	eB, cB := wirePoliciesEngine(t, policiesTestSeed)
	if res := enactOverProtocol(eA, "nationalMinimumWage", "citywide", ""); !res.Accepted {
		t.Fatalf("Enact rejected: %+v", res.Error)
	}
	// Positive control: with money in the treasury the uplift DOES pay.
	advanceMonths(t, eA, 1)
	advanceMonths(t, eB, 1)
	ctlA := sumEntries(cA.state.finance.LinesByCategory(finance.CatWages), finance.AcctHouseholds, finance.SideCredit)
	ctlB := sumEntries(cB.state.finance.LinesByCategory(finance.CatWages), finance.AcctHouseholds, finance.SideCredit)
	if ctlA <= ctlB {
		t.Fatalf("control: funded month wages A=%d not above B=%d", ctlA, ctlB)
	}
	hhA0 := ledgerBalance(cA.state.finance, finance.AcctHouseholds)
	hhB0 := ledgerBalance(cB.state.finance, finance.AcctHouseholds)

	skipped := 0
	for m := 0; m < 3; m++ {
		f832DrainTreasury(t, cA.state)
		f832DrainTreasury(t, cB.state)
		advanceMonths(t, eA, 1)
		advanceMonths(t, eB, 1)
		fa, fb := cA.state.finance, cB.state.finance
		_, _, byTreasuryA, _ := r2WageLegs(fa)
		_, _, byTreasuryB, _ := r2WageLegs(fb)
		if ledgerBalance(fa, finance.AcctTreasury) < 0 {
			t.Fatalf("month %d: treasury negative %d", m, ledgerBalance(fa, finance.AcctTreasury))
		}
		if s := r2SumAll(fa); s != 0 {
			t.Fatalf("month %d: double entry sums to %d", m, s)
		}
		if v := fa.FindConservationViolations(); len(v) != 0 {
			t.Fatalf("month %d: conservation violations %+v", m, v)
		}
		if byTreasuryA == byTreasuryB {
			skipped++
		}
		t.Logf("month %d: treasury-paid wages A=%d B=%d", m, byTreasuryA, byTreasuryB)
	}
	if skipped == 0 {
		t.Fatal("no drained month exercised the skip path (precondition)")
	}
	// No phantom uplift reached households across the drained months beyond
	// what the twins' identical histories differ by before the drain.
	dA := ledgerBalance(cA.state.finance, finance.AcctHouseholds) - hhA0
	dB := ledgerBalance(cB.state.finance, finance.AcctHouseholds) - hhB0
	if dA != dB {
		t.Fatalf("drained months: households moved A=%d B=%d; the unfunded uplift leaked", dA, dB)
	}
}

// Attack 2: repeal is exact across many enact/repeal cycles: wage effect,
// park-access satisfaction and the freeport tax multiplier return to their
// exact baseline values each time (no float/bp drift), and a repealed id
// cannot be repealed again.
func TestAttackF832_RepealCyclesNoDrift(t *testing.T) {
	e, comp := wirePoliciesEngine(t, policiesTestSeed)
	st := comp.state
	did := testDistrict(t, comp)
	baseSat, _ := st.attractWellbeing()
	baseMult, err := st.pol.tax.GetDistrictMultiplier(tax.DistrictID(did), "business-rates")
	if err != nil {
		t.Fatal(err)
	}
	repeal := func(id string) protocol.CommandResult {
		return e.HandleCommand(policyCmd(protocol.KindRepealPolicy, protocol.RepealPolicyPayload{EnactmentID: id}))
	}
	for i := 0; i < 15; i++ {
		var ids []string
		for _, c := range []struct{ id, kind, district string }{
			{"nationalMinimumWage", "citywide", ""},
			{"parkAccessProgramme", "district", string(did)},
			{"freeportTaxFreeHarbour", "district", string(did)},
		} {
			if res := enactOverProtocol(e, c.id, c.kind, c.district); !res.Accepted {
				t.Fatalf("cycle %d enact %s: %+v", i, c.id, res.Error)
			}
		}
		for _, en := range st.pol.api.ExportState().Enactments {
			ids = append(ids, string(en.ID))
		}
		if m, _ := st.pol.tax.GetDistrictMultiplier(tax.DistrictID(did), "business-rates"); m != 0 {
			t.Fatalf("cycle %d: freeport multiplier %v while active, want 0", i, m)
		}
		for _, id := range ids {
			if res := repeal(id); !res.Accepted {
				t.Fatalf("cycle %d repeal %s: %+v", i, id, res.Error)
			}
			if res := repeal(id); res.Accepted {
				t.Fatalf("cycle %d: double repeal of %s accepted", i, id)
			}
		}
		if eff := st.policyWageEffect(); eff != 0 {
			t.Fatalf("cycle %d: wage effect %v after repeal", i, eff)
		}
		if s, _ := st.attractWellbeing(); s != baseSat {
			t.Fatalf("cycle %d: satisfaction %v, want exact baseline %v", i, s, baseSat)
		}
		if m, _ := st.pol.tax.GetDistrictMultiplier(tax.DistrictID(did), "business-rates"); m != baseMult {
			t.Fatalf("cycle %d: multiplier %v, want exact %v", i, m, baseMult)
		}
		for _, cs := range st.pol.api.CoefficientState() {
			if cs.Delta != 0 {
				t.Fatalf("cycle %d: stale coefficient %s=%v", i, cs.Key, cs.Delta)
			}
		}
	}
	for _, id := range []string{"enactment-999", "e-1", strings.Repeat("9", 64)} {
		if res := repeal(id); res.Accepted {
			t.Fatalf("repeal of unknown id %q accepted", id)
		}
	}
}

// Attack 3: save/load resurrection both ways, and an older save with no
// policies shard clears live enactments.
func TestAttackF832_SaveLoadResurrection(t *testing.T) {
	e, comp := wirePoliciesEngine(t, policiesTestSeed)
	st := comp.state
	if res := enactOverProtocol(e, "nationalMinimumWage", "citywide", ""); !res.Accepted {
		t.Fatalf("Enact: %+v", res.Error)
	}
	advanceMonths(t, e, 1)
	pre := t.TempDir()
	if err := comp.Save(pre); err != nil {
		t.Fatalf("Save pre: %v", err)
	}
	id := string(st.pol.api.ExportState().Enactments[0].ID)
	if res := e.HandleCommand(policyCmd(protocol.KindRepealPolicy, protocol.RepealPolicyPayload{EnactmentID: id})); !res.Accepted {
		t.Fatalf("Repeal: %+v", res.Error)
	}
	post := t.TempDir()
	if err := comp.Save(post); err != nil {
		t.Fatalf("Save post: %v", err)
	}
	if err := comp.Load(pre); err != nil {
		t.Fatalf("Load pre: %v", err)
	}
	if n := len(st.pol.api.ExportState().Enactments); n != 1 || int64(math.Round(st.policyWageEffect()*10000)) != 500 {
		t.Fatalf("pre-repeal save: %d enactments, wage effect %v; want 1 and 0.05", n, st.policyWageEffect())
	}
	// The resurrected enactment is a real one: it repeals, and re-enacting
	// the same scope is refused while it lives.
	if res := enactOverProtocol(e, "nationalMinimumWage", "citywide", ""); res.Accepted {
		t.Fatal("re-enact over a loaded live enactment accepted")
	}
	if err := comp.Load(post); err != nil {
		t.Fatalf("Load post: %v", err)
	}
	if n := len(st.pol.api.ExportState().Enactments); n != 0 || st.policyWageEffect() != 0 {
		t.Fatalf("post-repeal save resurrected: %d enactments, effect %v", n, st.policyWageEffect())
	}
	// Enactment ids do not restart and collide after a load.
	if res := enactOverProtocol(e, "nationalMinimumWage", "citywide", ""); !res.Accepted {
		t.Fatalf("enact after load: %+v", res.Error)
	}
	if got := string(st.pol.api.ExportState().Enactments[0].ID); got == id {
		t.Fatalf("enactment id %q reused after load", got)
	}
	// Older save (no policies shard) clears a live enactment.
	if err := st.resetPoliciesIfNoShard([]string{"finance", "citizens"}); err != nil {
		t.Fatal(err)
	}
	if n := len(st.pol.api.ExportState().Enactments); n != 0 || st.policyWageEffect() != 0 {
		t.Fatalf("no-shard reset left %d enactments", n)
	}
}

// Attack 4: hostile strings through the REAL codec. Every refusal leaves
// zero ledger lines and an unchanged money stock; the 64-byte boundary is
// inclusive for the composition's MET-G5500 check.
func TestAttackF832_HostileStringsThroughCodec(t *testing.T) {
	e, comp := wirePoliciesEngine(t, policiesTestSeed)
	fin := comp.state.finance
	before := fin.TotalMoneyInCirculation()
	lines := f832LineCount(fin)
	enc := func(cmd protocol.Command) protocol.CommandResult {
		raw, err := protocol.EncodeCommand(cmd)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		dec, err := protocol.DecodeCommand(raw)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		return e.HandleCommand(dec)
	}
	x64, x65 := strings.Repeat("x", 64), strings.Repeat("x", 65)
	cases := []struct {
		name    string
		cmd     protocol.Command
		wantG55 bool
	}{
		{"64-byte id passes boundary, unknown policy", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: x64, ScopeKind: "citywide"}), false},
		{"65-byte id", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: x65, ScopeKind: "citywide"}), true},
		{"1MB id", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: strings.Repeat("y", 1<<20), ScopeKind: "citywide"}), true},
		{"multibyte 33 runes = 66 bytes", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: strings.Repeat("é", 33), ScopeKind: "citywide"}), true},
		{"NUL-prefixed real id", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: "\x00nationalMinimumWage", ScopeKind: "citywide"}), false},
		{"control chars", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: "national\r\nMinimumWage", ScopeKind: "citywide"}), false},
		{"empty scope kind", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: "nationalMinimumWage", ScopeKind: ""}), true},
		{"case-variant scope", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: "nationalMinimumWage", ScopeKind: "Citywide"}), true},
		{"65-byte district", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: "parkAccessProgramme", ScopeKind: "district", District: x65}), true},
		{"unknown 64-byte district", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: "parkAccessProgramme", ScopeKind: "district", District: x64}), false},
		{"65-byte road", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: "congestionCharge", ScopeKind: "road", Road: x65}), true},
		{"citywide policy given district scope", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: "nationalMinimumWage", ScopeKind: "district", District: "d-1"}), false},
		{"empty repeal id", policyCmd(protocol.KindRepealPolicy, protocol.RepealPolicyPayload{EnactmentID: ""}), true},
		{"65-byte repeal id", policyCmd(protocol.KindRepealPolicy, protocol.RepealPolicyPayload{EnactmentID: x65}), true},
	}
	for _, c := range cases {
		res := enc(c.cmd)
		if res.Accepted {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if res.Error == nil || !strings.HasPrefix(res.Error.Code, "MET-") {
			t.Errorf("%s: no registry code: %+v", c.name, res.Error)
			continue
		}
		if got := res.Error.Code == ErrPolicyCommandInvalid; got != c.wantG55 {
			t.Errorf("%s: code %s, want G5500=%v", c.name, res.Error.Code, c.wantG55)
		}
	}
	// Payload of the wrong type for the kind, straight to handleGameplay.
	if err := comp.state.handleGameplay(protocol.Command{Kind: protocol.KindEnactPolicy, Payload: protocol.RepealPolicyPayload{EnactmentID: "x"}}); err == nil {
		t.Error("mismatched payload accepted by handleGameplay")
	}
	if err := comp.state.handleGameplay(protocol.Command{Kind: protocol.KindRepealPolicy, Payload: nil}); err == nil {
		t.Error("nil payload accepted by handleGameplay")
	}
	if n := len(comp.state.pol.api.ExportState().Enactments); n != 0 {
		t.Fatalf("%d enactments after hostile commands", n)
	}
	if fin.TotalMoneyInCirculation() != before || f832LineCount(fin) != lines {
		t.Fatal("a refused hostile command posted a line or moved money")
	}
}

// Attack 3b: Load of a REAL bundle written without the policies shard (every
// save taken before this wiring) must clear a live composition's
// enactments through Load itself (resetPoliciesIfNoShard's call site), not
// only when the helper is called directly.
func TestAttackF832_LoadPrePoliciesSaveClearsEnactments(t *testing.T) {
	e, comp := wirePoliciesEngine(t, policiesTestSeed)
	st := comp.state
	root := t.TempDir()
	clock, err := e.Clock()
	if err != nil {
		t.Fatal(err)
	}
	mode, err := st.gameInit.GameModeWire(st.cid)
	if err != nil {
		t.Fatal(err)
	}
	var old []save.Participant
	for _, p := range comp.Participants() {
		if p.Kind() != kindComposePolicies {
			old = append(old, p)
		}
	}
	if len(old) != len(comp.Participants())-1 {
		t.Fatalf("precondition: policies participant not found among %d", len(comp.Participants()))
	}
	ctx := save.Context{WorldSeed: int64(st.seed), CreatedAtTick: clock.Tick(), GameMonth: clock.Month(), AppVersion: compositionSaveAppVersion, GameMode: mode}
	if err := save.NewManager(root, old, st.cid).SaveManual(ctx, compositionSaveName); err != nil {
		t.Fatalf("pre-policies SaveManual: %v", err)
	}
	if res := enactOverProtocol(e, "nationalMinimumWage", "citywide", ""); !res.Accepted {
		t.Fatalf("Enact: %+v", res.Error)
	}
	if err := comp.Load(root); err != nil {
		t.Fatalf("Load pre-policies save: %v", err)
	}
	if n := len(st.pol.api.ExportState().Enactments); n != 0 || st.policyWageEffect() != 0 {
		t.Fatalf("pre-policies save left %d live enactments (effect %v)", n, st.policyWageEffect())
	}
}

// Attack 6: two same-seed six-month runs with enact, a district policy, a
// mid-run repeal and a forced drift checkpoint hash byte-identically, and
// the drift events come out in the same order.
func TestAttackF832_SixMonthDeterminism(t *testing.T) {
	run := func() ([32]byte, string) {
		e, comp := wirePoliciesEngine(t, policiesTestSeed)
		st := comp.state
		did := testDistrict(t, comp)
		for _, c := range []struct{ id, kind, district string }{
			{"nationalMinimumWage", "citywide", ""},
			{"parkAccessProgramme", "district", string(did)},
			{"lowEmissionZone", "district", string(did)},
			{"congestionCharge", "citywide", ""},
		} {
			if res := enactOverProtocol(e, c.id, c.kind, c.district); !res.Accepted {
				t.Fatalf("enact %s: %+v", c.id, res.Error)
			}
		}
		st.pol.baselines[policyWageCoefficient].set(50)
		advanceMonths(t, e, 3)
		id := string(st.pol.api.ExportState().Enactments[1].ID)
		if res := e.HandleCommand(policyCmd(protocol.KindRepealPolicy, protocol.RepealPolicyPayload{EnactmentID: id})); !res.Accepted {
			t.Fatalf("repeal: %+v", res.Error)
		}
		advanceMonths(t, e, 3)
		raw, err := st.buildPoliciesPatch()
		if err != nil {
			t.Fatal(err)
		}
		return comp.StateDigest(), string(raw)
	}
	d1, v1 := run()
	d2, v2 := run()
	if d1 != d2 {
		t.Fatal("six-month same-seed digests differ")
	}
	if v1 != v2 {
		t.Fatal("six-month same-seed f7.policies views differ (drift/conflict ordering unstable)")
	}
	if !strings.Contains(v1, "driftEvents\":[{") {
		t.Fatalf("precondition: no drift event raised in the run: %.300s", v1)
	}
}
