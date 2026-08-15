package spiral

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestGhostCityConfigDriftsAgainstProjections is the weakness-pattern-#2
// drift guard: this package's ghost-city figures (spiral.json's ghostCity
// block) duplicate engine.projections' embedded deathwarnings.json ghostCity
// entry — engine.projections owns the warning-side observation of the
// threshold and this package owns the death-condition gate that consumes it,
// but the two packages are decoupled (GR#20) so the value legitimately
// exists in both places. Silent divergence is the forbidden outcome: if a
// future balance pass tunes one without the other, this test must fail with
// a message explaining the duplication.
func TestGhostCityConfigDriftsAgainstProjections(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	projFile := filepath.Join(filepath.Dir(thisFile), "..", "projections", "deathwarnings.json")
	raw, err := os.ReadFile(projFile)
	if err != nil {
		t.Fatalf("read %s: %v", projFile, err)
	}
	var projCfg struct {
		GhostCity struct {
			WarningThresholdMonths float64 `json:"warningThresholdMonths"`
			MinWarningLeadMonths   float64 `json:"minWarningLeadMonths"`
		} `json:"ghostCity"`
	}
	if err := json.Unmarshal(raw, &projCfg); err != nil {
		t.Fatalf("unmarshal %s: %v", projFile, err)
	}

	cfg, err := loadConfig(testCorrelationID())
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.GhostCity.MinWarningLeadMonths != projCfg.GhostCity.MinWarningLeadMonths {
		t.Errorf("minWarningLeadMonths drifted: spiral.json=%v, projections deathwarnings.json=%v — "+
			"the ghost-city warning lead time is duplicated across the decoupled packages (GR#20), "+
			"so changing one requires changing the other (weakness pattern #2)",
			cfg.GhostCity.MinWarningLeadMonths, projCfg.GhostCity.MinWarningLeadMonths)
	}
	if cfg.GhostCity.WarningThresholdMonths != projCfg.GhostCity.WarningThresholdMonths {
		t.Errorf("warningThresholdMonths drifted: spiral.json=%v, projections deathwarnings.json=%v — "+
			"the ghost-city warning threshold is duplicated across the decoupled packages (GR#20), "+
			"so changing one requires changing the other (weakness pattern #2)",
			cfg.GhostCity.WarningThresholdMonths, projCfg.GhostCity.WarningThresholdMonths)
	}
}
