package news

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestSalience_HigherMagnitudeRanksAboveLower is AC-2: two events in the
// SAME category with different magnitudes — the higher-magnitude event
// must rank above the lower one in the bulletin's ordering. This is the
// check a chronological- or insertion-order implementation cannot pass:
// the higher-magnitude event is injected SECOND, so ordering by arrival
// would put it last.
func TestSalience_HigherMagnitudeRanksAboveLower(t *testing.T) {
	cfg := testConfig()
	events := []Event{
		{ID: "d-low", Tick: 0, Category: CategoryDeath, Magnitude: 2, Text: "2 deaths"},
		{ID: "d-high", Tick: 1, Category: CategoryDeath, Magnitude: 9, Text: "9 deaths"},
	}
	stories := buildBulletin(recordsOf(events), cfg)
	if len(stories) != 2 {
		t.Fatalf("got %d stories, want 2", len(stories))
	}
	if got := stories[0].EventID; got != "d-high" {
		t.Errorf("higher-magnitude event must rank above lower: rank 1 = %q, want d-high", got)
	}
	if got := stories[1].EventID; got != "d-low" {
		t.Errorf("lower-magnitude event must rank below: rank 2 = %q, want d-low", got)
	}
	if stories[0].Salience <= stories[1].Salience {
		t.Errorf("rank-1 salience (%v) must exceed rank-2 salience (%v)", stories[0].Salience, stories[1].Salience)
	}
	if stories[0].Rank != 1 || stories[1].Rank != 2 {
		t.Errorf("ranks = %d,%d, want 1,2", stories[0].Rank, stories[1].Rank)
	}
}

// TestSalience_BulletinCapsAtFive is the §29.2 "3–5 ranked stories"
// ceiling: with more than five events, exactly five are selected.
func TestSalience_BulletinCapsAtFive(t *testing.T) {
	cfg := testConfig()
	events := make([]Event, 0, 8)
	for i := 0; i < 8; i++ {
		events = append(events, Event{
			ID:        string(rune('a' + i)),
			Tick:      int64(i),
			Category:  CategoryRecord,
			Magnitude: int64(i + 1),
			Text:      "story",
		})
	}
	stories := buildBulletin(recordsOf(events), cfg)
	if len(stories) != maxBulletinStories {
		t.Errorf("bulletin selected %d stories, want %d", len(stories), maxBulletinStories)
	}
}

// TestDeterminism_TiedSalienceStableOrder is AC-10: two events with a
// deliberately TIED salience score must order identically across repeated
// runs (and worker counts), tie-broken by EventID ascending. The tied case
// is what a map-iteration-order nondeterminism would hide in; distinct
// scores never exercise the tie-break.
func TestDeterminism_TiedSalienceStableOrder(t *testing.T) {
	cfg := testConfig()
	// Same category AND same magnitude => identical salience, a forced tie.
	events := []Event{
		{ID: "tie-z", Tick: 2, Category: CategoryDeath, Magnitude: 5, Text: "5 deaths"},
		{ID: "tie-a", Tick: 0, Category: CategoryDeath, Magnitude: 5, Text: "5 deaths"},
		{ID: "tie-m", Tick: 1, Category: CategoryDeath, Magnitude: 5, Text: "5 deaths"},
	}

	first := buildBulletin(recordsOf(events), cfg)
	want := make([]string, len(first))
	for i, s := range first {
		want[i] = s.EventID
	}
	// Tie-break must be EventID ascending: tie-a, tie-m, tie-z.
	if want[0] != "tie-a" || want[1] != "tie-m" || want[2] != "tie-z" {
		t.Fatalf("tie-break order = %v, want [tie-a tie-m tie-z] (EventID ascending)", want)
	}

	// Repeat many times; the order must be byte-identical every run.
	for run := 0; run < 100; run++ {
		got := buildBulletin(recordsOf(events), cfg)
		for i := range want {
			if got[i].EventID != want[i] {
				t.Fatalf("run %d: order = %v, want %v (nondeterministic tie-break)", run, eventIDs(got), want)
			}
		}
	}
}

func eventIDs(stories []BulletinStory) []string {
	out := make([]string, len(stories))
	for i, s := range stories {
		out[i] = s.EventID
	}
	return out
}

// TestSalienceData_LoadsAllFiveCategories is GR#15: the weight table comes
// from the embedded salience.json, and every §29 category must be present
// with a positive weight.
func TestSalienceData_LoadsAllFiveCategories(t *testing.T) {
	weights, err := loadSalienceWeights("salience-data-correlation")
	if err != nil {
		t.Fatalf("loadSalienceWeights: %v", err)
	}
	for _, c := range allCategories {
		w, ok := weights[c]
		if !ok {
			t.Errorf("category %q has no weight in salience.json", c)
		}
		if w <= 0 {
			t.Errorf("category %q weight = %v, want positive", c, w)
		}
	}
}

// TestSalienceData_CarriesDisclosure pins the GR#15 disclosure that the
// weights are placeholders pending the balance pass — a future edit that
// drops the disclosure (and claims the numbers are final) fails here.
func TestSalienceData_CarriesDisclosure(t *testing.T) {
	var sf salienceFile
	if err := json.Unmarshal(embeddedSalienceJSON, &sf); err != nil {
		t.Fatalf("unmarshal salience.json: %v", err)
	}
	if sf.Disclosure == "" {
		t.Error("salience.json must carry a non-empty pending-tuning disclosure (GR#15)")
	}
}

// TestNoWallClockUsage is AC-11 (self-enforcing): no non-test source in
// this package references the wall clock. Generation is a function of the
// event log and month/tick index only.
func TestNoWallClockUsage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", name, err)
		}
		if strings.Contains(string(b), "time.Now") || strings.Contains(string(b), "time.Since") {
			t.Errorf("%s references the wall clock (AC-11: news generation must be a function of the sim log and tick/month only)", name)
		}
	}
}
