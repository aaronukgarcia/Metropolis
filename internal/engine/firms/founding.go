package firms

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FoundingProbability is the pure, per-citizen founding probability
// (AC-2/AC-3): a documented function of the citizen's OWN ambition,
// education/attainment, sector experience and wealth, plus the shared
// FoundingContext (premises availability, demand signal, exit-history
// angel boost). It returns per-mille in [0,1000], computed in integer
// fixed-point only (GR#21 — no float on a decision path). The weights are
// data (data/firms.json), never Go literals (GR#15). It is deterministic
// and side-effect-free, which is what lets the AC-3 isolation test perturb
// ONE citizen and observe only that citizen's probability move.
func (f *FirmsAPI) FoundingProbability(cit citizens.Citizen, ctx FoundingContext) int64 {
	// Guard against a copied value even though this method is pure — it
	// reads only f.cfg, but a copied API must never silently produce a
	// result (SEC-020-class).
	if err := f.checkNotCopied("FoundingProbability"); err != nil {
		return 0
	}
	c := f.cfg.Founding

	ambition := int64(cit.Personality[citizens.AxisAmbition]) // 0-100
	education := int64(cit.Education.Attainment)
	if education < 0 {
		education = 0
	}
	if education > 100 {
		education = 100
	}

	p := c.BasePerMille
	p += satMul(c.AmbitionPerMille, ambition) / 100
	p += satMul(c.EducationPerMille, education) / 100
	if cit.Employment.Sector != citizens.SectorNone {
		p += c.SectorExperiencePerMille
	}
	wealthBand := int64(citizens.IncomeBandFor(cit.Wealth)) // 0-4
	p += satMul(c.WealthPerMille, wealthBand) / 4
	if ctx.PremisesAvailable {
		p += c.PremisesPerMille
	}
	if ctx.DemandSignal > 0 {
		p += c.DemandPerMille
	}
	if ctx.ExitedFounder {
		p += c.ExitFounderBoostPerMille
	}
	return clampPerMille(p)
}

// FoundingProbabilityFor resolves a citizen through CitizensAPI and
// returns their current per-citizen founding probability — the
// founder-history-aware accessor (AC-12): a citizen with a logged
// successful exit gets the angel boost, an otherwise-identical citizen
// without one does not. Returns ErrUnknownCitizen for an unresolved ID.
func (f *FirmsAPI) FoundingProbabilityFor(citizenID uint64, correlationID string) (int64, error) {
	if err := f.checkNotCopied("FoundingProbabilityFor"); err != nil {
		return 0, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.foundingProbabilityForLocked(citizenID, correlationID)
}

func (f *FirmsAPI) foundingProbabilityForLocked(citizenID uint64, correlationID string) (int64, error) {
	if f.citizens == nil {
		return 0, errs.New(ErrDependencyMissing, correlationID, map[string]any{
			"operation": "FoundingProbabilityFor", "dependency": "citizens",
		})
	}
	cit, ok := f.citizens.CitizenAt(citizenID, correlationID)
	if !ok {
		return 0, errs.New(ErrUnknownCitizen, correlationID, map[string]any{"citizen": citizenID})
	}
	ctx := f.foundingContextLocked(correlationID)
	ctx.ExitedFounder = f.founderExitedLocked(citizenID)
	return f.FoundingProbability(cit, ctx), nil
}

// foundingContextLocked derives the shared founding context from the wired
// dependencies (engine.build for premises, engine.market for the demand
// signal). The caller holds f.mu.
func (f *FirmsAPI) foundingContextLocked(correlationID string) FoundingContext {
	ctx := FoundingContext{}
	if f.build != nil {
		// Premises are available when the build catalogue carries a zone the
		// founding composite recognises (a Startup's "dwelling" home-office).
		if _, ok := f.build.ZoneTypeByID(f.stageConfigFor(StageStartup).PremiseClass); ok {
			ctx.PremisesAvailable = true
		}
	}
	if f.market != nil {
		// Demand signal: the Startup input commodity's capacity is a coarse
		// positive-signal proxy (§45's "local demand signal" — a documented
		// placeholder, ASM-logged).
		avail, err := f.market.Availability(market.ConsumerGoods, 1)
		if err == nil && avail.Available > 0 {
			ctx.DemandSignal = 1
		}
	}
	return ctx
}

// Found founds a Startup firm from a single, real citizen (AC-15: the
// founder ID must resolve through CitizensAPI — an unknown ID is rejected,
// never a placeholder). It is the primitive EvaluateFounding uses for each
// citizen that passes the probability gate.
func (f *FirmsAPI) Found(founder uint64) (FirmID, error) {
	if err := f.checkNotCopied("Found"); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.citizens == nil {
		return 0, errs.New(ErrDependencyMissing, f.correlationID, map[string]any{
			"operation": "Found", "dependency": "citizens",
		})
	}
	cit, ok := f.citizens.CitizenAt(founder, f.correlationID)
	if !ok {
		return 0, errs.New(ErrUnknownCitizen, f.correlationID, map[string]any{"citizen": founder})
	}
	return f.foundLocked(cit), nil
}

// foundLocked registers a Startup firm for an already-resolved founder
// (the caller holds f.mu).
func (f *FirmsAPI) foundLocked(cit citizens.Citizen) FirmID {
	// Deterministic ID keyed by the founder + founding month (SEC-102):
	// sharded concurrent founding maps each founder to the same firm ID
	// regardless of goroutine scheduling.
	id := f.firmIDForLocked(cit.ID, f.month, "firmid")
	// The founder is the first staff member of their own startup (AC-4's
	// "staff are real citizens").
	fs := &firmState{firm: Firm{
		ID:               id,
		Name:             "startup-" + itoa(int(id)),
		FounderCitizenID: cit.ID,
		Stage:            StageStartup,
		Staff:            []uint64{cit.ID},
		Sector:           cit.Employment.Sector,
		InputCommodity:   inputCommodityFor(cit.Employment.Sector),
		InputRequired:    1,
		Financial:        Financial{OutputScale: 1000},
	}}
	f.firms[id] = fs
	f.foundedCount++
	f.foundedEvents = append(f.foundedEvents, foundedEvent{FirmID: id, Month: f.month})
	f.emitLocked(LifecycleEvent{Kind: EventFounded, FirmID: id, Month: f.month})
	return id
}

// EvaluateFounding runs the monthly per-citizen founding evaluation
// (AC-2/AC-3): for each candidate citizen ID it resolves the citizen
// through CitizensAPI, computes that citizen's OWN founding probability,
// draws deterministically, and founds firms. Candidate IDs are sorted
// first (GR#21). Returns the founded Startups. An unresolved candidate ID
// is an error (AC-15), never a silently-skipped record.
func (f *FirmsAPI) EvaluateFounding(citizenIDs []uint64, month int64) ([]Startup, error) {
	if err := f.checkNotCopied("EvaluateFounding"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.citizens == nil {
		return nil, errs.New(ErrDependencyMissing, f.correlationID, map[string]any{
			"operation": "EvaluateFounding", "dependency": "citizens",
		})
	}
	f.month = month

	ids := append([]uint64(nil), citizenIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	ctx := f.foundingContextLocked(f.correlationID)
	var startups []Startup
	for _, id := range ids {
		cit, ok := f.citizens.CitizenAt(id, f.correlationID)
		if !ok {
			return nil, errs.New(ErrUnknownCitizen, f.correlationID, map[string]any{"citizen": id})
		}
		ctx.ExitedFounder = f.founderExitedLocked(id)
		p := f.FoundingProbability(cit, ctx)
		stream := det.NewStream(f.seed, id, month, "founding")
		if stream.IntN(1000) < p {
			firmID := f.foundLocked(cit)
			startups = append(startups, Startup{FounderCitizenID: id, FirmID: firmID})
		}
	}
	return startups, nil
}

// founderExitedLocked reads the founder-history ledger (the caller holds
// f.mu).
func (f *FirmsAPI) founderExitedLocked(citizenID uint64) bool {
	rec, ok := f.founderHistory[citizenID]
	return ok && rec.exited
}

// CultureIndex returns entrepreneur culture — startups founded per 1,000
// population over the documented rolling window (AC-10). It is computed
// from the REAL founding events (the same events EvaluateFounding/Found
// produced), never a separately-maintained shadow counter. Population is
// read from the wired CitizensAPI; with no citizens wired the index reads
// 0 (never a panic, never a division by a zero population).
func (f *FirmsAPI) CultureIndex() int64 {
	if err := f.checkNotCopied("CultureIndex"); err != nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	window := f.cfg.Credit.CultureWindowMonths
	var startups int64
	for _, e := range f.foundedEvents {
		if e.Month >= f.month-window {
			startups++
		}
	}
	var population int64
	if f.citizens != nil {
		population = int64(f.citizens.TotalPopulation(f.correlationID))
	}
	if population <= 0 {
		return 0
	}
	return startups * 1000 / population
}
