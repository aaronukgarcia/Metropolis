package news

// Category is one of the five story categories §29 names for the
// salience-scoring editor: deaths, firsts, records, crises, milestones.
// The underlying string is also the category's identity in the salience
// weight data file (salience.json), so a category's Go identity and its
// JSON identity are the same value — there is no name-mapping table to
// drift out of sync.
type Category string

const (
	// CategoryDeath is a death event (e.g. "42 deaths"). Magnitude is the
	// death count.
	CategoryDeath Category = "death"
	// CategoryFirst is a "first" event (e.g. "First Cheriton University
	// graduates enter workforce"). Magnitude is a salience weight; the
	// count itself is one.
	CategoryFirst Category = "first"
	// CategoryRecord is a record event (e.g. "M20 queue hits 2 hours").
	// Magnitude is the record's margin in whole units.
	CategoryRecord Category = "record"
	// CategoryCrisis is a crisis event (e.g. a port strike, a gang tax).
	// Magnitude is the crisis severity.
	CategoryCrisis Category = "crisis"
	// CategoryMilestone is a milestone event (e.g. "University opens").
	// Magnitude is a salience weight; the count itself is one.
	CategoryMilestone Category = "milestone"
)

// allCategories is the exhaustive, ordered set of §29's five categories
// (AC-2). Ordered (not a map) so any caller ranging over it gets a
// deterministic order (GR#21) — nothing in this package ranges over the
// weight map directly on a path whose result matters.
var allCategories = []Category{
	CategoryDeath, CategoryFirst, CategoryRecord, CategoryCrisis, CategoryMilestone,
}

// ValidCategory reports whether c is one of §29's five story categories.
func ValidCategory(c Category) bool {
	switch c {
	case CategoryDeath, CategoryFirst, CategoryRecord, CategoryCrisis, CategoryMilestone:
		return true
	default:
		return false
	}
}

// Event is one real sim event the ticker-ingestion path accepts (AC-1).
// It is engine.news's own representation of an engine.core event-bus
// delivery: the composition root calls [NewsAPI.Ingest] with one Event per
// atomic sim event. Every field is a fact sourced from the sim; none is
// prose invented here (§29: "the facts always come from the engine").
type Event struct {
	// ID is the event's unique identifier. Mandatory and non-empty after
	// trimming — it is the drill-through reference every emitted story
	// carries back to its originating record (AC-1, TIK-5).
	ID string
	// Tick is the simulation tick the event occurred at (never wall
	// time — GR#21). Month is derived from Tick, never stored separately.
	Tick int64
	// Category is the §29 salience category the event belongs to.
	Category Category
	// Magnitude is the salience-relevant magnitude of the event — a death
	// count, a record's margin (in whole units), a crisis severity. Kept
	// as int64 so the annual/epilogue aggregates stay exact (no float→int
	// loss, GR#16); salience scoring widens to float64 only for ranking.
	Magnitude int64
	// EntityID is the primary named entity the story is about, as an
	// engine-side ID (e.g. a road ID). "" means the story has no named
	// entity. Today only road IDs are resolvable through a registered
	// outbound edge (engine.roads) — see [RoadNamer] and doc.go's BUG-058
	// note.
	EntityID string
	// Text is the engine fact prose for the event ("42 deaths", "third
	// shop shuts"). The named entity, when present, is carried separately
	// in [Story.Name], never baked into Text by this package.
	Text string
}

// Story is the single emitted news item every layer produces (AC-1): the
// rolling ticker item, one bulletin story, the annual review's biggest
// story, an archive entry, and an epilogue milestone. One type carrying
// the source reference everywhere is what makes "every claim traceable to
// a sim record" checkable rather than promised (US-1/US-5).
type Story struct {
	// EventID is the originating event ID this story restates — the
	// drill-through reference (AC-1). Never empty for a story this
	// package emits (Ingest rejects an empty-ID event).
	EventID string `json:"eventId"`
	// EntityID is the source entity reference (the engine-side ID the
	// story is about), kept alongside EventID so a caller can ask "says
	// who" and get both the event and the entity (AC-1). Engine-internal:
	// not part of the f9.* wire schema (json:"-").
	EntityID string `json:"-"`
	// Tick is the sim tick the event occurred at.
	Tick int64 `json:"tick"`
	// Month is the calendar month, derived from Tick
	// (Tick / dailyTicksPerMonth). Engine-internal (json:"-"): the
	// bulletin wire carries month at the patch level, not per story.
	Month int64 `json:"-"`
	// Name is the §20 auto-name of the primary named entity, resolved
	// through [RoadNamer] at generation time; "" when the story has no
	// named entity (or none is resolvable — see AC-8).
	Name string `json:"name,omitempty"`
	// Text is the engine fact prose, displayed verbatim.
	Text string `json:"text"`
}

// BulletinStory is one salience-ranked story on the monthly bulletin
// front page (§29.2): a [Story] plus the editor's salience score and
// 1-based rank.
type BulletinStory struct {
	Story
	// Salience is the editor's score for this story (category weight ×
	// magnitude — see salience.go).
	Salience float64 `json:"salience"`
	// Rank is the 1-based salience rank (1 = front-page lead). Ties are
	// broken by EventID ascending (AC-10).
	Rank int `json:"rank"`
}

// AnnualNumber is one "year in numbers" figure on the annual review
// (§29.3). Labels are a fixed, documented set — see aggregateNumbers.
type AnnualNumber struct {
	Label string `json:"label"`
	Value int64  `json:"value"`
}

// AnnualReport is the year-in-numbers plus biggest-story view (§29.3)
// returned by [AnnualReview], computed from the same [History] the bulletin
// draws from all year (AC-4) — never a second, independently-accumulated
// running total.
type AnnualReport struct {
	// Year is the reviewed game year (year index = month / monthsPerYear,
	// not a wall-clock year).
	Year int64 `json:"year"`
	// Numbers is the year-in-numbers figure list, in a fixed documented
	// order (aggregateNumbers).
	Numbers []AnnualNumber `json:"numbers"`
	// BiggestStory is the year's highest-salience story. HasBiggest is
	// false when the year had no recorded events — an explicit
	// "no story this year" rather than a fabricated one (SF-7).
	BiggestStory Story `json:"biggestStory"`
	HasBiggest   bool  `json:"hasBiggest"`
}

// EpilogueReport is the end-game closing view (§29.4) returned by
// [Epilogue], generated exclusively from the whole persisted [History]
// (AC-5): every milestone claim is traceable to a milestone event still
// present in the log, so a claim the log does not support is structurally
// impossible.
type EpilogueReport struct {
	// Milestones is every milestone-category claim, one per milestone
	// event in the log, in ingest order.
	Milestones []Story `json:"milestones"`
	// Numbers are the lifetime "year in numbers" aggregates over the
	// whole log (aggregateNumbers over every event).
	Numbers []AnnualNumber `json:"numbers"`
	// BiggestStory is the single highest-salience story of the run.
	BiggestStory Story `json:"biggestStory"`
	HasBiggest   bool  `json:"hasBiggest"`
}

// RoadNamer is the dependency-inversion seam for engine.roads (MOD-024, in
// progress) — the naming-registry half of the registered RoadsAPI outbound
// edge (§20: "Auto-naming (every object, deterministic from seed+id)").
// engine.news consumes road names through this seam so a story about a
// road carries that road's real §20 name, never a generated or placeholder
// string (AC-3). When engine.roads lands, its RoadsAPI implements this
// interface; until a concrete implementation is wired, a story whose event
// references a road ID cannot be resolved and errors (AC-8) rather than
// fabricating a name.
type RoadNamer interface {
	// RoadName resolves a road ID to that road's §20 auto-name (e.g.
	// "Pent Lane"). It returns a non-nil error when the ID is unknown or
	// no longer registered — the signal AC-8 turns into a registry error.
	RoadName(roadID string) (string, error)
}

// ProseRewriter is the optional LLM soft-layer seam (§29, post-v1 per
// V.1's scope table): it rewrites engine prose with flavour. The facts
// always come from the engine; this seam's output is only ever ACCEPTED
// after [FactLock] confirms it did not alter a fact (AC-6), and a
// failure/timeout/disabled rewriter never blocks or drops a story (AC-7).
// A nil ProseRewriter means the soft-layer is disabled — the v1 default.
type ProseRewriter interface {
	// Rewrite returns rewritten prose for one story. It may return an
	// error (failure/timeout); the caller falls back to the engine prose.
	Rewrite(in Story) (string, error)
}

// Config is the static generation configuration the deterministic
// generation functions take: the salience weight table (from salience.json,
// GR#15) and the §20 name resolver (static naming data). It is immutable
// after construction and carries no live engine/world reference.
type Config struct {
	// Weights maps each §29 category to its salience weight. Loaded from
	// the embedded salience.json, never a Go literal (GR#15).
	Weights map[Category]float64
	// Namer is the §20 name resolver; nil means no named entity is
	// resolvable (a story whose event references an entity then errors
	// per AC-8).
	Namer RoadNamer
}
