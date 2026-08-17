// Package news is the engine.news module (MOD-043): the four-layer news
// system generated from real sim events with real names, per §29.
//
// Module key: engine.news (see code.json; GUID
// 129592d7-2873-4857-b733-e21f175b2ec2)
// Spec refs:  §29 The News System (four layers, salience-scoring editor,
// archive searchable, LLM soft-layer optional/online); §20 Roads &
// Auto-Naming ("The ticker uses names, so the city reads like a place, not
// a spreadsheet"); §3 Time (the two-layer clock, whose month index is the
// bulletin's natural cadence); V.1 (the LLM soft-layer is explicitly
// "optional online feature, post-v1").
//
// # The four layers (§29)
//
//   - Ticker (NewsAPI.Ingest) — the rolling feed of atomic events: one
//     emitted Story per ingested sim event, each carrying the source
//     reference (event ID, entity ID, tick, month) back to the originating
//     record.
//   - Monthly Bulletin (Bulletin) — the month-end front page: the top
//     3–5 stories ranked by the salience-scoring editor over the month's
//     actual events.
//   - Annual Review (AnnualReview) — the year in numbers plus the biggest
//     story, drawn from the same event log the bulletin used all year.
//   - Epilogue (Epilogue) — the city's whole history at win/death,
//     generated exclusively from the persisted log.
//
// Every layer reads from one append-only [History] (the archive), which is
// the single source of truth: a story that "didn't make the front page" is
// still queryable via [NewsAPI.Archive]/[NewsAPI.Query] — distinguishable
// from "never happened" (AC-9).
//
// # Salience scoring (§29.2, AC-2)
//
// The editor scores each event as category-weight × magnitude, with the
// weight table loaded from the embedded salience.json (GR#15 — §29 names
// the categories but no relative weighting, so the weights are data, not
// literals; they are placeholders pending Aaron's balance pass, which
// salience.json's disclosure field states). Ranking is deterministic:
// salience descending, ties broken by EventID ascending (AC-10), so
// repeated runs and different worker counts produce byte-identical
// selections.
//
// # The LLM soft-layer is fact-locked, not trusted (§29, AC-6/AC-7)
//
// The optional online LLM soft-layer rewrites prose with flavour; the
// facts always come from the engine, and "no hallucinated news" is an
// enforced property of the pipeline rather than a hope about model
// behaviour. The rewrite touches only prose ([FactLock] verifies the
// story's facts survive); a fact-altering rewrite is rejected and the
// engine prose retained, and a failing/timing-out/disabled rewriter never
// blocks or drops a story. The soft-layer is optional and post-v1 (V.1's
// scope table): the mechanism and its fact-lock contract are implemented
// and tested against a mock, but no real LLM is wired.
//
// # What a fact is (the FactLock contract, enforced — not prose)
//
// A fact is any prose-visible claim the soft-layer could alter, and the
// lock rejects a rewrite it cannot prove preserves every one. There are
// three classes, each checked against the original story:
//
//   - Ordered number sequence. Every signed maximal run of Unicode numeric
//     characters — decimal digits (any script, SEC-145) and "other number"
//     homoglyphs (superscripts, subscripts, circled numbers, fractions,
//     SEC-205) — is a numeric fact, and the ORDER of the runs is itself a
//     fact: "2 deaths on 3 roads" is a different fact from "3 deaths on 2
//     roads" (SEC-144), and a sign is part of the fact ("-2" is not "2",
//     SEC-205) whatever sign homoglyph it wears (SEC-214). The sequence must
//     survive exactly, in order.
//   - Named entity. The story's Name must appear in the rewritten prose as
//     a whole-token phrase bounded on both sides (SEC-108/SEC-146) — never a
//     substring, never extended by an adjacent token on either side, and with
//     any embedded number surviving too (SEC-207: "M20" is not "M 2"). When
//     the story has no Name, "empty stays empty": the rewrite must not invent
//     a named entity the original prose did not carry (SEC-216).
//   - Fact-word set. The set of fact-bearing tokens present in the prose
//     (event-type words and month/date words — see data/news-facts.json) must
//     be unchanged (SEC-148): "2 deaths" must not become "2 births", and
//     "record set in March" must not become "record set in April".
//
// The lock is conservative by construction: it rejects any rewrite whose
// ordered number sequence, name phrase, or fact-word set differs from the
// original, and — when the fact-word list cannot be loaded — it fails closed
// and rejects everything. A false rejection is harmless (AC-7 falls back to
// the engine prose); a false acceptance is a hallucinated fact.
//
// # Which §29 categories this module can source today (BUG-058)
//
// §29 names five story categories — deaths, firsts, records, crises,
// milestones. engine.news is an event-bus consumer: it ingests events of
// all five categories through its registered engine.core edge (the inbound
// NewsAPI that the composition root feeds). What is NOT yet registered is
// named-entity attribution for anything but roads: the only registered
// outbound naming edge is engine.roads (via the [RoadNamer] seam), so a
// story whose primary named entity is a road carries that road's real §20
// name, while a story about a citizen (deaths/firsts), a financial
// milestone, or a crime/health crisis has no registered edge from which to
// resolve its name — those categories arrive with no resolvable name today.
// Registering the missing edges (deaths/firsts → citizens/population,
// milestones → finance/core, crises → crime/health/finance) is BUG-058's
// job (a master-plan + regenerate task, never a hand-edit to code.json);
// this module documents the gap rather than silently importing an
// unregistered edge (AC-14).
package news
