package news

// Registry error codes for engine.news (MOD-043). Range: G2300-G2399,
// claimed here per docs/planning/acceptance/README.md's "Conventions
// ratified during Sprint 1" (per-module error subranges are claimed at
// build time by the owning module, not pre-allocated in a master table).
//
// The G layer (engine second block) was fully claimed through G2200-G2299
// (engine.wellbeing) by the time this package landed, so engine.news opens
// G2300-G2399 under BUG-234's three-to-four-digit code-format widening —
// NOT the G1800-G1899 range an earlier dispatch brief suggested, which
// engine.education had already claimed. Checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-G23" internal/ cmd/` before
// claiming, per BUG-008's lesson — no prior MET-G23xx code existed either
// place. Every code below IS registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against this ever
// drifting out of sync.
const (
	// ErrUnresolvedEntity: a story's event references an entity ID that
	// does not resolve to a name at generation time — the ID is unknown,
	// was never registered, or the naming seam (engine.roads) is not
	// wired. AC-8: this is a registry-sourced error, never a
	// silently-dropped story and never a fabricated placeholder name
	// substituted in its place. Generation-time.
	ErrUnresolvedEntity = "MET-G2300"

	// ErrSalienceDataInvalid: the embedded salience.json could not be
	// loaded or failed schema validation (malformed JSON, an unrecognised
	// category key, a non-positive weight, or a missing weight for one of
	// §29's five categories). Load-time, GR#15/GR#7 — the weight table
	// never falls back to a silent default.
	ErrSalienceDataInvalid = "MET-G2301"

	// ErrFactLockRejected: the optional LLM soft-layer returned rewritten
	// prose that altered an engine fact (a name, a number, a fact-word) —
	// detected by FactLock and rejected, the engine prose retained (AC-6).
	// Raised (and logged) so a rejection is surfaced, never silently
	// absorbed (GR#17).
	ErrFactLockRejected = "MET-G2302"

	// ErrFactListInvalid: the embedded news-facts.json (the fact-bearing
	// token list the FactLock requires, SEC-148) could not be loaded or
	// failed schema validation — malformed JSON, an empty token list, an
	// empty pending-tuning disclosure, a non-letter token, or a duplicate
	// token after lowercasing. Load-time, GR#15/GR#7 — the FactLock never
	// falls back to a silently-empty fact-word set (it fails closed and
	// rejects every rewrite until the data is corrected).
	ErrFactListInvalid = "MET-G2305"

	// ErrInvalidEvent: an event passed to Ingest failed validation — empty
	// ID after trimming, negative tick, negative magnitude, an unknown
	// category, or empty prose. Ingest-time, GR#1/GR#16 — never silently
	// accepted into the log.
	ErrInvalidEvent = "MET-G2303"

	// ErrCopiedValue: a method was called on a struct-copied *NewsAPI
	// (SEC-020 family). A copied value aliases the original's mutex while
	// holding an independent lock, so it is rejected before the lock is
	// touched.
	ErrCopiedValue = "MET-G2304"
)
