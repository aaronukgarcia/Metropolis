package compose

import (
	"encoding/json"
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// BUG-324: "chrome.topbar" — the persistent top bar's six vital-sign
// figures (date, clock cycle, speed, money, population, credit rating).
//
// internal/ui/screens/chrome has been fully built and tested since
// FEAT-042, but nothing ever registered the view it renders FROM, so
// registering the screen alone would have reproduced the blank-map
// failure exactly: a Subscribe("chrome.topbar") rejected by the engine
// the same way mapScreen's "f1.viewport" is, and therefore a
// permanently empty top bar. This file is the engine-side half that has
// to land first.
//
// It mirrors finance_publish.go / services_publish.go's one-file-per-
// integration convention and, per the same GR#20 discipline, builds
// compose's OWN copy of the wire schema — the same JSON tags as
// ui.screen.chrome's wire.go's wireFiguresPatch / Figures, duplicated
// independently, NEVER importing internal/ui/screens/chrome.
//
// # Per-field sourcing (the honesty ledger)
//
// Every figure below comes from real engine state. Where a figure the
// UI type names does not exist engine-side, it is rendered as what DOES
// exist and labelled as such — never as a plausible-looking zero and
// never as an invented scale (a status bar showing a confident wrong
// number is worse than one showing fewer fields):
//
//   - Date: engine.core's Clock.Month() is an absolute month index, and
//     data/seasonal.json's meta.monthIndexConvention pins index 0 =
//     January. That gives a REAL calendar month name. It does NOT give a
//     calendar YEAR: nothing anywhere in the tree pins which real year
//     world genesis falls in (the same open assumption seasonal.json
//     records for the month). So the year is rendered as an ordinal
//     world year — "Jan Y1", not a fabricated "Jan 2026".
//   - ClockCycle: Clock.DayInMonth(), 0..29 of the 30 logistics
//     day-ticks — exactly what the field documents.
//   - Speed: Clock.Speed(), the real multiplier (1/2/4/8), reported as 0
//     while Clock.Paused(). NOT the 0/1/2/3 ordinal the UI type's
//     original doc comment guessed at — see chrome.Figures.Speed, whose
//     comment this integration corrected.
//   - Money: the city treasury, converted from micropounds to WHOLE
//     POUNDS. The raw micropound figure would render as "money
//     10000000" for a £10 treasury — off by six orders of magnitude to
//     any player reading it, which is precisely the
//     confident-wrong-number failure this item exists to avoid.
//     Sourced from simState's treasury (via its publish mirror, see
//     setTreasury below) and NOT from engine.finance's AcctTreasury
//     account, which finance_publish.go reads: baseline one never funds
//     the finance ledger's accounts — Wire seeds st.treasury and
//     financeHook moves st.treasury — so AcctTreasury is flatly zero
//     and publishing it would have put "money 0" on the bar of a city
//     with £10 in the bank. (That is a REAL, pre-existing defect in
//     "f2.finance"'s balance sheet, which publishes an all-zero balance
//     sheet for the same reason. It is recorded here, not silently
//     fixed as a side effect of a P0 top-bar item.)
//   - Population: citizens.TotalPopulation — the live citizen count.
//   - Rating: finance.CreditRatingNow(), the city's real 0..1000 credit
//     score, rendered as "N/1000". There is NO letter-grade ("AA")
//     rating scale anywhere in the engine — the UI type's example is
//     aspirational — and inventing a score-to-letters mapping here would
//     be fabricating balance data (GR#15). The real score, on its own
//     declared scale, is published instead.
//
// Concurrency (subscribe.go's ViewPatchFunc contract): this runs on the
// subscription pump goroutine, concurrently with tick-phase writes to
// simState. Every read below therefore goes through an accessor that
// takes its own module's lock — Engine.Clock (e.mu, the same call
// engine.status' own patch func makes from this same goroutine),
// FinanceAPI.AccountBalance / CreditRatingNow (f.mu), and
// CitizensAPI.TotalPopulation (c.mu) — never a plain simState field
// read.

// chromeWireSchemaVersion mirrors ui.screen.chrome/wire.go's
// wireSchemaVersion constant VALUE (kept as a separate, independently
// maintained value per the same GR#20/SF-1 discipline
// services_publish.go's and finance_publish.go's identical constants
// follow — a version mismatch is exactly what chrome's own
// decodeFiguresPatch schemaVersion check exists to catch).
const chromeWireSchemaVersion = 1

// chromeViewSubscriptionName mirrors internal/ui/screens/chrome's
// ViewChrome constant VALUE ("chrome.topbar") — duplicated
// independently as compose's own string literal, never imported from
// internal/ui/screens/chrome (GR#20). Kept as its own named constant
// for the same reason servicesViewSubscriptionName is: a compose test
// needs a symbol to assert the registered view-name set against.
const chromeViewSubscriptionName = "chrome.topbar"

// chromeMonthNames is the Gregorian calendar-month abbreviation table,
// indexed by data/seasonal.json's documented monthIndexConvention
// (index 0 = January ... index 11 = December). A slice, never a map —
// nothing in this package ranges a map (GR#21) — and not balance data:
// these are the names of the months, not a tunable figure.
var chromeMonthNames = []string{
	"Jan", "Feb", "Mar", "Apr", "May", "Jun",
	"Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
}

// chromeMonthsPerYear is the calendar length chromeDateString folds an
// absolute month index by. It matches engine.season's own monthsPerYear
// and is derived from the name table above rather than repeated as a
// second literal (GR#3).
var chromeMonthsPerYear = int64(len(chromeMonthNames))

// chromeFigures mirrors ui.screen.chrome's exported Figures type
// field-for-field, including its JSON tags.
type chromeFigures struct {
	Date       string `json:"date"`
	ClockCycle int    `json:"clockCycle"`
	Speed      int    `json:"speed"`
	Money      int64  `json:"money"`
	Population int64  `json:"population"`
	Rating     string `json:"rating"`
}

// chromeTopBarWirePatch is compose's own copy of ui.screen.chrome/
// wire.go's wireFiguresPatch. Unlike f2.finance/f4.services' patches,
// Figures is a VALUE with no omitempty: chrome's own decodeFiguresPatch
// replaces the whole figures block per delta (there are no independent
// sub-views to add later), so every patch always carries all six
// fields.
type chromeTopBarWirePatch struct {
	SchemaVersion int           `json:"schemaVersion"`
	Figures       chromeFigures `json:"figures"`
}

// chromeDateString renders an absolute month index (0 = world genesis)
// as the top bar's date. See this file's sourcing ledger for why the
// year is an ordinal world year rather than a calendar year. A negative
// index cannot occur (Clock.Month() is floor(tick/30) over a
// monotonically non-negative tick) but is clamped to genesis rather
// than allowed to index the name table out of range or produce a
// negative-modulo (GR#16 boundary discipline).
func chromeDateString(monthIndex int64) string {
	if monthIndex < 0 {
		monthIndex = 0
	}
	name := chromeMonthNames[monthIndex%chromeMonthsPerYear]
	year := monthIndex/chromeMonthsPerYear + 1
	return name + " Y" + strconv.FormatInt(year, 10)
}

// chromeSpeedFigure maps the engine clock's paused flag + multiplier
// onto the top bar's single Speed integer: 0 means paused, otherwise
// the real multiplier (1/2/4/8). The two are separate facts in
// engine.core (Clock.Paused and Clock.Speed) and a paused clock retains
// its last multiplier, so "paused" must win — a bar reading "speed 4"
// on a frozen simulation is the confident-wrong-number failure again.
func chromeSpeedFigure(paused bool, speed int) int {
	if paused {
		return 0
	}
	return speed
}

// chromeRatingString renders engine.finance's 0..1000 credit score on
// its own declared scale. See this file's sourcing ledger: no
// letter-grade scale exists engine-side, and one is not invented here.
func chromeRatingString(score finance.CreditScore) string {
	return strconv.FormatInt(int64(score), 10) + "/1000"
}

// setTreasury is the ONLY place st.treasury is ever assigned. It keeps
// the BUG-324 publish mirror (treasuryPub) in step with the simulation
// field, so a ViewPatchFunc running on the subscription pump goroutine
// can read the player's money without racing the phase pipeline. See
// simState's own doc comment for the full rationale (and for why
// engine.finance's AcctTreasury is not the source: baseline one never
// funds it).
//
// Callers pass the already-computed new value (st.setTreasury(
// num.SatSub(st.treasury, x))) rather than a delta, so every existing
// saturating-arithmetic call site keeps its exact shape and this helper
// adds no arithmetic of its own to get wrong.
func (st *simState) setTreasury(v int64) {
	st.treasury = v
	st.treasuryPub.Store(v)
}

// publishedTreasury reads the treasury mirror. Lock-free and safe from
// any goroutine — the read side of setTreasury. Nothing in the
// simulation may call this: it exists for view publishing only, so a
// mirror bug can never change a simulated outcome.
func (st *simState) publishedTreasury() int64 { return st.treasuryPub.Load() }

// buildChromeTopBarPatch is "chrome.topbar"'s ViewPatchFunc — the
// engine-side publisher BUG-324 was blocked on. It returns a patch
// carrying all six figures, every one sourced from live module state
// (see the file's sourcing ledger), so the bar is non-empty from the
// very first delta rather than only after the world has run.
func (st *simState) buildChromeTopBarPatch() (json.RawMessage, error) {
	clock, err := st.e.Clock()
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{
			"module": "core", "accessor": "Clock",
		})
	}

	patch := chromeTopBarWirePatch{
		SchemaVersion: chromeWireSchemaVersion,
		Figures: chromeFigures{
			Date:       chromeDateString(clock.Month()),
			ClockCycle: int(clock.DayInMonth()),
			Speed:      chromeSpeedFigure(clock.Paused(), int(clock.Speed())),
			// Integer division, not a rounding helper: a status bar
			// reads whole pounds, and truncation toward zero is the
			// documented behaviour of the field (see the ledger).
			Money:      st.publishedTreasury() / int64(finance.MicropoundsPerPound),
			Population: int64(st.citizens.TotalPopulation(st.cid)),
			Rating:     chromeRatingString(st.finance.CreditRatingNow()),
		},
	}

	raw, err := json.Marshal(patch)
	if err != nil {
		// Marshalling a plain struct of strings/ints cannot fail;
		// unreachable in practice — mirrored on
		// buildFinanceBalanceSheetPatch's identical "cannot fail"
		// branch. Per GR#1, degrade loudly rather than panic.
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "chrome", "accessor": "json.Marshal"})
	}
	return raw, nil
}
