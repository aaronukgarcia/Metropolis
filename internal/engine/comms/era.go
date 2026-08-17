package comms

// Era is one rung of the §35 six-era connectivity ladder. The ORDER and
// the six NAMES are §35's fixed vocabulary — telephone exchange → dial-up →
// broadband hub → fibre backbone → cellular masts (2G→5G coverage overlay)
// → submarine cable landing station. It is an ordered, monotonic
// progression: each subsequent era strictly supersedes the prior one's
// capability gates (AC-2). The per-era gate VALUES attached to each rung
// are data (data/comms.json), never Go literals (GR#15).
type Era uint8

const (
	// EraTelephoneExchange is the first era: the manual telephone exchange.
	EraTelephoneExchange Era = iota
	// EraDialUp is the second era: dial-up internet.
	EraDialUp
	// EraBroadbandHub is the third era: the broadband hub.
	EraBroadbandHub
	// EraFibreBackbone is the fourth era: the fibre backbone.
	EraFibreBackbone
	// EraCellular is the fifth era: cellular masts, the 2G→5G coverage
	// overlay (its sub-tiers are carried in data, see [CommsAPI.CellularSubTier]).
	EraCellular
	// EraSubmarineCable is the sixth and final era: the submarine cable
	// landing station.
	EraSubmarineCable
)

// numEras is the fixed size of the six-era ladder (a schema constant, not a
// balance number — the six eras are §35's vocabulary).
const numEras = int(EraSubmarineCable) + 1

// String renders the era's canonical §35 name (from data, falling back to a
// stable placeholder for an out-of-enum value so String never panics).
func (e Era) String() string {
	switch e {
	case EraTelephoneExchange:
		return "Telephone exchange"
	case EraDialUp:
		return "Dial-up"
	case EraBroadbandHub:
		return "Broadband hub"
	case EraFibreBackbone:
		return "Fibre backbone"
	case EraCellular:
		return "Cellular masts (2G-5G)"
	case EraSubmarineCable:
		return "Submarine cable landing station"
	}
	return "Unknown era"
}

// validEra reports whether e is one of the six documented eras. It bounds
// against the enum's own terminal value (a schema fact), not a balance
// number.
func validEra(e Era) bool {
	return e <= EraSubmarineCable
}

// Sector identifies a remote-work-affinity class. It mirrors engine.citizens'
// five Sector buckets (SectorNone..SectorPublic) as a local type so engine.comms
// need not import engine.citizens — which is NOT a registered outbound edge of
// engine.comms (GR#20, code.json). The mirror is held in lockstep by
// TestSectorMirrorsCitizens (a _test.go import of engine.citizens, the
// sanctioned exemption) — the duplication is deliberate, the drift is guarded
// (weakness pattern #2).
type Sector uint8

const (
	// SectorNone is the "no sector" bucket (child / never worked).
	SectorNone Sector = iota
	// SectorPrimary is agriculture / mining.
	SectorPrimary
	// SectorSecondary is manufacturing / construction.
	SectorSecondary
	// SectorTertiary is services / retail.
	SectorTertiary
	// SectorPublic is the public sector.
	SectorPublic
)

// numSectors is the fixed size of the five-bucket sector ladder.
const numSectors = int(SectorPublic) + 1

// sectorSlug returns the data-file slug for a sector (data/comms.json's
// "sectors" keys). It is the value the config loader folds into the
// sector-affinity table.
func sectorSlug(s Sector) string {
	switch s {
	case SectorNone:
		return "none"
	case SectorPrimary:
		return "primary"
	case SectorSecondary:
		return "secondary"
	case SectorTertiary:
		return "tertiary"
	case SectorPublic:
		return "public"
	}
	return ""
}

// validSector reports whether s is one of the five documented sector buckets.
func validSector(s Sector) bool {
	return s <= SectorPublic
}

// MilestoneGate reports whether a §4 milestone tier has been reached. It is
// the seam engine.unlocks implements ([unlocks.UnlocksAPI.MilestoneReached]),
// the same shape engine.finance consumes for loan gating — so the composition
// root wires the real engine.unlocks gate here directly. engine.comms never
// imports engine.unlocks (not a registered outbound edge, GR#20); it consumes
// the gate through this injected interface, exactly as engine.finance does.
type MilestoneGate interface {
	// MilestoneReached reports whether tier (1..13) has been reached.
	MilestoneReached(tier int) bool
}

// MilestoneGateFunc adapts a plain function to MilestoneGate, for tests and
// one-line composition-root wiring.
type MilestoneGateFunc func(tier int) bool

// MilestoneReached implements MilestoneGate.
func (f MilestoneGateFunc) MilestoneReached(tier int) bool { return f(tier) }

// parcelCommodity is the §6 commodity e-commerce parcels move as (AC-8): the
// consumer-goods shelf logistics carries. It is declared as an untyped string
// constant so DeliverParcels can pass it to engine.logistics'
// market.CommodityType parameter WITHOUT engine.comms importing engine.market
// (not a registered outbound edge — GR#20, code.json). The value duplicates
// engine.market's ConsumerGoods constant across the module boundary; that
// duplication is deliberate and held in lockstep by
// TestParcelCommodityMatchesMarket (weakness pattern #2).
const parcelCommodity = "consumerGoods"
