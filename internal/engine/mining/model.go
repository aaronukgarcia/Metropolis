package mining

import "github.com/aaronukgarcia/Metropolis/internal/engine/world"

// This file is the deposit taxonomy and the inert deposit record
// (FEAT-049 AC-2/AC-4): the resource-type enum covering every named real
// resource in resources-design-brief.md §2 plus one sanctioned fictional
// slot, and the Deposit value a shuffled map stores and queries return.
// Depth bands are deliberately NOT literals here — they are resolved from
// the GR#15 data file via DepositParams.DepthBand (params.go), so a
// balance pass on "realistic depths" edits data/deposits.json, never this
// file.

// DepositType is one resource kind the shuffle can place (AC-2). The
// canonical string form (String) is the exact key used in
// data/deposits.json's "resources" object, so the enum, the data file,
// and the loader stay in lock-step from one name table.
type DepositType uint8

const (
	DepositCopper  DepositType = iota // metallic ore
	DepositTin                        // metallic ore
	DepositIron                       // metallic ore
	DepositUranium                    // metallic ore — suppressed in chalk
	DepositREM                        // metallic ore — rare-earth metals
	DepositGas                        // hydrocarbon — offshore-capable
	DepositOil                        // hydrocarbon — offshore-capable
	DepositCoal                       // hydrocarbon/coal — East Kent coalfield
	DepositArcana                     // sanctioned fictional resource slot
)

// String returns the canonical lowercase name, identical to the matching
// data/deposits.json resource key.
func (d DepositType) String() string {
	switch d {
	case DepositCopper:
		return "copper"
	case DepositTin:
		return "tin"
	case DepositIron:
		return "iron"
	case DepositUranium:
		return "uranium"
	case DepositREM:
		return "rem"
	case DepositGas:
		return "gas"
	case DepositOil:
		return "oil"
	case DepositCoal:
		return "coal"
	case DepositArcana:
		return "arcana"
	default:
		return "unknown"
	}
}

// IsMetal reports whether d is one of the five metallic ores. Metallic
// ores are never placed on sea cells (AC-3).
func (d DepositType) IsMetal() bool {
	switch d {
	case DepositCopper, DepositTin, DepositIron, DepositUranium, DepositREM:
		return true
	default:
		return false
	}
}

// Deposit is the inert record a shuffled map stores for one cell (AC-4):
// three independently-sampled, first-class numeric fields — size, density,
// and depth — never a single combined "richness" scalar. Size and density
// are drawn from independent data-sourced curves; depth is constrained to
// the type's data-sourced realistic band (DepthBand). The cell location
// is not part of Deposit — DepositAt returns it for a caller-chosen cell,
// and AllDeposits/TileDeposits pair each Deposit with its location as a
// LocatedDeposit.
type Deposit struct {
	Type    DepositType
	Size    float64 // tonnes-equivalent, from the size curve
	Density float64 // grade, from the density curve (independent of Size)
	Depth   float64 // metres below surface, within the type's depth band
}

// LocatedDeposit pairs a placed Deposit with the cell it occupies. The
// exported sort order below (tile X, tile Y, row, col) is what makes
// AllDeposits deterministic for deep-equality across runs.
type LocatedDeposit struct {
	Tile    world.TileCoord
	Local   world.CellLocal
	Deposit Deposit
}
