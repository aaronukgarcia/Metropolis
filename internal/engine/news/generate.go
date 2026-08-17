package news

import "github.com/aaronukgarcia/Metropolis/internal/foundation/num"

// This file holds the three deterministic generation layers — the monthly
// bulletin (§29.2), the annual review (§29.3), and the epilogue (§29.4) —
// plus the shared aggregation they all draw from. Each is a pure function
// of the [History] log plus the static [Config] (naming/formatting); none
// reads the wall clock (AC-11) and none reads live engine state through any
// channel but the log (AC-5). Because the §20 name is resolved once at
// ingest and persisted with the record, generation cannot fail on a name
// resolution and returns no error (SEC-110).

// dailyTicksPerMonth is the number of logistics day-ticks per calendar
// month (§3), used to derive a story's Month from its Tick. It is
// duplicated from engine.core's DailyTicksPerMonth rather than imported, so
// this package carries no engine.core production import (GR#20/AC-14). The
// duplication is guarded by a drift test (drift_test.go) that imports
// engine.core in a _test.go file (the sanctioned exemption) and asserts the
// two agree — changing one requires changing the other (dev-team-process
// weakness pattern #2).
const (
	dailyTicksPerMonth int64 = 30
	// monthsPerYear is the calendar months per game year, used to fold
	// months into years for the annual review. A self-contained schema
	// constant (engine.core does not export a counterpart to mirror; the
	// same value 12 is independently defined in engine.season/engine.census).
	monthsPerYear int64 = 12
	// maxBulletinStories is the front-page ceiling (§29.2: "3–5 ranked
	// stories"). A month with fewer than five events yields fewer stories;
	// the "3" is the natural floor whenever three or more exist.
	maxBulletinStories = 5
)

// monthOf derives the calendar month from a tick (§9's month index:
// floor(tick / dailyTicksPerMonth)).
func monthOf(tick int64) int64 { return tick / dailyTicksPerMonth }

// yearOf folds a month into a game year.
func yearOf(month int64) int64 { return month / monthsPerYear }

// Bulletin generates the monthly bulletin front page for the given month:
// every event whose derived month matches is scored by the salience editor
// and ranked, and the top maxBulletinStories are returned with salience
// and 1-based rank. It is a pure, deterministic function of its inputs
// (AC-10): the same log and Config always produce the same story set and
// order, tie-broken by EventID ascending.
func Bulletin(h *History, month int64, cfg Config) []BulletinStory {
	records := h.SnapshotWhere(func(r record) bool { return monthOf(r.ev.Tick) == month })
	return buildBulletin(records, cfg)
}

// buildBulletin scores and ranks records already filtered to one month. It
// is the shared core Bulletin uses; it also runs directly in tests against a
// hand-built record set.
func buildBulletin(records []record, cfg Config) []BulletinStory {
	ranked := rankEvents(records, cfg)
	limit := len(ranked)
	if limit > maxBulletinStories {
		limit = maxBulletinStories
	}
	out := make([]BulletinStory, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, BulletinStory{
			Story:    ranked[i].story,
			Salience: ranked[i].salience,
			Rank:     i + 1,
		})
	}
	return out
}

// AnnualReview computes the "year in numbers plus biggest story" view for
// the given game year, from the SAME [History] the bulletin drew from all
// year (AC-4) — there is no parallel accumulator struct. The numbers are
// aggregateNumbers over the year's events, so the annual review's totals
// reconcile with what the bulletin's events independently total.
func AnnualReview(h *History, year int64, cfg Config) AnnualReport {
	records := h.SnapshotWhere(func(r record) bool { return yearOf(monthOf(r.ev.Tick)) == year })
	return buildAnnualReview(year, records, cfg)
}

// buildAnnualReview is the shared core AnnualReview uses and tests drive
// directly. If the year had no events, Numbers is still the fixed
// five-figure set (all zero) and HasBiggest is false — an explicit
// "no story this year", never a fabricated one.
func buildAnnualReview(year int64, records []record, cfg Config) AnnualReport {
	review := AnnualReport{Year: year, Numbers: aggregateNumbers(records)}
	ranked := rankEvents(records, cfg)
	if len(ranked) > 0 {
		review.BiggestStory = ranked[0].story
		review.HasBiggest = true
	}
	return review
}

// Epilogue generates the end-game closing view from the whole persisted
// [History] (AC-5). Its only data input is the log plus the static
// naming/formatting Config — it has no side channel to live engine state
// that bypasses the log, so it is structurally incapable of asserting a
// fact the log does not support. Removing (redacting) a milestone record
// from the log removes that milestone's claim from the output.
func Epilogue(h *History, cfg Config) EpilogueReport {
	return buildEpilogue(h.Snapshot(), cfg)
}

// buildEpilogue is the shared core Epilogue uses and tests drive directly.
func buildEpilogue(records []record, cfg Config) EpilogueReport {
	ep := EpilogueReport{Numbers: aggregateNumbers(records)}
	for _, r := range records {
		if r.ev.Category != CategoryMilestone {
			continue
		}
		ep.Milestones = append(ep.Milestones, buildStory(r.ev, r.name))
	}
	ranked := rankEvents(records, cfg)
	if len(ranked) > 0 {
		ep.BiggestStory = ranked[0].story
		ep.HasBiggest = true
	}
	return ep
}

// aggregateNumbers is the single "year in numbers" aggregation shared by
// the annual review (filtered to one year) and the epilogue (the whole
// log) — one implementation, so the two can never drift (GR#3/AC-4). It
// returns a fixed, documented set in a fixed order: Deaths (sum of death
// magnitudes — an exact int64 count), then the count of Firsts, Records,
// Crises, and Milestones. The death total accumulates through
// [num.SatAdd] (GR#16/SEC-206), so two death events each at
// math.MaxInt64 saturate to math.MaxInt64 rather than wrapping to a
// negative "Deaths" figure — a wrapped total would be a hallucinated fact.
func aggregateNumbers(rs []record) []AnnualNumber {
	var deaths, firsts, records, crises, milestones int64
	for _, r := range rs {
		switch r.ev.Category {
		case CategoryDeath:
			deaths = num.SatAdd(deaths, r.ev.Magnitude)
		case CategoryFirst:
			firsts++
		case CategoryRecord:
			records++
		case CategoryCrisis:
			crises++
		case CategoryMilestone:
			milestones++
		}
	}
	return []AnnualNumber{
		{Label: "Deaths", Value: deaths},
		{Label: "Firsts", Value: firsts},
		{Label: "Records", Value: records},
		{Label: "Crises", Value: crises},
		{Label: "Milestones", Value: milestones},
	}
}
