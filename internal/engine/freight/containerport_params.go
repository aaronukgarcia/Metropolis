package freight

import (
	"encoding/json"
	"os"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// This file is the GR#15 data-file contract for feat.containerport:
// containerPortConfig is the validated, ordered view of
// data/containerport.json, and LoadContainerPortConfig is the loader. Every
// tunable number the deep-sea terminal consumes — the port-tier ladder
// (milestone + cost) and each tier's berths/crane-rate/hours/customs/ship
// tonnage/jobs — comes from here, never from a Go literal in this package
// (GR#15). The loader is self-contained (os.ReadFile + encoding/json +
// buildContainerPortConfig) so this feature consumes no unregistered module
// edge for its own data file (GR#20), the same pattern engine.freight's
// LoadConfig and engine.mining's LoadDepositParams use. Loading is
// all-or-nothing: any missing/malformed/schema failure returns
// ErrContainerPortDataInvalid and no config — there is no partial ladder and
// no silent default substitution (AC-9).

// PortTier is one rung of the §33 port ladder (AC-2): cargo_port_small →
// container_terminal → deep_sea_terminal, each with its milestone/cost (for
// the tier-above-container_terminal ordering) and the capacity figures the
// tier reads through FreightAPI. Every figure is a data-driven placeholder
// (Disclosure names it pending Aaron's balance pass) — no final magnitude is
// pinned in Go.
type PortTier struct {
	Key                         string
	Name                        string
	Milestone                   int
	CostMillions                int64
	Berths                      int64
	CraneRateTonnesPerHour      int64
	OperatingHoursPerDay        int64
	CustomsCapacityTonnesPerDay int64
	ShipTonnage                 int64
	Jobs                        int64
	Disclosure                  string
}

// containerPortConfig is the fully-validated, ordered view of
// data/containerport.json: the port-tier ladder (sorted ascending by
// milestone, then cost, then key so [Tiers] is deterministic even for
// equal-rank tiers), a keyed lookup, and the deep-sea tier key.
type containerPortConfig struct {
	tiers       []PortTier
	byKey       map[string]PortTier
	deepSeaTier string
}

// rawContainerPortData is data/containerport.json's JSON wire shape, decoded
// only to be validated and folded into the ordered config above.
type rawContainerPortData struct {
	Version     int           `json:"version"`
	DeepSeaTier string        `json:"deepSeaTier"`
	Tiers       []rawPortTier `json:"tiers"`
}

type rawPortTier struct {
	Key                         string `json:"key"`
	Name                        string `json:"name"`
	Milestone                   int    `json:"milestone"`
	CostMillions                int64  `json:"costMillions"`
	Berths                      int64  `json:"berths"`
	CraneRateTonnesPerHour      int64  `json:"craneRateTonnesPerHour"`
	OperatingHoursPerDay        int64  `json:"operatingHoursPerDay"`
	CustomsCapacityTonnesPerDay int64  `json:"customsCapacityTonnesPerDay"`
	ShipTonnage                 int64  `json:"shipTonnage"`
	Jobs                        int64  `json:"jobs"`
	Disclosure                  string `json:"disclosure"`
}

// LoadContainerPortConfig reads, decodes and validates data/containerport.json
// from path, returning the ordered config or ErrContainerPortDataInvalid.
// Every failure is a registry-sourced *errs.E — never a panic, never a
// silent default.
func LoadContainerPortConfig(path, correlationID string) (containerPortConfig, error) {
	var zero containerPortConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return zero, errs.Wrap(ErrContainerPortDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	var raw rawContainerPortData
	if err := json.Unmarshal(b, &raw); err != nil {
		return zero, errs.Wrap(ErrContainerPortDataInvalid, correlationID, err, map[string]any{
			"path":  path,
			"cause": err.Error(),
		})
	}

	return buildContainerPortConfig(raw, path, correlationID)
}

func buildContainerPortConfig(raw rawContainerPortData, path, correlationID string) (containerPortConfig, error) {
	fail := func(field, rule string) (containerPortConfig, error) {
		return containerPortConfig{}, errs.New(ErrContainerPortDataInvalid, correlationID, map[string]any{
			"path":  path,
			"field": field,
			"rule":  rule,
			"cause": field + ": " + rule,
		})
	}

	var c containerPortConfig
	if raw.Version <= 0 {
		return fail("version", "required, must be a positive integer")
	}
	if raw.DeepSeaTier == "" {
		return fail("deepSeaTier", "required, must name one of the tiers")
	}
	if len(raw.Tiers) == 0 {
		return fail("tiers", "required, must list at least one port tier")
	}

	c.byKey = make(map[string]PortTier, len(raw.Tiers))
	for i, rt := range raw.Tiers {
		field := "tiers[" + itoa(i) + "]"
		if rt.Key == "" {
			return fail(field+".key", "required, must be a non-empty tier key")
		}
		if _, dup := c.byKey[rt.Key]; dup {
			return fail(field+".key", "duplicate tier key "+rt.Key)
		}
		if rt.Name == "" {
			return fail(field+".name", "required, must be a non-empty tier name")
		}
		if rt.Disclosure == "" {
			return fail(field+".disclosure", "required — every numeric entry must carry a non-empty disclosure naming it a placeholder pending Aaron's balance pass (AC-14)")
		}
		if rt.Milestone < 0 {
			return fail(field+".milestone", "must be >= 0")
		}
		if rt.CostMillions < 0 {
			return fail(field+".costMillions", "must be >= 0")
		}
		if rt.Berths <= 0 {
			return fail(field+".berths", "must be > 0")
		}
		if rt.CraneRateTonnesPerHour <= 0 {
			return fail(field+".craneRateTonnesPerHour", "must be > 0")
		}
		if rt.OperatingHoursPerDay <= 0 {
			return fail(field+".operatingHoursPerDay", "must be > 0")
		}
		if rt.CustomsCapacityTonnesPerDay <= 0 {
			return fail(field+".customsCapacityTonnesPerDay", "must be > 0")
		}
		if rt.ShipTonnage <= 0 {
			return fail(field+".shipTonnage", "must be > 0")
		}
		if rt.Jobs < 0 {
			return fail(field+".jobs", "must be >= 0")
		}

		c.byKey[rt.Key] = PortTier{
			Key:                         rt.Key,
			Name:                        rt.Name,
			Milestone:                   rt.Milestone,
			CostMillions:                rt.CostMillions,
			Berths:                      rt.Berths,
			CraneRateTonnesPerHour:      rt.CraneRateTonnesPerHour,
			OperatingHoursPerDay:        rt.OperatingHoursPerDay,
			CustomsCapacityTonnesPerDay: rt.CustomsCapacityTonnesPerDay,
			ShipTonnage:                 rt.ShipTonnage,
			Jobs:                        rt.Jobs,
			Disclosure:                  rt.Disclosure,
		}
	}

	if _, ok := c.byKey[raw.DeepSeaTier]; !ok {
		return fail("deepSeaTier", "names an unregistered tier key")
	}
	c.deepSeaTier = raw.DeepSeaTier

	// Tiers in ascending (milestone, cost, key) order — deterministic iteration
	// and the AC-2 "strictly above container_terminal in milestone/cost"
	// ordering (GR#21). The sort is stable AND the key tie-break makes the
	// order total: two tiers sharing an equal (milestone, cost) rank (a valid
	// data file — the duplicate check is on key only) otherwise fall back to
	// map-iteration seed, producing distinct Tiers() orderings across loads.
	c.tiers = make([]PortTier, 0, len(c.byKey))
	for _, t := range c.byKey {
		c.tiers = append(c.tiers, t)
	}
	sort.SliceStable(c.tiers, func(i, j int) bool {
		if c.tiers[i].Milestone != c.tiers[j].Milestone {
			return c.tiers[i].Milestone < c.tiers[j].Milestone
		}
		if c.tiers[i].CostMillions != c.tiers[j].CostMillions {
			return c.tiers[i].CostMillions < c.tiers[j].CostMillions
		}
		return c.tiers[i].Key < c.tiers[j].Key
	})

	// Ladder validation (AC-2, GR#21): the documented §33 port ladder
	// cargo_port_small → container_terminal → deep_sea_terminal must be
	// strictly ascending on the (milestone, cost) key. The deep-sea tier and
	// container_terminal are milestone-equal, so their ordering rests on cost
	// alone — without this check a data file pricing deep_sea_terminal below
	// container_terminal would load with no error and sort the ladder
	// inverted, silently breaking US-1/AC-2's "genuine rung above" claim.
	if err := validatePortLadder(c, raw.DeepSeaTier, path, correlationID); err != nil {
		return containerPortConfig{}, err
	}

	return c, nil
}

// portLadderBase is the documented lower rungs of the §33 port ladder
// (AC-2). The deep-sea tier (data's deepSeaTier field) closes the ladder as
// its top rung; the base rungs are structural ladder keys, not tunable balance
// numbers, so they are named here rather than read from the data file (GR#15
// applies to the numeric balance figures, not the ladder's shape).
var portLadderBase = []string{"cargo_port_small", "container_terminal"}

// tierAbove reports whether a sorts strictly above b on the (milestone, cost)
// key — the ladder's deterministic ordering (GR#21).
func tierAbove(a, b PortTier) bool {
	if a.Milestone != b.Milestone {
		return a.Milestone > b.Milestone
	}
	return a.CostMillions > b.CostMillions
}

// validatePortLadder rejects a data file whose port ladder is inverted: each
// rung of cargo_port_small → container_terminal → deep_sea_terminal must sort
// strictly above the one before it. The ladder is checked over whichever of
// its documented rungs are present, but a missing rung never breaks the chain:
// the ordering invariant is enforced between each present rung and the
// last-seen present rung before it, so a data file omitting container_terminal
// still has cargo_port_small compared against deep_sea_terminal. An inverted
// pair is ErrContainerPortDataInvalid, never a silently re-sorted ladder.
func validatePortLadder(c containerPortConfig, deepSeaTier, path, correlationID string) error {
	fail := func(field, rule string) error {
		return errs.New(ErrContainerPortDataInvalid, correlationID, map[string]any{
			"path":  path,
			"field": field,
			"rule":  rule,
			"cause": field + ": " + rule,
		})
	}

	ladder := append(append([]string{}, portLadderBase...), deepSeaTier)
	lastSeenKey := ""
	for _, key := range ladder {
		tier, ok := c.byKey[key]
		if !ok {
			continue
		}
		if lastSeenKey != "" {
			lower := c.byKey[lastSeenKey]
			if !tierAbove(tier, lower) {
				return fail("tiers",
					lastSeenKey+" must sort strictly below "+key+" on the (milestone, cost) key — ladder inverted (AC-2)")
			}
		}
		lastSeenKey = key
	}
	return nil
}
