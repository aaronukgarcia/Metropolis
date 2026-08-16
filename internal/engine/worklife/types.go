package worklife

// PatternKind is the time-pattern vocabulary every placed job carries
// (AC-1): core-hours (a fixed working-day window), shift (24x7 rotations),
// or any-time (flexible placement within weekly hours).
type PatternKind string

// The three documented pattern kinds. The on-duty profiles (hours/day,
// start/end, rotations, coverage span) live in data/worklife.json — these
// strings only name the kinds, never carry an hour figure (AC-2).
const (
	PatternCoreHours PatternKind = "core-hours"
	PatternShift     PatternKind = "shift"
	PatternAnyTime   PatternKind = "any-time"
)

// Worker is a placed/employed job as worklife sees it: a worker identity
// plus the time pattern they are rostered under. worklife is deliberately
// decoupled from engine.citizens' Citizen and engine.staffing's labour pool
// (GR#20): staffing maps a citizen to a Worker, worklife supplies the
// schedule — it does not own who fills which slot.
type Worker struct {
	ID      uint64
	Pattern PatternKind
}

// WorkingWeekEffect is the active working-week policy's effect, as consumed
// through the PoliciesAPI seam (AC-8). It carries the three placeholders
// the balance-number regime names — hours, wage coefficient, wellbeing
// weight — inseparably: the wage gain and the wellbeing cost come from the
// SAME effect, so there is no configuration in which 996 yields the wage
// gain with zero wellbeing cost (AC-13).
type WorkingWeekEffect struct {
	// HoursPerWeek is the policy's weekly hours (e.g. 72 for 996).
	HoursPerWeek int64
	// WageCoefficient is the multiplier on the base wage (1.0 default, >1
	// for 996). Must be finite and >= 1.
	WageCoefficient float64
	// WellbeingWeight is the non-negative overwork cost subtracted from the
	// §42 discretionary-hours balance (0 default, >0 for 996).
	WellbeingWeight float64
}
