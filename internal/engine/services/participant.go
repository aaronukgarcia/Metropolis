package services

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-build-services-bridge-2026-09-02 round remedy (root fix) — engine.
// services becomes its OWN save.Participant, mirroring the engine.crime /
// engine.finance / engine.citizens pattern exactly (this file is deliberately
// structured like crime/participant.go, the closest-sized precedent: a
// handful of map-backed collections, no long-lived RNG). Serialization here
// is DATA-ONLY: engine.services draws no det.Stream and holds no RNG cursor
// anywhere in api.go/staffing.go/coverage.go, so there is nothing
// RNG-shaped to persist (mirrors the RNG-stateless finding already recorded
// against engine.crime/engine.finance).
//
// GR#25 outbound edge this file introduces: engine.services ->
// int.serializer (moduleGuid 8ee49e96-0de9-4326-9b90-e94622874f94). The
// build lane correctly STOPPED at this missing edge rather than
// hand-registering it; the lead registered it in master-plan-v2.1.json /
// code.json (commit 73696fd, independently verified — edge-lint's
// EDGE-LINT-001 finding for this import resolves against that registry),
// mirroring engine.crime's own entry.
//
// # Durable-vs-derived analysis (verified field-by-field against ServicesAPI,
// serviceInstance, demandRecord in api.go / service.go / coverage.go /
// staffing.go)
//
//	DURABLE — serialized:
//	  kinds (one "services.kind" record per entry, sorted by ServiceKind
//	  lexically — ServiceKind's underlying type is string, so a lexical sort
//	  IS the numeric-equivalent stable order GR#21 asks for): the FULL
//	  registered kind table, built-ins AND any synthetic kind a caller
//	  registered via RegisterKind (AC-2's extensibility contract — a
//	  synthetic kind registered at runtime is exactly the class of state a
//	  fresh New()'s built-in reseed does NOT reproduce). resetForLoad clears
//	  this map to empty and every save unconditionally re-emits every entry
//	  (including built-ins), so a load never depends on New()'s defaults
//	  agreeing with what was actually registered at save time.
//	  instances (one "services.instance" record per entry, sorted by
//	  ServiceID lexically): the full serviceInstance — spec (every
//	  ServiceSpec field, including the UpgradePath slice), currentUpgrade
//	  (AC-9's upgrade progress — re-deriving it from build's serviceByOrder
//	  sweep would silently reset every already-upgraded service back to step
//	  0), funding (AC-1/AC-12's slider setting — the whole point of this
//	  participant: BUG-586's registerCompletedServicesLocked sweep only ever
//	  re-derives a FRESH ServiceSpecFromBuilding at funding=0/demand=0/
//	  currentUpgrade=0, so a live-composition rewind that relied solely on
//	  the sweep would silently zero every funded/upgraded/demand-carrying
//	  service even for orders the sweep DOES re-track), demand/demandDist
//	  (UpdateDemand's last-pushed input — queryable via Demand() between
//	  ticks, so must round-trip like engine.crime's SecurityInput), and
//	  allocated (AllocateStaffing's last output — queryable via
//	  StaffingAllocations() WITHOUT re-running the allocation, so it cannot
//	  be recomputed on load without re-invoking a caller-driven command that
//	  a bare Load never re-issues).
//	  districtDemand (one "services.districtdemand" record per (district,
//	  service) pair, sorted by DistrictID then ServiceID lexically): every
//	  caller-pushed demand record UpdateDistrictDemand installed — read by
//	  CoverageByDistrict/CoverageForDistrict, and never re-derivable (this
//	  package performs no spatial read of its own, AC-21).
//	  poolAvailable (one "services.pool" record per entry, sorted by pool id
//	  lexically): SetPoolStaff's last-pushed per-pool availability — queryable
//	  via StaffingAllocations/AllocateStaffing between ticks, mirroring
//	  crime's SecurityInput precedent exactly.
//
//	DERIVED / CONFIG / INJECTED / RUNTIME — NOT serialized (excluded, with
//	the reason recorded here in place of a separate field-parity drift test
//	allowlist):
//	  - correlationID: per-instance error correlation, not simulation state.
//	  - pools ([]StaffingPool) / pie ([]PieBenchmark) / wagePerStaffMicropounds
//	    / severityHalfPoint: immutable data (data/services.json), reloaded by
//	    [Load] — a save must not pin old balance rules (FEAT-1972079897),
//	    mirroring engine.crime's excluded cfg.
//	  - gate (UnlockGate): an INJECTED dependency (an interface wired by the
//	    composition root via SetUnlockGate), re-wired on load — never part of
//	    a save, mirroring engine.crime's excluded prison dependency.
//	  - mu: runtime lock, not state.
//	  - self: SEC-020 copy-guard pointer, re-armed by New/Load.
//
// Every wire projection carries explicit json tags: the domain structs
// (ServiceSpec / UpgradeStep / KindDef / demandRecord) are never marshalled
// directly, mirroring every prior participant's convention.
//
// # BUG-586 interaction (reconciliation rule)
//
// This participant does NOT remove the compose.Load -> BuildAPI.
// RegisterCompletedServices() call (compose/save_wire.go) — that call stays,
// and the two are made IDEMPOTENT together by the ALREADY-idempotent
// registerServiceLocked (build/build.go): RegisterService returning
// ErrDuplicateService is treated as a no-op success. With this participant
// wired BEFORE the RegisterCompletedServices call in Composition.Load (load
// order below), the reconciliation is:
//
//   - A service THIS participant restores, and the sweep ALSO wants to
//     register (order still complete+standing in the restored build queue):
//     the sweep's RegisterService call hits ErrDuplicateService against the
//     id THIS participant already installed, and registerServiceLocked
//     treats that as success — a no-op. The RESTORED funding/currentUpgrade/
//     demand/allocated state WINS (the sweep never overwrites an existing
//     instance; RegisterService only ever inserts a NEW entry). This closes
//     the exact wedge the round found: a rewind-load into a live composition
//     no longer loses funding/upgrade/demand state to the sweep's fresh-spec
//     re-derivation.
//   - A service THIS participant restores, but the loaded build queue no
//     longer marks the underlying order complete+standing (a save taken
//     AFTER a later demolition that a rewind-to-an-earlier-save then
//     re-applies, or a savepoint from before a later game-state change):
//     this participant's resetForLoad+applyLoadRecord sequence REPLACES the
//     live services instance table wholesale (see resetForLoad below) — a
//     loaded-but-no-longer-standing entry restored by THIS participant, that
//     the sweep does NOT ALSO want (order not standing), is exactly the
//     stale carry-over a whole-state replace is designed to avoid holding.
//     Per the bridge design, BUILD's own state (b.structures — the
//     authoritative "is this order's building still standing" record, which
//     already round-trips exactly through build's own save schema) is
//     authoritative for standing structures: a service instance surviving a
//     Load with no corresponding standing build order is a genuine
//     conflict the composition root has an existing remedy for — build's own
//     demolish path (SubmitDemolishCommand) is the only caller that removes
//     a services instance, and a save/load round-trip through THIS
//     participant alone does not re-run demolition. This is documented,
//     not silently patched here, because closing it fully requires a
//     cross-participant reconciliation pass (comparing the restored
//     instances table against the restored build.structures set) that
//     belongs in compose/save_wire.go's Load, alongside the existing
//     RegisterCompletedServices call — see the compose-side comment this
//     participant's wiring adds.
//   - The PRE-participant baseline (an old save with no "services.*" shard
//     at all): Source() never emits a services.* record for a bundle saved
//     before this participant existed, so Handler() never runs
//     resetForLoad/applyLoadRecord for services — the live ServicesAPI this
//     participant wraps is left exactly as SetServices/New constructed it
//     (empty instance table), and the UNCHANGED RegisterCompletedServices
//     sweep is the sole rebuild path, precisely the "old-savepoint defaults
//     explicit / migration path preserved" behaviour this round's fix must
//     not regress.
//
// SaveParticipant does NOT import internal/engine/save: it satisfies
// save.Participant STRUCTURALLY (Kind/Source/Handler), consuming only
// internal/foundation/serialize's Record/RecordSource/RecordHandler
// vocabulary.

const (
	// KindServices is this participant's stable shard label.
	KindServices = "services"

	recServicesKind           = "services.kind"
	recServicesInstance       = "services.instance"
	recServicesDistrictDemand = "services.districtdemand"
	recServicesPool           = "services.pool"
)

// kindWire is one kinds entry on the wire. The map key (ServiceKind) is
// carried in the record so a load reconstructs the map from the record
// alone.
type kindWire struct {
	Kind      ServiceKind `json:"kind"`
	Name      string      `json:"name"`
	Benchmark string      `json:"benchmark"`
}

// upgradeStepWire is one UpgradeStep on the wire.
type upgradeStepWire struct {
	BuildingID      string  `json:"buildingID"`
	Name            string  `json:"name"`
	Milestone       int     `json:"milestone"`
	CapacityCeiling float64 `json:"capacityCeiling"`
}

// serviceInstanceWire is one instances entry on the wire: the service's
// full spec plus its runtime-mutated fields. The map key (ServiceID) is
// carried in the record so a load reconstructs the map from the record
// alone.
type serviceInstanceWire struct {
	ID             ServiceID         `json:"id"`
	Kind           ServiceKind       `json:"kind"`
	CapacityRaw    string            `json:"capacityRaw"`
	CoverageRadius float64           `json:"coverageRadius"`
	X              float64           `json:"x"`
	Y              float64           `json:"y"`
	Milestone      int               `json:"milestone"`
	StaffingNeed   float64           `json:"staffingNeed"`
	UpgradePath    []upgradeStepWire `json:"upgradePath"`
	CurrentUpgrade int               `json:"currentUpgrade"`
	Funding        float64           `json:"funding"`
	Demand         float64           `json:"demand"`
	DemandDist     float64           `json:"demandDist"`
	Allocated      float64           `json:"allocated"`
}

// districtDemandWire is one (district, service) pushed-demand pair on the
// wire.
type districtDemandWire struct {
	District DistrictID `json:"district"`
	Service  ServiceID  `json:"service"`
	Demand   float64    `json:"demand"`
	Distance float64    `json:"distance"`
}

// poolWire is one pool's last-pushed availability on the wire.
type poolWire struct {
	PoolID    string  `json:"poolID"`
	Available float64 `json:"available"`
}

// upgradePathToWire projects an UpgradeStep slice to its wire form.
func upgradePathToWire(steps []UpgradeStep) []upgradeStepWire {
	if steps == nil {
		return nil
	}
	out := make([]upgradeStepWire, len(steps))
	for i, s := range steps {
		// UpgradeStep and upgradeStepWire share an identical field sequence
		// (name/type), so this is a plain field-order conversion, not an
		// alias of the domain type on the wire (staticcheck S1016).
		out[i] = upgradeStepWire(s)
	}
	return out
}

// upgradePathFromWire reconstructs an UpgradeStep slice from its wire form.
func upgradePathFromWire(steps []upgradeStepWire) []UpgradeStep {
	if steps == nil {
		return nil
	}
	out := make([]UpgradeStep, len(steps))
	for i, s := range steps {
		out[i] = UpgradeStep(s)
	}
	return out
}

// instanceToWire projects a serviceInstance to its wire form.
func instanceToWire(id ServiceID, inst *serviceInstance) serviceInstanceWire {
	return serviceInstanceWire{
		ID:             id,
		Kind:           inst.spec.Kind,
		CapacityRaw:    inst.spec.CapacityRaw,
		CoverageRadius: inst.spec.CoverageRadius,
		X:              inst.spec.X,
		Y:              inst.spec.Y,
		Milestone:      inst.spec.Milestone,
		StaffingNeed:   inst.spec.StaffingNeed,
		UpgradePath:    upgradePathToWire(inst.spec.UpgradePath),
		CurrentUpgrade: inst.currentUpgrade,
		Funding:        inst.funding,
		Demand:         inst.demand,
		DemandDist:     inst.demandDist,
		Allocated:      inst.allocated,
	}
}

// servicesSnapshot is a point-in-time, deterministically-ordered copy of
// ServicesAPI's mutable state, taken under the lock in one shot. Every
// map-backed collection is flattened to a slice sorted by (lexical) key —
// ServiceKind/ServiceID/DistrictID/pool-id are all plain strings, so a
// lexical sort IS the stable GR#21 order.
type servicesSnapshot struct {
	kinds     []kindWire
	instances []serviceInstanceWire
	demand    []districtDemandWire
	pools     []poolWire
}

// total is the number of records the snapshot emits.
func (s *servicesSnapshot) total() int {
	return len(s.kinds) + len(s.instances) + len(s.demand) + len(s.pools)
}

// locate maps a global record index to its (Kind, wire value) without
// encoding anything.
func (s *servicesSnapshot) locate(i int) (string, any) {
	if i < len(s.kinds) {
		return recServicesKind, s.kinds[i]
	}
	i -= len(s.kinds)
	if i < len(s.instances) {
		return recServicesInstance, s.instances[i]
	}
	i -= len(s.instances)
	if i < len(s.demand) {
		return recServicesDistrictDemand, s.demand[i]
	}
	i -= len(s.demand)
	return recServicesPool, s.pools[i]
}

// recordAt marshals exactly the i-th record of the deterministic emission
// sequence (kinds, instances, districtDemand, pools).
func (s *servicesSnapshot) recordAt(i int) (serialize.Record, error) {
	kind, value := s.locate(i)
	data, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("services: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: data}, nil
}

// snapshotForSave copies the full mutable state into a
// deterministically-ordered servicesSnapshot under the read lock.
func (a *ServicesAPI) snapshotForSave() (servicesSnapshot, error) {
	if err := a.checkNotCopied("snapshotForSave"); err != nil {
		return servicesSnapshot{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	var snap servicesSnapshot

	kindKeys := make([]ServiceKind, 0, len(a.kinds))
	for k := range a.kinds {
		kindKeys = append(kindKeys, k)
	}
	sort.Slice(kindKeys, func(i, j int) bool { return kindKeys[i] < kindKeys[j] })
	snap.kinds = make([]kindWire, 0, len(kindKeys))
	for _, k := range kindKeys {
		def := a.kinds[k]
		snap.kinds = append(snap.kinds, kindWire{Kind: k, Name: def.Name, Benchmark: def.Benchmark})
	}

	instIDs := make([]ServiceID, 0, len(a.instances))
	for id := range a.instances {
		instIDs = append(instIDs, id)
	}
	sort.Slice(instIDs, func(i, j int) bool { return instIDs[i] < instIDs[j] })
	snap.instances = make([]serviceInstanceWire, 0, len(instIDs))
	for _, id := range instIDs {
		snap.instances = append(snap.instances, instanceToWire(id, a.instances[id]))
	}

	districtIDs := make([]DistrictID, 0, len(a.districtDemand))
	for d := range a.districtDemand {
		districtIDs = append(districtIDs, d)
	}
	sort.Slice(districtIDs, func(i, j int) bool { return districtIDs[i] < districtIDs[j] })
	for _, d := range districtIDs {
		records := a.districtDemand[d]
		svcIDs := make([]ServiceID, 0, len(records))
		for sid := range records {
			svcIDs = append(svcIDs, sid)
		}
		sort.Slice(svcIDs, func(i, j int) bool { return svcIDs[i] < svcIDs[j] })
		for _, sid := range svcIDs {
			rec := records[sid]
			snap.demand = append(snap.demand, districtDemandWire{
				District: d,
				Service:  sid,
				Demand:   rec.demand,
				Distance: rec.distance,
			})
		}
	}

	poolIDs := make([]string, 0, len(a.poolAvailable))
	for p := range a.poolAvailable {
		poolIDs = append(poolIDs, p)
	}
	sort.Strings(poolIDs)
	snap.pools = make([]poolWire, 0, len(poolIDs))
	for _, p := range poolIDs {
		snap.pools = append(snap.pools, poolWire{PoolID: p, Available: a.poolAvailable[p]})
	}

	return snap, nil
}

// resetForLoad clears the mutable state to EMPTY (never reseeded with the
// built-in kinds — see this file's doc comment: the kinds records
// themselves carry the built-ins forward) under the write lock, before a
// Load streams records in. gate/pools/pie/wagePerStaffMicropounds/
// severityHalfPoint/correlationID are left untouched: they are
// re-established by the composition root / Load, not part of a save.
func (a *ServicesAPI) resetForLoad() error {
	if err := a.checkNotCopied("resetForLoad"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.kinds = make(map[ServiceKind]KindDef)
	a.instances = make(map[ServiceID]*serviceInstance)
	a.districtDemand = make(map[DistrictID]map[ServiceID]demandRecord)
	a.poolAvailable = make(map[string]float64)
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect
// directly into the state under the write lock — one record at a time.
// Every float field is validated finite (SEC-093/GR#16 — a NaN/±Inf value
// decoded from a corrupted save must never reach the quality/staffing
// arithmetic), a funding value outside [0,1] is rejected with the SAME
// registry code SetFunding already uses (ErrInvalidFunding — no new code
// minted), and an instance naming a kind not yet installed by an earlier
// "services.kind" record (kinds are always emitted first — see the
// deterministic emission order above) is rejected with ErrUnknownServiceKind,
// the same referential-integrity check RegisterService performs live.
// Returns a decode/kind error verbatim so ReadShard fails loud and closed
// rather than loading a partial state silently.
func (a *ServicesAPI) applyLoadRecord(rec serialize.Record) error {
	if err := a.checkNotCopied("applyLoadRecord"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	switch rec.Kind {
	case recServicesKind:
		var w kindWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("services: decoding %s record: %w", rec.Kind, err)
		}
		if w.Kind == "" {
			return serviceErr(a.correlationID, ErrUnknownServiceKind, map[string]any{"kind": string(w.Kind), "step": "load"})
		}
		a.kinds[w.Kind] = KindDef{Name: w.Name, Benchmark: w.Benchmark}

	case recServicesInstance:
		var w serviceInstanceWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("services: decoding %s record: %w", rec.Kind, err)
		}
		if field := nonFiniteInstanceWireField(w); field != "" {
			return serviceErr(a.correlationID, ErrNonFiniteInput, map[string]any{"field": field, "service": string(w.ID), "step": "load"})
		}
		if w.Funding < 0 || w.Funding > 1 {
			return serviceErr(a.correlationID, ErrInvalidFunding, map[string]any{"service": string(w.ID), "level": w.Funding, "step": "load"})
		}
		if _, ok := a.kinds[w.Kind]; !ok {
			return serviceErr(a.correlationID, ErrUnknownServiceKind, map[string]any{"kind": string(w.Kind), "service": string(w.ID), "step": "load"})
		}
		path := upgradePathFromWire(w.UpgradePath)
		if w.CurrentUpgrade < 0 || (len(path) == 0 && w.CurrentUpgrade != 0) || (len(path) > 0 && w.CurrentUpgrade >= len(path)) {
			return serviceErr(a.correlationID, ErrUpgradeUnavailable, map[string]any{"service": string(w.ID), "currentUpgrade": w.CurrentUpgrade, "step": "load"})
		}
		a.instances[w.ID] = &serviceInstance{
			spec: ServiceSpec{
				ID:             w.ID,
				Kind:           w.Kind,
				CapacityRaw:    w.CapacityRaw,
				CoverageRadius: w.CoverageRadius,
				X:              w.X,
				Y:              w.Y,
				Milestone:      w.Milestone,
				StaffingNeed:   w.StaffingNeed,
				UpgradePath:    path,
			},
			currentUpgrade: w.CurrentUpgrade,
			funding:        w.Funding,
			demand:         w.Demand,
			demandDist:     w.DemandDist,
			allocated:      w.Allocated,
		}

	case recServicesDistrictDemand:
		var w districtDemandWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("services: decoding %s record: %w", rec.Kind, err)
		}
		if !num.IsFinite(w.Demand) || !num.IsFinite(w.Distance) {
			return serviceErr(a.correlationID, ErrNonFiniteInput, map[string]any{"field": "districtDemand", "district": string(w.District), "service": string(w.Service), "step": "load"})
		}
		if a.districtDemand[w.District] == nil {
			a.districtDemand[w.District] = make(map[ServiceID]demandRecord)
		}
		a.districtDemand[w.District][w.Service] = demandRecord{demand: w.Demand, distance: w.Distance}

	case recServicesPool:
		var w poolWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("services: decoding %s record: %w", rec.Kind, err)
		}
		if !num.IsFinite(w.Available) {
			return serviceErr(a.correlationID, ErrNonFiniteInput, map[string]any{"field": "poolAvailable", "pool": w.PoolID, "step": "load"})
		}
		a.poolAvailable[w.PoolID] = w.Available

	default:
		return fmt.Errorf("services: unknown services save record kind %q", rec.Kind)
	}
	return nil
}

// nonFiniteInstanceWireField returns the name of the first non-finite float
// field in w, or "" when every float field (including every upgrade step's
// capacity ceiling) is finite — the load-time mirror of
// nonFiniteSpecField/RegisterService's boundary guard (SEC-093).
func nonFiniteInstanceWireField(w serviceInstanceWire) string {
	switch {
	case !num.IsFinite(w.CoverageRadius):
		return "coverageRadius"
	case !num.IsFinite(w.X), !num.IsFinite(w.Y):
		return "location"
	case !num.IsFinite(w.StaffingNeed):
		return "staffingNeed"
	case !num.IsFinite(w.Funding):
		return "funding"
	case !num.IsFinite(w.Demand):
		return "demand"
	case !num.IsFinite(w.DemandDist):
		return "demandDist"
	case !num.IsFinite(w.Allocated):
		return "allocated"
	}
	for _, step := range w.UpgradePath {
		if !num.IsFinite(step.CapacityCeiling) {
			return "upgradePath.capacityCeiling"
		}
	}
	return ""
}

// SaveParticipant adapts a *ServicesAPI to the save.Participant contract
// (Kind/Source/Handler) without this package importing engine/save — the
// interface is satisfied structurally. Construct via NewSaveParticipant; the
// wrapped ServicesAPI is the live state Source snapshots on save and the
// target Handler rebuilds on load.
type SaveParticipant struct {
	a *ServicesAPI
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing a's
// state. On save it snapshots a; on load it resets a's runtime state and
// rebuilds it from the streamed records — so a load target is typically a
// FRESH New/Load of the same data directory whose empty runtime state is
// replaced by the saved one (then re-wired by the composition root via
// SetUnlockGate, and re-swept by build.RegisterCompletedServices — see this
// file's doc comment's BUG-586 interaction section).
func NewSaveParticipant(a *ServicesAPI) *SaveParticipant {
	// SEC-020 pre-lock guard (astgate live-tree): a copied ServicesAPI is
	// still wrapped so the caller gets a non-nil participant, but every
	// method below re-checks checkNotCopied and fails closed, so a copy can
	// never actually read or mutate the state through this participant.
	_ = a.checkNotCopied("NewSaveParticipant")
	return &SaveParticipant{a: a}
}

// Kind returns the services shard label. A copied ServicesAPI yields the
// empty kind, which save.Load and registry validation reject rather than
// routing a shard to a copy.
func (p *SaveParticipant) Kind() string {
	if err := p.a.checkNotCopied("Kind"); err != nil {
		return ""
	}
	return KindServices
}

// Source returns a fresh pull-iterator over the services state. It snapshots
// the full mutable state under the lock once, up front, then yields one
// record at a time, marshalling each on demand. The emission order is
// kinds (sorted), instances (sorted), districtDemand (sorted by district
// then service), pools (sorted). A copied-value guard failure (SEC-020)
// surfaces on the first pull.
func (p *SaveParticipant) Source() serialize.RecordSource {
	if err := p.a.checkNotCopied("Source"); err != nil {
		return func() (serialize.Record, bool, error) { return serialize.Record{}, false, err }
	}
	snap, snapErr := p.a.snapshotForSave()
	idx := 0
	return func() (serialize.Record, bool, error) {
		if snapErr != nil {
			err := snapErr
			snapErr = nil
			return serialize.Record{}, false, err
		}
		if idx >= snap.total() {
			return serialize.Record{}, false, nil
		}
		rec, err := snap.recordAt(idx)
		if err != nil {
			return serialize.Record{}, false, err
		}
		idx++
		return rec, true, nil
	}
}

// Handler returns a fresh sink that rebuilds the services state from the
// streamed records. It clears the target's runtime state on the first
// record, then installs each record's effect directly under the lock — one
// record at a time.
func (p *SaveParticipant) Handler() serialize.RecordHandler {
	if err := p.a.checkNotCopied("Handler"); err != nil {
		return func(serialize.Record) error { return err }
	}
	reset := false
	return func(rec serialize.Record) error {
		if !reset {
			if err := p.a.resetForLoad(); err != nil {
				return err
			}
			reset = true
		}
		return p.a.applyLoadRecord(rec)
	}
}
