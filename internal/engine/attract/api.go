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
// every term drill-through"): the §11 master dial — the seven attractiveness
// terms, each independently queryable, the composite A() score, and the
// command-based monthly migration-mutation path ([ApplyMigration]) consumed
// by engine.spiral and engine.tourism through this interface alone.
//
// The zero value is not usable; construct via [New]. A *AttractAPI is safe
// for concurrent use (AC-14): every mutable field is guarded by mu, and
// checkNotCopied rejects a method call on a struct-copied value
// (SEC-020-class, mirroring engine.households' HouseholdsAPI).
//
// Term provenance (AC-3, ASM-243/BUG-058): five of the seven terms
// (JobAvailability, ServiceCoverage, Environment, LeisureFit, Safety) are
// accepted as pushed input via [SetTermInputs], because code.json registers
// no outbound call edge from engine.attract to engine.firms/market,
// engine.services, engine.world, engine.leisure, or engine.crime.
// HousingAffordability is computed by calling engine.households'
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
		correlationID: correlationID,
		weights:       cfg.Weights,
		world:         cfg.World,
		migrationRate: cfg.MigrationRate,
		repCfg:        cfg.Reputation,
		seed:          seed,
		nextMigrantID: 1,
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

// TermInputs carries the five §11 terms engine.attract cannot compute from
// a registered outbound call (JobAvailability, ServiceCoverage,
// Environment, LeisureFit, Safety — ASM-243/BUG-058) plus the housing
// context the computed HousingAffordability term needs: the household-id
// set to aggregate over, and the Baseline One monthly rent (monthly income
// is derived from engine.finance's posted wage bill). The five term values
// are on a [0,100] scale (matching citizens' satisfaction components and
// households' affordability Index).
type TermInputs struct {
	JobAvailability float64
	ServiceCoverage float64
	Environment     float64
	LeisureFit      float64
	Safety          float64

	// HouseholdIDs is the household-id set the composition root supplies
	// (CitizensAPI exposes per-id queries, not an enumeration) for the
	// HousingAffordability term and the vacancy-bound immigration path.
	HouseholdIDs []uint64

	// MonthlyRentMicroPounds is the Baseline One citywide monthly rent in
	// micro-pounds (M0-ENG §1.2). It must be non-negative (FEAT-086).
	MonthlyRentMicroPounds int64
}

// validateTermInputs rejects a non-finite or out-of-range term value, or a
// negative rent, with a registry-sourced error and no state change
// (FEAT-086: validate every numeric input at every entry point).
func validateTermInputs(in TermInputs, correlationID string) error {
	fields := []struct {
		name  string
		value float64
	}{
		{"jobAvailability", in.JobAvailability},
		{"serviceCoverage", in.ServiceCoverage},
		{"environment", in.Environment},
		{"leisureFit", in.LeisureFit},
		{"safety", in.Safety},
	}
	for _, f := range fields {
		if !num.IsFinite(f.value) || f.value < 0 || f.value > 100 {
			return errs.New(ErrInvalidTermInput, correlationID, map[string]any{
				"field": f.name,
				"value": f.value,
			})
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

// JobAvailability returns the §11 job-availability term (pushed input,
// [0,100]).
func (a *AttractAPI) JobAvailability() float64 {
	if err := a.checkNotCopied("JobAvailability"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.termInputs.JobAvailability
}

// ServiceCoverage returns the §11 service-coverage term (pushed input,
// [0,100]).
func (a *AttractAPI) ServiceCoverage() float64 {
	if err := a.checkNotCopied("ServiceCoverage"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.termInputs.ServiceCoverage
}

// Environment returns the §11 environment term (pushed input, [0,100]).
func (a *AttractAPI) Environment() float64 {
	if err := a.checkNotCopied("Environment"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.termInputs.Environment
}

// LeisureFit returns the §11 leisure-fit term (pushed input, [0,100]).
func (a *AttractAPI) LeisureFit() float64 {
	if err := a.checkNotCopied("LeisureFit"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.termInputs.LeisureFit
}

// Safety returns the §11 safety term (pushed input, [0,100]).
func (a *AttractAPI) Safety() float64 {
	if err := a.checkNotCopied("Safety"); err != nil {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.termInputs.Safety
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

// termsSnapshot is the six non-reputation term values read under one
// consistent snapshot of the pushed inputs and one households+finance
// affordability call.
type termsSnapshot struct {
	jobAvailability      float64
	housingAffordability float64
	serviceCoverage      float64
	environment          float64
	leisureFit           float64
	safety               float64
}

// fundamentals returns the mean of the six non-reputation terms — the
// signal the reputation momentum tracks (AC-5).
func (t termsSnapshot) fundamentals() float64 {
	return (t.jobAvailability + t.housingAffordability + t.serviceCoverage +
		t.environment + t.leisureFit + t.safety) / 6
}

// snapshotTerms computes the six non-reputation term values. It never holds
// a.mu while calling into households/finance (they hold their own locks),
// so it cannot deadlock against a concurrent SetTermInputs/SetHouseholds.
func (a *AttractAPI) snapshotTerms() (termsSnapshot, error) {
	if err := a.checkNotCopied("snapshotTerms"); err != nil {
		return termsSnapshot{}, err
	}
	a.mu.RLock()
	in := cloneTermInputs(a.termInputs)
	households := a.households
	finance := a.finance
	a.mu.RUnlock()

	t := termsSnapshot{
		jobAvailability: in.JobAvailability,
		serviceCoverage: in.ServiceCoverage,
		environment:     in.Environment,
		leisureFit:      in.LeisureFit,
		safety:          in.Safety,
	}

	if households == nil || finance == nil {
		return t, errs.New(ErrDependencyMissing, a.correlationID, map[string]any{
			"dependency": "households/finance",
			"operation":  "HousingAffordability",
		})
	}
	// The Baseline One monthly-income figure derives from engine.finance's
	// posted wage bill (aggregate CatWages household credit) divided by the
	// household count — a uniform per-household income (differentiation is a
	// later sprint). A vacant city or a non-positive wage bill reads 0, which
	// households interprets via its rent-burden sentinel.
	wages := int64(finance.WagesPosted())
	if wages < 0 {
		wages = 0
	}
	income := positiveDiv(wages, int64(len(in.HouseholdIDs)))

	aff, err := households.HousingAffordability(in.HouseholdIDs, in.MonthlyRentMicroPounds, income)
	if err != nil {
		return t, err
	}
	t.housingAffordability = float64(aff.Index)
	return t, nil
}

// HousingAffordability returns the §11 housing-affordability term,
// computed by calling engine.households' HousingAffordability (per
// engine.households AC-9) combined with engine.finance's wage/income
// context — both registered outbound edges (AC-3). The result is the
// households affordability Index on a [0,100] scale (higher = more
// affordable). It errors (ErrDependencyMissing) before households/finance
// are wired.
func (a *AttractAPI) HousingAffordability() (float64, error) {
	if err := a.checkNotCopied("HousingAffordability"); err != nil {
		return 0, err
	}
	t, err := a.snapshotTerms()
	if err != nil {
		return 0, err
	}
	return t.housingAffordability, nil
}

// weightedSum folds the six non-reputation terms and the signed reputation
// into the seven-term weighted A score.
func weightedSum(w Weights, t termsSnapshot, rep float64) float64 {
	return w.JobAvailability*t.jobAvailability +
		w.HousingAffordability*t.housingAffordability +
		w.ServiceCoverage*t.serviceCoverage +
		w.Environment*t.environment +
		w.LeisureFit*t.leisureFit +
		w.Safety*t.safety +
		w.Reputation*rep
}

// A returns the composite seven-term attractiveness score
//
//	A = w₁·jobAvailability + w₂·housingAffordability + w₃·serviceCoverage
//	    + w₄·environment + w₅·leisureFit + w₆·safety + w₇·reputation
//
// the term-decomposed master-dial value (AC-1). It is a pure read: it
// neither advances reputation nor mutates any state. The result is always
// finite (every input is validated finite and reputation is clamped).
func (a *AttractAPI) A() (float64, error) {
	if err := a.checkNotCopied("A"); err != nil {
		return 0, err
	}
	t, err := a.snapshotTerms()
	if err != nil {
		return 0, err
	}
	a.mu.RLock()
	w := a.weights
	rep := a.reputation.value
	a.mu.RUnlock()

	sum := weightedSum(w, t, rep)
	if !num.IsFinite(sum) {
		return 0, errs.New(ErrConfigInvalid, a.correlationID, map[string]any{
			"field": "A",
			"value": sum,
		})
	}
	return sum, nil
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
