package defence

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// validConfig returns a small, schema-valid Config mirroring data/defence.json
// (three thresholds, ≥2 choices each, a facility table covering every
// referenced type). Counts are deliberately tiny so tests run fast; the
// magnitudes are placeholders (GR#15's balance regime).
func validConfig() Config {
	return Config{
		Version: 1,
		GrantPots: []GrantPotConfig{
			{ID: "transport", Name: "Transport", BaseWinProbability: 0.30, MatchFundingWeight: 0.40, PlanningQualityWeight: 0.30, MaxMatchMicropounds: 1_000_000_000, AwardMicropounds: 500_000_000},
		},
		FormulaSupport: FormulaSupportConfig{TaxCapacityThresholdMicropounds: 10_000_000_000, FormulaAmountMicropounds: 200_000_000},
		Mandates: []MandateConfig{
			{
				ID: "naval-100k", PopulationThreshold: 100_000, FacilityType: "naval",
				CompensationMicropounds: 4_000_000_000,
				Choices: []MandateChoiceConfig{
					{ID: "naval-base", FacilityType: "naval", Description: "full naval base"},
					{ID: "naval-patrol-berth", FacilityType: "naval-patrol", Description: "patrol berth"},
				},
			},
			{
				ID: "army-500k", PopulationThreshold: 500_000, FacilityType: "army",
				CompensationMicropounds: 6_000_000_000,
				Choices: []MandateChoiceConfig{
					{ID: "infantry-garrison", FacilityType: "army", Description: "garrison"},
					{ID: "armoured-regiment", FacilityType: "army-armoured", Description: "regiment"},
				},
			},
			{
				ID: "airdefence-1m", PopulationThreshold: 1_000_000, FacilityType: "air",
				CompensationMicropounds: 8_000_000_000,
				Choices: []MandateChoiceConfig{
					{ID: "radar-station", FacilityType: "air-radar", Description: "radar"},
					{ID: "fast-jet-station", FacilityType: "air", Description: "fast jet"},
				},
			},
		},
		Facilities: map[string]FacilityConfig{
			"naval":         {BuildZone: "heavy_industry", PayrollMicropounds: 1_000_000, PayrollFloorMicropounds: 1_000_000, PersonnelCount: 6, MarriedQuarters: 2, ChildrenPerQuarter: 1, ProcurementMicropounds: 100_000},
			"naval-patrol":  {BuildZone: "heavy_industry", PayrollMicropounds: 500_000, PayrollFloorMicropounds: 500_000, PersonnelCount: 4, MarriedQuarters: 0, ChildrenPerQuarter: 0, ProcurementMicropounds: 50_000},
			"army":          {BuildZone: "heavy_industry", PayrollMicropounds: 2_000_000, PayrollFloorMicropounds: 2_000_000, PersonnelCount: 8, MarriedQuarters: 2, ChildrenPerQuarter: 1, ProcurementMicropounds: 80_000},
			"army-armoured": {BuildZone: "heavy_industry", PayrollMicropounds: 2_500_000, PayrollFloorMicropounds: 2_500_000, PersonnelCount: 8, MarriedQuarters: 2, ChildrenPerQuarter: 1, ProcurementMicropounds: 90_000},
			"air":           {BuildZone: "heavy_industry", PayrollMicropounds: 1_500_000, PayrollFloorMicropounds: 1_500_000, PersonnelCount: 6, MarriedQuarters: 2, ChildrenPerQuarter: 1, ProcurementMicropounds: 70_000},
			"air-radar":     {BuildZone: "heavy_industry", PayrollMicropounds: 800_000, PayrollFloorMicropounds: 800_000, PersonnelCount: 4, MarriedQuarters: 1, ChildrenPerQuarter: 1, ProcurementMicropounds: 40_000},
		},
		Reputation: ReputationConfig{RefusalPenaltyPoints: 50},
	}
}

// newDefence builds a DefenceAPI over cfg with the given world seed (unwired).
func newDefence(t *testing.T, cfg Config, seed uint64) *DefenceAPI {
	t.Helper()
	d, err := New(cfg, seed, "corr-defence")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

// newBuild constructs a fully-submittable BuildAPI: the minimal §34 zone
// catalogue, an owned world tile, and the real season — everything
// SubmitBuildCommand needs to accept a build order.
func newBuild(t *testing.T) *build.BuildAPI {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "buildings.json"), []byte(buildingsFixtureJSON()), 0o644); err != nil {
		t.Fatalf("write buildings fixture: %v", err)
	}
	b, err := build.Load(dir, "corr-defence")
	if err != nil {
		t.Fatalf("build.Load: %v", err)
	}
	wapi := world.NewWorldAPI(world.TileCoord{X: 0, Y: 0})
	res := wapi.PurchaseTile(world.PurchaseCommand{CorrelationID: "corr-defence", Tile: world.TileCoord{X: 0, Y: 0}, BuyerID: 1})
	if !res.Accepted {
		t.Fatalf("PurchaseTile: %v", res.Error)
	}
	if err := b.SetWorld(wapi); err != nil {
		t.Fatalf("SetWorld: %v", err)
	}
	s, err := season.LoadDefault("corr-defence")
	if err != nil {
		t.Fatalf("season.LoadDefault: %v", err)
	}
	if err := b.SetSeason(s); err != nil {
		t.Fatalf("SetSeason: %v", err)
	}
	return b
}

// newGrantDefence builds a DefenceAPI with only finance wired — enough for
// the grant-bid path (a winning bid posts its award through finance).
func newGrantDefence(t *testing.T, seed uint64) (*DefenceAPI, *finance.FinanceAPI) {
	t.Helper()
	d := newDefence(t, validConfig(), seed)
	f := finance.NewFinanceAPI("corr-defence")
	if err := d.SetFinance(f); err != nil {
		t.Fatalf("SetFinance: %v", err)
	}
	return d, f
}

// newWiredDefence builds a fully-wired DefenceAPI (build + finance + citizens)
// over a fresh build fixture, ready to accept a mandate.
func newWiredDefence(t *testing.T, seed uint64) (*DefenceAPI, *finance.FinanceAPI, *citizens.CitizensAPI) {
	t.Helper()
	d := newDefence(t, validConfig(), seed)
	f := finance.NewFinanceAPI("corr-defence")
	c, err := citizens.NewCitizensAPI(seed, "corr-defence")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	if err := d.SetFinance(f); err != nil {
		t.Fatalf("SetFinance: %v", err)
	}
	if err := d.SetCitizens(c); err != nil {
		t.Fatalf("SetCitizens: %v", err)
	}
	if err := d.SetBuild(newBuild(t)); err != nil {
		t.Fatalf("SetBuild: %v", err)
	}
	return d, f, c
}

// buildingsFixtureJSON is the minimal §34 zone catalogue build.Load needs
// (mirrors engine.attract's own fixture — the eight required zone types).
func buildingsFixtureJSON() string {
	type z struct{ id, name string }
	zones := []z{
		{"dwelling", "Dwelling"},
		{"shop", "Shop"},
		{"office", "Office"},
		{"entertainment", "Entertainment"},
		{"farming", "Farming"},
		{"manufacturing", "Manufacturing"},
		{"heavy_industry", "Heavy Industry"},
		{"mining", "Mining"},
	}
	var sb strings.Builder
	sb.WriteString(`{"version":1,"meta":{"labourPerTick":1},"zones":[`)
	for i, z := range zones {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"id":%q,"name":%q,"materialsBill":{"constructionMaterials":100},"labour":10,"baseLeadTimeDays":1}`, z.id, z.name)
	}
	sb.WriteString(`],"entries":[]}`)
	return sb.String()
}

// isErr asserts err is a registry-sourced error with the given code (GR#7 —
// the code itself, not merely a matching test-function name, per BUG-100).
func isErr(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected registry error %s, got nil", code)
	}
	var e *errs.E
	if !errors.As(err, &e) {
		t.Fatalf("expected *errs.E, got %T: %v", err, err)
	}
	if e.Code != code {
		t.Fatalf("expected error code %s, got %s", code, e.Code)
	}
}

// hasMandate reports whether a mandate id is present in a PendingMandates
// slice (used to assert the correct NAMED mandate fires, not "some" mandate).
func hasMandate(ms []Mandate, id string) bool {
	for _, m := range ms {
		if m.ID == id {
			return true
		}
	}
	return false
}
