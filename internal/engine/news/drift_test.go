package news

import (
	"testing"

	core "github.com/aaronukgarcia/Metropolis/internal/engine/core"
)

// TestClockConstantsAgreeWithEngineCore is the drift guard for the one
// time-derivation constant this package mirrors across a module boundary.
//
// Why the duplication exists: internal/engine/news must derive a story's
// Month from its Tick, and the canonical month length lives in engine.core's
// DailyTicksPerMonth. Importing engine.core from production news code would
// violate GR#20's registered-edge discipline (and engine.core is not a
// production dependency of news), so the value is duplicated here as
// dailyTicksPerMonth. The duplication is acceptable; silent divergence is
// not — this test makes the two agree, so changing one requires changing the
// other, and a future drift in the clock's month derivation fails loudly
// here instead of silently producing wrong month/year buckets.
func TestClockConstantsAgreeWithEngineCore(t *testing.T) {
	if dailyTicksPerMonth != core.DailyTicksPerMonth {
		t.Errorf(
			"dailyTicksPerMonth (%d) has drifted from engine.core.DailyTicksPerMonth (%d): "+
				"these must stay equal because news derives Month = Tick / dailyTicksPerMonth and "+
				"engine.core derives its month index as Tick / DailyTicksPerMonth — changing one "+
				"without the other silently diverges the news calendar from the simulation clock",
			dailyTicksPerMonth, core.DailyTicksPerMonth)
	}
}
