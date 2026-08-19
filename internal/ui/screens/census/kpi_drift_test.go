package census

// TestKPIKeys_MatchEngineConstants proves this package's KPIKey* literal
// copies (types.go) stay byte-identical to engine.census's own exported
// KPIKey* constants (internal/engine/census/demographics.go) — a drift
// test, not a runtime dependency: this file imports internal/engine/census
// under README.md's sanctioned _test.go depguard carve-out (fixtures/
// assertions only, never production wiring), mirroring
// ui.screen.services'/ui.screen.menu's cross-screen literal drift tests
// (drill_map_test.go).

import (
	"testing"

	engcensus "github.com/aaronukgarcia/Metropolis/internal/engine/census"
)

func TestKPIKeys_MatchEngineConstants(t *testing.T) {
	cases := []struct {
		local  string
		engine string
		name   string
	}{
		{KPIKeyGDP, engcensus.KPIKeyGDP, "KPIKeyGDP"},
		{KPIKeyHappiness, engcensus.KPIKeyHappiness, "KPIKeyHappiness"},
		{KPIKeyLandValue, engcensus.KPIKeyLandValue, "KPIKeyLandValue"},
		{KPIKeyHomeless, engcensus.KPIKeyHomeless, "KPIKeyHomeless"},
		{KPIKeyInHospital, engcensus.KPIKeyInHospital, "KPIKeyInHospital"},
		{KPIKeyOutOfWork, engcensus.KPIKeyOutOfWork, "KPIKeyOutOfWork"},
		{KPIKeyUnfilledJobs, engcensus.KPIKeyUnfilledJobs, "KPIKeyUnfilledJobs"},
		{KPIKeyJobSkillDemand, engcensus.KPIKeyJobSkillDemand, "KPIKeyJobSkillDemand"},
	}
	for _, c := range cases {
		if c.local != c.engine {
			t.Errorf("%s: this package's literal %q != engine.census's %q -- schema drift, update types.go", c.name, c.local, c.engine)
		}
	}
}
