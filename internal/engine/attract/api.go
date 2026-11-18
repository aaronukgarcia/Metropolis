package attract

import (
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/households"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// AttractAPI is code.json's "engine.attract" inbound contract
// (GUID 0c36bd43-d123-42bd-ab73-be06d33887d3, "term-decomposed A score,
// every term drill-through"): the §11 master dial — the ten attractiveness
// levers (levers.go) and two extra modifiers, the composite A() score, and the
// command-based monthly migration-mutation path ([ApplyMigration]) consumed
// by engine.spiral and engine.tourism through this interface alone.
//
// The zero value is not usable; construct via [New]. A *AttractAPI is safe
// for concurrent use (AC-14): every mutable field is guarded by mu, and
// checkNotCopied rejects a method call on a struct-copied value
// (SEC-020-class, mirroring engine.households' HouseholdsAPI).
//
// Term provenance (AC-3, ASM-243/BUG-058): nine of the ten levers and the
// two modifiers are accepted as pushed input via [SetTermInputs], because
// code.json registers no outbound call edge from engine.attract to
// engine.firms/market, engine.services, engine.world, engine.leisure, or
// engine.crime. The housing lever is computed by calling engine.households'
// HousingAffordability combined with engine.finance's wage/income context —
// both registered outbound edges. Reputation is computed internally as
// asymmetric momentum.
type AttractAPI struct {
	correlationID string
	weights       Weights
	world         WorldPool
	migrationRate float64
	repCfg        ReputationConfig

	seed uint64

	// Dependencies, wired via SetCitizens/SetFinance/SetHouseholds and read
	// under mu. Named to keep the package-qualified call sites (households.,
	// finance.) visible to the AC-3 drill-through check.
	citizens   *citizens.CitizensAPI
	finance    *finance.FinanceAPI
	households *households.HouseholdsAPI

	mu sync.RWMutex

	// termInputs is the current pushed-term + housing-context snapshot,
	// set via SetTermInputs (the AC-1 command-based input path for the five
	// pushed terms).
	termInputs TermInputs

	// reputation is the momentum state, advanced once per month by
	// ApplyMigration (never by a term-accessor query — AC-1's term
	// isolation holds).
	reputation        reputationState
	lastAdvancedMonth int64
	hasAdvanced       bool

	// nextMigrantID is the deterministic migrant-id counter (high-bit
	// prefix, so admitted migrants never collide with small seeded ids).
	nextMigrantID uint64

	// migrantAdmittedMonth maps an admitted migrant's citizen id to the
	// simulation month applyImmigration (migration.go) admitted them —
	// BUG-380's tenure-grace state: applyEmigration skips a migrant whose
	// tenure (currentMonth - admittedMonth) is below
	// migrantTenureGraceMonths, so a just-arrived migrant cannot be
	// immediately re-selected by the very next decline month (the
	// mechanism that produced the "sawtooth boom/bust population
	// collapse" documented on compose.go's residentIDs()/migrantIDsFromCount).
	// Populated once, at mint time (recordMigrantAdmission, called from
	// applyImmigration right after each birthMigrant succeeds) — a
	// migrant's arrival month is a fact, never revised.
	//
	// BUG-380 round finding P2 (opus-round-bug380, 2026-09-05/06): this
	// field was originally documented as "never pruned... bounded by
	// MigrantsAdmitted()" — measured false. The attacker's
	// TestAttack380_TenureMapGrowsWithCumulativeAdmissionsNeverPruned
	// (compose package) found 39.4% of entries already belonged to
	// departed/dead citizens after just 48 months, an unbounded-with-time
	// growth law, not the small/bounded table this comment claimed.
	// pruneMigrantTenure (migration.go) deletes an entry the INSTANT
	// applyEmigration itself removes that citizen (the one departure
	// channel this package issues itself).
	//
	// BUG-380 re-round finding P1 (opus-reround-bug380): pruning on
	// emigration ALONE still left an unbounded "orphan" residual for
	// migrants who died of NATURAL causes (citizens' own coldpass has no
	// callback into this package) — measured at 45 orphans by month 200 on
	// a roughly linear ~0.25/month growth, not the small constant a fixed
	// orphanSlack could safely bound. Closed by sweepDepartedMigrantTenure
	// (migration.go), called once at the top of every ApplyMigration:
	// rather than adding a NEW engine.citizens -> engine.attract "citizen
	// removed" notification edge (GR#25: none is registered, and none is
	// added here), it reuses the ALREADY-REGISTERED engine.attract ->
	// engine.citizens edge (CitizenAt) to directly check every tenured
	// migrant's liveness and delete the dead ones. Between the two
	// mechanisms, this map's steady-state size now tracks live migrants
	// EXACTLY (proved over a 200-month run, slack 0 — see
	// TestAttack380_TenureMapStaysBoundedToLiveMigrantsPlusOrphanSlack's
	// re-round revision) rather than the open-ended residual the first
	// round's fix left. Never iterated in a hot path outside this sweep
	// (every OTHER consumer looks up by a SPECIFIC id — GR#21's "no
	// map-range-with-break" is not implicated by the sweep either: a plain
	// map range with an in-loop delete and no early break, whose RESULT
	// does not depend on visitation order).
	//
	// Persisted via participant.go
	// (attractMetaWire.MigrantAdmittedMonths, sorted by id for GR#21) so
	// tenure survives save/restore. BUG-380 re-round finding P0
	// (opus-reround-bug380, BLOCKING): the wire field is a POINTER
	// precisely so a MODERN save's (possibly already-pruned) slice is
	// trusted verbatim with NO backfill, distinguishing it from an OLD
	// save (taken before this field existed at all, decodes nil) which
	// backfills every real migrant id at the save's own last-advanced
	// month — see attractMetaWire's own doc comment for why a plain slice
	// could not make this distinction, and the round-1 regression it
	// caused (a save/load round trip undid every prune: 102 entries at a
	// month-30 save became 208 after LoadAt).
	migrantAdmittedMonth map[uint64]int64

	// wellbeingModifiers is MOD-034's OPTIONAL injected seam (the registered
	// engine.attract -> engine.wellbeing edge, code.json): a plain getter
	// returning the two §18 downstream-effect modifiers this package's
	// migration arithmetic consumes (SatisfactionModifier scales the A
	// score, EmigrationModifier scales the per-resident emigration hazard —
	// see ApplyMigration/applyEmigration). A plain func()(float64,float64)
	// rather than a *wellbeing.WellbeingAPI reference: the two modifiers are
	// functions of the composition root's monthly cohort-mean reconstruction
	// (compose_wellbeing.go), which only the composition root can supply, so
	// there is nothing this package would gain from importing the
	// wellbeing package's types directly. Consulted exactly once per month,
	// inside ApplyMigration's existing once-per-month idempotent block
	// (never mid-month — the same snapshot-at-month-start discipline
	// engine.citizens' SetMortalityModifier documents). nil (every existing
	// New(...) caller) is a documented no-op returning the neutral (1.0,
	// 1.0) pair — migration behaves exactly as before this seam existed.
	// Deliberately NOT persisted: a live composition-root wire
	// re-established on every load, mirroring SetCitizens/SetFinance/
	// SetHouseholds's identical precedent, never simulation state of its
	// own.
	wellbeingModifiers func() (satisfaction, emigration float64)

	// self is the SEC-020 copy guard (atomic.Pointer, mirroring
	// engine.build's BuildAPI.self). Stored exactly once, in New, before
	// the value is returned to any caller.
	self atomic.Pointer[AttractAPI]
}

// New constructs an AttractAPI from a validated Config and a world seed
// (used for every counter-based hash draw — AC-12). correlationID is
// attached to every error this call (and the returned API's methods)
// construct (GR#1). An invalid Config is rejected with a registry-sourced
// error — never a silently-defaulted weight or a zero-substituted A_world
// (AC-10/AC-11). The citizens/finance/households dependencies are wired
// later via SetCitizens/SetFinance/SetHouseholds.
func New(cfg Config, seed uint64, correlationID string) (*AttractAPI, error) {
	if correlationID == "" {
		correlationID = errs.NewCorrelationID()
	}
	if err := cfg.validate(correlationID); err != nil {
		return nil, err
	}
	a := &AttractAPI{
		correlationID:        correlationID,
		weights:              cfg.Weights,
		world:                cfg.World,
		migrationRate:        cfg.MigrationRate,
		repCfg:               cfg.Reputation,
		seed:                 seed,
		nextMigrantID:        1,
		migrantAdmittedMonth: make(map[uint64]int64),
	}
	// Armed exactly once, before a is returned to any caller (SEC-020).
	a.self.Store(a)
	return a, nil
}

// checkNotCopied rejects a method call on a struct-copied *AttractAPI
// (SEC-020 family). Lock-free — a single atomic.Pointer.Load — and
// therefore safe to run before mu is ever touched.
func (a *AttractAPI) checkNotCopied(method string) error {
	if a.self.Load() != a {
		return errs.New(ErrCopiedValue, a.correlationID, map[string]any{"method": method})
	}
	return nil
}

// SetCitizens wires the engine.citizens dependency used by the migration
// mutation path (admitting/removing citizens).
func (a *AttractAPI) SetCitizens(c *citizens.CitizensAPI) error {
	if err := a.checkNotCopied("SetCitizens"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.citizens = c
	return nil
}

// SetFinance wires the engine.finance dependency used by the
// HousingAffordability term's wage/income context.
func (a *AttractAPI) SetFinance(f *finance.FinanceAPI) error {
	if err := a.checkNotCopied("SetFinance"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finance = f
	return nil
}

// SetHouseholds wires the engine.households dependency used by the
// HousingAffordability term and the vacancy-bound immigration path.
func (a *AttractAPI) SetHouseholds(h *households.HouseholdsAPI) error {
	if err := a.checkNotCopied("SetHouseholds"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.households = h
	return nil
}

// SetWellbeingModifiers wires MOD-034's (engine.wellbeing) satisfaction and
// emigration modifier seam. Optional and idempotent-to-call-once, mirroring
// SetCitizens/SetFinance/SetHouseholds's post-construction wiring
// precedent — an AttractAPI never wired here (nil getter, every existing
// New(...) call site) keeps the documented neutral (1.0, 1.0) pair, i.e.
// exactly today's behaviour. Passing nil restores that default. The getter
// itself must be a pure, deterministic function of already-committed state
// (the month-start snapshot rule, mirroring citizens.SetMortalityModifier's
// identical contract) — this method does not and cannot enforce that on
// the caller, so the composition root is responsible for snapshotting the
// wellbeing cohort value at month start (compose_wellbeing.go) rather than
// reading a live, mutating value.
func (a *AttractAPI) SetWellbeingModifiers(getter func() (satisfaction, emigration float64)) error {
	if err := a.checkNotCopied("SetWellbeingModifiers"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.wellbeingModifiers = getter
	return nil
}

// TermInputs carries the lever inputs engine.attract cannot compute from a
// registered outbound call, plus the housing context the computed housing
// lever needs (FEAT-9995385616, ASM-243/BUG-058).
//
//   - Levers: one LeverTerm per lever, indexed by LeverID. A lever that is
//     not Built contributes exactly neutral. The LeverHousing slot is
//     special: attract computes the housing value itself (households
//     affordability), so only that slot's Built flag is read (it gates the
//     computation); its Value is ignored. A caller must set
//     Levers[LeverHousing].Built = true for housing to count.
//   - Modifiers: the safety and environment modifiers (outside the ten).
//   - Reinforcers: the named reputation-momentum input slots; none is
//     built yet, so all are neutral today.
//   - HouseholdIDs is the household-id set the composition root supplies
//     (CitizensAPI exposes per-id queries, not an enumeration).
//   - MonthlyRentMicroPounds is the Baseline One citywide monthly rent in
//     micro-pounds (M0-ENG section 1.2); it must be non-negative.
//
// Every Value is on a [0,100] scale (matching citizens' satisfaction
// components and households' affordability Index) and is validated even
// when its lever is not built.
type TermInputs struct {
	Levers      [LeverCount]LeverTerm
	Modifiers   [ModifierCount]LeverTerm
	Reinforcers [ReinforcerCount]LeverTerm

	HouseholdIDs           []uint64
	MonthlyRentMicroPounds int64
}

// validateTermInputs rejects a non-finite or out-of-range term value (built
// or not), or a negative rent, with a registry-sourced error and no state
// change (FEAT-086: validate every numeric input at every entry point).
func validateTermInputs(in TermInputs, correlationID string) error {
	for i := 0; i < LeverCount; i++ {
		if err := validateLeverTerm("levers", LeverID(i).String(), in.Levers[i], correlationID); err != nil {
			return err
		}
	}
	for i := 0; i < ModifierCount; i++ {
		if err := validateLeverTerm("modifiers", ModifierID(i).String(), in.Modifiers[i], correlationID); err != nil {
			return err
		}
	}
	for i := 0; i < ReinforcerCount; i++ {
		if err := validateLeverTerm("reinforcers", ReinforcerID(i).String(), in.Reinforcers[i], correlationID); err != nil {
			return err
		}
	}
	if in.MonthlyRentMicroPounds < 0 {
		return errs.New(ErrInvalidTermInput, correlationID, map[string]any{
			"field": "monthlyRentMicroPounds",
			"value": in.MonthlyRentMicroPounds,
		})
	}
	return nil
}

// cloneTermInputs deep-copies the mutable parts of TermInputs so the stored
// snapshot can never alias caller-owned memory.
func cloneTermInputs(in TermInputs) TermInputs {
	in.HouseholdIDs = append([]uint64(nil), in.HouseholdIDs...)
	return in
}

// SetTermInputs sets the pushed-term + housing-context snapshot (the AC-1
// command-based input path). It validates every numeric input and rejects
// with no state change on failure. It does NOT advance the reputation
// momentum — reputation advances only in ApplyMigration — so changing one
// term's input changes only that term's accessor output and the composite
// A(), never the reputation accessor (AC-1 term isolation).
func (a *AttractAPI) SetTermInputs(in TermInputs) error {
	if err := a.checkNotCopied("SetTermInputs"); err != nil {
		return err
	}
	if err := validateTermInputs(in, a.correlationID); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.termInputs = cloneTermInputs(in)
	return nil
}

// Reputation returns the §11 reputation term — the signed asymmetric
// momentum value (AC-5). It is advanced only by ApplyMigration, never by a
// term-accessor query.
func (a *AttractAPI) Reputation() float64 {
	if err := a.checkNotCopied("Reputation"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.reputation.value
}

// housingIndex computes the housing lever's value: the households
// affordability Index (0..100, higher = more affordable) from the pushed
// household-id set, the rent, and engine.finance's posted wage bill. It
// never holds a.mu (households/finance hold their own locks).
func (a *AttractAPI) housingIndex(in TermInputs, hh *households.HouseholdsAPI, fin *finance.FinanceAPI) (float64, error) {
	if err := a.checkNotCopied("housingIndex"); err != nil {
		return 0, err
	}
	if hh == nil || fin == nil {
		return 0, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "households/finance",
			"operation":  "HousingAffordability",
		})
	}
	// The Baseline One monthly-income figure derives from engine.finance's
	// posted wage bill (aggregate CatWages household credit) divided by the
	// household count - a uniform per-household income (differentiation is a
	// later sprint). A vacant city or a non-positive wage bill reads 0, which
	// households interprets via its rent-burden sentinel.
	wages := int64(fin.WagesPosted())
	if wages < 0 {
		wages = 0
	}
	income := positiveDiv(wages, int64(len(in.HouseholdIDs)))
	aff, err := hh.HousingAffordability(in.HouseholdIDs, in.MonthlyRentMicroPounds, income)
	if err != nil {
		return 0, err
	}
	return float64(aff.Index), nil
}

// snapshotLevers reads one consistent snapshot of the pushed inputs and the
// weights, computes the housing lever when its gate is built, and folds
// everything into lever results and the momentum input. It never holds a.mu
// while calling into households/finance.
func (a *AttractAPI) snapshotLevers(aWorld float64) (levers [LeverCount]LeverResult, mods [ModifierCount]LeverResult, fundamentals float64, err error) {
	if err = a.checkNotCopied("snapshotLevers"); err != nil {
		return levers, mods, 0, err
	}
	a.mu.RLock()
	in := cloneTermInputs(a.termInputs)
	hh := a.households
	fin := a.finance
	w := a.weights
	a.mu.RUnlock()

	var housing float64
	if in.Levers[LeverHousing].Built {
		housing, err = a.housingIndex(in, hh, fin)
		if err != nil {
			return levers, mods, 0, err
		}
	}
	levers, mods, fundamentals = leverResults(w, in, housing, aWorld)
	return levers, mods, fundamentals, nil
}

// HousingAffordability returns the housing lever's value, computed by
// calling engine.households' HousingAffordability (per engine.households
// AC-9) combined with engine.finance's wage/income context - both
// registered outbound edges (AC-3). The result is the households
// affordability Index on a [0,100] scale (higher = more affordable),
// regardless of whether the housing lever's gate is built. It errors
// (ErrDependencyMissing) before households/finance are wired.
func (a *AttractAPI) HousingAffordability() (float64, error) {
	if err := a.checkNotCopied("HousingAffordability"); err != nil {
		return 0, err
	}
	a.mu.RLock()
	in := cloneTermInputs(a.termInputs)
	hh := a.households
	fin := a.finance
	a.mu.RUnlock()
	return a.housingIndex(in, hh, fin)
}

// A returns the composite attractiveness score
//
//	A = A_world + sum(w_i * (lever_i - A_world))   [built levers only]
//	    + safety and environment modifier points + w_reputation * reputation
//
// the term-decomposed master-dial value (AC-1) BEFORE the wellbeing factor
// (ApplyMigration applies that once a month and reports it separately). It
// is a pure read: it neither advances reputation nor mutates any state. The
// result is always finite (every input is validated finite and reputation
// is clamped).
func (a *AttractAPI) A() (float64, error) {
	if err := a.checkNotCopied("A"); err != nil {
		return 0, err
	}
	aWorld := a.AWorld()
	levers, mods, fundamentals, err := a.snapshotLevers(aWorld)
	if err != nil {
		return 0, err
	}
	a.mu.RLock()
	w := a.weights
	rep := a.reputation.value
	a.mu.RUnlock()

	b := composeBreakdown(w, levers, mods, fundamentals, rep, 1, aWorld)
	if !num.IsFinite(b.score) {
		return 0, errs.New(ErrConfigInvalid, a.correlationID, map[string]any{
			"field": "A",
			"value": b.score,
		})
	}
	return b.score, nil
}

// G is §11's raw (pre-capacity) monthly net-migration response g(x) for an
// attractiveness gap x = A − A_world: g(x) = migrationRate · x, signed —
// positive = net immigration, negative = net emigration (AC-4's
// bidirectional dial, never clamped to non-negative). The migrationRate
// coefficient is data-loaded (Config), never a literal. A non-finite result
// (a NaN/±Inf gap, or a finite gap whose product overflows) is rejected with
// a registry-sourced error, never returned as +Inf/NaN (FEAT-086 — the
// float64 path is backstopped exactly like the int64 path).
func (a *AttractAPI) G(x float64) (float64, error) {
	if err := a.checkNotCopied("G"); err != nil {
		return 0, err
	}
	g := a.migrationRate * x
	if !num.IsFinite(g) {
		return 0, errs.New(ErrConfigInvalid, a.correlationID, map[string]any{
			"field": "net",
			"value": g,
		})
	}
	return g, nil
}

// AWorld returns the comparison baseline attractiveness through the §4
// WorldPool seam (AC-8).
func (a *AttractAPI) AWorld() float64 {
	if err := a.checkNotCopied("AWorld"); err != nil {
		return 0
	}
	return a.world.AWorld()
}

// MigrantsAdmitted returns the cumulative COUNT of migrant citizens ever
// admitted (a.nextMigrantID — see mintMigrantID's doc comment: it starts at
// 0 and increments by exactly 1 per minted citizen, so this IS the count,
// not an id). BUG-529/BUG-535: the composition root needs this to
// reconstruct the exact set of migrant ids it should treat as live
// residents for wage/employment/household-formation purposes
// ([MigrantIDBase+1, MigrantIDBase+MigrantsAdmitted()], per
// migrantIDHighBit's doc comment on the three-package disjoint id map) —
// this is a LIVE read of attract's own already-correctly-persisted counter
// (participant.go), never a value compose tracks or caches itself, so it
// stays correct across a save/LoadAt round-trip with no new persisted
// state of compose's own (a compose-side shadow counter was tried first
// and found to desync from this one across a LoadAt boundary, since a
// compose-tracked copy is not itself part of any snapshot payload).
func (a *AttractAPI) MigrantsAdmitted() uint64 {
	if err := a.checkNotCopied("MigrantsAdmitted"); err != nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.nextMigrantID
}

// LeverValue returns the last pushed 0..100 value of a lever (the housing
// lever reads 0: attract computes it, see HousingAffordability) and whether
// the lever is built. An out-of-range id reads (0, false). A pure read of
// the pushed snapshot; it replaces the five per-term accessors the seven-term
// formula had (FEAT-9995385616).
func (a *AttractAPI) LeverValue(id LeverID) (float64, bool) {
	if err := a.checkNotCopied("LeverValue"); err != nil || int(id) >= LeverCount {
		return 0, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	t := a.termInputs.Levers[id]
	return t.Value, t.Built
}

// ModifierValue is LeverValue for the safety and environment modifiers.
func (a *AttractAPI) ModifierValue(id ModifierID) (float64, bool) {
	if err := a.checkNotCopied("ModifierValue"); err != nil || int(id) >= ModifierCount {
		return 0, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	t := a.termInputs.Modifiers[id]
	return t.Value, t.Built
}
