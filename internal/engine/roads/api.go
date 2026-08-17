package roads

import (
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// RoadsAPI is code.json's "engine.roads" inbound contract (RoadsAPI,
// GUID 6510e884-5a50-4b50-86c1-78a6329b26d0): road-as-named-edge identity
// (class, lanes, speed limit, maintenance state, endpoints), the §51 class
// ladder with in-place upgrades, simulated roadworks, and the deterministic
// seed+id naming-registry service other modules consume. All graph mutations
// are commands ([AddNode], [AddRoad], [ApplyUpgrade], [ScheduleRoadworks],
// [Rename], [SetSpeedLimit], [RepairRoad]) — there is no exported field a
// caller could write (AC-1/GR#20). The read surface is [RoadInfo],
// [CurrentLaneCount], [PreviewCapacityDelta], [ClassProfile],
// [MaintenanceState], [NameRoad] and [NameFor].
//
// The zero value is not usable; construct via [Load] or [LoadDefault], then
// wire the engine.world dependency with [SetWorld] (needed only for the
// AC-5 widening/footprint check). A *RoadsAPI is safe for concurrent use:
// mutable state is guarded by mu, and checkNotCopied rejects a method call
// on a struct-copied value (SEC-020-class, mirroring engine.comms).
type RoadsAPI struct {
	mu            sync.RWMutex
	correlationID string

	seed   uint64
	cfg    config
	corpus data.NamingCorpus

	world *world.WorldAPI

	nodes    map[NodeID]Node
	roads    map[RoadID]*roadState
	renames  map[nameKey]string
	nowMonth int64

	// self is the SEC-020 copy guard, stored exactly once in Load before
	// the value is returned to any caller (mirroring engine.comms).
	self atomic.Pointer[RoadsAPI]
}

// roadState is the unexported runtime record of one road edge. Reachable
// only through RoadsAPI's exported command/query surface (GR#20).
type roadState struct {
	id           RoadID
	name         string
	class        RoadClass
	speedLimit   int // player-settable KPH within the class's speedMin..speedMax
	start        NodeID
	end          NodeID
	condition    float64 // maintenance state in [0,1]; 1 = perfect
	renamed      bool
	footprint    []CellRef        // sorted, deterministic
	roadworks    []RoadworksPhase // sorted by StartMonth, non-overlapping
	pendingClass *RoadClass       // non-nil while an upgrade is in flight (commits on completion)
}

// Load reads and validates data/roads.json and data/naming_corpus.json from
// dir and returns a ready *RoadsAPI with an empty graph. seed is the world
// seed the deterministic auto-naming is keyed on (AC-14). correlationID is
// attached to every error this call (and the returned API's methods)
// construct (GR#1). Every failure is a registry-sourced *errs.E — never a
// silent default substitution, never a panic (AC-13).
func Load(dir string, seed uint64, correlationID string) (*RoadsAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	cfg, err := loadRoadsConfig(dir, correlationID)
	if err != nil {
		return nil, err
	}
	corpus, err := data.LoadNamingCorpus(dir, correlationID)
	if err != nil {
		return nil, errs.Wrap(ErrCorpusLoadFailed, correlationID, err, map[string]any{
			"dir":   dir,
			"cause": err.Error(),
		})
	}
	a := &RoadsAPI{
		correlationID: correlationID,
		seed:          seed,
		cfg:           cfg,
		corpus:        corpus,
		nodes:         make(map[NodeID]Node),
		roads:         make(map[RoadID]*roadState),
		renames:       make(map[nameKey]string),
	}
	a.self.Store(a)
	return a, nil
}

// LoadDefault resolves data/'s directory via foundation/data's
// ResolveDataDir and then [Load]s it.
func LoadDefault(seed uint64, correlationID string) (*RoadsAPI, error) {
	dir, err := data.ResolveDataDir(correlationID)
	if err != nil {
		return nil, err
	}
	return Load(filepath.Join(dir), seed, correlationID)
}

// roadsErr builds a registry-sourced error under the API's correlation ID
// (GR#7/GR#1). It is a package-level function so checkNotCopied can call it
// without recursing.
func roadsErr(correlationID, code string, ctx map[string]any) *errs.E {
	return errs.New(code, correlationID, ctx)
}

// checkNotCopied rejects a method call on a struct-copied *RoadsAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load.
func (a *RoadsAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return roadsErr(a.correlationID, ErrCopiedValue, map[string]any{"method": method})
	}
	return nil
}

// SetWorld wires the engine.world dependency (AC-5's widening/footprint
// occupancy check). A nil world is tolerated at construction; the widening
// check then fails closed with ErrWorldNotWired when it would be needed.
func (a *RoadsAPI) SetWorld(w *world.WorldAPI) error {
	if err := a.checkNotCopied("SetWorld"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.world = w
	return nil
}

// resolveName returns the player's rename for key if one exists, else the
// auto-name from auto (AC-11: a rename survives later unrelated naming
// passes because it is stored, not re-derived).
func (a *RoadsAPI) resolveName(key nameKey, auto func() string) string {
	a.mu.RLock()
	if n, ok := a.renames[key]; ok {
		a.mu.RUnlock()
		return n
	}
	a.mu.RUnlock()
	return auto()
}

// AddNodeCommand registers a network node.
type AddNodeCommand struct {
	CorrelationID string
	ID            NodeID
	Pos           CellRef
}

// AddNode registers a node for later AddRoad edges. A zero NodeID is
// ErrInvalidInput; a duplicate ID at the SAME position is an idempotent
// no-op, but re-registering a node that a road already references at a
// DIFFERENT position is ErrNodeReferenced — the road's stored footprint was
// computed from the node's original position, so silently moving it would
// desync the footprint from its endpoints (SEC-236). A Pos whose tile is
// outside the 30x30 expansion extent or whose local row/col is outside the
// 200x200 tile grid is rejected with ErrInvalidInput BEFORE any footprint
// work, so a hostile coordinate can never make AddRoad's Bresenham stamp run
// unbounded (SEC-222).
func (a *RoadsAPI) AddNode(cmd AddNodeCommand) error {
	if err := a.checkNotCopied("AddNode"); err != nil {
		return err
	}
	if cmd.ID == 0 {
		return roadsErr(a.correlationID, ErrInvalidInput, map[string]any{"field": "ID"})
	}
	if !cellRefInDomain(cmd.Pos) {
		return roadsErr(a.correlationID, ErrInvalidInput, map[string]any{
			"field": "Pos",
			"cell":  cellRefString(cmd.Pos),
		})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if existing, ok := a.nodes[cmd.ID]; ok && existing.Pos != cmd.Pos {
		// SEC-236: moving a node a road already references would leave that
		// road's stored footprint computed from the OLD position while its
		// endpoints name the NEW one — silently corrupting widening land-cost
		// and obstruction checks (GR#3/GR#17). Reject rather than overwrite.
		if a.nodeReferencedLocked(cmd.ID) {
			return roadsErr(a.correlationID, ErrNodeReferenced, map[string]any{"node": uint64(cmd.ID)})
		}
	}
	a.nodes[cmd.ID] = Node{ID: cmd.ID, Pos: cmd.Pos}
	return nil
}

// nodeReferencedLocked reports whether any road references the given node as
// its start or end. The caller holds a.mu. The answer is a pure existence
// predicate, so the map iteration order cannot affect the result (AC-15's
// determinism discipline is about result-affecting order, which this is not).
func (a *RoadsAPI) nodeReferencedLocked(id NodeID) bool {
	for _, rs := range a.roads {
		if rs.start == id || rs.end == id {
			return true
		}
	}
	return false
}

// AddRoadCommand creates a road edge (AC-2's "named edge"). The road is
// auto-named from (seed, ID) unless ContinueFrom references an existing
// road of the SAME class, in which case the new road continues that road's
// name through the junction (the AC-9 continuation rule — a class change
// breaks the continuation and yields a fresh name).
type AddRoadCommand struct {
	CorrelationID string
	ID            RoadID
	Start         NodeID
	End           NodeID
	Class         RoadClass
	ContinueFrom  RoadID
}

// AddRoad creates a road edge and returns its read-only [Road] view. It
// rejects an unknown class, a zero ID, a self-loop, and a reference to an
// unregistered start/end node (ErrNodeNotFound). The road's footprint is
// computed deterministically from the endpoint positions and the class
// width.
func (a *RoadsAPI) AddRoad(cmd AddRoadCommand) (Road, error) {
	if err := a.checkNotCopied("AddRoad"); err != nil {
		return Road{}, err
	}
	if cmd.ID == 0 {
		return Road{}, roadsErr(a.correlationID, ErrInvalidInput, map[string]any{"field": "ID"})
	}
	if !cmd.Class.valid() {
		return Road{}, roadsErr(a.correlationID, ErrInvalidClass, map[string]any{"class": uint8(cmd.Class)})
	}
	if cmd.Start == cmd.End {
		return Road{}, roadsErr(a.correlationID, ErrInvalidInput, map[string]any{"reason": "self-loop edge"})
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	start, ok := a.nodes[cmd.Start]
	if !ok {
		return Road{}, roadsErr(a.correlationID, ErrNodeNotFound, map[string]any{"node": uint64(cmd.Start)})
	}
	end, ok := a.nodes[cmd.End]
	if !ok {
		return Road{}, roadsErr(a.correlationID, ErrNodeNotFound, map[string]any{"node": uint64(cmd.End)})
	}

	name := autoNameRoad(a.seed, uint64(cmd.ID), cmd.Class, a.corpus)
	renamed := false
	// SEC-227: a recorded player rename must win over the freshly derived
	// auto-name, exactly as resolveName does for NameRoad/NameFor — otherwise
	// Rename-then-AddRoad makes RoadInfo and NameRoad diverge (GR#3). The
	// caller holds a.mu, so consult a.renames directly rather than resolveName
	// (which would re-lock).
	if n, ok := a.renames[nameKey{kind: KindRoad, seed: a.seed, id: uint64(cmd.ID)}]; ok {
		name = n
		renamed = true
	} else if cmd.ContinueFrom != 0 {
		if prev, ok := a.roads[cmd.ContinueFrom]; ok && prev.class == cmd.Class {
			name = prev.name
		}
	}

	rs := &roadState{
		id:         cmd.ID,
		name:       name,
		class:      cmd.Class,
		speedLimit: a.cfg.classes[cmd.Class].SpeedLimit,
		start:      cmd.Start,
		end:        cmd.End,
		condition:  1.0,
		renamed:    renamed,
		footprint:  computeFootprint(start.Pos, end.Pos, a.cfg.classes[cmd.Class].WidthCells),
	}
	a.roads[cmd.ID] = rs
	return a.roadViewLocked(rs, a.nowMonth), nil
}

// RoadInfo returns the read-only identity/capacity view of a road at the
// given simulation month (AC-8): name, endpoints, class, steady-state and
// current lane counts, speed limit, and maintenance condition. It carries
// no volume/v-c/OD-flow/alternative-route data — those are engine.traffic's
// query surface. An unknown road is ErrRoadNotFound.
func (a *RoadsAPI) RoadInfo(id RoadID, atMonth int64) (Road, error) {
	if err := a.checkNotCopied("RoadInfo"); err != nil {
		return Road{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	rs, ok := a.roads[id]
	if !ok {
		return Road{}, roadsErr(a.correlationID, ErrRoadNotFound, map[string]any{"road": uint64(id)})
	}
	return a.roadViewLocked(rs, atMonth), nil
}

// roadViewLocked renders a roadState as its exported [Road] view (AC-2/AC-8)
// — the single place the record-to-view mapping lives (GR#3). The caller
// holds a.mu.
func (a *RoadsAPI) roadViewLocked(rs *roadState, atMonth int64) Road {
	return Road{
		ID:            rs.id,
		Name:          rs.name,
		Class:         rs.class,
		Lanes:         a.cfg.classes[rs.class].Lanes,
		CurrentLanes:  a.currentLanesLocked(rs, atMonth),
		SpeedLimitKPH: rs.speedLimit,
		Start:         rs.start,
		End:           rs.end,
		Condition:     rs.condition,
		Renamed:       rs.renamed,
	}
}

// CurrentLaneCount returns the road's lane count at the given simulation
// month, including any active roadworks reduction (AC-6's own read path —
// the value engine.traffic reads on its normal per-assignment query). An
// unknown road is ErrRoadNotFound.
func (a *RoadsAPI) CurrentLaneCount(id RoadID, atMonth int64) (int, error) {
	if err := a.checkNotCopied("CurrentLaneCount"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	rs, ok := a.roads[id]
	if !ok {
		return 0, roadsErr(a.correlationID, ErrRoadNotFound, map[string]any{"road": uint64(id)})
	}
	return a.currentLanesLocked(rs, atMonth), nil
}

// currentLanesLocked computes the steady-state lane count reduced by any
// roadworks phase active at atMonth (the minimum OpenLanes across active
// phases wins). The caller holds a.mu.
func (a *RoadsAPI) currentLanesLocked(rs *roadState, atMonth int64) int {
	lanes := a.cfg.classes[rs.class].Lanes
	for _, p := range rs.roadworks {
		// SEC-226: saturate the phase end so a far-future phase cannot wrap its
		// end month negative and silently never activate.
		if atMonth >= p.StartMonth && atMonth < num.SatAdd(p.StartMonth, p.DurationMonths) && p.OpenLanes < lanes {
			lanes = p.OpenLanes
		}
	}
	return lanes
}

// ClassProfile returns one class's read-only §51 attribute set (AC-3). An
// unknown class is ErrInvalidClass.
func (a *RoadsAPI) ClassProfile(class RoadClass) (ClassProfile, error) {
	if err := a.checkNotCopied("ClassProfile"); err != nil {
		return ClassProfile{}, err
	}
	if !class.valid() {
		return ClassProfile{}, roadsErr(a.correlationID, ErrInvalidClass, map[string]any{"class": uint8(class)})
	}
	cc := a.cfg.classes[class]
	return ClassProfile{
		Class:          class,
		Name:           cc.Name,
		Lanes:          cc.Lanes,
		SpeedLimit:     cc.SpeedLimit,
		SpeedMin:       cc.SpeedMin,
		SpeedMax:       cc.SpeedMax,
		Parking:        cc.Parking,
		TreeVerge:      cc.TreeVerge,
		WidthCells:     cc.WidthCells,
		BaseCostPounds: cc.BaseCostPounds,
	}, nil
}

// Advance moves the simulation clock forward to toMonth, degrading every
// road's condition over the elapsed months (AC-2/US-6) and committing any
// roadworks whose phases have all ended (flipping an in-flight upgrade's
// class to its target). Time must not run backwards (ErrMonthRegression,
// AC-17's month-index discipline).
func (a *RoadsAPI) Advance(toMonth int64) error {
	if err := a.checkNotCopied("Advance"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if toMonth < a.nowMonth {
		return roadsErr(a.correlationID, ErrMonthRegression, map[string]any{
			"now": a.nowMonth, "target": toMonth,
		})
	}
	a.advanceLocked(a.nowMonth, toMonth)
	a.nowMonth = toMonth
	return nil
}

// advanceLocked degrades condition and commits completed roadworks over
// [fromMonth, toMonth). The caller holds a.mu. Roads are visited in sorted
// ID order (AC-15: a map range must not feed a result-affecting path
// without a deterministic ordering step — the per-road decay is already
// order-independent, but the sort keeps the invariant mechanical rather
// than argued).
func (a *RoadsAPI) advanceLocked(fromMonth, toMonth int64) {
	months := toMonth - fromMonth
	if months <= 0 {
		return
	}
	decay := a.cfg.maintenance.ConditionDecayPerMonth * float64(months)
	ids := make([]RoadID, 0, len(a.roads))
	for id := range a.roads {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		rs := a.roads[id]
		if rs.condition > decay {
			rs.condition -= decay
		} else {
			rs.condition = 0
		}
		a.commitCompletedLocked(rs, toMonth)
	}
}

// commitCompletedLocked flips an in-flight upgrade's class to its target
// once every roadworks phase has ended at or before month. The caller holds
// a.mu.
func (a *RoadsAPI) commitCompletedLocked(rs *roadState, month int64) {
	if len(rs.roadworks) == 0 {
		return
	}
	last := rs.roadworks[len(rs.roadworks)-1]
	// SEC-226: saturate the phase end; a wrapped negative end would make a
	// far-future phase's class change commit immediately instead of when the
	// phase actually ends.
	if month < num.SatAdd(last.StartMonth, last.DurationMonths) {
		return
	}
	if rs.pendingClass != nil {
		rs.class = *rs.pendingClass
		rs.speedLimit = a.cfg.classes[rs.class].SpeedLimit
		rs.footprint = a.recomputeFootprintLocked(rs)
		rs.pendingClass = nil
	}
	rs.roadworks = nil
}

// recomputeFootprintLocked recomputes a road's footprint from its current
// class width and endpoint positions (after a class change). The caller
// holds a.mu.
func (a *RoadsAPI) recomputeFootprintLocked(rs *roadState) []CellRef {
	start, end := a.nodes[rs.start], a.nodes[rs.end]
	return computeFootprint(start.Pos, end.Pos, a.cfg.classes[rs.class].WidthCells)
}
