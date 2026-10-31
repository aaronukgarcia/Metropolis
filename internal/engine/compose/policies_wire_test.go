package compose

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/policies"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-2326609832 tests for the engine.policies wiring (pack
// docs/planning/acceptance/wire-engine.policies.md, AC-1..AC-10, as amended
// by the lead rulings on the BOW item: AC-2 asserts double-entry balance,
// an empty FindConservationViolations and the invariant - the enactment cost
// LEAVES the economy to AcctExternal so TotalMoneyInCirculation FALLS by it;
// AC-7 is the stored series plus the drift event, not a pre-enact preview
// over the protocol; district-scoped tests create districts through
// PoliciesAPI).

const policiesTestSeed = uint64(2326609832)

func wirePoliciesEngine(t *testing.T, seed uint64) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, comp
}

func policyCmd(kind protocol.Kind, payload protocol.CommandPayload) protocol.Command {
	return protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.NewCorrelationID(), Kind: kind, Payload: payload}
}

func enactOverProtocol(e *core.Engine, id, scopeKind, district string) protocol.CommandResult {
	return e.HandleCommand(policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: id, ScopeKind: scopeKind, District: district}))
}

func advanceMonths(t *testing.T, e *core.Engine, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := e.AdvanceTicks(errs.NewCorrelationID(), core.DailyTicksPerMonth); err != nil {
			t.Fatalf("AdvanceTicks month %d: %v", i, err)
		}
	}
}

func sumEntries(es []finance.Entry, acct finance.AccountID, side finance.Side) int64 {
	var s int64
	for _, e := range es {
		if e.Account == acct && e.Side == side {
			s += int64(e.Amount)
		}
	}
	return s
}

func testDistrict(t *testing.T, comp *Composition) policies.DistrictID {
	t.Helper()
	id, err := comp.state.pol.api.CreateDistrict("Test", []policies.CellRef{{Tile: world.TileCoord{X: 0, Y: 0}, Local: world.CellLocal{Row: 1, Col: 1}}})
	if err != nil {
		t.Fatalf("CreateDistrict: %v", err)
	}
	return id
}

// AC-1: reachability.
func TestPoliciesWire_AC1_ViewAndCommandReachable(t *testing.T) {
	found := false
	for _, n := range RegisteredViewNames() {
		if n == policiesViewSubscriptionName {
			found = true
		}
	}
	if !found {
		t.Fatalf("%q not in RegisteredViewNames %v", policiesViewSubscriptionName, RegisteredViewNames())
	}
	foundKind := 0
	for _, k := range protocol.KnownKinds() {
		if k == protocol.KindEnactPolicy || k == protocol.KindRepealPolicy {
			foundKind++
		}
	}
	if foundKind != 2 {
		t.Fatalf("EnactPolicy/RepealPolicy not both in KnownKinds (found %d)", foundKind)
	}

	e, _ := wirePoliciesEngine(t, policiesTestSeed)
	transport := protocol.NewInProcTransport(protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer, protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { _ = transport.Close() }()
	if _, err := e.StartSubscriptionPump(ctx, transport); err != nil {
		t.Fatalf("StartSubscriptionPump: %v", err)
	}
	go func() { _ = e.RunCommandLoop(ctx, transport) }()
	_, d := subscribeAndAwaitFirstDelta(t, transport, policiesViewSubscriptionName)

	var patch struct {
		Disclosure string `json:"disclosure"`
		Library    []struct {
			ID                   string `json:"id"`
			Name                 string `json:"name"`
			Scope                string `json:"scope"`
			EnactmentMicroPounds int64  `json:"enactmentMicroPounds"`
			Mechanism            []any  `json:"mechanism"`
		} `json:"library"`
	}
	if err := json.Unmarshal(d.Patch, &patch); err != nil {
		t.Fatalf("patch not valid JSON: %v", err)
	}
	if len(patch.Library) != 9 {
		t.Fatalf("library has %d entries, want 9", len(patch.Library))
	}
	for _, p := range patch.Library {
		if p.ID == "" || p.Name == "" || p.Scope == "" || len(p.Mechanism) == 0 {
			t.Fatalf("library entry incomplete: %+v", p)
		}
	}
	if !strings.Contains(patch.Disclosure, "no revenue effect") {
		t.Fatalf("disclosure missing the tax caveat: %q", patch.Disclosure)
	}
}

// AC-2: enact over the protocol debits the one money, double-entry.
func TestPoliciesWire_AC2_EnactDebitsLedgerBalanced(t *testing.T) {
	e, comp := wirePoliciesEngine(t, policiesTestSeed)
	fin := comp.state.finance
	before := fin.TotalMoneyInCirculation()

	res := enactOverProtocol(e, "nationalMinimumWage", "citywide", "")
	if !res.Accepted {
		t.Fatalf("Enact rejected: %+v", res.Error)
	}
	opex := fin.LinesByCategory(finance.CatOpex)
	treasuryDebit := sumEntries(opex, finance.AcctTreasury, finance.SideDebit)
	externalCredit := sumEntries(opex, finance.AcctExternal, finance.SideCredit)
	const cost = int64(1_000_000) // data/policies.json nationalMinimumWage.enactmentMicroPounds
	if treasuryDebit != cost || externalCredit != cost {
		t.Fatalf("opex lines treasury debit=%d external credit=%d, want both %d", treasuryDebit, externalCredit, cost)
	}
	if v := fin.FindConservationViolations(); len(v) != 0 {
		t.Fatalf("FindConservationViolations = %+v, want none", v)
	}
	after := fin.TotalMoneyInCirculation()
	if int64(before)-int64(after) != cost {
		t.Fatalf("TotalMoneyInCirculation fell by %d, want exactly the cost %d (money leaves to AcctExternal)", int64(before)-int64(after), cost)
	}
	// The same-tick people/money invariant must hold with the debit folded
	// into moneyDelta (AdvanceTicks errors on a violation).
	if err := e.AdvanceTicks(errs.NewCorrelationID(), 1); err != nil {
		t.Fatalf("AdvanceTicks after enact (invariant): %v", err)
	}
}

// AC-3: recurring opex posts once per month.
func TestPoliciesWire_AC3_MonthlyOpexOncePerMonth(t *testing.T) {
	e, comp := wirePoliciesEngine(t, policiesTestSeed)
	if res := enactOverProtocol(e, "nationalMinimumWage", "citywide", ""); !res.Accepted {
		t.Fatalf("Enact rejected: %+v", res.Error)
	}
	const opex = int64(50_000)
	total := 0
	for m := 0; m < 3; m++ {
		advanceMonths(t, e, 1)
		n := 0
		for _, l := range comp.state.finance.LinesByCategory(finance.CatOpex) {
			if l.Account == finance.AcctTreasury && l.Side == finance.SideDebit && int64(l.Amount) == opex {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("month %d: %d monthly opex lines, want exactly 1", m, n)
		}
		total += n
	}
	const wantOpexLines = 3 // one line per advanced month in this fixture
	if total != wantOpexLines {
		t.Fatalf("3 months posted %d opex lines, want %d", total, wantOpexLines)
	}
}

func monthlyWageTotals(t *testing.T, e *core.Engine, comp *Composition, months int) []int64 {
	t.Helper()
	out := make([]int64, 0, months)
	for i := 0; i < months; i++ {
		advanceMonths(t, e, 1)
		out = append(out, sumEntries(comp.state.finance.LinesByCategory(finance.CatWages), finance.AcctHouseholds, finance.SideCredit))
	}
	return out
}

// AC-4: the lever changes behaviour (same seed A/B, directional).
func TestPoliciesWire_AC4_MinimumWageRaisesWages(t *testing.T) {
	eA, cA := wirePoliciesEngine(t, policiesTestSeed)
	eB, cB := wirePoliciesEngine(t, policiesTestSeed)
	if res := enactOverProtocol(eA, "nationalMinimumWage", "citywide", ""); !res.Accepted {
		t.Fatalf("Enact rejected: %+v", res.Error)
	}
	a := monthlyWageTotals(t, eA, cA, 6)
	b := monthlyWageTotals(t, eB, cB, 6)
	var sa, sb int64
	for i := range a {
		sa += a[i]
		sb += b[i]
	}
	t.Logf("wages A=%v B=%v", a, b)
	if sb <= 0 {
		t.Fatalf("baseline wage total %d, want > 0 (test precondition)", sb)
	}
	if sa <= sb {
		t.Fatalf("A (minimum wage enacted) wage total %d is not greater than B %d", sa, sb)
	}
}

// AC-5: repeal reverts the lever.
func TestPoliciesWire_AC5_RepealReverts(t *testing.T) {
	eA, cA := wirePoliciesEngine(t, policiesTestSeed)
	eB, cB := wirePoliciesEngine(t, policiesTestSeed)
	if res := enactOverProtocol(eA, "nationalMinimumWage", "citywide", ""); !res.Accepted {
		t.Fatalf("Enact rejected: %+v", res.Error)
	}
	snap := cA.state.pol.api.ExportState()
	if len(snap.Enactments) != 1 {
		t.Fatalf("want 1 enactment, got %d", len(snap.Enactments))
	}
	id := string(snap.Enactments[0].ID)
	a1 := monthlyWageTotals(t, eA, cA, 3)
	b1 := monthlyWageTotals(t, eB, cB, 3)
	if a1[2] <= b1[2] {
		t.Fatalf("precondition: while enacted A %d should exceed B %d", a1[2], b1[2])
	}
	if res := eA.HandleCommand(policyCmd(protocol.KindRepealPolicy, protocol.RepealPolicyPayload{EnactmentID: id})); !res.Accepted {
		t.Fatalf("Repeal rejected: %+v", res.Error)
	}
	if eff := cA.state.policyWageEffect(); eff != 0 {
		t.Fatalf("wage effect after repeal = %v, want 0", eff)
	}
	if n := len(cA.state.pol.api.ExportState().Enactments); n != 0 {
		t.Fatalf("enactments after repeal = %d, want 0", n)
	}
	a2 := monthlyWageTotals(t, eA, cA, 3)
	b2 := monthlyWageTotals(t, eB, cB, 3)
	t.Logf("post-repeal wages A=%v B=%v", a2, b2)
	// The policy's effect is gone: the wage lines are exactly the un-enacted
	// city's (the cost's second-order money effects do not reach wages).
	for i := range a2 {
		if a2[i] != b2[i] {
			t.Fatalf("month %d after repeal: A %d != B %d (uplift persisted)", i, a2[i], b2[i])
		}
	}
}

// AC-6: parkAccessProgramme feeds the satisfaction getter attract consumes.
func TestPoliciesWire_AC6_ParkAccessLiftsSatisfaction(t *testing.T) {
	e, comp := wirePoliciesEngine(t, policiesTestSeed)
	st := comp.state
	baseSat, baseEmi := st.attractWellbeing()
	if baseSat <= 0 {
		t.Fatalf("precondition: baseline satisfaction modifier %v must be > 0", baseSat)
	}
	did := testDistrict(t, comp)
	if res := enactOverProtocol(e, "parkAccessProgramme", "district", string(did)); !res.Accepted {
		t.Fatalf("Enact rejected: %+v", res.Error)
	}
	sat, emi := st.attractWellbeing()
	if sat <= baseSat {
		t.Fatalf("satisfaction %v not above baseline %v after parkAccessProgramme", sat, baseSat)
	}
	if emi != baseEmi {
		t.Fatalf("emigration modifier changed %v -> %v, must be untouched", baseEmi, emi)
	}
	id := string(st.pol.api.ExportState().Enactments[0].ID)
	if res := e.HandleCommand(policyCmd(protocol.KindRepealPolicy, protocol.RepealPolicyPayload{EnactmentID: id})); !res.Accepted {
		t.Fatalf("Repeal rejected: %+v", res.Error)
	}
	if sat2, _ := st.attractWellbeing(); sat2 != baseSat {
		t.Fatalf("satisfaction after repeal %v, want baseline %v", sat2, baseSat)
	}
}

// AC-7 (as amended): the view carries the stored at-enact series, and a
// forced divergence raises a queryable drift event.
func TestPoliciesWire_AC7_StoredSeriesAndDriftEvent(t *testing.T) {
	e, comp := wirePoliciesEngine(t, policiesTestSeed)
	st := comp.state
	if res := enactOverProtocol(e, "nationalMinimumWage", "citywide", ""); !res.Accepted {
		t.Fatalf("Enact rejected: %+v", res.Error)
	}
	raw, err := st.buildPoliciesPatch()
	if err != nil {
		t.Fatalf("buildPoliciesPatch: %v", err)
	}
	var v struct {
		Previews []struct {
			Series []struct {
				Key    string
				Points []struct{ Month int64 }
			}
		} `json:"previews"`
		DriftEvents []any `json:"driftEvents"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Previews) != 1 || len(v.Previews[0].Series) == 0 || len(v.Previews[0].Series[0].Points) == 0 {
		t.Fatalf("view carries no stored preview series: %s", raw)
	}
	if len(v.DriftEvents) != 0 {
		t.Fatalf("drift events before divergence: %d", len(v.DriftEvents))
	}
	// Force the observed curve to diverge, then run past the quarterly checkpoint.
	st.pol.baselines[policyWageCoefficient].set(50)
	advanceMonths(t, e, 4)
	if n := len(st.pol.api.PreviewDriftEvents()); n == 0 {
		t.Fatal("no PreviewDrift event raised after forced divergence + checkpoint")
	}
	raw, _ = st.buildPoliciesPatch()
	v.DriftEvents = nil
	_ = json.Unmarshal(raw, &v)
	if len(v.DriftEvents) == 0 {
		t.Fatalf("drift event not in the view: %s", raw)
	}
}

// AC-8: conflict warning surfaces in the view.
func TestPoliciesWire_AC8_ConflictWarningInView(t *testing.T) {
	e, comp := wirePoliciesEngine(t, policiesTestSeed)
	did := testDistrict(t, comp)
	if res := enactOverProtocol(e, "lowEmissionZone", "district", string(did)); !res.Accepted {
		t.Fatalf("Enact LEZ rejected: %+v", res.Error)
	}
	if res := enactOverProtocol(e, "congestionCharge", "citywide", ""); !res.Accepted {
		t.Fatalf("Enact congestionCharge rejected (a conflict must warn, not block): %+v", res.Error)
	}
	raw, err := comp.state.buildPoliciesPatch()
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Conflicts []struct{ EnactedPolicy, ConflictWith string } `json:"conflicts"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Conflicts) == 0 || v.Conflicts[0].EnactedPolicy != "congestionCharge" || v.Conflicts[0].ConflictWith != "lowEmissionZone" {
		t.Fatalf("conflict warning missing from view: %+v", v.Conflicts)
	}
}

// AC-9: save/load round trip and same-seed determinism.
func TestPoliciesWire_AC9_SaveLoadAndDeterminism(t *testing.T) {
	run := func() [32]byte {
		e, comp := wirePoliciesEngine(t, policiesTestSeed)
		if res := enactOverProtocol(e, "nationalMinimumWage", "citywide", ""); !res.Accepted {
			t.Fatalf("Enact rejected: %+v", res.Error)
		}
		advanceMonths(t, e, 2)
		return comp.StateDigest()
	}
	// Two independent same-seed runs (run wires a fresh engine per call).
	first := run()
	second := run()
	if first != second {
		t.Fatal("two same-seed runs produced different digests")
	}

	e, comp := wirePoliciesEngine(t, policiesTestSeed)
	if res := enactOverProtocol(e, "nationalMinimumWage", "citywide", ""); !res.Accepted {
		t.Fatalf("Enact rejected: %+v", res.Error)
	}
	advanceMonths(t, e, 2)
	root := t.TempDir()
	if err := comp.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	want := comp.StateDigest()
	// Wipe policies, then Load must resurrect them from the shard.
	if err := comp.state.pol.api.ImportState(policies.StateSnapshot{LastPostedMonth: -1}); err != nil {
		t.Fatal(err)
	}
	if comp.StateDigest() == want {
		t.Fatal("precondition: wiping policies must change the digest (digest covers policies)")
	}
	if err := comp.Load(root); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := comp.StateDigest(); got != want {
		t.Fatal("digest after Load differs from digest at Save")
	}
	if n := len(comp.state.pol.api.ExportState().Enactments); n != 1 {
		t.Fatalf("enactments after Load = %d, want 1", n)
	}
}

// AC-10: invalid input fails closed with a registry code, state unchanged.
func TestPoliciesWire_AC10_InvalidInputFailsClosed(t *testing.T) {
	e, comp := wirePoliciesEngine(t, policiesTestSeed)
	fin := comp.state.finance
	before := fin.TotalMoneyInCirculation()
	long := strings.Repeat("x", 65)
	cases := []struct {
		name string
		cmd  protocol.Command
	}{
		{"unknown policy", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: "noSuchPolicy", ScopeKind: "citywide"})},
		{"over-length id", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: long, ScopeKind: "citywide"})},
		{"empty id", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: "", ScopeKind: "citywide"})},
		{"bad scope kind", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: "nationalMinimumWage", ScopeKind: "galaxy"})},
		{"district policy without district", policyCmd(protocol.KindEnactPolicy, protocol.EnactPolicyPayload{PolicyID: "parkAccessProgramme", ScopeKind: "district"})},
		{"unknown enactment repeal", policyCmd(protocol.KindRepealPolicy, protocol.RepealPolicyPayload{EnactmentID: "nope"})},
	}
	for _, c := range cases {
		res := e.HandleCommand(c.cmd)
		if res.Accepted {
			t.Errorf("%s: accepted, want refusal", c.name)
			continue
		}
		if res.Error == nil || !strings.HasPrefix(res.Error.Code, "MET-") {
			t.Errorf("%s: error %+v lacks a registry MET- code", c.name, res.Error)
		}
	}
	if n := len(comp.state.pol.api.ExportState().Enactments); n != 0 {
		t.Fatalf("enactments after refusals = %d, want 0", n)
	}
	if len(fin.LinesByCategory(finance.CatOpex)) != 0 || fin.TotalMoneyInCirculation() != before {
		t.Fatal("a refused command left a ledger line or changed the money stock")
	}
}

// scaleWageBill is exact integer basis-point arithmetic.
func TestPoliciesWire_ScaleWageBill(t *testing.T) {
	if got := scaleWageBill(100_000_000, 0.05); got != 105_000_000 {
		t.Fatalf("scaleWageBill(+5%%) = %d, want 105000000", got)
	}
	if got := scaleWageBill(100_000_000, 0); got != 100_000_000 {
		t.Fatalf("zero effect changed the bill: %d", got)
	}
	if got := scaleWageBill(0, 0.05); got != 0 {
		t.Fatalf("zero bill scaled to %d", got)
	}
}
