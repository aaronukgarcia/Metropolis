package attract

import (
	"math"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// Migrant wealth draw (FEAT-1972079927 Q5, Aaron's 2026-08-31 ruling): each
// admitted migrant arrives with a VARIED wealth, drawn from a deterministic
// (seeded) log-normal distribution — never all-zero, never a flat constant.
// Real-world-grounded (docs/planning/money-numbers-real-world.md §3, ONS UK
// household liquid-savings distribution, arriving-migrant proxy): median
// £2,500, mean £6,000 (log-normal, right-skewed — wealth cannot go
// negative and a long positive tail pulls the mean above the median).
// wealthLogSigma is the log-scale shape parameter the doc derives from
// that median/mean pair. This is a per-citizen data field (Citizen.Wealth),
// never posted to engine.finance's ledger, so it is safe to use at full
// real-world scale regardless of baseline-one's much smaller toy treasury
// (see internal/engine/compose's ledgerScaleDivisor doc comment for why
// the LEDGER-facing amounts are scaled down and this one is not).
const (
	// migrantWealthMedianMicropounds is the log-normal median (exp(ln-mean)
	// when Z=0) — real-world-grounded, balance-pass adjustable.
	migrantWealthMedianMicropounds = 2_500_000_000 // µ£, £2,500
	// migrantWealthLogSigma is the log-scale standard deviation — real-
	// world-grounded (derived from the £2,500 median / £6,000 mean pair),
	// balance-pass adjustable.
	migrantWealthLogSigma = 1.1
)

// migrantWealth draws one migrant's arriving wealth from the deterministic
// log-normal distribution above: Wealth = median * exp(sigma * Z), where Z
// is a standard-normal variate produced by a Box-Muller transform over two
// uniform draws from the citizen's own counter-based RNG stream (seeded by
// worldSeed, the migrant's own id, and month — never math/rand, never wall
// clock, per FEAT-1972079927's determinism requirement). Two identical
// runs draw the identical Z for the identical id/month, so the whole
// simulation stays byte-reproducible. A non-positive result cannot occur
// (exp() is always positive; median is a positive constant), but the
// result is clamped at zero as defense-in-depth against a pathological Z.
func migrantWealth(worldSeed uint64, citizenID uint64, month int64) int64 {
	stream := det.NewStream(worldSeed, citizenID, month, "migrant-wealth")
	u1 := stream.Float64()
	u2 := stream.Float64()
	// Box-Muller: guard u1 against exactly 0 (log(0) = -Inf) — Float64's
	// range is [0,1), so 0 is reachable in principle; nudge to the
	// smallest positive representable step instead of special-casing.
	if u1 <= 0 {
		u1 = 1.0 / (1 << 53)
	}
	z := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	wealth := float64(migrantWealthMedianMicropounds) * math.Exp(migrantWealthLogSigma*z)
	if !num.IsFinite(wealth) || wealth < 0 {
		return 0
	}
	return num.ClampInt64FromFloat(wealth)
}

// migrantHouseholdSize is the v1 admitted-migrant household size. It is a
// SCHEMA consequence, not a balance number: engine.citizens' only household-
// formation primitive (LifeEventPartner) always forms a 2-member household,
// and AC-9 step 2 requires every new citizen to be attached to a household
// (never floating unassigned) — so immigration admits citizens in partnered
// pairs. A single-citizen household is impossible through the registered
// citizens interface today; flagging rather than inventing a phantom partner.
const migrantHouseholdSize = int64(2)

// migrantIDHighBit is the high-bit prefix on admitted-migrant citizen ids,
// keeping them clear of the small ids a seeded population uses. A schema
// constant (id-space partitioning), not a balance number.
//
// Part of a THREE-PACKAGE disjoint id map (FEAT-169, destructive-review
// REJECT finding): compose mints seed/direct ids from [1, 2^62), this
// package mints admitted-migrant ids from [2^62, 2^63) (this constant),
// and engine.citizens mints fertility-born child ids from
// [2^63, ...) (citizens.FertilityChildIDBase). The three counters are
// independent — not a shared allocator — so the disjointness is a
// convention, not a structural guarantee; internal/engine/compose asserts
// it at Wire time via [MigrantIDBase] (ErrCitizenIDNamespaceSeam), and
// engine.citizens independently rejects a duplicate-id birth as
// defense-in-depth (ErrDuplicateCitizenID). See citizens/doc.go's "Live-tick
// wiring" section for the full map, documented identically in
// compose/doc.go.
const migrantIDHighBit = uint64(1) << 62

// MigrantIDBase exports migrantIDHighBit's value (unchanged) so
// internal/engine/compose can assert the id-namespace-seam contract at
// Wire time (FEAT-169) without hand-duplicating the literal 1<<62 — see
// migrantIDHighBit's doc comment for the full three-package id map.
const MigrantIDBase = migrantIDHighBit

// emigrationBaseRate is the ambition-independent floor of the per-resident
// emigration hazard: hazard = decline · (base + (1−base)·ambitionScale).
// A directional placeholder pending M2 Batch tuning (GR#15's balance-number
// regime) — what matters for AC-6 is that the hazard is strictly increasing
// in ambition for any positive decline, never the absolute magnitude.
const emigrationBaseRate = 0.2

// MigrationCommand is the command-based monthly migration mutation (AC-1).
// It carries the simulation month, the resident set eligible for
// personality-weighted emigration, and the two capacity constraints on
// immigration: housing vacancy (dwelling units) and junction arrival
// throughput (people) — both supplied by the composition root / scenario
// (ASM-246), since engine.attract registers no direct call edge to
// engine.logistics.
type MigrationCommand struct {
	// Month is the absolute simulation month this migration applies to. It
	// keys the per-resident emigration hash stream (AC-12) and the
	// once-per-month reputation advance.
	Month int64

	// ResidentIDs is the citizen-id set eligible for emigration (the
	// composition root supplies the id set — CitizensAPI exposes per-id
	// queries, not an enumeration).
	ResidentIDs []uint64

	// HousingVacancy is the number of vacant dwelling units available for
	// incoming migrant households (scenario-computed from engine.households
	// stock vs occupancy). Must be non-negative.
	HousingVacancy int64

	// JunctionThroughput is the junction arrival capacity in people this
	// month (scenario-computed from engine.logistics). Must be non-negative.
	JunctionThroughput int64
}

// MigrationResult is ApplyMigration's return: the decomposed A score, the
// A_world baseline, the raw (pre-capacity) net migration, the applied
// inflow/outflow citizen counts, and the reputation after the step. The
// conservation invariant "net population change == Inflow − Outflow" is
// exactly checkable against CitizensAPI's reported population.
type MigrationResult struct {
	Month      int64
	A          float64
	AWorld     float64
	Net        float64 // g(A − A_world), signed, pre-capacity
	Inflow     int64   // citizens admitted (0 when Net <= 0)
	Outflow    int64   // citizens departed (0 when Net >= 0)
	Reputation float64 // reputation after this month's advance
}

// NetApplied returns Inflow − Outflow — the signed population change this
// migration actually applied (the conservation figure).
func (r MigrationResult) NetApplied() int64 {
	return num.SatSub(r.Inflow, r.Outflow)
}

// ApplyMigration runs one monthly migration step:
//
//  1. snapshot the six-term fundamentals and advance the reputation
//     momentum once for this month (idempotent per month);
//  2. compute A = weighted seven-term score;
//  3. compute net = g(A − A_world), signed (AC-4);
//  4. apply: a positive net admits capacity-capped migrant households
//     (AC-7), a negative net removes residents by personality-weighted
//     per-resident emigration hazards (AC-6).
//
// Every numeric input is validated at entry (FEAT-086); an invalid command
// mutates nothing. A missing citizens/finance/households dependency is a
// registry-sourced error, never a silent no-op.
func (a *AttractAPI) ApplyMigration(cmd MigrationCommand) (MigrationResult, error) {
	if err := a.checkNotCopied("ApplyMigration"); err != nil {
		return MigrationResult{}, err
	}
	if cmd.Month < 0 {
		return MigrationResult{}, errs.New(ErrInvalidMonth, a.correlationID, map[string]any{"month": cmd.Month})
	}
	if cmd.HousingVacancy < 0 {
		return MigrationResult{}, errs.New(ErrInvalidCapacity, a.correlationID, map[string]any{
			"field": "housingVacancy",
			"value": cmd.HousingVacancy,
		})
	}
	if cmd.JunctionThroughput < 0 {
		return MigrationResult{}, errs.New(ErrInvalidCapacity, a.correlationID, map[string]any{
			"field": "junctionThroughput",
			"value": cmd.JunctionThroughput,
		})
	}

	aWorld := a.AWorld()

	// Re-validate the world-pool baseline on every read (FEAT-086, defect
	// #2): a stateful/dynamic WorldPool may have returned a finite value at
	// construction and a NaN/±Inf/absurd value now. A non-finite baseline
	// must surface as a registry error, never as Net=NaN/±Inf with err==nil.
	if err := validateWorldScore(aWorld, a.correlationID); err != nil {
		return MigrationResult{}, err
	}

	terms, err := a.snapshotTerms()
	if err != nil {
		return MigrationResult{}, err
	}

	// Advance reputation once for this month (momentum reacts to this
	// month's fundamentals). Idempotent per month: re-running the same
	// month does not double-advance (GR#21 determinism).
	a.mu.Lock()
	if !a.hasAdvanced || cmd.Month != a.lastAdvancedMonth {
		a.reputation.advance(terms.fundamentals(), a.repCfg.RiseRate, a.repCfg.FallRate, a.repCfg.Max)
		a.hasAdvanced = true
		a.lastAdvancedMonth = cmd.Month
	}
	rep := a.reputation.value
	w := a.weights
	a.mu.Unlock()

	score := weightedSum(w, terms, rep)
	if !num.IsFinite(score) {
		return MigrationResult{}, errs.New(ErrConfigInvalid, a.correlationID, map[string]any{
			"field": "A",
			"value": score,
		})
	}

	net, err := a.G(score - aWorld)
	if err != nil {
		return MigrationResult{}, err
	}

	res := MigrationResult{
		Month:      cmd.Month,
		A:          score,
		AWorld:     aWorld,
		Net:        net,
		Reputation: rep,
	}

	switch {
	case net > 0:
		inflow, err := a.applyImmigration(cmd, net)
		if err != nil {
			return MigrationResult{}, err
		}
		res.Inflow = inflow
	case net < 0:
		outflow, err := a.applyEmigration(cmd, net)
		if err != nil {
			return MigrationResult{}, err
		}
		res.Outflow = outflow
	}
	return res, nil
}

// applyImmigration admits up to the capacity-capped number of migrants as
// partnered households (migrantHouseholdSize citizens each), each admitted
// citizen attached to a household via engine.citizens' LifeEventPartner
// (AC-9 step 2). Returns the number of citizens admitted. Capacity is the
// minimum of the junction arrival throughput (people) and the housing
// vacancy converted to people (dwelling units × household size); a vacancy
// of zero therefore caps admission at zero regardless of a large positive
// gap (AC-7).
func (a *AttractAPI) applyImmigration(cmd MigrationCommand, net float64) (int64, error) {
	if err := a.checkNotCopied("applyImmigration"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	cit := a.citizens
	a.mu.RUnlock()
	if cit == nil {
		return 0, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "citizens",
			"operation":  "immigration",
		})
	}

	raw := num.ClampInt64FromFloat(net)
	if raw <= 0 {
		return 0, nil
	}

	// Housing vacancy is in dwelling units (households); convert to people
	// so both capacity terms are comparable. Saturating multiply (FEAT-086).
	vacancyPeople, _ := num.SafeMul(cmd.HousingVacancy, migrantHouseholdSize)
	capPeople := minI64(cmd.JunctionThroughput, vacancyPeople)
	admitPeople := minI64(raw, capPeople)
	if admitPeople <= 0 {
		return 0, nil
	}
	// Whole migrant households only (migrantHouseholdSize citizens each).
	pairs := admitPeople / migrantHouseholdSize
	if pairs <= 0 {
		return 0, nil
	}

	month := cmd.Month
	var admitted int64
	for i := int64(0); i < pairs; i++ {
		idA := a.mintMigrantID()
		idB := a.mintMigrantID()
		if err := a.birthMigrant(cit, idA, month); err != nil {
			return admitted, err
		}
		if err := a.birthMigrant(cit, idB, month); err != nil {
			return admitted, err
		}
		if err := cit.ApplyLifeEventCommand(citizens.LifeEventCommand{
			CorrelationID: a.correlationID,
			Kind:          citizens.LifeEventPartner,
			CitizenID:     idA,
			PartnerID:     idB,
		}); err != nil {
			return admitted, err
		}
		admitted = num.SatAdd(admitted, migrantHouseholdSize)
	}
	return admitted, nil
}

// mintMigrantID returns the next deterministic, collision-cleared migrant
// citizen id (high-bit prefix). Guarded by mu — two sequential calls always
// yield distinct ids.
func (a *AttractAPI) mintMigrantID() uint64 {
	if err := a.checkNotCopied("mintMigrantID"); err != nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextMigrantID++
	return migrantIDHighBit | a.nextMigrantID
}

// birthMigrant creates one migrant citizen via engine.citizens'
// LifeEventBirth command — the only registered citizen-creation path. The
// record is neutral (a documented v1 placeholder: neutral personality, no
// wealth, unemployed — the world-pool personality distribution is a future
// hook) and always passes citizens' validation.
func (a *AttractAPI) birthMigrant(cit *citizens.CitizensAPI, id uint64, month int64) error {
	if err := a.checkNotCopied("birthMigrant"); err != nil {
		return err
	}
	rec := citizens.Citizen{
		ID:          id,
		BirthMonth:  clampBirthMonth(month),
		Sex:         citizens.Sex(id & 1),
		Personality: neutralMigrantPersonality(),
		HealthBand:  citizens.HealthGood,
		Employment: citizens.Employment{
			State:  citizens.EmploymentUnemployed,
			Sector: citizens.SectorNone,
		},
		Fidelity: citizens.FidelityCold,
		// FEAT-1972079927 Q5: migrants arrive with varied wealth (a
		// deterministic log-normal draw), not the old flat zero — see
		// migrantWealth's doc comment.
		Wealth: migrantWealth(a.seed, id, month),
	}
	return cit.ApplyLifeEventCommand(citizens.LifeEventCommand{
		CorrelationID: a.correlationID,
		Kind:          citizens.LifeEventBirth,
		Citizen:       rec,
	})
}

// clampBirthMonth coerces a simulation month into the citizens hot record's
// int16 birth-month domain (defensive — a month beyond 32767 would wrap a
// bare int32→int16 narrowing). Baseline One's months never approach the
// bound; the clamp keeps a fuzzed month from ever wrapping negative.
func clampBirthMonth(month int64) int32 {
	if month < 0 {
		return 0
	}
	if month > math.MaxInt16 {
		return math.MaxInt16
	}
	return int32(month)
}

// neutralMigrantPersonality returns the v1 neutral migrant personality
// (each axis at the midpoint), derived from citizens.MaxPersonalityAxis
// rather than a literal — a documented placeholder pending the world-pool
// personality distribution (a future hook).
func neutralMigrantPersonality() citizens.Personality {
	var p citizens.Personality
	for axis := 0; axis < citizens.NumPersonalityAxes; axis++ {
		p[axis] = citizens.MaxPersonalityAxis / 2
	}
	return p
}

// applyEmigration removes up to |net| residents via per-resident,
// personality-weighted (ambition) hazards — AC-6's "ambitious citizens leave
// sooner when opportunity dries up" — with each departure decided by the
// counter-based hash stream hash(worldSeed, id, month, "emigrate") (AC-12).
// Returns the number of citizens departed.
func (a *AttractAPI) applyEmigration(cmd MigrationCommand, net float64) (int64, error) {
	if err := a.checkNotCopied("applyEmigration"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	cit := a.citizens
	a.mu.RUnlock()
	if cit == nil {
		return 0, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "citizens",
			"operation":  "emigration",
		})
	}

	decline := clampFloat(-net, 0, 1) // |net| saturating at 1 — the decline magnitude
	if decline <= 0 {
		return 0, nil
	}

	var departed int64
	for _, id := range cmd.ResidentIDs {
		hazard := a.emigrationHazardLocked(cit, id, decline)
		stream := det.NewStream(a.seed, id, cmd.Month, "emigrate")
		if stream.Float64() >= hazard {
			continue
		}
		if err := cit.ApplyLifeEventCommand(citizens.LifeEventCommand{
			CorrelationID: a.correlationID,
			Kind:          citizens.LifeEventDeath,
			CitizenID:     id,
		}); err != nil {
			// LifeEventDeath is a pure deletion and a no-op for an id that no
			// longer resolves; the only reachable error is a copied-value /
			// validation rejection, which must propagate rather than be
			// silently swallowed (GR#1).
			return departed, err
		}
		departed = num.SatAdd(departed, 1)
	}
	return departed, nil
}

// emigrationHazardLocked is the per-resident emigration hazard, computed
// from the citizen's ambition axis and the decline magnitude. It is the
// AC-6 function: hazard is strictly increasing in ambition for any positive
// decline. The citizen is read via CitizensAPI (a per-id query); an
// unresolvable id yields hazard 0 (no departure).
func (a *AttractAPI) emigrationHazardLocked(cit *citizens.CitizensAPI, id uint64, decline float64) float64 {
	if err := a.checkNotCopied("emigrationHazardLocked"); err != nil {
		return 0
	}
	c, ok := cit.CitizenAt(id, a.correlationID)
	if !ok {
		return 0
	}
	ambition := clampFloat(float64(c.Personality[citizens.AxisAmbition]), 0, float64(citizens.MaxPersonalityAxis))
	return EmigrationHazard(ambition, decline)
}

// EmigrationHazard returns one resident's per-month emigration probability
// in a declining city (AC-6's per-resident decision, exposed for direct
// inspection): hazard = decline · (base + (1−base)·ambitionScale), where
// ambitionScale ∈ [0,1] and decline ∈ [0,1]. Strictly increasing in
// ambition for any positive decline, so the higher-ambition of two
// otherwise-identical citizens always has the greater hazard.
func EmigrationHazard(ambition, decline float64) float64 {
	ambition = clampFloat(ambition, 0, 100)
	decline = clampFloat(decline, 0, 1)
	ambitionScale := ambition / float64(citizens.MaxPersonalityAxis)
	hazard := decline * (emigrationBaseRate + (1-emigrationBaseRate)*ambitionScale)
	return clampFloat(hazard, 0, 1)
}
