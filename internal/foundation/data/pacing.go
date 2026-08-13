package data

// This file defines data/pacing.json's typed schema (engine.core,
// FEAT-030), routed through the SAME generic [Load] every other config
// file in this package uses -- matching MarketFile's split (market.go)
// rather than inventing a third loading pattern for a ninth file.
//
// FEAT-030 closes MOD-012's interim ruling (2026-08-09, see
// clock.go's former doc comment in internal/engine/core): the master
// doc §3 pacing default (secondsPerMonthAt1x, 480) used to live only as
// a named Go var because this package's original eight §24 files had
// no natural home for a timing/pacing value. pacing.json is that home,
// added the same way market.json was (MOD-020 ruling 1) -- not part of
// the eight-file set [LoadAll] aggregates.

// FilePacing is data/pacing.json's filename, relative to the resolved
// data directory (see ResolveDataDir). Not part of the original §24
// config set FileConsumption..FilePolicies enumerate, matching
// FileMarket's own precedent for a file added after that set.
const FilePacing = "pacing.json"

// Pacing is data/pacing.json's top-level schema: the master doc §3
// real-time pacing knob. SecondsPerMonthAt1x is the number of real
// seconds one calendar month takes to elapse at Speed1x -- engine.core's
// Clock reads it only through the value this struct decodes to, never a
// hardcoded literal (GR#15).
type Pacing struct {
	Version int `json:"version"`

	// SecondsPerMonthAt1x is the master doc §3 pacing default (480 =
	// "1 game month ~= 8 real minutes"). Must be positive: a zero or
	// negative pacing constant would make Clock.SecondsPerMonth divide
	// by zero or produce a nonsensical negative real-time duration.
	SecondsPerMonthAt1x int64 `json:"secondsPerMonthAt1x"`
}

// Validate implements Validator.
func (p *Pacing) Validate() error {
	if err := requireVersion(p.Version); err != nil {
		return err
	}
	if p.SecondsPerMonthAt1x <= 0 {
		return fieldErr("secondsPerMonthAt1x", "must be > 0")
	}
	return nil
}
