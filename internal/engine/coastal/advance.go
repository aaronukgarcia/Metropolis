package coastal

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/news"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// This file is the scheduled/seeded simulation process — the ONLY arrival-
// generation path (AC-2). Every stochastic draw uses det.NewStream
// (hash(worldSeed, i, m, purpose), AC-15): never math/rand unseeded, never
// wall-clock, so "never player-triggered" is matched by "never seed-dependent
// on anything but world state".

// resolution is the commit-time update for one resolved case.
type resolution struct {
	stage         CaseStage
	citizenID     uint64
	departureCost int64
}

// Advance runs one simulation month: generates arrival events on shore cells
// (AC-2/AC-3/AC-15), mounts the rescue response (AC-4), receives and assigns
// cases against the caseworker-throughput ceiling with a hotel-requisition
// overflow (AC-5), resolves due cases into granted citizens or managed
// departures (AC-6/AC-7), and reports factually through engine.news (AC-12).
//
// It is the only arrival-generation path: there is no exported Create/Trigger/
// AddArrival command (AC-2). A malformed or missing shore cell is rejected
// with ErrInvalidShoreCell and no event is placed (AC-14). It is NOT
// re-entrant — callers (the tick loop) serialize months; concurrent QUERIES
// are safe (AC-17).
func (c *CoastalAPI) Advance(month int64) (AdvanceResult, error) {
	if err := c.checkNotCopied("Advance"); err != nil {
		return AdvanceResult{}, err
	}
	if month < 0 {
		return AdvanceResult{}, c.coastalErr(ErrOutOfRange, map[string]any{"field": "month", "value": month})
	}

	// Snapshot inputs, dependency refs, and the state this month's computation
	// needs, then release before any seam call (SEC-173/FEAT-135: never hold
	// c.mu across a callback that could synchronously re-enter).
	c.mu.RLock()
	seed := c.seed
	cfg := c.cfg
	tier := c.eraTier
	season := c.seasonIndex
	cond := c.worldConditions
	funding := c.processingFunding
	approach := c.housingApproach
	investment := c.integrationInvestment
	cit := c.citizens
	svc := c.services
	nws := c.news
	shore := c.shore
	nextCase := c.nextCaseID
	nextArr := c.nextArrivalID
	oldBacklog := make([]CaseID, len(c.backlog))
	copy(oldBacklog, c.backlog)
	due := make([]Case, 0)
	for _, id := range c.caseOrder {
		k := c.cases[id]
		if k.Stage == CaseProcessing && k.ResolveMonth != 0 && k.ResolveMonth <= month {
			due = append(due, k)
		}
	}
	c.mu.RUnlock()

	if shore == nil {
		return AdvanceResult{}, c.coastalErr(ErrShoreNotWired, nil)
	}

	var result AdvanceResult
	result.Month = month

	speed := clamp01(
		investment*cfg.Policy.IntegrationInvestmentGainPerUnit -
			approach*cfg.Policy.HousingApproachIntegrationPenaltyPerUnit,
	)

	// 1. Arrival generation (AC-2/AC-3/AC-15).
	shoreCells := sortedCellCoords(shore.ShoreCells())
	rate := rateForMonth(cfg, tier, season, cond)
	countStream := det.NewStream(seed, uint64(month), month, "coastal.arrival")
	count := arrivalCount(countStream, rate, cfg.MaxArrivalsPerMonth)

	// 2. Rescue response (AC-4): read coastguard/lifeboat capacity live.
	remainingCap := rescueCapacity(svc, cfg.Rescue)

	var newEvents []ArrivalEvent
	for i := 0; i < count; i++ {
		sizeStream := det.NewStream(seed, uint64(i), month, "coastal.size")
		size := 1 + sizeStream.IntN(cfg.MaxBoatSize)

		cellStream := det.NewStream(seed, uint64(i), month, "coastal.cell")
		var cell CellCoord
		if len(shoreCells) > 0 {
			cell = shoreCells[cellStream.IntN(int64(len(shoreCells)))]
		}
		if !shore.IsShore(cell) {
			// AC-14: reject loudly, never silently place on an arbitrary cell.
			return AdvanceResult{}, c.coastalErr(ErrInvalidShoreCell, map[string]any{"x": cell.X, "y": cell.Y})
		}

		ev := ArrivalEvent{ID: nextArr + uint64(i), Month: month, Cell: cell, Size: size}
		ev.Rescue = rescueOutcomeFor(size, remainingCap)
		if remainingCap > 0 {
			if float64(size) >= remainingCap {
				remainingCap = 0
			} else {
				remainingCap -= float64(size)
			}
		}
		newEvents = append(newEvents, ev)
	}
	result.Arrivals = len(newEvents)

	// 3. Create one case per person. totalSize is the saturated sum of the
	// month's arrival sizes (GR#16): the raw `+=` it replaces wrapped a large
	// month negative, and a negative total handed to make([]Case, 0, total) is
	// the SEC-210 makeslice panic.
	newCaseRecords := make([]Case, 0, satArrivalSize(newEvents))
	for _, ev := range newEvents {
		for p := int64(0); p < ev.Size; p++ {
			id := CaseID(nextCase) + CaseID(len(newCaseRecords))
			newCaseRecords = append(newCaseRecords, Case{
				ID:        id,
				ArrivalID: ev.ID,
				Month:     month,
				Stage:     CaseProcessing,
			})
		}
	}
	result.NewCases = int64(len(newCaseRecords))

	// 4. Reception (AC-5): assign caseworkers up to the throughput ceiling;
	//    the unassigned remainder is the overflow -> hotels at cost + friction.
	T := throughput(cfg, funding)
	queue := make([]CaseID, 0, len(oldBacklog)+len(newCaseRecords))
	queue = append(queue, oldBacklog...)
	for _, rec := range newCaseRecords {
		queue = append(queue, rec.ID)
	}
	assigned := int64(T)
	if assigned < 0 {
		assigned = 0
	}
	if assigned > int64(len(queue)) {
		assigned = int64(len(queue))
	}

	assignMonths := make(map[CaseID]int64)
	for i := int64(0); i < assigned; i++ {
		id := queue[i]
		dm := durationFor(cfg, seed, id, month, speed)
		if i >= int64(len(oldBacklog)) {
			idx := i - int64(len(oldBacklog))
			newCaseRecords[idx].ResolveMonth = month + dm
		} else {
			assignMonths[id] = month + dm
		}
	}
	newBacklog := queue[assigned:]

	overflow := int64(len(newBacklog))
	hotelDelta := int64(0)
	frictionDelta := float64(0)
	if overflow > 0 {
		hotelDelta = satMul(overflow, effectiveHotelCost(cfg, approach))
		frictionDelta = effectiveFriction(cfg, approach) * float64(overflow)
	}
	result.HotelRequisitionCost = hotelDelta
	result.SatisfactionFriction = frictionDelta

	// 5. Resolve due cases (AC-6/AC-7).
	resolutions := make(map[CaseID]resolution)
	var departureDelta int64
	for _, k := range due {
		if verdictGranted(cfg, seed, k.ID, k.ResolveMonth) {
			if cit == nil {
				return AdvanceResult{}, c.coastalErr(ErrDependencyMissing, map[string]any{
					"dependency": "citizens",
					"operation":  "granted-citizen-creation",
				})
			}
			citizenID, err := c.createGrantedCitizen(cit, cfg, seed, k, k.ResolveMonth)
			if err != nil {
				return AdvanceResult{}, err
			}
			resolutions[k.ID] = resolution{stage: CaseGranted, citizenID: citizenID}
			result.ResolvedGranted++
		} else {
			dp := cfg.Pipeline.DepartureCostPerCase
			departureDelta = num.SatAdd(departureDelta, dp)
			resolutions[k.ID] = resolution{stage: CaseNotGranted, departureCost: dp}
			result.ResolvedNotGranted++
		}
	}
	result.DepartureCost = departureDelta

	// 6. Commit under the write lock (atomic vs concurrent queries, AC-17).
	c.mu.Lock()
	c.arrivals = append(c.arrivals, newEvents...)
	for _, rec := range newCaseRecords {
		c.cases[rec.ID] = rec
		c.caseOrder = append(c.caseOrder, rec.ID)
	}
	for id, m := range assignMonths {
		k := c.cases[id]
		k.ResolveMonth = m
		c.cases[id] = k
	}
	for id, r := range resolutions {
		k := c.cases[id]
		k.Stage = r.stage
		k.CitizenID = r.citizenID
		k.DepartureCost = r.departureCost
		c.cases[id] = k
	}
	c.backlog = newBacklog
	c.nextCaseID = CaseID(nextCase) + CaseID(len(newCaseRecords))
	c.nextArrivalID = nextArr + uint64(len(newEvents))
	c.processingOpex = num.SatAdd(c.processingOpex, f64ToInt64(funding*float64(cfg.Policy.ProcessingFundingOpexPerUnitPerMonth)))
	c.integrationOpex = num.SatAdd(c.integrationOpex, f64ToInt64(investment*float64(cfg.Policy.IntegrationInvestmentOpexPerUnitPerMonth)))
	c.hotelCost = num.SatAdd(c.hotelCost, hotelDelta)
	c.departureCost = num.SatAdd(c.departureCost, departureDelta)
	c.friction += frictionDelta
	result.Backlog = int64(len(newBacklog))
	c.mu.Unlock()

	// 7. Factual news reporting (AC-12) — a downstream side effect, never a
	//    correctness dependency on coastal's own ledger. It runs AFTER the
	//    commit so a reporting failure can never lose coastal's own record;
	//    the error is still surfaced (GR#1), never silently dropped.
	if err := c.reportNews(nws, month, newEvents, overflow, result); err != nil {
		return result, err
	}
	return result, nil
}

// rateForMonth is the arrival-frequency function (AC-3): the base rate scaled
// by the era/milestone tier, the season, and the world-conditions push factor.
func rateForMonth(cfg Config, tier, season int, conditions float64) float64 {
	return cfg.BaseArrivalRate *
		cfg.EraMultipliers[tier] *
		cfg.SeasonMultipliers[season] *
		(1 + cfg.WorldConditionsScale*conditions)
}

// arrivalCount derives the number of arrivals this month from the scaled rate,
// deterministically (AC-15): the whole part plus a Bernoulli draw on the
// fractional part, capped at the data ceiling (weakness pattern #6: bound the
// work, not just the output).
func arrivalCount(stream det.Stream, rate float64, maxArrivals int64) int {
	if !num.IsFinite(rate) || rate <= 0 {
		return 0
	}
	if rate >= float64(maxArrivals) {
		return int(maxArrivals)
	}
	whole := int64(rate)
	frac := rate - float64(whole)
	n := whole
	if frac > 0 && stream.Float64() < frac {
		n++
	}
	if n > maxArrivals {
		n = maxArrivals
	}
	return int(n)
}

// durationFor draws a case's months-long pipeline duration deterministically
// from hash(worldSeed, caseID, month, "coastal.duration"), then reduces it by
// the integration-speed effect (bounded by the data's max reduction), clamped
// to at least one month.
func durationFor(cfg Config, seed uint64, id CaseID, month int64, speed float64) int64 {
	stream := det.NewStream(seed, uint64(id), month, "coastal.duration")
	span := cfg.Pipeline.MaxMonths - cfg.Pipeline.MinMonths + 1
	base := cfg.Pipeline.MinMonths + stream.IntN(span)
	reduction := int64(speed * float64(cfg.Pipeline.MaxReductionMonths))
	d := base - reduction
	if d < 1 {
		d = 1
	}
	return d
}

// verdictGranted draws a case's granted/not-granted verdict deterministically
// from hash(worldSeed, caseID, resolveMonth, "coastal.verdict").
func verdictGranted(cfg Config, seed uint64, id CaseID, resolveMonth int64) bool {
	stream := det.NewStream(seed, uint64(id), resolveMonth, "coastal.verdict")
	return stream.Float64() < cfg.Pipeline.GrantRate
}

// attainmentFor draws a granted citizen's skills (education attainment) from
// the configurable world-profile distribution: mean ± a bounded deterministic
// spread (AC-6), never a hardcoded uniform default.
func attainmentFor(cfg Config, seed uint64, id CaseID, month int64) int32 {
	stream := det.NewStream(seed, uint64(id), month, "coastal.skills")
	delta := int32(stream.IntN(int64(2*cfg.WorldProfile.AttainmentSpread+1))) - cfg.WorldProfile.AttainmentSpread
	v := cfg.WorldProfile.AttainmentMean + delta
	if v < 0 {
		return 0
	}
	if v > 32767 {
		return 32767
	}
	return v
}

// createGrantedCitizen creates one full citizen record through engine.citizens
// (AC-6) — a LifeEventBirth, never a Status: Granted field flip. The record's
// skills (education attainment) come from the configured world profile, and
// its personality is the neutral midpoint (the same documented placeholder
// engine.attract uses for migrants — a future world-pool hook). Returns the
// minted citizen ID.
func (c *CoastalAPI) createGrantedCitizen(cit *citizens.CitizensAPI, cfg Config, seed uint64, k Case, month int64) (uint64, error) {
	rec := citizens.Citizen{
		ID:          uint64(k.ID),
		BirthMonth:  clampBirthMonth(month),
		Sex:         citizens.Sex(uint64(k.ID) & 1),
		Personality: neutralPersonality(),
		Education: citizens.Education{
			Attainment: attainmentFor(cfg, seed, k.ID, month),
		},
		HealthBand: citizens.HealthGood,
		Employment: citizens.Employment{
			State:  citizens.EmploymentUnemployed,
			Sector: citizens.SectorNone,
		},
		Fidelity: citizens.FidelityCold,
	}
	err := cit.ApplyLifeEventCommand(citizens.LifeEventCommand{
		CorrelationID: c.correlationID,
		Kind:          citizens.LifeEventBirth,
		Citizen:       rec,
	})
	if err != nil {
		return 0, err
	}
	return uint64(k.ID), nil
}

// neutralPersonality returns the v1 neutral personality (each axis at the
// midpoint), derived from citizens.MaxPersonalityAxis rather than a literal —
// the same documented placeholder engine.attract uses for migrants.
func neutralPersonality() citizens.Personality {
	var p citizens.Personality
	for axis := 0; axis < citizens.NumPersonalityAxes; axis++ {
		p[axis] = citizens.MaxPersonalityAxis / 2
	}
	return p
}

// clampBirthMonth coerces a simulation month into the citizens hot record's
// int32 birth-month domain (a month beyond MaxInt32 would wrap a bare
// narrowing — GR#16).
func clampBirthMonth(month int64) int32 {
	if month < 0 {
		return 0
	}
	if month > 1<<31-1 {
		return 1<<31 - 1
	}
	return int32(month)
}

// rescueCapacity reads the coastguard + lifeboat capacity ceilings from the
// wired engine.services (AC-4). An unwired services API, an unregistered
// service, or any read error yields 0 — a total shortfall, never a fabricated
// capacity.
func rescueCapacity(svc *services.ServicesAPI, cfg RescueConfig) float64 {
	if svc == nil {
		return 0
	}
	total := 0.0
	coastguard, err := svc.Capacity(services.ServiceID(cfg.CoastguardServiceID))
	if err == nil && num.IsFinite(coastguard) && coastguard > 0 {
		total += coastguard
	}
	lifeboat, err := svc.Capacity(services.ServiceID(cfg.LifeboatServiceID))
	if err == nil && num.IsFinite(lifeboat) && lifeboat > 0 {
		total += lifeboat
	}
	return total
}

// rescueOutcomeFor records the rescue response for one arrival against the
// remaining month capacity (AC-4): a fully-resourced rescue leaves the
// shortfall zero; an under-resourced one records the shortfall.
func rescueOutcomeFor(size int64, remainingCap float64) RescueOutcome {
	out := RescueOutcome{Responded: true}
	if remainingCap <= 0 {
		out.CapacityShortfall = true
		out.ShortfallPeople = size
		return out
	}
	if float64(size) > remainingCap {
		out.CapacityShortfall = true
		out.ShortfallPeople = size - int64(remainingCap)
	}
	return out
}

// reportNews pushes factual, non-editorialised event records through the
// wired engine.news edge (AC-12): arrival figures (case count), capacity
// shortfalls, and pipeline resolutions. Facts only — no sentiment/spin field
// exists on news.Event, and none is fabricated here. When the news edge is
// unwired, reporting is skipped (the arrival stream itself is coastal's own
// record); reporting is a downstream side effect, not a correctness dependency.
// Any ingest failure is returned (GR#1 — never silently dropped).
func (c *CoastalAPI) reportNews(nws *news.NewsAPI, month int64, events []ArrivalEvent, overflow int64, result AdvanceResult) error {
	if nws == nil {
		return nil
	}
	ingest := func(ev news.Event) error {
		_, err := nws.Ingest(ev)
		return err
	}
	for _, ev := range events {
		if err := ingest(news.Event{
			ID:        fmt.Sprintf("coastal.arrival.%d.%d", month, ev.ID),
			Tick:      month,
			Category:  news.CategoryRecord,
			Magnitude: ev.Size,
			Text:      fmt.Sprintf("%d people arrived by small boat", ev.Size),
		}); err != nil {
			return err
		}
	}
	if overflow > 0 {
		if err := ingest(news.Event{
			ID:        fmt.Sprintf("coastal.overflow.%d", month),
			Tick:      month,
			Category:  news.CategoryCrisis,
			Magnitude: overflow,
			Text:      fmt.Sprintf("%d cases overflowed reception capacity to requisitioned hotels", overflow),
		}); err != nil {
			return err
		}
	}
	if result.ResolvedGranted > 0 {
		if err := ingest(news.Event{
			ID:        fmt.Sprintf("coastal.granted.%d", month),
			Tick:      month,
			Category:  news.CategoryRecord,
			Magnitude: result.ResolvedGranted,
			Text:      fmt.Sprintf("%d asylum cases granted", result.ResolvedGranted),
		}); err != nil {
			return err
		}
	}
	if result.ResolvedNotGranted > 0 {
		if err := ingest(news.Event{
			ID:        fmt.Sprintf("coastal.departure.%d", month),
			Tick:      month,
			Category:  news.CategoryRecord,
			Magnitude: result.ResolvedNotGranted,
			Text:      fmt.Sprintf("%d asylum cases not granted, managed departure", result.ResolvedNotGranted),
		}); err != nil {
			return err
		}
	}
	return nil
}

// satMul multiplies two int64 values, saturating at the int64 extremes on
// overflow (GR#16) — never a silent wrap.
func satMul(a, b int64) int64 {
	p, _ := num.SafeMul(a, b)
	return p
}

// satArrivalSize returns the saturated total number of people across the
// month's arrival events (GR#16). Each addition saturates at math.MaxInt64, so
// the total can never wrap negative — the raw `total += size` it replaces
// wrapped a large month's sum negative, and a negative total handed to
// make([]Case, 0, total) is the SEC-210 makeslice panic. The per-event size is
// otherwise bounded upstream by Validate's maxFrequencyCap ceiling on
// MaxBoatSize and MaxArrivalsPerMonth; the saturation here is defence in depth
// against a future config that raises that cap.
func satArrivalSize(events []ArrivalEvent) int64 {
	var total int64
	for _, ev := range events {
		total = num.SatAdd(total, ev.Size)
	}
	return total
}

// f64ToInt64 converts a float64 to int64 with saturation (GR#16) — never a
// bare int64() that wraps 2^63.
func f64ToInt64(f float64) int64 {
	return num.ClampInt64FromFloat(f)
}
