package compose

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/policies"
	"github.com/aaronukgarcia/Metropolis/internal/engine/projections"
	"github.com/aaronukgarcia/Metropolis/internal/engine/tax"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// Wire engine.policies (MOD-064) into the composition root
// (FEAT-2326609832, docs/planning/acceptance/wire-engine.policies.md).
//
// What this file does, and what it deliberately does NOT do (disclosure):
//
//   - Constructs TaxAPI + ProjectionsAPI + PoliciesAPI in the order the pack
//     names (finance already exists; tax; projections; policies; then
//     SetProjections/SetFinance/SetTax), registers a baseline curve for every
//     coefficient key the policy library declares, and exposes the library
//     through the "f7.policies" view and the EnactPolicy / RepealPolicy
//     protocol commands (handleGameplay).
//   - Runs PoliciesAPI.AdvanceMonth once per month (policiesHook): the
//     recurring enforcement opex posts, and the quarterly PreviewDrift
//     checkpoint is evaluated on the tick path.
//   - Consumes TWO coefficients so the wiring changes behaviour:
//     economy.wage.level multiplies the finance hook's monthly wage bills
//     (policyWageEffect, read in financeHook.RunShard), and
//     wellbeing.parkAccess multiplies the satisfaction modifier handed to
//     attract (attractWellbeing). Every OTHER coefficient (movement.*,
//     housing.affordable, wellbeing.composite, worklife.weeklyHours,
//     tax.businessRates.districtMultiplier) is enactable and listed but has
//     NO Go consumer yet: movement mode split lives in the TS sim, the 996 /
//     40h lever needs engine.worklife (MOD-080).
//   - Does NOT make engine.tax the revenue poster. finance.CollectTax with
//     incomeNITaxRateBp stays the income-tax path (lead ruling Q3), so the
//     tax-moving policies (freeportTaxFreeHarbour) change a district
//     multiplier inside TaxAPI that nothing in compose reads: enactable, no
//     revenue effect.
//   - Districts have no protocol command yet: a district-scoped policy can be
//     enacted over the wire only against a district that already exists
//     (created through PoliciesAPI by a future districts screen / F8).
//   - The pre-enact preview is not exposed over the protocol: a
//     ViewPatchFunc receives no params and PreviewImpact mutates the shared
//     projections clock, so running it from the subscription pump would race
//     Enact. The view carries what Enact stored (the Computed preview series
//     per active enactment) and the raised PreviewDrift events.

// ErrPolicyCommandInvalid (MET-G5500, owner feat.compositionroot): an
// EnactPolicy / RepealPolicy payload failed the composition root's boundary
// validation (empty or over-length id, unknown scope kind). Engine-side
// validation (unknown policy, scope mismatch, unaffordable cost) keeps
// engine.policies' own registry codes.
const ErrPolicyCommandInvalid = "MET-G5500"

const (
	// policyMaxIDLen bounds every id string a policy command may carry
	// (SEC-195/202 class: unbounded protocol strings).
	policyMaxIDLen = 64

	// policyWageCoefficient / policyParkAccessCoefficient are the two
	// library coefficient keys this composition consumes.
	policyWageCoefficient       = "economy.wage.level"
	policyParkAccessCoefficient = "wellbeing.parkAccess"

	kindComposePolicies = "policies"
	recComposePolicies  = "policies.state"
)

// policyBaseline is one coefficient curve's baseline value: a constant
// placeholder (ASM-284) held in an atomic so a test can force the observed
// curve to diverge from a stored preview (AC-7) without a lock.
type policyBaseline struct {
	bits atomic.Uint64
}

func (b *policyBaseline) Value(int64) (float64, error) {
	return math.Float64frombits(b.bits.Load()), nil
}

func (b *policyBaseline) set(v float64) { b.bits.Store(math.Float64bits(v)) }

// policiesState is the wired policies bundle held on simState.
type policiesState struct {
	api       *policies.PoliciesAPI
	tax       *tax.TaxAPI
	proj      *projections.ProjectionsAPI
	baselines map[string]*policyBaseline // read-only after construction; looked up by key, never ranged
}

// buildPolicies constructs tax, projections and policies in the documented
// order and wires them. It runs inside Wire BEFORE the first hook
// registration, so a failure leaves zero hooks registered.
func buildPolicies(cid string, fin *finance.FinanceAPI) (*policiesState, error) {
	taxAPI, err := tax.LoadDefault(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "policies", "step": "tax.LoadDefault"})
	}
	proj := projections.NewProjectionsAPI(projections.WithCorrelationID(cid))
	pol, err := policies.LoadDefault(cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "policies", "step": "policies.LoadDefault"})
	}
	if err := pol.SetProjections(proj); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "policies", "step": "SetProjections"})
	}
	if err := pol.SetFinance(fin); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "policies", "step": "SetFinance"})
	}
	if err := pol.SetTax(taxAPI); err != nil {
		return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "policies", "step": "SetTax"})
	}

	// One baseline curve per distinct coefficient key the library declares
	// (data-sourced, GR#15), registered in sorted key order (GR#21).
	keySet := make(map[string]struct{})
	for _, info := range pol.Policies() {
		for _, cd := range info.Mechanism {
			keySet[cd.Key] = struct{}{}
		}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	baselines := make(map[string]*policyBaseline, len(keys))
	for _, k := range keys {
		b := &policyBaseline{}
		if err := proj.RegisterCurveProvider(k, b); err != nil {
			return nil, errs.Wrap(ErrModuleFailed, cid, err, map[string]any{"module": "policies", "step": "RegisterCurveProvider", "key": k})
		}
		baselines[k] = b
	}
	return &policiesState{api: pol, tax: taxAPI, proj: proj, baselines: baselines}, nil
}

// --- consumers --------------------------------------------------------

// policyCoefficient returns the combined citywide effect of one coefficient,
// or 0 (and a loud log) when unavailable or non-finite. Safe on a nil
// receiver state (a hand-built simState in a unit test).
func (st *simState) policyCoefficient(key string) float64 {
	if st == nil || st.pol == nil {
		return 0
	}
	v, err := st.pol.api.CombinedEffect(key, policies.Scope{Kind: policies.ScopeCitywide})
	if err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "policies", "op": "CombinedEffect", "key": key, "cause": err.Error()})
		return 0
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// policyWageEffect is economy.wage.level's combined citywide effect, read in
// financeHook.RunShard (never in ApplyEffect, never in a map range).
func (st *simState) policyWageEffect() float64 {
	v := st.policyCoefficient(policyWageCoefficient)
	if v <= -1 { // a >=100% cut would zero or negate wages: clamp to "no effect"
		return 0
	}
	return v
}

// scaleWageBill applies a wage-level effect (a fraction, +0.05 = +5%) to a
// micro-pound wage bill in integer basis points. Effect 0 returns bill
// unchanged (byte-identical to the pre-wiring behaviour).
func scaleWageBill(bill int64, effect float64) int64 {
	if effect == 0 || bill <= 0 {
		return bill
	}
	bp := int64(math.Round(effect * 10000))
	extra := bill/10000*bp + (bill%10000)*bp/10000
	return num.SatAdd(bill, extra)
}

// attractWellbeing is the getter handed to attract.SetWellbeingModifiers: the
// month-start wellbeing pair with the satisfaction modifier multiplied by
// (1 + the combined wellbeing.parkAccess effect). Emigration is untouched.
func (st *simState) attractWellbeing() (float64, float64) {
	sat := st.wellbeingStatus.SatisfactionModifier
	if eff := st.policyCoefficient(policyParkAccessCoefficient); eff != 0 && eff > -1 {
		sat *= 1 + eff
	}
	return sat, st.wellbeingStatus.EmigrationModifier
}

// withLedgerDelta runs fn (which posts to the finance ledger outside the
// finance hook), then re-mirrors the treasury/household pots from the ledger
// and folds the measured change in the tracked money stock into moneyDelta,
// exactly as financeHook does for its own legs, so the same-tick people/money
// conservation check still balances when money leaves to AcctExternal.
func (st *simState) withLedgerDelta(fn func() error) error {
	pre := num.SatAdd(st.treasury, st.citizenWealth)
	err := fn()
	st.syncMoneyFromLedger()
	post := num.SatAdd(st.treasury, st.citizenWealth)
	st.moneyDelta = num.SatAdd(st.moneyDelta, num.SatSub(post, pre))
	return err
}

// --- monthly hook -----------------------------------------------------

type policiesEffect struct{}

// policiesHook advances engine.policies one month on PhaseFinance, after the
// finance hook (so BeginMonth has already opened the month's tick log and the
// opex lines it posts land inside it).
type policiesHook struct {
	st *simState
}

func (h *policiesHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	return []core.Effect{{Sequence: 0, Payload: policiesEffect{}}}, nil
}

func (h *policiesHook) ApplyEffect(eff core.Effect) {
	if _, ok := eff.Payload.(policiesEffect); !ok {
		return
	}
	st := h.st
	if st.pol == nil {
		return
	}
	clock, err := st.e.Clock()
	if err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "policies", "cause": err.Error()})
		return
	}
	if err := st.withLedgerDelta(func() error {
		_, aerr := st.pol.api.AdvanceMonth(clock.Month())
		return aerr
	}); err != nil {
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "policies", "op": "AdvanceMonth", "cause": err.Error()})
	}
}

// --- protocol commands ------------------------------------------------

func (st *simState) policyCommandInvalid(kind protocol.Kind, cause string) error {
	return errs.New(ErrPolicyCommandInvalid, st.cid, map[string]any{"kind": string(kind), "cause": cause})
}

func policyIDOK(s string) bool { return s != "" && len(s) <= policyMaxIDLen }

// handlePolicyCommand serves EnactPolicy / RepealPolicy from handleGameplay.
func (st *simState) handlePolicyCommand(cmd protocol.Command) error {
	if st.pol == nil {
		return st.policyCommandInvalid(cmd.Kind, "policies not wired")
	}
	switch cmd.Kind {
	case protocol.KindEnactPolicy:
		p, ok := cmd.Payload.(protocol.EnactPolicyPayload)
		if !ok {
			return st.policyCommandInvalid(cmd.Kind, "malformed payload")
		}
		if !policyIDOK(p.PolicyID) {
			return st.policyCommandInvalid(cmd.Kind, "policyId is empty or longer than 64 characters")
		}
		if len(p.District) > policyMaxIDLen || len(p.Road) > policyMaxIDLen {
			return st.policyCommandInvalid(cmd.Kind, "district or road id longer than 64 characters")
		}
		scope := policies.Scope{Kind: policies.ScopeKind(p.ScopeKind), District: policies.DistrictID(p.District), Road: policies.RoadID(p.Road)}
		switch scope.Kind {
		case policies.ScopeCitywide, policies.ScopeDistrict, policies.ScopeRoad:
		default:
			return st.policyCommandInvalid(cmd.Kind, "scopeKind must be citywide, district or road")
		}
		return st.withLedgerDelta(func() error {
			_, err := st.pol.api.Enact(policies.PolicyID(p.PolicyID), scope)
			return err
		})
	case protocol.KindRepealPolicy:
		p, ok := cmd.Payload.(protocol.RepealPolicyPayload)
		if !ok {
			return st.policyCommandInvalid(cmd.Kind, "malformed payload")
		}
		if !policyIDOK(p.EnactmentID) {
			return st.policyCommandInvalid(cmd.Kind, "enactmentId is empty or longer than 64 characters")
		}
		return st.pol.api.Repeal(policies.EnactmentID(p.EnactmentID))
	default:
		return st.policyCommandInvalid(cmd.Kind, "unhandled policy kind")
	}
}

// --- save participant -------------------------------------------------

type policiesParticipant struct {
	st *simState
}

func newPoliciesParticipant(st *simState) *policiesParticipant { return &policiesParticipant{st: st} }

func (p *policiesParticipant) Kind() string { return kindComposePolicies }

func (p *policiesParticipant) Source() serialize.RecordSource {
	snap := p.st.pol.api.ExportState()
	emitted := false
	return func() (serialize.Record, bool, error) {
		if emitted {
			return serialize.Record{}, false, nil
		}
		data, err := json.Marshal(snap)
		if err != nil {
			return serialize.Record{}, false, fmt.Errorf("compose: marshalling %s record: %w", recComposePolicies, err)
		}
		emitted = true
		return serialize.Record{Kind: recComposePolicies, Data: data}, true, nil
	}
}

func (p *policiesParticipant) Handler() serialize.RecordHandler {
	return func(rec serialize.Record) error {
		if rec.Kind != recComposePolicies {
			return fmt.Errorf("compose: unknown policies save record kind %q", rec.Kind)
		}
		var snap policies.StateSnapshot
		if err := json.Unmarshal(rec.Data, &snap); err != nil {
			return fmt.Errorf("compose: decoding %s record: %w", rec.Kind, err)
		}
		return p.st.pol.api.ImportState(snap)
	}
}

// resetPoliciesIfNoShard returns policies to clean state when a loaded
// bundle carries no policies shard (every save taken before this wiring):
// save.Manager.Load only calls Handlers for shards the header lists, so
// without this an older save would leave a live composition's enactments in
// place.
func (st *simState) resetPoliciesIfNoShard(shardKinds []string) error {
	for _, k := range shardKinds {
		if k == kindComposePolicies {
			return nil
		}
	}
	return st.pol.api.ImportState(policies.StateSnapshot{LastPostedMonth: -1})
}

// digestPolicies folds the policies state into StateDigest, but only once a
// player has actually used policies: a composition with no enactments,
// districts, events or warnings hashes exactly as it did before the wiring,
// so existing stored determinism-gate digests stay valid.
func (st *simState) digestPolicies(h interface{ Write([]byte) (int, error) }) {
	if st.pol == nil {
		return
	}
	s := st.pol.api.ExportState()
	if len(s.Enactments) == 0 && len(s.Events) == 0 && len(s.Districts) == 0 && len(s.Warnings) == 0 && s.NextEnactmentID <= 1 {
		return
	}
	_, _ = h.Write([]byte("policies.v1\x00"))
	raw, err := json.Marshal(s)
	if err != nil {
		_, _ = h.Write([]byte{0})
		return
	}
	_, _ = h.Write(raw)
}
