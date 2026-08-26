package spiral

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file implements AC-9's documented, versioned, scripted shock
// scenario — the canonical S7 exit-gate reproducibility fixture (ASM-240):
// a major-employer-closure shock at a fixed month against a fixed synthetic
// starting city, run headless to a death condition or a fixed month cap.
// The outcome is the ordered event sequence plus the final state hash, and
// it is byte-identical across repeated runs and across worker/shard counts
// (GR#21) because every step is a pure function of the scenario's fixed
// fields and the API's own deterministic state.

// ShockType names the shock a scripted scenario applies (AC-11). The
// canonical ASM-240 fixture is a major-employer closure.
type ShockType string

const (
	// ShockMajorEmployerClosure is §12's first-listed example shock and the
	// ASM-240 canonical S7 fixture.
	ShockMajorEmployerClosure ShockType = "majorEmployerClosure"
)

// Scenario is one fixed, versioned scripted shock scenario (AC-9/ASM-240).
// Every field is a fixture constant — the scenario's determinism is exactly
// the fact that these fields (and only these) drive the whole run.
type Scenario struct {
	Version     int
	Seed        uint64
	ShockMonth  int64
	ShockType   ShockType
	ShockTarget *CellRef // optional; when set it must name a valid cell (AC-11)

	MonthCap int64

	// Starting city (fixed, standing in for the harness.synth-generated
	// fixture — ASM-240).
	StartingPopulation int64
	StartTile          world.TileCoord

	// Shock dynamics (fixture constants).
	AttractivenessDeclinePerMonth float64
	EmigrationPerMonth            int64 // fixed net emigration post-shock (linear decline)

	// Abandonment schedule (fixture constant).
	AbandonPerMonth int
	AbandonGrid     int // the abandoned cells live in a [0,AbandonGrid)² block
}

// CanonicalScenario returns the ASM-240 canonical reproducibility fixture:
// major-employer-closure at month 6 against a fixed 60,000-person city, with
// a linear post-shock population decline slow enough that the ghost-city
// warning (12 months' lead) is recorded well before the trigger (>= 6
// months, MinWarningLeadMonths). The exact numbers are fixture constants —
// the reproducibility CHECK is unaffected by their tuning (ASM-240).
func CanonicalScenario() Scenario {
	return Scenario{
		Version:                       1,
		Seed:                          0x5eed_2026_08_15,
		ShockMonth:                    6,
		ShockType:                     ShockMajorEmployerClosure,
		MonthCap:                      120,
		StartingPopulation:            60_000,
		StartTile:                     world.TileCoord{X: 15, Y: 15},
		AttractivenessDeclinePerMonth: 4.0,
		EmigrationPerMonth:            2_500,
		AbandonPerMonth:               2,
		AbandonGrid:                   20,
	}
}

// validate rejects a malformed scenario (AC-11): an unrecognised shock type,
// or a shock target that names an out-of-bounds cell. The rejection is
// registry-sourced and names the malformed field; nothing is silently
// ignored or substituted.
func (s Scenario) validate(correlationID string) error {
	if s.ShockType != ShockMajorEmployerClosure {
		return errs.New(ErrInvalidScenario, correlationID, map[string]any{
			"field": "shockType", "got": string(s.ShockType),
		})
	}
	if s.ShockTarget != nil {
		if !s.ShockTarget.Tile.InExtent() || !s.ShockTarget.Local.InBounds() {
			return errs.New(ErrInvalidScenario, correlationID, map[string]any{
				"field": "shockTarget", "got": s.ShockTarget.String(),
			})
		}
	}
	if s.MonthCap < 0 || s.ShockMonth < 0 || s.StartingPopulation < 0 || s.EmigrationPerMonth < 0 {
		return invalidScenario(correlationID, "scenario", "negative month/population/emigration")
	}
	return nil
}

// ScenarioOutcome is the reproducible result of one scenario run (AC-9): the
// ordered event sequence and the final state hash, plus the death verdict
// and the final month.
type ScenarioOutcome struct {
	Events    []Event
	StateHash string
	Death     DeathVerdict
	// DeathErr surfaces a death-condition GATE rejection that occurred
	// during the run (e.g. ErrGhostCityNoWarning when the ghost-city
	// threshold was reached with no qualifying warning on record) — the
	// run's death verdict alone cannot distinguish "no death condition
	// reached" from "the death condition was reached but the gate refused
	// to fire" (SEC-088). Nil when no gate rejection occurred.
	DeathErr error
	Month    int64
}

// attractivenessAt returns the scenario's attractiveness score at month —
// the engine.attract stand-in value this fixture feeds the stage
// transitions. Pre-shock it is the healthy baseline; post-shock it declines
// linearly to zero.
func (s Scenario) attractivenessAt(month int64) float64 {
	const baseline = 80.0
	post := month - s.ShockMonth
	if post <= 0 {
		return baseline
	}
	a := baseline - float64(post)*s.AttractivenessDeclinePerMonth
	if a < 0 {
		return 0
	}
	return a
}

// netMigrationAt returns the scenario's signed net migration at month — the
// engine.attract stand-in. Pre-shock mildly positive; post-shock a fixed
// negative emigration (the linear decline that makes the ghost-city warning
// lead exact and deterministic).
func (s Scenario) netMigrationAt(month int64) float64 {
	if month < s.ShockMonth {
		return 500
	}
	return -float64(s.EmigrationPerMonth)
}

// taxDeltaAt returns the scenario's tax-receipts change at month — the
// engine.finance stand-in. Post-shock the tax base shrinks with emigration.
func (s Scenario) taxDeltaAt(month int64) int64 {
	if month < s.ShockMonth {
		return 100_000
	}
	return -s.EmigrationPerMonth * 10
}

// abandonCellsAt returns the deterministic set of cells abandoned at month —
// the same set every run, regardless of worker count. Post-shock only, once
// the population is declining.
func (s Scenario) abandonCellsAt(month int64) []CellRef {
	if month < s.ShockMonth || s.AbandonPerMonth <= 0 {
		return nil
	}
	grid := s.AbandonGrid
	if grid <= 0 {
		grid = 20
	}
	out := make([]CellRef, 0, s.AbandonPerMonth)
	for i := 0; i < s.AbandonPerMonth; i++ {
		stream := det.NewStream(s.Seed, uint64(month), int64(i), "abandon")
		row := int(stream.IntN(int64(grid)))
		col := int(stream.IntN(int64(grid)))
		out = append(out, CellRef{
			Tile:  s.StartTile,
			Local: world.CellLocal{Row: row, Col: col},
		})
	}
	return out
}

// RunScenario runs the scenario headless to completion with a single shard —
// a death condition fires, or MonthCap is reached — returning the ordered
// event sequence and the final state hash (AC-9). It delegates to
// RunScenarioWorkers so the single-worker and multi-worker paths can never
// drift apart.
func (d *DecayAPI) RunScenario(sc Scenario) (ScenarioOutcome, error) {
	if err := d.checkNotCopied("RunScenario"); err != nil {
		return ScenarioOutcome{}, err
	}
	return d.RunScenarioWorkers(sc, 1)
}

// RunScenarioWorkers runs the scenario with a specific shard count for the
// decay-aging step (AC-9(b): identical outcome at different worker counts).
// The result is byte-identical across repeated runs and across worker
// counts: the only varying input is the shard count, which only
// re-partitions the deterministic decay-aging work.
func (d *DecayAPI) RunScenarioWorkers(sc Scenario, workers int) (ScenarioOutcome, error) {
	if err := d.checkNotCopied("RunScenarioWorkers"); err != nil {
		return ScenarioOutcome{}, err
	}
	if err := sc.validate(d.correlationID); err != nil {
		return ScenarioOutcome{}, err
	}
	var out ScenarioOutcome
	pop := sc.StartingPopulation
	for month := int64(0); month <= sc.MonthCap; month++ {
		res, err := d.AdvanceMonth(MonthInput{
			Month:          month,
			Attractiveness: sc.attractivenessAt(month),
			NetMigration:   sc.netMigrationAt(month),
			TaxDelta:       sc.taxDeltaAt(month),
			InsolvencyRisk: false,
			Population:     pop,
			ShockRecorded:  month >= sc.ShockMonth,
			AbandonCells:   sc.abandonCellsAt(month),
			Workers:        workers,
		})
		if err != nil {
			return out, err
		}
		out.Events = append(out.Events, res.Events...)
		out.Month = month
		// SEC-088: a gate rejection (res.DeathErr) must be surfaced, not
		// swallowed — otherwise a decline faster than the warning lead reads
		// as a silent "no death" when the death condition was actually
		// reached and refused by the FEAT-068 gate. Keep the first (and
		// therefore earliest) rejection, which is the one that explains why
		// the trigger never fired.
		if res.DeathErr != nil && out.DeathErr == nil {
			out.DeathErr = res.DeathErr
		}
		if res.Death != DeathNone {
			out.Death = res.Death
			break
		}
		pop = num.SatAdd(pop, num.ClampInt64FromFloat(sc.netMigrationAt(month)))
		if pop < 0 {
			pop = 0
		}
	}
	out.StateHash = d.StateHash()
	return out, nil
}
