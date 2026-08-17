package roads

import "github.com/aaronukgarcia/Metropolis/internal/engine/world"

// RoadClass is the §51 class ladder, ordered from the narrowest/slowest
// rung (alley) to the widest/fastest (motorway). The 11 values are §51's
// fixed vocabulary, not balance numbers; their per-rung attributes (lanes,
// speed limits, parking, tree/verge, width, base cost) come from
// data/roads.json (GR#15). The first nine rungs correspond one-for-one with
// the class keys in data/naming_corpus.json's "roadSuffixes" table (held by
// the drift test); the two highest rungs (urban expressway, motorway) use
// §20's M-/A- numbering scheme instead of a place-name+suffix pairing.
type RoadClass uint8

const (
	// ClassAlley is the narrowest, slowest rung: a single-lane service way.
	ClassAlley RoadClass = iota
	// ClassGravel is an unsurfaced single-lane track.
	ClassGravel
	// ClassResidentialStreet is a two-lane street with parking and verge.
	ClassResidentialStreet
	// ClassTwoLane is a two-lane through road.
	ClassTwoLane
	// ClassOneWayPairs is a paired one-way street couplet.
	ClassOneWayPairs
	// ClassAvenue2Plus2 is a four-lane avenue (2+2).
	ClassAvenue2Plus2
	// ClassBusLaneVariant is a four-lane road with a dedicated bus lane.
	ClassBusLaneVariant
	// ClassTramTrackVariant is a four-lane road with tram tracks.
	ClassTramTrackVariant
	// ClassDualCarriageway is a divided four-lane road.
	ClassDualCarriageway
	// ClassUrbanExpressway is a high-speed urban route (A-numbered).
	ClassUrbanExpressway
	// ClassMotorway is the highest rung (M-numbered).
	ClassMotorway

	numClasses // unexported sentinel; also the ladder length
)

// String returns the class's §51 slug (schema vocabulary, matching the
// data/roads.json "id" field and data/naming_corpus.json's suffix keys for
// the nine non-numbered rungs — held by the drift test). The human display
// name lives in data/roads.json's "name" field, exposed via
// [RoadsAPI.ClassProfile].
func (c RoadClass) String() string {
	if c.valid() {
		return classSlugs[c]
	}
	return "unknown"
}

// classSlugs is the fixed §51 slug vocabulary, in ladder order. These are
// schema facts (like comms' eraOrder), not balance numbers; they must match
// the data/roads.json "id" field and, for the first nine, the
// data/naming_corpus.json "roadSuffixes" keys.
var classSlugs = [numClasses]string{
	"alley",
	"gravel",
	"residential_street",
	"two_lane",
	"one_way_pairs",
	"avenue_2_plus_2",
	"bus_lane_variant",
	"tram_track_variant",
	"dual_carriageway",
	"urban_expressway",
	"motorway",
}

// objectKindNames is the fixed §20 object-kind slug vocabulary.
var objectKindNames = [numKinds]string{
	"road",
	"civic_building",
	"infrastructure",
	"district",
	"transit",
}

// valid reports whether c is one of the eleven §51 rungs.
func (c RoadClass) valid() bool { return c < numClasses }

// numbered reports whether c uses §20's M-/A- numbering scheme rather than
// a place-name+suffix pairing (the two highest rungs).
func (c RoadClass) numbered() bool { return c == ClassUrbanExpressway || c == ClassMotorway }

// ObjectKind is the set of object kinds the naming-registry service names
// (§20: roads, civic buildings, infrastructure, districts, transit).
type ObjectKind uint8

const (
	KindRoad ObjectKind = iota
	KindCivicBuilding
	KindInfrastructure
	KindDistrict
	KindTransit

	numKinds // unexported sentinel; also the kind count
)

// String returns the object kind's slug.
func (k ObjectKind) String() string {
	if k.valid() {
		return objectKindNames[k]
	}
	return "unknown"
}

// valid reports whether k is a known object kind.
func (k ObjectKind) valid() bool { return k < numKinds }

// NodeID identifies a network node; RoadID identifies a road edge. Both are
// plain uint64s so callers can mint deterministic IDs from their own seed
// space.
type NodeID uint64
type RoadID uint64

// Node is a network node: an ID and its world-cell position. The position
// anchors the road's footprint geometry (see computeFootprint) so widening
// (AC-5) can be checked against engine.world occupancy.
type Node struct {
	ID  NodeID
	Pos CellRef
}

// CellRef is a world-cell reference (tile + local row/col), the unit the
// road footprint is expressed in.
type CellRef struct {
	Tile  world.TileCoord
	Local world.CellLocal
}

// Road is the read-only edge view [RoadsAPI.RoadInfo] returns (AC-2/AC-8):
// name, class, steady-state lane count, current lane count (at the queried
// month, including any roadworks reduction), the player-settable speed
// limit, start/end node references, and the maintenance condition. It
// deliberately carries NO volume/v-c/OD-flow/alternative-route fields — those
// are engine.traffic's query surface, composed by a consuming layer.
type Road struct {
	ID            RoadID
	Name          string
	Class         RoadClass
	Lanes         int // steady-state lane count (from the class)
	CurrentLanes  int // at the queried month, incl. any roadworks reduction
	SpeedLimitKPH int // player-settable within the class's speedMin..speedMax
	Start         NodeID
	End           NodeID
	Condition     float64 // maintenance state in [0,1]; 1 = perfect
	Renamed       bool    // true once the player renamed this road
}

// ClassProfile is the read-only per-rung attribute set [RoadsAPI.ClassProfile]
// returns (AC-3): the §51 fields each class carries.
type ClassProfile struct {
	Class          RoadClass
	Name           string
	Lanes          int
	SpeedLimit     int
	SpeedMin       int
	SpeedMax       int
	Parking        bool
	TreeVerge      bool
	WidthCells     int
	BaseCostPounds int64
}

// RoadworksPhase is one phased lane closure (§51/AC-6): a start month, a
// duration in months, and the number of lanes OPEN during the phase (which
// must not exceed the road's steady-state lane count).
type RoadworksPhase struct {
	StartMonth     int64
	DurationMonths int64
	OpenLanes      int
}

// RoadworksWindow is the scheduling window a roadworks phase may be
// restricted to (§51's "phase works at night/summer"). At the month-granular
// simulation tick, only summer is expressible as a calendar predicate;
// "night" (a sub-day concept) is not modelled at month granularity and is
// deliberately absent (see the doc.go scope note).
type RoadworksWindow uint8

const (
	// WindowAny allows scheduling in any month.
	WindowAny RoadworksWindow = iota
	// WindowSummer restricts scheduling to the calendar summer months
	// (June–August, i.e. calendar months 5–7) via a month-index predicate.
	WindowSummer
)

// UpgradeQuote is the result of an approved [RoadsAPI.ApplyUpgradeCommand]:
// the data-driven cost (Micropounds) and the roadworks phases the upgrade
// schedules (AC-4/AC-6). The class change commits when the last phase ends
// (see [RoadsAPI.Advance]).
type UpgradeQuote struct {
	CostMicropounds int64
	Phases          []RoadworksPhase
}

// CapacityDelta is [RoadsAPI.PreviewCapacityDelta]'s return (AC-7): the
// before/after lane counts and classes for a proposed upgrade — roads-owned
// fields only, never a journey-time estimate (that is a consuming layer's
// composition with engine.traffic).
type CapacityDelta struct {
	BeforeClass RoadClass
	AfterClass  RoadClass
	BeforeLanes int
	AfterLanes  int
}
