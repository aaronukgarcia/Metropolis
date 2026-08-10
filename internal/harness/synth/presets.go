package synth

// The two scale presets M0-ENG names explicitly (AC-3): "we must know
// the 10M-citizen tick cost in month 3 of development" (§2.4) and the
// perf CI job's own 1M-citizen regression gate (§6 point 5).
const (
	OneMillionCitizens int64 = 1_000_000
	TenMillionCitizens int64 = 10_000_000
)

// DefaultPresetSprawl and DefaultPresetNetworkShape are the sprawl/shape
// values Preset1M/Preset10M use for a caller that only cares about the
// citizen-count scale point, not a specific sprawl/shape combination.
//
// Neither figure comes from the spec — M0-ENG names only the citizen-
// count presets, not a canonical sprawl or network shape to pair with
// them — so these are this package's own choice, logged as an
// assumption against this item's BOW record: 0.5 is Sprawl's domain
// exact midpoint (the least arbitrary single point in [0,1]), and
// NetworkGrid is the simplest, cheapest-to-reason-about member of the
// closed shape enum. A caller that needs a specific sprawl/shape
// combination at 1M/10M citizens should construct Params directly rather
// than use these presets.
const (
	DefaultPresetSprawl                    = 0.5
	DefaultPresetNetworkShape NetworkShape = NetworkGrid
)

// Preset1M returns the named 1M-citizen synthetic-city Params (AC-3) —
// the scale point M0-ENG §6 point 5's perf-regression gate runs against.
func Preset1M(seed uint64) Params {
	return Params{CitizenCount: OneMillionCitizens, Seed: seed, Sprawl: DefaultPresetSprawl, NetworkShape: DefaultPresetNetworkShape}
}

// Preset10M returns the named 10M-citizen synthetic-city Params (AC-3) —
// the scale point M0-ENG §2.4 names as the "know the cost in month 3"
// target.
func Preset10M(seed uint64) Params {
	return Params{CitizenCount: TenMillionCitizens, Seed: seed, Sprawl: DefaultPresetSprawl, NetworkShape: DefaultPresetNetworkShape}
}
