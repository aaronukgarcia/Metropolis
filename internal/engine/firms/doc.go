// Package firms is the entrepreneur-to-enterprise module (MOD-058):
// the firm lifecycle (founding from real ambitious citizens →
// Startup→Small→Medium→Enterprise → failure/insolvency/acquisition),
// the entrepreneur-culture index, the superlinear professional/financial-
// services demand relationship, and the banking layer that turns deposits
// into firm credit priced against the off-map central-bank base-rate
// cycle.
//
// Module key: engine.firms (see code.json)
// GUID:        df06bec1-ea6c-456d-9d06-690cb18fa2a0
// Spec refs:   §45 (Firms — Entrepreneur Culture to Enterprise);
//
//	§II.4 (Economy — "Firms: founded by actual ambitious citizens,
//	progressing startup→SME→enterprise, consuming professional services
//	superlinearly, banked by a credit layer under an off-map
//	central-bank rate cycle")
//
// # The per-citizen founding contract (AC-2/AC-3)
//
// Monthly founding is evaluated PER CITIZEN, not sampled from a
// population-level distribution. For each candidate citizen the founding
// probability is a documented function of that citizen's OWN record —
// ambition (Personality[AxisAmbition], 0–100), education/attainment
// (Education.Attainment, clamped to [0,100]), sector experience (derived:
// the citizen's Employment.Sector is not SectorNone), and wealth/capital
// access (citizens.IncomeBandFor(Wealth), 0–4) — plus shared context:
// premises availability (engine.build) and the local demand signal
// (engine.market/aggregate), and an exit-history angel boost (AC-12).
//
// The composite, in fixed-point per-mille (0–1000, integer-only for
// determinism), is:
//
//	p = basePerMille
//	  + ambitionPerMille × ambition/100
//	  + educationPerMille × attainment/100
//	  + sectorExperiencePerMille           (if the founder has worked)
//	  + wealthPerMille × wealthBand/4
//	  + premisesPerMille                   (if premises available)
//	  + demandPerMille                     (if the demand signal is positive)
//	  + exitFounderBoostPerMille           (if the founder has a logged exit)
//
// clamped to [0,1000]. The weights are data (data/firms.json), never Go
// literals (GR#15). The founding DECISION draws det.NewStream(seed,
// citizenID, month, "founding") and founds when the draw < p.
//
// The isolation guarantee (AC-3, the check that distinguishes real
// per-citizen driving from an aggregate-rate-plus-post-hoc-labels model):
// perturbing exactly one citizen's ambition moves ONLY that citizen's
// probability; a label permutation that holds the value multiset fixed
// and only moves which ID holds the high-ambition value moves the founder
// ID with it while leaving the founding count unchanged. An aggregate
// implementation (sample a count from a citywide mean, then assign IDs
// post-hoc) cannot pass either test because it has no per-citizen
// probability to move.
//
// # Staff are real citizens, not headcount (AC-4/AC-5)
//
// A firm's [Firm.Staff] is a slice of real CitizenIDs (uint64, the same
// ID type CitizensAPI uses), never a bare integer headcount. Growth to
// the next stage requires the roster to reach that stage's staff floor
// with real hires, and when a firm fails (insolvency/closure, not
// acquisition) every staff CitizenID has its employmentState set to
// unemployed through CitizensAPI's command surface — citizens NOT on the
// roster are provably unaffected.
//
// # Superlinear professional-services demand (AC-11)
//
// Professional/financial-services demand from non-services firms grows
// SUPERLINEARLY with the count n of served non-services firms, with the
// functional form
//
//	Demand(n) = multiplier × n^(exponent),   exponent > 1
//
// (exponent carried as exponentPerMille, e.g. 1300 → 1.3; multiplier and
// exponent are data, data/firms.json — the exact figures are balance data
// pending M2 tuning). A linear Demand(n) = k·n is exactly the lazy
// implementation this form rejects: doubling n must more than double the
// demand figure.
//
// # The off-map base-rate cycle (AC-14)
//
// Firm borrowing cost is a function of the off-map central-bank base-rate
// cycle. code.json's inbound "pattern" text names external_world.json as
// that cycle's source, but at this build data/external_world.json holds
// §21 off-map job pools (data.modes-naming/FEAT-047), not a rate cycle —
// so the cycle lives in data/firms.json's credit.baseRateCycle until the
// registry is reconciled (a documented drift, ASM-logged). The cycle is a
// month→basis-point step curve; a spike raises the borrowing cost of
// credit-dependent Startup/Small firms, which raises their insolvency
// rate in [FirmsAPI.ResolveMonth].
//
// # Determinism (AC-17/AC-18)
//
// Founding, growth/failure resolution, and credit approval are pure
// functions of (worldSeed, month, prior state, citizen records, commands).
// No method reads the wall clock (the wall-clock accessor scan over this
// package's non-test files returns no matches, AC-18), every stochastic
// draw uses det.NewStream keyed (seed, citizenID, month, purpose), and
// every map that feeds a result is iterated in sorted key order (GR#21).
// Repeated runs from identical state yield byte-identical founder IDs,
// stage transitions, and failure events across worker counts.
//
// # The vacancy-vs-workforce labour-market aggregate (AC-21..AC-27)
//
// [FirmsAPI.TotalVacancies] returns the city-wide vacancy count — Σ over
// every firm of max(0, bandCeiling(stage) − len(Staff)) — where Staff is
// the firm's real CitizenID roster (AC-4) and bandCeiling is derived from
// data/firms.json (GR#15): the next stage's minStaff − 1 for
// Startup/Small/Medium, and the data-declared labourMarket.enterpriseCeiling
// for Enterprise (which §45 leaves unbounded, "250+"). [FirmsAPI.LabourMarket]
// returns the same vacancy count together with Workforce — read live from
// CitizensAPI.TotalPopulation over the already-registered engine.firms →
// engine.citizens edge — and the per-mille ratio
//
//	VacancyRatePerMille = 0 when Workforce == 0, else vacancies×1000/workforce
//
// (integer arithmetic, division-by-zero guarded, never NaN/Inf; no upper
// clamp, so a rate above 1000‰ is legal and grows strictly with vacancies).
// Workforce is a labour-supply PROXY: CitizensAPI exposes no
// working-age/unemployment aggregate today, so the denominator is
// vacancies-per-resident rather than vacancies-per-seeker — a coarse but
// honest signal, refinable later by a citizens-side unemployment aggregate.
// Calling LabourMarket before SetCitizens fails closed with
// ErrDependencyMissing (MET-G1409), never a silent zero Workforce.
package firms
