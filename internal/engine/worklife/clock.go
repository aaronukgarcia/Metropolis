package worklife

// The fixed simulation-clock structure. These two constants are the ONLY
// hour-shaped numeric literals in this package's non-test source (see
// doc.go's "tick is a simulation hour" note): a day has 24 hours and a week
// has 7 days — physical facts of the two-layer clock, NOT balance
// tunables. Every schedule figure (pattern hours-per-day, rotation windows,
// weekly hours, wage/wellbeing coefficients) lives in data/worklife.json,
// never here.
const (
	// hoursPerDay is the number of simulation hours in a day.
	hoursPerDay = 24
	// daysPerWeek is the number of days in a week (day-of-week 0 = Monday
	// .. 6 = Sunday).
	daysPerWeek = 7
)

// hoursPerWeek is the number of simulation hours in a week, derived from
// the two clock constants above rather than written as a third literal.
const hoursPerWeek = hoursPerDay * daysPerWeek

// hourOfDay returns the hour within the day, in [0, 24), for an absolute
// simulation-hour tick.
func hourOfDay(tick int64) int64 {
	return tick % hoursPerDay
}

// dayIndex returns the absolute day index (tick / 24) for an absolute
// simulation-hour tick.
func dayIndex(tick int64) int64 {
	return tick / hoursPerDay
}

// dayOfWeek returns the day within the week, in [0, 7), 0 = Monday, for an
// absolute simulation-hour tick.
func dayOfWeek(tick int64) int64 {
	return dayIndex(tick) % daysPerWeek
}

// weekIndex returns the absolute week index (tick / (24*7)) for an absolute
// simulation-hour tick.
func weekIndex(tick int64) int64 {
	return tick / hoursPerWeek
}
