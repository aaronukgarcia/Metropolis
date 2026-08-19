package census

// Independent-destructive addition (GR#23): doc.go labels BLOCKED-1
// (personality/leisure-taste) and BLOCKED-2 (finer sector/skill
// breakdown) as engine gaps verified against the live engine.census
// source (not paraphrased from the spec), per the same
// districts/services tripwire_test.go convention
// (internal/ui/screens/services/tripwire_test.go, PR #46's F8 round). Each
// tripwire fails loudly the moment the underlying engine gap closes,
// forcing a human back to doc.go and this package's Scope section instead
// of the gap silently staying unbuilt forever once the source lands.
//
// BLOCKED-3 (non-citizen bio completeness) is NOT given a tripwire here —
// it is feat.citycensus.md's own already-open escalation (ES-3), with no
// stable, already-agreed detection point to probe (mirrors
// ui.screen.services' SVC-3, which also gets no tripwire for the same
// reason: its blocker, ui.screen.map's AC-3, has no exported symbol or
// code.json edge this test could reliably name in advance without
// guessing).

import (
	"os"
	"regexp"
	"testing"
)

// engineCensusSourceGlobPath is this package's relative path to
// engine.census's own source directory. Path arithmetic: this package
// lives at internal/ui/screens/census/, so three levels up plus
// engine/census ("../../../engine/census") is internal/engine/census —
// verified the same way ui.screen.services' tripwire verified its own
// code.json path arithmetic, against this package's own known location.
const engineCensusSourceDir = "../../../engine/census"

// readEngineCensusSource concatenates every non-test .go file in
// engine.census's source directory, for a single grep-equivalent scan.
func readEngineCensusSource(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(engineCensusSourceDir)
	if err != nil {
		t.Fatalf("could not read %s (path arithmetic wrong, or engine.census moved -- fix this test's path, do not delete it): %v", engineCensusSourceDir, err)
	}
	var all []byte
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || len(name) < 3 || name[len(name)-3:] != ".go" {
			continue
		}
		b, err := os.ReadFile(engineCensusSourceDir + "/" + name)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		all = append(all, b...)
		all = append(all, '\n')
	}
	if len(all) == 0 {
		t.Fatalf("no .go files found under %s -- engine.census source tree is empty or path arithmetic is wrong", engineCensusSourceDir)
	}
	return string(all)
}

// TestTripwire_PersonalityLeisureTasteStillAbsent is BLOCKED-1's
// mechanical tripwire (doc.go's own grep check: `grep -c
// "Personality\|LeisureTaste" internal/engine/census/*.go` must return 0).
func TestTripwire_PersonalityLeisureTasteStillAbsent(t *testing.T) {
	src := readEngineCensusSource(t)
	re := regexp.MustCompile(`Personality|LeisureTaste`)
	if re.MatchString(src) {
		t.Fatal(
			"TRIPWIRE FIRED: engine.census now carries a Personality or " +
				"LeisureTaste field -- BLOCKED-1 (§13-F6's personality & " +
				"leisure-taste distribution) is no longer correctly blocked. " +
				"docs/planning/acceptance/ui.screen.census.md's BLOCKED-1 and " +
				"this package's doc.go must be revisited to build a real pane " +
				"against the new field instead of leaving this gap unbuilt.",
		)
	}
	// Expected today: no personality/leisure-taste field exists anywhere
	// in engine.census (confirmed at dispatch by reading
	// internal/engine/census/sources.go's CitizenView directly).
	// BLOCKED-1 remains correctly blocked.
}

// TestTripwire_SectorSkillSeriesStillAbsent is BLOCKED-2's mechanical
// tripwire (doc.go's own grep check: `grep -c "func (c \*CensusAPI)
// SectorSeries\|func (c \*CensusAPI) SkillSeries"
// internal/engine/census/*.go` must return 0).
func TestTripwire_SectorSkillSeriesStillAbsent(t *testing.T) {
	src := readEngineCensusSource(t)
	re := regexp.MustCompile(`func \(c \*CensusAPI\) SectorSeries|func \(c \*CensusAPI\) SkillSeries`)
	if re.MatchString(src) {
		t.Fatal(
			"TRIPWIRE FIRED: engine.census now exposes a SectorSeries or " +
				"SkillSeries accessor -- BLOCKED-2 (the finer sector/skill " +
				"workforce breakdown) is no longer correctly blocked. " +
				"docs/planning/acceptance/ui.screen.census.md's BLOCKED-2 and " +
				"this package's doc.go/RenderBlueWhiteCollar must be revisited " +
				"to build the finer breakdown against the new accessor instead " +
				"of only the 2-way blue/white split.",
		)
	}
	// Expected today: only CensusAPI.BlueWhiteCollar exists (confirmed at
	// dispatch by reading internal/engine/census/demographics.go
	// directly). BLOCKED-2 remains correctly blocked.
}
