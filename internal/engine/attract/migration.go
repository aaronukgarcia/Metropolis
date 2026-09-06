package attract

import (
	"math"
	"sort"

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
// real-world scale regardless of baseline-one's treasury (see
// internal/engine/compose/moneycirc.go's "Ledger scale vs real-world
// scale" doc comment for why LEDGER-facing amounts get separate treatment
// and this one does not — pre-BUG-452 that doc comment described a
// ledgerScaleDivisor hack; BUG-452 (2026-09-01) retired it in favour of
// posting real figures directly against a real-scale treasury).
const (
	// migrantWealthMedianMicropounds is the log-normal median (exp(ln-mean)
	// when Z=0) — real-world-grounded, balance-pass adjustable. Rebased
	// 2_500_000_000 -> 2_500_000 (BUG-452, 2026-09-01) alongside the money
	// base-unit rebase (1e-6 GBP/unit -> 1e-3 GBP/unit) so this stays the
	// same real £2,500, not a value 1000x too large.
	migrantWealthMedianMicropounds = 2_500_000 // µ£, £2,500
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

// migrantTenureGraceMonths is BUG-380's fix for the reproducible "sawtooth
// boom/bust population collapse": a migrant admitted this month is NOT
// emigration-eligible until this many months of simulated residency have
// passed (applyEmigration below skips any id whose tenure is under this
// floor). Aaron's ruling (2026-09-05, BUG-380): the sawtooth was never
// evidence that migrants must stay permanently exempt from emigration
// (compose.go's residentIDs()/migrantIDsFromCount doc comments record two
// earlier, reverted attempts that read it that way) — it is the expected
// result of letting a migrant admitted in month M be an emigration
// candidate again in month M+1, before the city has had a chance to
// stabilise around them (wage/employment/household formation all take a
// few months to catch up — see liveResidentIDs' BUG-529/BUG-535 history).
// 12 (one simulated year) is a directional PLACEHOLDER pending Aaron's
// balance pass (GR#15's balance-number regime, same tier as
// emigrationBaseRate above) — a var, not a const, so a test can override
// it to demonstrate the sawtooth returns at grace=0 (this ticket's own
// RED-proof: TestMigrantTenureGrace_ZeroGraceReproducesSawtooth).
var migrantTenureGraceMonths int64 = 12

// emigrationMaxMonthlyShare is a HARD ceiling (BUG-380 third re-round,
// opus-reround3-bug380, P0-class finding): the maximum fraction of
// cmd.ResidentIDs that may depart via emigration in a single
// applyEmigration call, independent of net's magnitude — a safety backstop
// so a runaway is impossible even if the demanded outflow (|net|) is
// itself huge. A placeholder pending Aaron's balance pass (same tier as
// emigrationBaseRate/migrantTenureGraceMonths above), 2% chosen only to be
// small enough that the "capped at |net|" behaviour below (which already
// enforces the documented "removes up to |net| residents" contract) is
// the binding constraint in ordinary play, and this ceiling only bites a
// pathological net.
const emigrationMaxMonthlyShare = 0.02

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

	// BUG-380 re-round finding P1 (opus-reround-bug380): sweep
	// migrantAdmittedMonth for entries whose citizen no longer resolves —
	// see sweepDepartedMigrantTenure's own doc comment for why this reuses
	// the ALREADY-REGISTERED engine.attract -> engine.citizens edge
	// (CitizenAt) rather than a new "citizen removed" notification edge
	// (GR#25: no such inbound edge exists, and none is added here).
	// Best-effort: a caller that has not yet wired citizens (cit == nil)
	// is a pre-existing, valid state for a net==0 month that never touches
	// cit at all elsewhere in this function either — skipping the sweep
	// once is harmless, it simply runs on the next call that has cit wired.
	a.mu.RLock()
	sweepCit := a.citizens
	a.mu.RUnlock()
	if sweepCit != nil {
		a.sweepDepartedMigrantTenure(sweepCit)
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

	// MOD-034 seam: fold the injected wellbeing satisfaction modifier into
	// the attractiveness score multiplicatively (SatisfactionModifier is
	// 1.0 at perfect health, falling as the cohort's wellbeing worsens —
	// wellbeing.WellbeingAPI.SatisfactionModifier's own doc comment), so a
	// declining-wellbeing city becomes less attractive to migrants exactly
	// the way a declining reputation or job market already does. Consulted
	// once here per ApplyMigration call, which is itself the once-per-month
	// migration step (see this file's package doc) — never re-consulted
	// mid-month. nil getter (the default) leaves score unchanged, i.e.
	// today's behaviour.
	satisfactionMod, emigrationMod := a.wellbeingModifierPair()
	score *= satisfactionMod

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
		outflow, err := a.applyEmigration(cmd, net, emigrationMod)
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
		// BUG-380 tenure grace: record the admission month for BOTH
		// household members now that birth+partner have both succeeded —
		// see recordMigrantAdmission's own doc comment and
		// migrantAdmittedMonth's field doc (api.go).
		a.recordMigrantAdmission(idA, month)
		a.recordMigrantAdmission(idB, month)
		admitted = num.SatAdd(admitted, migrantHouseholdSize)
	}
	return admitted, nil
}

// recordMigrantAdmission stores id's admission month in
// migrantAdmittedMonth under mu (BUG-380 tenure grace). Called once per
// admitted migrant, right after that migrant's birth+partner commands both
// succeed — never before, so a migrant that failed validation mid-admission
// (an early return above) is never recorded as tenured for an id that does
// not actually exist as a citizen.
func (a *AttractAPI) recordMigrantAdmission(id uint64, month int64) {
	if err := a.checkNotCopied("recordMigrantAdmission"); err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.migrantAdmittedMonth == nil {
		a.migrantAdmittedMonth = make(map[uint64]int64)
	}
	a.migrantAdmittedMonth[id] = month
}

// mintMigrantID returns the next deterministic, collision-cleared migrant
// citizen id (high-bit prefix). Guarded by mu — two sequential calls always
// yield distinct ids.
//
// BUG-380 round finding P3 (opus-round-bug380, noted not fixed — see
// below): New() initialises nextMigrantID to 1, so a normal FIRST call
// returns base+2, matching compose.go's migrantIDsFromCount's [base+2,
// base+M] enumeration. An ANCIENT save record — one predating this
// package's nextMigrantID field entirely, decoding it as the Go zero value
// 0 via participant.go's applyLoadRecord — leaves the counter at 0
// instead, so THAT composition's first post-load mint returns base+1, an
// id migrantIDsFromCount can never enumerate (attack_bug380_round_test.go's
// TestAttack380_PostLoadFirstMintCanBeBasePlusOne pins exactly this).
// Not fixed here: distinguishing "an ancient record with the key entirely
// absent" from "a hypothetical modern record explicitly encoding
// nextMigrantID:0" requires a raw pre-pass over the JSON (no version of
// this participant's own serializer has ever WRITTEN 0, so in practice a
// decoded 0 always means the ancient case) or changing the wire type to a
// pointer — either is more than this round's P3 budget, and coercing
// every decoded 0 to 1 unconditionally would break
// TestAttack_HandlerResetsExactlyOncePerLoad's existing, correct
// expectation that an explicit all-zero record leaves nextMigrantID at 0.
// Left as a known, narrow limitation (affects at most one id, on saves
// old enough to predate FEAT-1972079947 entirely) rather than risking a
// new regression to close it.
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
// wealth — the world-pool personality distribution is a future hook) and
// always passes citizens' validation.
//
// BUG-529: Employment.State is minted EmploymentNone (the zero value, "not
// yet decided" — same state the seed population starts in), NEVER
// EmploymentUnemployed. compose.desiredEmployment (moneycirc.go) treats
// Employed/Unemployed/OffMap as TERMINAL — once a citizen is in one of
// those three states it is never redrawn — so minting a migrant directly
// into EmploymentUnemployed permanently excluded every migrant from the
// 75%-employed employmentDecision draw: only the ~55 seed citizens (minted
// with a zero-value Employment{}, i.e. EmploymentNone) ever became
// Employed, and the wage bill pinned at monthlyWagesFloor as the seed
// cohort attrited and organic migration grew the population (probe: 41->1
// employed over 24 months while population grew to 113). Option A over
// Option B (a decided-vs-undecided-unemployed split in desiredEmployment):
// EmploymentNone already IS citizens' "not yet decided" state for exactly
// this purpose (every other reader of EmploymentNone treats it as "child OR
// adult never worked" — see leisure.lifeStageFor's age fallback and
// extcommute's own EmploymentNone doc comment — never as "must be a
// newborn"), so redirecting migrants through the SAME undecided state the
// seed population already uses is the lower-blast-radius fix: no new state,
// no new branch in desiredEmployment, and every existing EmploymentNone
// reader already handles an adult correctly by age, not by assuming child.
func (a *AttractAPI) birthMigrant(cit *citizens.CitizensAPI, id uint64, month int64) error {
	if err := a.checkNotCopied("birthMigrant"); err != nil {
		return err
	}
	// BUG-517: an arriving migrant is not a newborn — they arrive with a
	// realistic UK-like age (drawn deterministically from citizens' age
	// pyramid, same mechanism as the seed population), never a flat age 0.
	age := citizens.DrawAgeAtCreationMonths(a.seed, id, month)
	rec := citizens.Citizen{
		ID:          id,
		BirthMonth:  citizens.BirthMonthForAge(month, age),
		Sex:         citizens.Sex(id & 1),
		Personality: neutralMigrantPersonality(),
		HealthBand:  citizens.HealthGood,
		Employment: citizens.Employment{
			State:  citizens.EmploymentNone,
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
// emigrationMod is MOD-034's wellbeing emigration multiplier (1.0 at
// perfect health, rising as the cohort's wellbeing worsens —
// ApplyMigration's caller already resolved it, via
// wellbeingModifiersLocked, to the neutral 1.0 when no seam is wired),
// folded in multiplicatively so a declining-wellbeing city loses residents
// faster at the same ambition/decline shape. Returns the number of
// citizens departed.
//
// BUG-380 third re-round finding P0 (opus-reround3-bug380): "removes up
// to |net| residents" above was aspirational, not enforced — pre-fix, net
// was used ONLY to derive decline (the [0,1] hazard MAGNITUDE), and the
// loop below walked cmd.ResidentIDs UNCAPPED, departing every id whose
// independent per-resident hazard draw cleared, with no relationship to
// |net|'s actual size. decline saturates to 1.0 for almost any real
// declining city (clampFloat(-net,0,1): net's raw magnitude is city-scale,
// routinely >>1, so -net clamps straight to the ceiling), and the
// per-resident hazard floor is emigrationBaseRate=0.2 regardless of
// ambition — so a sustained decline removed >=20% of the ENTIRE eligible
// pool every month. Pre-BUG-380, residentIDs() enumerated only the closed
// ~55-citizen seed range, so this was invisible (20% of ~55 native
// survivors is a handful of people, never noticed). Once residentIDs()
// was correctly widened to include the full live migrant population
// (BUG-380's own fix), the SAME uncapped loop walked the ENTIRE city:
// measured on seed 4242 (TestBUG380_PopulationNeverCollapsesMoreThanCap),
// population fell 905->292 in ONE month at month 191 (a >65% single-month
// collapse), with organic admissions flatlined at 1087 since ~month 160
// (every arriving cohort immediately eligible for the same uncapped
// 20%+/month cull once past the tenure grace).
//
// Fixed: total departures per call are now capped at
// min(rawOutflow, hardCeiling), where rawOutflow is |net| converted to a
// headcount exactly the way applyImmigration's own admitPeople derives its
// cap from the POSITIVE branch's net (num.ClampInt64FromFloat — the same
// truncating conversion, so a sub-1-person demanded outflow rounds to
// zero departures that month, mirroring immigration's identical "raw<=0 ->
// no-op" floor) and hardCeiling is emigrationMaxMonthlyShare (2%,
// placeholder) of the ACTUALLY-LIVE resident count (a fresh CitizenAt
// walk, deliberately not len(cmd.ResidentIDs) — see the hardCeiling
// computation's own doc comment for why that slice's length can drift
// above the true population) — a backstop independent of net's
// magnitude, so a runaway is impossible even if the demanded outflow
// itself is huge.
//
// BUG-380 fourth re-round finding P0 (opus-reround4-bug380): the FIRST cap
// implementation (immediately above, third re-round) selected which ids
// actually departed by walking cmd.ResidentIDs in its existing order and
// taking the first N (by that order) whose hazard draw cleared, stopping
// once the cap was reached. cmd.ResidentIDs is sorted ascending by id
// (natives, then migrants by admission order — compose.go's residentIDs()),
// so under a BINDING cap (more eligible candidates than the cap allows —
// the common case under sustained decline) this deterministically starved
// every HIGH-id citizen: the lowest ids always exhaust the cap first,
// every single month, regardless of ambition. Measured on seed 4242 over
// 200 months: native citizens (ids 1-64) fell to 1/64 (98% gone) while the
// newest migrant cohort stayed 97% intact — pure scan-position bias, and
// it INVERTS AC-6 ("ambitious citizens leave sooner"): an id's position in
// a sorted enumeration has nothing to do with its ambition.
//
// Fixed: candidates are no longer selected by scan order. EVERY eligible
// id's hazard and RNG draw are evaluated first (unconditionally, cap not
// yet applied), producing a candidate list of every id whose draw actually
// cleared its own hazard; candidates are then SORTED by draw/hazard ratio
// ascending (the strongest margin — furthest below its own threshold —
// first; ties broken by id ascending for a total, deterministic order,
// GR#21: an explicit sort.Slice call, never a map range), and only the
// first outflowCap-many (by that sorted order) actually depart. This
// preserves AC-6 exactly: a high-ambition citizen's higher hazard makes a
// low draw/hazard ratio easier to achieve (the same draw distribution
// against a larger denominator), so ambition still drives who leaves
// first under a binding cap — id/admission-order no longer does. See
// TestBUG380_SurvivalIsFairAcrossIDBands (compose) for the 200-month
// fairness proof and TestBUG380_AmbitionStillDrivesSelectionUnderCap
// (attract) for the direct AC-6 pin.
func (a *AttractAPI) applyEmigration(cmd MigrationCommand, net float64, emigrationMod float64) (int64, error) {
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

	// BUG-380 third re-round finding P0 (opus-reround3-bug380): the
	// documented "removes up to |net| residents" cap, actually enforced.
	// rawOutflow mirrors applyImmigration's own admitPeople derivation
	// (num.ClampInt64FromFloat(net) there, on the POSITIVE branch) exactly
	// — the same truncating float->int64 conversion, so a demanded outflow
	// under 1 person rounds to zero departures this month, symmetric with
	// immigration's identical "raw<=0 -> no-op" floor.
	rawOutflow := num.ClampInt64FromFloat(-net)
	// hardCeiling: emigrationMaxMonthlyShare of the ACTUALLY-LIVE resident
	// count, a backstop independent of net's magnitude — see
	// emigrationMaxMonthlyShare's own doc comment for why this must hold
	// even when rawOutflow itself is huge (a pathological score/A_world
	// gap). Deliberately NOT len(cmd.ResidentIDs): that slice is
	// compose.go's residentIDs(), which enumerates the full HISTORICAL
	// migrant id range (migrantIDsFromCount) regardless of whether each id
	// is still alive — a departed-or-dead id is simply skipped downstream
	// by CitizenAt !ok checks, so the slice's LENGTH only ever grows and
	// can meaningfully exceed the true live population once enough
	// deaths/departures have accumulated. Basing the 2% ceiling on that
	// ever-growing count would let the ceiling itself drift upward past
	// what "2% of the real city" actually means, undermining the exact
	// safety property this backstop exists for (caught by this fix's own
	// TestBUG380_PopulationNeverCollapsesMoreThanCap: a len(ResidentIDs)
	// -based ceiling reproduced a bound-exceeding drop at month 191 even
	// WITH the cap logic in place, because ResidentIDs had already grown
	// past the live population by then).
	//
	// BUG-380 fourth re-round finding P3 (opus-reround4-bug380,
	// performance): liveResidentCount is CitizensAPI's own
	// TotalPopulation() — an O(shards) aggregate the citizens module
	// already maintains — NOT a second CitizenAt walk over
	// cmd.ResidentIDs. The third re-round's cap fix walked
	// cmd.ResidentIDs twice (once to count live residents, once inside
	// emigrationHazardLocked per hazard-eligible id); this round folded
	// those into one shared walk (the candidate-building loop below), but
	// a full walk just to COUNT live residents is still needless work
	// TotalPopulation already answers directly. residentCount and
	// TotalPopulation can differ very slightly in principle (ResidentIDs
	// is compose's own resident enumeration; TotalPopulation is
	// CitizensAPI's own live count across every shard, fidelity, and
	// range — including, e.g., a citizen compose's enumeration has not
	// yet been told about this exact tick) but they describe the SAME "how
	// big is the city" question this ceiling exists to bound, and the
	// small residual is immaterial next to the 2% share's own
	// already-generous rounding.
	liveResidentCount := cit.TotalPopulation(a.correlationID)
	hardCeiling := num.ClampInt64FromFloat(math.Ceil(float64(liveResidentCount) * emigrationMaxMonthlyShare))
	outflowCap := rawOutflow
	if hardCeiling < outflowCap {
		outflowCap = hardCeiling
	}
	if outflowCap <= 0 {
		// No budget to spend — skip the belowGrace/candidate-building walk
		// below entirely (a cheap early exit now that hardCeiling no
		// longer needs that walk to compute itself).
		return 0, nil
	}

	// BUG-380 round finding P3 (opus-round-bug380, performance): a single
	// RLock over the whole ResidentIDs slice, rather than
	// migrantBelowTenureGrace's own per-id RLock/RUnlock pair (still used
	// by callers outside this hot loop, e.g. tests). cmd.ResidentIDs can be
	// the full live population every month (compose.go's residentIDs()),
	// so per-id lock/unlock churn is real, avoidable contention. The gate
	// decision for every id is fully determined by migrantAdmittedMonth as
	// of THIS instant — nothing later in this function (hazard calc,
	// LifeEventDeath) can change tenure eligibility mid-pass — so computing
	// every id's gate result up front under one lock, then running the
	// rest lock-free, changes nothing observable.
	a.mu.RLock()
	belowGrace := make([]bool, len(cmd.ResidentIDs))
	for i, id := range cmd.ResidentIDs {
		belowGrace[i] = a.migrantBelowTenureGraceLocked(id, cmd.Month)
	}
	a.mu.RUnlock()

	// BUG-380 fourth re-round finding P0 (opus-reround4-bug380): draws
	// every non-grace-blocked id's hazard and RNG draw UNCONDITIONALLY (no
	// cap applied yet), collecting a candidate for every id whose draw
	// actually cleared its own hazard. EmigrationHazard (the pure,
	// citizens-independent function) is used directly against the single
	// CitizenAt lookup this loop already needs — no second per-id lookup
	// the way the third re-round's fix (a separate emigrationHazardLocked
	// call, itself calling CitizenAt again) required.
	type emigrationCandidate struct {
		id    uint64
		ratio float64 // draw/hazard — lower means a stronger margin to depart
	}
	candidates := make([]emigrationCandidate, 0, len(cmd.ResidentIDs))
	for i, id := range cmd.ResidentIDs {
		if belowGrace[i] {
			continue
		}
		c, ok := cit.CitizenAt(id, a.correlationID)
		if !ok {
			continue
		}
		ambition := clampFloat(float64(c.Personality[citizens.AxisAmbition]), 0, float64(citizens.MaxPersonalityAxis))
		hazard := clampFloat(EmigrationHazard(ambition, decline)*emigrationMod, 0, 1)
		if hazard <= 0 {
			continue
		}
		stream := det.NewStream(a.seed, id, cmd.Month, "emigrate")
		draw := stream.Float64()
		if draw >= hazard {
			continue
		}
		candidates = append(candidates, emigrationCandidate{id: id, ratio: draw / hazard})
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	// BUG-380 fourth re-round finding P0: SORT candidates by margin
	// (draw/hazard ascending — the strongest "wanted to leave" signal
	// first), id ascending as the deterministic tiebreak, so the outcome
	// never depends on cmd.ResidentIDs' own scan order. An explicit
	// sort.Slice over a slice, never a map range — GR#21's
	// "no map-range-with-break" is fully satisfied: the ORDER here is a
	// pure, deterministic function of (id, draw, hazard), identical on
	// every run of the same seed/month, and taking the first
	// outflowCap-many after sorting is exactly as deterministic as the
	// old first-N-by-scan-order selection was, without that selection's
	// id-position bias (see this function's own doc comment for the
	// measured bias: natives fell to 1/64 while the newest migrant cohort
	// stayed 97% intact under the position-biased selection).
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ratio != candidates[j].ratio {
			return candidates[i].ratio < candidates[j].ratio
		}
		return candidates[i].id < candidates[j].id
	})

	n := outflowCap
	if int64(len(candidates)) < n {
		n = int64(len(candidates))
	}
	var departed int64
	for i := int64(0); i < n; i++ {
		id := candidates[i].id
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
		// BUG-380 round finding P2 (opus-round-bug380): prune the departed
		// id's tenure entry NOW — this is the one departure channel attract
		// can actually see (it issued the LifeEventDeath itself). A no-op
		// map delete for a native id (never in the map). See
		// migrantAdmittedMonth's own field doc (api.go) for what this does
		// and does NOT bound: a citizen who dies of NATURAL causes
		// (mortality, citizens' own coldpass) departs with no callback to
		// this package at all, so THOSE entries are never pruned here —
		// the map's true steady-state bound is "live migrants (admitted,
		// not yet emigrated by attract) + migrants who died naturally that
		// attract was never notified of", not "live migrants" alone.
		a.pruneMigrantTenure(id)
		departed = num.SatAdd(departed, 1)
	}
	return departed, nil
}

// pruneMigrantTenure removes id's entry from migrantAdmittedMonth, if any
// (a no-op for a native/seed id, which was never in the map). Takes its
// own short-lived write lock — called at most once per ACTUAL departure in
// applyEmigration's loop (not once per candidate id), so this does not
// reintroduce the per-id lock churn the RLock-hoisting above just removed.
func (a *AttractAPI) pruneMigrantTenure(id uint64) {
	if err := a.checkNotCopied("pruneMigrantTenure"); err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.migrantAdmittedMonth, id)
}

// sweepDepartedMigrantTenure removes every migrantAdmittedMonth entry
// whose citizen no longer resolves via CitizensAPI.CitizenAt — BUG-380
// re-round finding P1 (opus-reround-bug380): pruneMigrantTenure above only
// catches emigration-caused departures (the ones THIS package issues
// itself via LifeEventDeath); a migrant who dies of NATURAL causes
// (citizens' own coldpass mortality hazard) has no callback into this
// package at all, so those entries used to accumulate as an unbounded
// "orphan" residual with no ceiling — measured (opus-reround-bug380's
// TestReround380_OrphanGrowthOverLongRun) at 45 orphans by month 200 on a
// roughly linear ~0.25/month growth, not the small constant the P2 fix's
// orphanSlack assumed.
//
// Rather than adding a NEW engine.citizens -> engine.attract "citizen
// removed" notification edge (GR#25: no such inbound edge is registered
// today, and this bug fix's scope does not license adding one), this
// sweep reuses the ALREADY-REGISTERED engine.attract -> engine.citizens
// edge (CitizenAt, called throughout this package: applyEmigration's
// candidate-building pass, birthMigrant's duplicate-id defense, etc.) to
// directly check every
// tenured migrant's liveness and prune the dead ones. Called once at the
// top of every ApplyMigration invocation (this package's only per-month
// entry point) — an O(len(migrantAdmittedMonth)) pass bounded by the
// map's own (now actively-pruned, so small) size.
//
// BUG-380 third re-round finding P3 (opus-reround3-bug380): does NOT hold
// a.mu across the CitizenAt calls — follows applyEmigration's own
// established precedent (its belowGrace pre-pass, above) of collecting
// under one short RLock, doing the potentially-many, potentially-slower
// cross-module work with NO lock held, then taking a second short Lock
// only for the actual mutation. Holding a.mu.Lock() (a WRITE lock) for the
// full duration of len(migrantAdmittedMonth) CitizenAt calls would block
// every other AttractAPI reader/writer for that whole pass — needless
// contention this three-step shape avoids. The final delete pass does not
// depend on any ordering (each id is deleted independently), so GR#21's
// "no map-range-with-break" is not implicated either way.
func (a *AttractAPI) sweepDepartedMigrantTenure(cit *citizens.CitizensAPI) {
	if err := a.checkNotCopied("sweepDepartedMigrantTenure"); err != nil {
		return
	}
	a.mu.RLock()
	ids := make([]uint64, 0, len(a.migrantAdmittedMonth))
	for id := range a.migrantAdmittedMonth {
		ids = append(ids, id)
	}
	a.mu.RUnlock()

	var departedIDs []uint64
	for _, id := range ids {
		if _, ok := cit.CitizenAt(id, a.correlationID); !ok {
			departedIDs = append(departedIDs, id)
		}
	}
	if len(departedIDs) == 0 {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for _, id := range departedIDs {
		delete(a.migrantAdmittedMonth, id)
	}
}

// wellbeingModifierPair returns the MOD-034 satisfaction/emigration
// modifier pair, resolving to the documented neutral (1.0, 1.0) when no
// getter is wired (SetWellbeingModifiers never called, or called with
// nil). Takes a.mu itself (RLock) — mirrors this package's other plain
// accessors (Reputation, JobAvailability, etc.), never called with mu
// already held.
func (a *AttractAPI) wellbeingModifierPair() (satisfaction, emigration float64) {
	if err := a.checkNotCopied("wellbeingModifierPair"); err != nil {
		return 1.0, 1.0
	}
	a.mu.RLock()
	getter := a.wellbeingModifiers
	a.mu.RUnlock()
	if getter == nil {
		return 1.0, 1.0
	}
	return getter()
}

// migrantBelowTenureGrace reports whether id is a MIGRANT whose tenure
// (month - admittedMonth) is still under migrantTenureGraceMonths
// (BUG-380). A native/seed id (never present in migrantAdmittedMonth) is
// never gated — the grace period only ever applies to admitted migrants,
// "on the same terms as natives" once past it. A migrant somehow queried
// for a month strictly before its own admission (tenure negative — should
// never happen in practice, since ApplyMigration's caller advances Month
// monotonically) is conservatively treated as still within grace (blocked),
// never as a hazard-eligible negative-tenure edge case.
func (a *AttractAPI) migrantBelowTenureGrace(id uint64, month int64) bool {
	// A copied receiver cannot be trusted: fail SAFE (treat as still under grace, so no departure is drawn).
	if err := a.checkNotCopied("migrantBelowTenureGrace"); err != nil {
		return true
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.migrantBelowTenureGraceLocked(id, month)
}

// migrantBelowTenureGraceLocked is migrantBelowTenureGrace's lock-free
// core: the CALLER must already hold a.mu (read or write). Factored out
// (BUG-380 round finding P3, opus-round-bug380) so applyEmigration's loop
// can take ONE RLock over the whole ResidentIDs slice instead of one
// RLock/RUnlock pair per candidate id.
func (a *AttractAPI) migrantBelowTenureGraceLocked(id uint64, month int64) bool {
	if err := a.checkNotCopied("migrantBelowTenureGraceLocked"); err != nil {
		return true
	}
	admittedMonth, isMigrant := a.migrantAdmittedMonth[id]
	if !isMigrant {
		return false
	}
	return month-admittedMonth < migrantTenureGraceMonths
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
