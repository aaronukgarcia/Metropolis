package services

import (
	"strconv"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// ServiceID is the stable identity of one registered service instance
// (a specific built clinic, fire station, …). It is the key the query and
// command methods take, so a caller can always address the exact instance
// it registered.
type ServiceID string

// UpgradeStep is one rung of a service's §10 upgrade path (AC-9): a
// catalogue building id, its display name, the §4 milestone tier it
// unlocks at, and the numeric capacity ceiling that building provides.
// Upgrading moves a service to the next step, which raises its capacity
// ceiling — funding, by contrast, only affects realised quality within the
// current ceiling.
type UpgradeStep struct {
	BuildingID      string
	Name            string
	Milestone       int
	CapacityCeiling float64
}

// ServiceSpec is the registration input for one service instance. Its
// catalogue-sourced fields (CapacityRaw, Milestone) are normally produced
// by [ServiceSpecFromBuilding] so capacity is never a hand-authored
// duplicate of data/buildings.json (AC-10).
type ServiceSpec struct {
	// ID is the unique instance identity (AC-11's lookup key).
	ID ServiceID
	// Kind must already be registered via [ServicesAPI.RegisterKind] — an
	// unregistered kind is rejected with ErrUnknownServiceKind (AC-11).
	Kind ServiceKind
	// CapacityRaw is the verbatim catalogue capacity string
	// (data/buildings.json's capacityRaw, e.g. "150 visits/d") — sourced,
	// not duplicated (AC-10).
	CapacityRaw string
	// CoverageRadius is the spatial reach from the service building's cell
	// (AC-3). X/Y locate the cell the reach is measured from.
	CoverageRadius float64
	X, Y           float64
	// Milestone is the §4 tier the enabling building unlocks at (parsed
	// from data/buildings.json's unlock.milestone, e.g. "M2" → 2). SetFunding
	// refuses to fund it until that tier is reached (AC-7).
	Milestone int
	// StaffingNeed is the benchmark staffing requirement this instance
	// draws from its shared pool (set per tick via UpdateStaffing, or here).
	StaffingNeed float64
	// UpgradePath is the catalogue tier progression (AC-9); step 0 is the
	// current building. May be empty (no upgrade path).
	UpgradePath []UpgradeStep
}

// ServiceSpecFromBuilding derives the catalogue-sourced fields of a
// ServiceSpec from a data.BuildingEntry: CapacityRaw comes verbatim from
// the entry's capacityRaw field (AC-10 — capacity is never a hand-authored
// duplicate), and Milestone is parsed from the entry's unlock.milestone
// ("M2" → 2). The remaining fields (coverage, staffing, upgrade path) are
// left at their zero values for the caller to supply, since the catalogue
// carries no coverage/upgrade-path columns. The initial UpgradePath is a
// single step carrying the building's id, name, milestone, and a numeric
// capacity ceiling parsed from CapacityRaw where it leads with a number
// (so the generic framework can seed the quality math from the catalogue
// rather than inventing a figure).
func ServiceSpecFromBuilding(id ServiceID, kind ServiceKind, entry data.BuildingEntry) ServiceSpec {
	step := UpgradeStep{
		BuildingID:      entry.ID,
		Name:            entry.Name,
		Milestone:       milestoneTier(entry.Unlock.Milestone),
		CapacityCeiling: CapacityFromRaw(entry.CapacityRaw),
	}
	return ServiceSpec{
		ID:          id,
		Kind:        kind,
		CapacityRaw: entry.CapacityRaw,
		Milestone:   step.Milestone,
		UpgradePath: []UpgradeStep{step},
	}
}

// milestoneTier parses a §4 unlock.milestone string ("M2") into its tier
// number. An empty or unparseable value yields 0 ("no tier constraint"),
// which SetFunding treats as "skip the tier-gate check" — matching how an
// entry the catalogue left without a milestone carries no tier gate.
func milestoneTier(milestone string) int {
	m := strings.TrimSpace(milestone)
	if len(m) < 2 || m[0] != 'M' {
		return 0
	}
	n, err := strconv.Atoi(m[1:])
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// CapacityFromRaw parses the leading non-negative number out of a
// catalogue capacityRaw string ("150 visits/d" → 150, "240" → 240,
// "4 appliances" → 4). A string with no leading number (or a non-numeric
// prefix) yields 0. It exists so a caller can seed a numeric capacity
// ceiling from the catalogue's verbatim field without re-authoring the
// value; the verbatim string itself remains available on the instance via
// [ServicesAPI.CapacityRaw] (AC-10).
func CapacityFromRaw(raw string) float64 {
	i := 0
	for i < len(raw) && !isDigit(raw[i]) {
		i++
	}
	j := i
	for j < len(raw) && isDigit(raw[j]) {
		j++
	}
	if i == j {
		return 0
	}
	v, err := strconv.ParseFloat(raw[i:j], 64)
	if err != nil {
		return 0
	}
	return v
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// ServiceInstance is the registered state of one service, reachable only
// through ServicesAPI's exported methods (GR#20). Unexported so no other
// package can mutate a service's funding/capacity directly.
type serviceInstance struct {
	spec ServiceSpec

	// currentUpgrade is the index into spec.UpgradePath the instance is
	// currently at (0 = the building it was registered as).
	currentUpgrade int

	// runtime per-tick state.
	funding    float64 // 0..1, mutated only via SetFunding
	demand     float64
	demandDist float64
	allocated  float64 // staff allocated from the shared pool this tick
}

// capacityCeiling returns the numeric capacity ceiling at the instance's
// current upgrade step (AC-9): step 0's ceiling from registration, or the
// upgraded step's ceiling after Upgrade.
func (s *serviceInstance) capacityCeiling() float64 {
	if len(s.spec.UpgradePath) == 0 {
		return 0
	}
	if s.currentUpgrade < 0 || s.currentUpgrade >= len(s.spec.UpgradePath) {
		return 0
	}
	return s.spec.UpgradePath[s.currentUpgrade].CapacityCeiling
}

// staffingRatio returns allocated / need, clamped to [0,1]. A service with
// zero staffing need is fully staffed by definition (there is nothing to
// under-staff).
func (s *serviceInstance) staffingRatio() float64 {
	if s.spec.StaffingNeed <= 0 {
		return 1
	}
	return clamp01(s.allocated / s.spec.StaffingNeed)
}

// currentMilestone returns the §4 milestone tier of the building the
// instance is CURRENTLY at: the current upgrade step's Milestone when the
// path declares one, else the registration-time spec.Milestone. SetFunding
// gates on this (not spec.Milestone) so that after an Upgrade the funding
// gate follows the upgraded building's tier rather than the original's
// (SEC-095).
func (s *serviceInstance) currentMilestone() int {
	if s.currentUpgrade >= 0 && s.currentUpgrade < len(s.spec.UpgradePath) {
		if m := s.spec.UpgradePath[s.currentUpgrade].Milestone; m > 0 {
			return m
		}
	}
	return s.spec.Milestone
}
