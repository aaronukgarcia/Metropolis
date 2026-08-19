package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/traffic"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FEAT-206 (docs/planning/icd/engine.traffic-tick.md): the composition-root
// wiring that (a) constructs the real *traffic.TrafficAPI and registers its
// daily AdvanceTick call at the day boundary, closing the MOD-023
// unbounded-demand defect the reset exists to fix, and (b) replaces
// extcommute's documented extCommuteTrafficSeamStub free-flow placeholder
// (extcommute_wire.go) with a real derivation off the composed instance.
// This file is the ONE place that mapping happens, mirroring the FEAT-167
// per-integration bridge-file pattern already established by
// servicesfirms_wire.go and extcommute_wire.go.

// --- traffic construction seam ---------------------------------------------

// loadDefaultTraffic constructs a *traffic.TrafficAPI and loads its config
// from the resolved data/ directory (data/traffic.json, GR#15). This is
// Deps.LoadTraffic's default — Wire calls it unless a caller (a test seam,
// AC-4's "a required module whose construction fails returns
// ErrModuleFailed") injects an override.
func loadDefaultTraffic(correlationID string) (*traffic.TrafficAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	t := traffic.New()
	if err := t.LoadConfig(dir); err != nil {
		return nil, err
	}
	return t, nil
}

// --- daily AdvanceTick hook (ICD §2/§6/§7) ---------------------------------

// trafficTickEffect is the daily traffic-reset tick marker (mirrors
// coldPassEffect/buildEffect's zero-payload shape above): AdvanceTick draws
// nothing from the effect itself, it exists only to move the "run the reset"
// instruction from RunShard (shard 0) to ApplyEffect (the single-goroutine
// barrier), the same shape every other daily hook in this package uses.
type trafficTickEffect struct{}

// trafficTickHook is the PhaseHook that calls TrafficAPI.AdvanceTick once
// per simulated day (ICD engine.traffic-tick.md, FEAT-206). Registered
// against core.PhaseDailyTick, FIRST among this package's PhaseDailyTick
// registrations (see registrationOrder in compose.go) — traffic's own
// doc.go "Day-boundary contract" section requires the reset to run "before
// that day's demand-generating systems... run their own tick logic for the
// day", so this hook's slot in registrationOrder is chosen to precede
// citizens (coldPassHook) and build (buildHook), the two other
// PhaseDailyTick registrations, and (by construction) any future
// demand-generating hook this package adds later. No demand-generating
// hook exists in this package today (extcommute only READS Congestion, it
// never calls AddDemand/AddTripDemand/RegisterTrip — see
// extCommuteTrafficSeamAdapter below), so this ordering is a forward-looking
// application of the doc.go contract rather than something a compose-level
// test can observe breaking today; the ICD's §11 "ordering / day-boundary"
// test is instead expressed as the demand-survives-until-next-AdvanceTick
// behaviour in traffic_wire_test.go, which the phase-registration order
// enables regardless of what (if anything) generates demand.
type trafficTickHook struct {
	st *simState
}

func (h *trafficTickHook) RunShard(shard int) ([]core.Effect, error) {
	if shard != 0 {
		return nil, nil
	}
	return []core.Effect{{Sequence: 0, Payload: trafficTickEffect{}}}, nil
}

func (h *trafficTickHook) ApplyEffect(eff core.Effect) {
	if _, ok := eff.Payload.(trafficTickEffect); !ok {
		return
	}
	st := h.st
	if err := st.traffic.AdvanceTick(st.cid); err != nil {
		// AdvanceTick's only real failure mode is a copied-handle rejection
		// (MET-G4599), which cannot happen given compose's single-owner
		// st.traffic field; log loudly rather than swallow (GR#1) instead of
		// a silent no-op — the same discipline coldPassHook/consumptionHook
		// use above.
		_ = errs.New(ErrModuleFailed, st.cid, map[string]any{"module": "traffic", "cause": err.Error()})
		return
	}
}

// SingleShard implements core.SingleShardHook (BUG-269): RunShard returns
// (nil, nil) for every shard except 0 — the only Effect ever emitted comes
// from shard 0, and AdvanceTick itself is a single map reallocation with no
// per-shard structure (ICD §6 Shard Scope).
func (h *trafficTickHook) SingleShard() bool { return true }

// --- extcommute TrafficSeam adapter (replaces extCommuteTrafficSeamStub) --

// congestionProbeCitizenID is the fixed, non-zero citizen id this adapter
// passes to TrafficAPI.CommuteHours. CommuteHours only rejects citizenID==0
// (ErrUnknownCitizen); it never looks the id up against a citizen store, so
// this probe id needs no correspondence to a real citizen — it exists only
// to satisfy CommuteHours' "not the zero id" precondition.
const congestionProbeCitizenID = uint64(1)

// extCommuteTrafficSeamAdapter implements extcommute.TrafficSeam against a
// real, composed *traffic.TrafficAPI (FEAT-206) — replacing
// extCommuteTrafficSeamStub's documented free-flow placeholder
// (extcommute_wire.go) now that engine.traffic is actually wired into this
// package.
//
// # Derivation formula and its honesty limits
//
// engine.traffic exposes NO per-channel Congestion query — only
// CommuteHours(citizenID, cid), which returns a CITYWIDE coarse figure
// (cfg.BaseCommuteHours * demandMultiplier(), see api.go/doc.go). This
// adapter derives extcommute's [0,1] congestion signal from that single
// citywide multiplier and IGNORES the channel argument entirely — every
// channel extcommute asks about reads the same citywide figure. This is a
// documented seam limitation, not a bug: adding a per-channel method to
// engine.traffic is explicitly OUT OF SCOPE for this integration (FEAT-206's
// brief: "if the API surface makes this impossible without new traffic
// methods, STOP on that seam and report — do not add traffic methods").
//
// baseCommuteHours is captured ONCE, at construction (newExtCommuteTrafficSeamAdapter,
// called from Wire immediately after traffic's config loads and before
// anything could add demand to it): with an empty demand map,
// demandMultiplier()==1.0 exactly (api.go's demandMultiplier: mult = 1.0 +
// totalDemand/1000*0.1, totalDemand==0), so CommuteHours at that moment
// equals cfg.BaseCommuteHours exactly — whatever data/traffic.json actually
// loaded, without this adapter needing a getter for the unexported Config
// field.
//
// Congestion(channel) then reads CommuteHours LIVE on every call and derives:
//
//	ratio      = currentCommuteHours / baseCommuteHours   (== demandMultiplier(), always >= 1.0)
//	congestion = 1 - 1/ratio
//
// demandMultiplier is always >= 1.0 (a saturating non-negative demand sum
// feeding a monotonically non-decreasing multiplier — see AddDemand/
// addDemandLocked's GR#16 saturating-add discipline), so ratio is always
// >= 1 and congestion is always in [0, 1) by construction — never negative,
// never >= 1 — satisfying transportAvailable's own
// "0 <= cong <= 1" range check (extcommute.go) without needing a defensive
// clamp on the happy path. A defensive clamp is still applied below for the
// same "never trust a formula blindly" reason every other numeric boundary
// in this package uses (GR#16): a non-finite/non-positive read (which
// should never happen given LoadConfig's validateConfig guarantees) is
// rejected with a registry error rather than propagated as NaN/Inf into
// extcommute's transport-capacity arithmetic.
//
// Because congestion moves with the exact same demand-accumulation state
// the MOD-023 defect this integration closes was about, extcommute's
// transport-capacity check now genuinely reads LIVE traffic state — proven
// by traffic_wire_test.go's TestFEAT206_ExtCommuteCongestionMovesWithDemand,
// which the old always-0.0 stub could never pass.
type extCommuteTrafficSeamAdapter struct {
	traffic          *traffic.TrafficAPI
	cid              string
	baseCommuteHours float64
}

// newExtCommuteTrafficSeamAdapter constructs the adapter, capturing
// baseCommuteHours via a single CommuteHours read against t while its
// demand map is still empty (the Wire-time precondition documented above).
// Returns the same error CommuteHours itself could return (ErrUnknownCitizen
// never fires given the fixed non-zero probe id; this signature exists so a
// hypothetical future TrafficAPI failure mode is not silently swallowed).
func newExtCommuteTrafficSeamAdapter(t *traffic.TrafficAPI, cid string) (*extCommuteTrafficSeamAdapter, error) {
	base, err := t.CommuteHours(congestionProbeCitizenID, cid)
	if err != nil {
		return nil, err
	}
	if !num.IsFinite(base) || base <= 0 {
		return nil, errs.New(ErrModuleFailed, cid, map[string]any{
			"module": "traffic", "cause": "non-finite or non-positive baseCommuteHours at construction", "value": base,
		})
	}
	return &extCommuteTrafficSeamAdapter{traffic: t, cid: cid, baseCommuteHours: base}, nil
}

// Congestion implements extcommute.TrafficSeam. See the type doc comment
// above for the derivation formula and its honesty limits. channel is
// accepted (to satisfy the interface) but not consulted — see the type doc
// comment's "ignores the channel argument entirely" note.
func (a *extCommuteTrafficSeamAdapter) Congestion(channel string) (float64, error) {
	current, err := a.traffic.CommuteHours(congestionProbeCitizenID, a.cid)
	if err != nil {
		return 0, err
	}
	if !num.IsFinite(current) || current <= 0 {
		return 0, errs.New(ErrModuleFailed, a.cid, map[string]any{
			"module": "traffic", "cause": "non-finite or non-positive commute hours", "channel": channel, "value": current,
		})
	}

	ratio := current / a.baseCommuteHours
	if !num.IsFinite(ratio) || ratio < 1 {
		// Defensive floor (GR#16): demandMultiplier is documented >= 1.0
		// always, so ratio < 1 should never happen, but a formula must never
		// leak a negative congestion figure into extcommute's arithmetic if
		// it somehow did.
		ratio = 1
	}
	cong := 1.0 - 1.0/ratio
	if cong < 0 {
		cong = 0
	}
	if cong > 1 {
		cong = 1
	}
	return cong, nil
}
