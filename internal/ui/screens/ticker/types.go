package ticker

// Story is the single news-story record every one of the four F9
// surfaces renders — the rolling ticker, the monthly bulletin, the
// annual review's biggest story, and the searchable history archive.
// Keeping them all one type is deliberate: TIK-6 requires the archive to
// be the epilogue's data source "a single store, not a duplicated one",
// and TIK-5 requires every rendered story to trace to a real sim event.
// One Story type carrying EventID everywhere is what makes both of those
// checkable rather than promised (see doc.go's TIK-5/TIK-6 notes).
type Story struct {
	// EventID is the originating engine.news event ID this story
	// restates. Mandatory (TIK-5): applyTicker/applyBulletin/
	// applyAnnual/applyArchive reject — and log via MET-U703 — any story
	// whose EventID is empty after trimming (an empty string or
	// whitespace-only, SEC-076), so a rendered Story always traces back to
	// a real sim event and "no hallucinated news" is an enforced property
	// of the data model, not a hope about the prose.
	EventID string

	// Tick is the simulation tick the event occurred at (never wall
	// time — GR#21 / SF-8).
	Tick int64

	// Name is the §20 auto-name of the primary named entity the story
	// is about (e.g. "Pent Lane"); "" when the story has no named
	// entity. This screen renders whatever Name the engine supplies — it
	// never generates one (name generation is engine.news/engine.roads'
	// job, out of scope per this item's acceptance doc).
	Name string

	// Text is the engine-supplied prose, displayed verbatim. Fact
	// correctness is the engine's responsibility (§29: "the facts always
	// come from the engine"); this screen neither rewrites nor verifies
	// prose, it only renders it (and requires the EventID that anchors
	// it).
	Text string
}

// BulletinStory is one salience-ranked story on the monthly bulletin
// front page (TIK-2). The engine's editor already picked and ranked the
// 3–5 stories (salience scoring is engine.news' job — out of scope
// here); this screen preserves that order and renders it.
type BulletinStory struct {
	Story

	// Salience is the engine editor's score for this story (§29's
	// "editor = salience scoring"). Carried through to the rendered row
	// for SF-2 traceability, not recomputed here.
	Salience float64

	// Rank is the engine's 1-based salience rank (1 = front page lead).
	// This screen sorts by it (deterministically, tie-broken by EventID)
	// so the rendered order matches the editor's order regardless of the
	// wire order the engine happened to send (GR#21).
	Rank int
}

// AnnualNumber is one "year in numbers" figure on the annual review
// (TIK-3): a labelled aggregate (e.g. "Deaths: 42"). Sourced from
// "f9.annual"'s numbers field.
type AnnualNumber struct {
	Label string
	Value int64
}

// AnnualReview is the year-in-numbers plus biggest-story view (TIK-3),
// sourced from "f9.annual".
type AnnualReview struct {
	// Year is the reviewed game year (the engine's year index, not a
	// wall-clock year).
	Year int64

	// Numbers is the year-in-numbers figure list, in the engine's order.
	Numbers []AnnualNumber

	// BiggestStory is the year's biggest story (§29's "biggest story").
	// HasBiggest is false when the engine supplied none (e.g. a year
	// with no recorded events) — the render path shows an explicit
	// "no story this year" rather than fabricating one (SF-7's
	// unavailable-data posture).
	BiggestStory Story
	HasBiggest   bool
}
