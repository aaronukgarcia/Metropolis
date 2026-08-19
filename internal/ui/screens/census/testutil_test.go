package census

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// mustJSON marshals v to a json.RawMessage, failing the test on error.
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

// protocolDelta builds a protocol.Delta carrying the given wirePatch for a
// subscription ID, for tests.
func protocolDelta(t *testing.T, sub protocol.SubscriptionID, p wirePatch) protocol.Delta {
	t.Helper()
	return protocol.Delta{SubscriptionID: sub, Patch: mustJSON(t, p)}
}

// renderedText returns each row of rect as a trimmed string (blank runes
// as spaces), mirroring ui.screen.services'/ui.screen.trade's
// renderedText helper.
func renderedText(buf *core.Buffer, rect core.Rect) []string {
	var lines []string
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		var sb strings.Builder
		for x := rect.X; x < rect.X+rect.W; x++ {
			c := buf.Get(x, y)
			if c.Rune == 0 {
				sb.WriteByte(' ')
			} else {
				sb.WriteRune(c.Rune)
			}
		}
		lines = append(lines, strings.TrimRight(sb.String(), " "))
	}
	return lines
}

func rowContains(rows []string, sub string) bool {
	for _, r := range rows {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

// renderCitizenBioRows renders bio into a fresh buffer via RenderCitizenBio
// and returns its rows as trimmed strings, for substring assertions.
func renderCitizenBioRows(t *testing.T, bio CitizenBio, have bool) []string {
	t.Helper()
	buf := core.NewBuffer(100, 10)
	rect := core.Rect{X: 0, Y: 0, W: 100, H: 10}
	RenderCitizenBio(buf, rect, bio, have, widgets.DefaultPalette.Style(widgets.TokenMoney))
	return renderedText(buf, rect)
}

// mustContainAll fails the test naming the first substring of wants not
// found anywhere across rows.
func mustContainAll(t *testing.T, rows []string, wants []string) {
	t.Helper()
	for _, w := range wants {
		if !rowContains(rows, w) {
			t.Errorf("rendered output missing expected substring %q", w)
		}
	}
}

func bufsEqual(a, b *core.Buffer, rect core.Rect) bool {
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		for x := rect.X; x < rect.X+rect.W; x++ {
			if a.Get(x, y) != b.Get(x, y) {
				return false
			}
		}
	}
	return true
}

// fullPatch returns a wirePatch populating every sub-surface with
// deterministic fixture data (schemaVersion 1) — the shared baseline the
// screen/regression tests apply.
func fullPatch() wirePatch {
	ageBands := [NumAgeBands]int64{1000, 2500, 2200, 1400, 600}
	sexSeries := [NumSexBuckets]int64{3800, 3900}
	eduTiers := [NumEducationTiers]int64{200, 300, 1500, 1800, 900, 700, 1800, 500}
	bwc := wireBlueWhiteCollar{Blue: 4200, White: 1800}
	kpis := []wireKPITile{
		{Key: KPIKeyGDP, Value: 125000000},
		{Key: KPIKeyHappiness, Value: 62.5},
		{Key: KPIKeyLandValue, Value: 87000000},
		{Key: KPIKeyHomeless, Value: 42},
		{Key: KPIKeyInHospital, Value: 128},
		{Key: KPIKeyOutOfWork, Value: 310},
		{Key: KPIKeyUnfilledJobs, Value: 96},
		{Key: KPIKeyJobSkillDemand, Value: 220},
	}
	kpiSources := []wireKPISource{
		{Key: KPIKeyHomeless, EntityIDs: []uint64{11, 22, 33}, LineValue: 3},
		{Key: KPIKeyOutOfWork, EntityIDs: []uint64{44, 55}, LineValue: 2},
		{Key: KPIKeyGDP, LineValue: 125000000},
	}
	bio := wireCitizenBio{
		GUID:       "citizen:42",
		ID:         42,
		BirthMonth: 100,
		Sex:        "male",
		Attainment: 750,
		Schooling:  144,
		Stages: []wireEducationStage{
			{Stage: "primary", StartMonth: 100, EndMonth: 172},
			{Stage: "university", StartMonth: 172, EndMonth: -1},
		},
		IndustryTie: "specialist-university:fintech",
		State:       "employed",
		Sector:      "tertiary",
		Workplace:   901,
		Household:   7,
		Partner:     43,
		Home:        501,
		Retirement:  900,
		Income:      45000000,
	}
	linkage := wireEducationCrimeLinkage{
		Population:         8000,
		MeanAttainment:     680.5,
		CrimeRate:          0.021,
		UneducatedFraction: 0.12,
		PolicyCoefficient:  0.35,
	}
	return wirePatch{
		SchemaVersion:         1,
		AgeBands:              &ageBands,
		SexSeries:             &sexSeries,
		EducationTiers:        &eduTiers,
		BlueWhiteCollar:       &bwc,
		KPIs:                  &kpis,
		KPISources:            &kpiSources,
		SelectedBio:           &bio,
		EducationCrimeLinkage: &linkage,
	}
}
