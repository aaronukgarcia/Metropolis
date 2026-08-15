package projections

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

func testCorrelationID() string {
	return errs.NewCorrelationID()
}

func TestHorizonConfigLoads(t *testing.T) {
	cfg, err := loadHorizonConfig(testCorrelationID())
	if err != nil {
		t.Fatalf("loadHorizonConfig: %v", err)
	}
	if cfg.BaseHorizonMonths <= 0 {
		t.Errorf("BaseHorizonMonths = %d, want a positive value", cfg.BaseHorizonMonths)
	}
	if cfg.Rationale == "" {
		t.Error("horizon.json's rationale field is empty — ASM-237's escalation must stay visible in the loaded config")
	}
}

// TestEmbeddedConfigMalformed proves loadHorizonConfig/
// loadDeathWarningConfig fail loudly (registry-sourced error, never a
// panic or a silent zero-value default) if the embedded bytes were
// ever hand-broken — exercised here by temporarily substituting
// malformed bytes and resetting the sync.Once cache, since the real
// go:embed bytes can't be corrupted at test time otherwise.
func TestEmbeddedConfigMalformed(t *testing.T) {
	origHorizon := embeddedHorizonJSON
	origDeathWarnings := embeddedDeathWarningsJSON
	t.Cleanup(func() {
		embeddedHorizonJSON = origHorizon
		embeddedDeathWarningsJSON = origDeathWarnings
		resetConfigCacheForTest()
	})

	embeddedHorizonJSON = []byte(`{ not valid json`)
	resetConfigCacheForTest()
	_, err := loadHorizonConfig(testCorrelationID())
	assertCode(t, err, ErrEmbeddedConfigInvalid)

	embeddedHorizonJSON = origHorizon
	embeddedDeathWarningsJSON = []byte(`{"version":1,"insolvency":{"warningThresholdMonths":1,"minWarningLeadMonths":1,"disclosure":""},"ghostCity":{"warningThresholdMonths":1,"minWarningLeadMonths":1,"disclosure":"x"}}`)
	resetConfigCacheForTest()
	_, err = loadDeathWarningConfig(testCorrelationID())
	assertCode(t, err, ErrEmbeddedConfigInvalid)
}

// sanity check that the real embedded bytes really are valid JSON,
// independent of loadHorizonConfig/loadDeathWarningConfig's own
// validation — catches a hand-edit mistake in the .json fixture files
// themselves with a clearer failure than the loader's wrapped error.
func TestEmbeddedJSONFilesAreWellFormed(t *testing.T) {
	var h map[string]any
	if err := json.Unmarshal(embeddedHorizonJSON, &h); err != nil {
		t.Errorf("horizon.json is not valid JSON: %v", err)
	}
	var d map[string]any
	if err := json.Unmarshal(embeddedDeathWarningsJSON, &d); err != nil {
		t.Errorf("deathwarnings.json is not valid JSON: %v", err)
	}
}
