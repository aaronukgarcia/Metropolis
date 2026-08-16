package education

import "testing"

// TestLoadFromDataFile proves the data/education.json path loads and its
// spec-stated gates land (GR#15: the balance file is the source of the age
// gates, not a Go literal).
func TestLoadFromDataFile(t *testing.T) {
	a, err := LoadDefault("test")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if got := a.cfg.EntryAgeMonths[StagePrimary]; got != 60 {
		t.Fatalf("primary entry age = %d, want 60 (5 years, §27)", got)
	}
	if got := a.cfg.EntryAgeMonths[StageSecondary]; got != 132 {
		t.Fatalf("secondary entry age = %d, want 132 (11 years, §27)", got)
	}
	// The fork gate pins leave-at-16 at 16 years (§27).
	if got := a.cfg.EntryAgeMonths[StageSixthForm]; got != 192 {
		t.Fatalf("fork entry age = %d, want 192 (16 years)", got)
	}
}
