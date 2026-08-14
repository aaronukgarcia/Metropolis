package data

import "fmt"

// This file is §21's own: the off-map external world — the named job
// pools citizens out-commute to (and in-commuters arrive from), each
// with a finite, era-scaled capacity and a monthly off-map wage, loaded
// from data/external_world.json (FEAT-047 / data.modes-naming). See
// docs/design/modes-naming-external-schema.md for the field reference
// and docs/planning/acceptance/data.modes-naming.md for the acceptance
// criteria.
//
// The schema is structural rather than a flat map: Profiles is a slice
// of the three named pools (london/ashford/dover), each carrying a
// capacity-by-era curve, an int64 wage, and a transport-requirement
// list. A slice rather than a map means the pool ids are not
// compile-time fixed, so Validate instead rejects a duplicate/empty id
// and enforces the per-pool numeric invariants (non-decreasing
// capacity, positive wage, era-scaled rail gating) the §21/A6 amendment
// states as mechanism.

// externalRailUnlockTier is the milestone tier §21 names as when the
// external rail station unlocks (era 5 — see the data file's own
// eraConvention note). Validate gates the "externalRail" transport
// channel to this tier or later: rail must not be silently available
// from turn 1.
const externalRailUnlockTier = 5

// ExternalWorld is data/external_world.json's top-level schema (§21 +
// A6): the off-map job pools plus the file's own convention notes.
type ExternalWorld struct {
	// Comment carries the file's top-level "$comment" provenance note
	// (spec sections, the transcription rule, and the placeholder
	// disclosure).
	Comment string `json:"$comment,omitempty"`

	Version int `json:"version"`

	// EraConvention documents how "era" maps onto the game's milestone
	// tier index and why the capacityByEra curves cover eras 1..12.
	EraConvention string `json:"eraConvention,omitempty"`

	// MoneyConvention documents wageMicropounds' int64 micro-pound
	// representation (1 GBP = 1,000,000 micropounds, matching
	// data/market.json) and its monthly-gross-wage interpretation.
	MoneyConvention string `json:"moneyConvention,omitempty"`

	// Profiles is the three named off-map job pools.
	Profiles []ExternalProfile `json:"profiles"`
}

// ExternalProfile is one off-map job pool (e.g. London).
type ExternalProfile struct {
	// ID is the stable lowercase pool slug ("london", "ashford",
	// "dover"). Globally unique within the file.
	ID string `json:"id"`

	// Name is the display name ("London", ...).
	Name string `json:"name"`

	// CapacityByEra is the finite, era-scaled job-pool capacity curve
	// (A6's "bounded and slowly growing" mechanism). Eras must be within
	// the milestone ladder and strictly increasing; capacity must be
	// non-negative and non-decreasing across eras.
	CapacityByEra []CapacityByEra `json:"capacityByEra"`

	// WageMicropounds is the monthly gross wage per off-map job held, in
	// int64 micro-pounds (never a float — see MoneyConvention).
	WageMicropounds int64 `json:"wageMicropounds"`

	// TransportRequirement lists the transport channels that gate access
	// to this pool and the milestone tier each becomes available from.
	TransportRequirement []TransportRequirement `json:"transportRequirement"`

	// Comment is the per-pool placeholder/tuning disclosure.
	Comment string `json:"comment,omitempty"`
}

// CapacityByEra is one (era, capacity) point of a pool's capacity
// curve.
type CapacityByEra struct {
	Era      int `json:"era"`
	Capacity int `json:"capacity"`
}

// TransportRequirement is one transport channel gating a pool, with the
// milestone tier it becomes available from.
type TransportRequirement struct {
	Channel string `json:"channel"`

	// AvailableFromTier is the milestone tier the channel unlocks at.
	// For the "externalRail" channel this must be >= externalRailUnlockTier.
	AvailableFromTier int `json:"availableFromTier"`
}

// Validate implements Validator. It enforces, in file order:
//
//   - a non-empty profile list with unique, non-empty profile ids and
//     non-empty display names;
//   - a positive int64 wage per profile;
//   - a non-empty, era-sorted capacity curve with non-negative,
//     non-decreasing capacity;
//   - a non-empty transport-requirement list with valid milestone
//     tiers, and "externalRail" gated to its §21-named unlock tier.
func (e *ExternalWorld) Validate() error {
	if err := requireVersion(e.Version); err != nil {
		return err
	}
	if len(e.Profiles) == 0 {
		return fieldErr("profiles", "required, must be non-empty")
	}

	seen := make(map[string]int, len(e.Profiles))
	for i := range e.Profiles {
		p := &e.Profiles[i]
		prefix := fmt.Sprintf("profiles[%d]", i)
		idPrefix := prefix
		if p.ID != "" {
			idPrefix = fmt.Sprintf("%s(id=%s)", prefix, p.ID)
		}

		if err := requireNonEmptyString(prefix+".id", p.ID); err != nil {
			return err
		}
		if first, dup := seen[p.ID]; dup {
			return fieldErr(idPrefix+".id", fmt.Sprintf("duplicate profile id (first seen at profiles[%d])", first))
		}
		seen[p.ID] = i

		if err := requireNonEmptyString(idPrefix+".name", p.Name); err != nil {
			return err
		}

		if p.WageMicropounds <= 0 {
			return fieldErr(idPrefix+".wageMicropounds", fmt.Sprintf("must be positive, got %d", p.WageMicropounds))
		}

		if err := validateCapacityByEra(idPrefix, p.CapacityByEra); err != nil {
			return err
		}
		if err := validateTransportRequirement(idPrefix, p.TransportRequirement); err != nil {
			return err
		}
	}
	return nil
}

// validateCapacityByEra checks a pool's capacity curve: non-empty,
// eras strictly increasing within the milestone ladder, capacity
// non-negative and non-decreasing across eras (A6's "bounded and slowly
// growing" mechanism — a shrinking pool would be a data-authoring
// error).
func validateCapacityByEra(prefix string, eras []CapacityByEra) error {
	if len(eras) == 0 {
		return fieldErr(prefix+".capacityByEra", "required, must be non-empty")
	}
	prevEra, prevCap := 0, 0
	for j, ce := range eras {
		p := fmt.Sprintf("%s.capacityByEra[%d]", prefix, j)
		if ce.Era < milestoneTierMin || ce.Era > milestoneTierMax {
			return fieldErr(p+".era",
				fmt.Sprintf("must be a milestone tier %d-%d, got %d", milestoneTierMin, milestoneTierMax, ce.Era))
		}
		if ce.Capacity < 0 {
			return fieldErr(p+".capacity", fmt.Sprintf("must be >= 0, got %d", ce.Capacity))
		}
		if j > 0 {
			if ce.Era <= prevEra {
				return fieldErr(p+".era", fmt.Sprintf("must be strictly increasing, got %d after %d", ce.Era, prevEra))
			}
			if ce.Capacity < prevCap {
				return fieldErr(p+".capacity", fmt.Sprintf("must be non-decreasing, got %d after %d", ce.Capacity, prevCap))
			}
		}
		prevEra, prevCap = ce.Era, ce.Capacity
	}
	return nil
}

// validateTransportRequirement checks a pool's transport channels:
// non-empty, each with a non-empty channel name and a valid milestone
// tier, and the "externalRail" channel gated to its §21-named unlock
// tier or later (not silently available from turn 1).
func validateTransportRequirement(prefix string, reqs []TransportRequirement) error {
	if len(reqs) == 0 {
		return fieldErr(prefix+".transportRequirement", "required, must be non-empty")
	}
	for j, r := range reqs {
		p := fmt.Sprintf("%s.transportRequirement[%d]", prefix, j)
		if err := requireNonEmptyString(p+".channel", r.Channel); err != nil {
			return err
		}
		if r.AvailableFromTier < milestoneTierMin || r.AvailableFromTier > milestoneTierMax {
			return fieldErr(p+".availableFromTier",
				fmt.Sprintf("must be a milestone tier %d-%d, got %d", milestoneTierMin, milestoneTierMax, r.AvailableFromTier))
		}
		if r.Channel == "externalRail" && r.AvailableFromTier < externalRailUnlockTier {
			return fieldErr(p+".availableFromTier",
				fmt.Sprintf("externalRail must be gated to tier >= %d, got %d", externalRailUnlockTier, r.AvailableFromTier))
		}
	}
	return nil
}
