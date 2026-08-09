package core

// This file implements the two-layer clock (§3, AC-1): one calendar
// month = one day-night cycle = DailyTicksPerMonth logistics day-ticks.
// Nothing in this file reads the wall clock — see doc.go and AC-12.

// DailyTicksPerMonth is the fixed number of logistics day-ticks inside
// one calendar-month cycle (§3: "Inside each cycle run 30 logistics
// day-ticks"). Like det.NumShards, this is a constant forever: it is
// never derived from config, speed, or anything else that could vary —
// only secondsPerMonthAt1x (the real-time pacing knob) is configurable;
// the day-tick *count* per month is a fixed rule of the simulation.
const DailyTicksPerMonth int64 = 30

// DefaultSecondsPerMonthAt1x is the master doc §3 pacing default: "1
// game month ~= 8 real minutes (config: secondsPerMonthAt1x, default
// 480)". AC-1 requires this be a single named, non-hardcoded value
// rather than a magic number sprinkled through the codebase.
//
// Decision (open question, see the dispatch report): foundation.data's
// eight §24 config files (internal/foundation/data/types.go) do not yet
// include a pacing/timing file, so there is nowhere to load this from
// per GR#15's letter today. This package therefore keeps it as the one
// named var below plus an Option (WithSecondsPerMonthAt1x) any caller
// can override with a real config-sourced value once foundation.data
// grows a pacing file — no other line in this package repeats 480.
var DefaultSecondsPerMonthAt1x int64 = 480

// Speed is the simulation pacing multiplier control (§3's speed table).
// Per M0-ENG §1.1 and this item's brief, Speed affects nothing
// in-engine yet except being queryable via Clock.SecondsPerMonth /
// TicksPerRealSecond — REAL wall-clock pacing (turning a multiplier
// into "call AdvanceTicks every N milliseconds") is the UI/transport's
// concern, not this package's. engine.core only stores and reports the
// requested speed.
type Speed int

// The documented speed multipliers (§3). Speed8xDebug is reserved for
// feat.debugmode; this package exposes the value and accepts it via
// SetSpeed but does not itself enforce the debug-mode gate (AC-2).
const (
	Speed1x      Speed = 1
	Speed2x      Speed = 2
	Speed4x      Speed = 4
	Speed8xDebug Speed = 8
)

// ValidSpeed reports whether s is one of the documented multipliers.
func ValidSpeed(s Speed) bool {
	switch s {
	case Speed1x, Speed2x, Speed4x, Speed8xDebug:
		return true
	default:
		return false
	}
}

// Clock models §3's two-layer cadence. Tick is the total elapsed
// daily-tick count since world genesis (tick 0); Month and DayInMonth
// are derived from it, never stored independently, so they can never
// drift out of sync with Tick. The zero Clock is a valid, paused,
// genesis-tick clock at Speed1x.
type Clock struct {
	tick                int64
	speed               Speed
	paused              bool
	secondsPerMonthAt1x int64
}

// NewClock constructs a Clock at genesis (tick 0), paused, at Speed1x,
// with the given pacing constant (pass DefaultSecondsPerMonthAt1x for
// the master doc §3 default).
func NewClock(secondsPerMonthAt1x int64) Clock {
	return Clock{
		speed:               Speed1x,
		paused:              true,
		secondsPerMonthAt1x: secondsPerMonthAt1x,
	}
}

// Tick returns the total elapsed daily-tick count since genesis.
func (c Clock) Tick() int64 { return c.tick }

// Month returns the number of calendar months fully completed so far
// (§9's month index): floor(Tick / DailyTicksPerMonth).
func (c Clock) Month() int64 { return c.tick / DailyTicksPerMonth }

// DayInMonth returns the 0-based day-tick offset within the current
// (possibly incomplete) calendar month: Tick modulo DailyTicksPerMonth.
func (c Clock) DayInMonth() int64 { return c.tick % DailyTicksPerMonth }

// Speed returns the currently configured pacing multiplier.
func (c Clock) Speed() Speed { return c.speed }

// Paused reports whether the clock is currently paused.
func (c Clock) Paused() bool { return c.paused }

// advanceOneDay advances the clock by exactly one daily tick and
// reports whether that tick completed a calendar month (i.e. whether
// the monthly phase pipeline should run after it). It does not check
// Paused — engine.go's AdvanceTicks command handler is the deliberate,
// explicit driver of ticks (the headless/replay/single-step primitive,
// per int.protocol's AdvanceTicksPayload doc); Paused only matters to a
// hypothetical future real-time autonomous driver owned by the
// UI/transport layer (M0-ENG §1.1), which is out of scope here.
func (c *Clock) advanceOneDay() (monthCompleted bool) {
	c.tick++
	return c.tick%DailyTicksPerMonth == 0
}

// setSpeed stores the requested multiplier. Validation (ValidSpeed) is
// the caller's responsibility (engine.go's HandleCommand rejects an
// invalid speed with a registry-sourced error before calling this).
func (c *Clock) setSpeed(s Speed) { c.speed = s }

func (c *Clock) setPaused(p bool) { c.paused = p }

// SecondsPerMonth returns how many real seconds one calendar month
// takes to elapse at the clock's current speed, given its configured
// secondsPerMonthAt1x pacing constant. Paused reports 0 (no real-time
// progress). This is a pure, queryable function of state — it never
// drives anything itself (AC-2: "speed affects nothing in-engine yet
// except being queryable").
func (c Clock) SecondsPerMonth() int64 {
	if c.paused || c.speed <= 0 {
		return 0
	}
	return c.secondsPerMonthAt1x / int64(c.speed)
}

// TicksPerRealSecond returns the queryable tick-advance rate implied by
// the clock's current speed: DailyTicksPerMonth ticks occur over
// SecondsPerMonth real seconds. Returns 0 while paused. float64 is used
// deliberately here (never in committed world state, never on the tick
// path) — this is a UI/telemetry-facing pacing figure, not a simulation
// aggregate, so §1.2 point 4's fixed-shard-order summation rule does not
// apply to it.
func (c Clock) TicksPerRealSecond() float64 {
	secs := c.SecondsPerMonth()
	if secs <= 0 {
		return 0
	}
	return float64(DailyTicksPerMonth) / float64(secs)
}
