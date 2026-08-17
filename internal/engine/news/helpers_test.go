package news

import "fmt"

// fakeNamer is a test [RoadNamer]: a fixed ID→name map plus an optional
// forced error. An ID absent from the map resolves to an "unknown road"
// error, which is what the AC-8 paths exercise.
type fakeNamer struct {
	names map[string]string
	err   error
}

func (f fakeNamer) RoadName(id string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if n, ok := f.names[id]; ok {
		return n, nil
	}
	return "", fmt.Errorf("unknown road %q", id)
}

// fakeRewriter is a test [ProseRewriter] driven by a closure, so a test
// can script an altered-fact response, an error, or a verbatim one.
type fakeRewriter struct {
	fn func(Story) (string, error)
}

func (f fakeRewriter) Rewrite(in Story) (string, error) { return f.fn(in) }

// testWeights is an explicit, self-contained salience weight table for
// tests that must not depend on the embedded salience.json's exact values
// (a balance edit to that file must not silently change what these tests
// assert — the ranking tests only assert the weight×magnitude *shape*, so
// they pin their own weights).
func testWeights() map[Category]float64 {
	return map[Category]float64{
		CategoryDeath:     3.0,
		CategoryFirst:     2.0,
		CategoryRecord:    1.5,
		CategoryCrisis:    2.5,
		CategoryMilestone: 2.0,
	}
}

// testConfig is a Config with explicit weights and no namer.
func testConfig() Config { return Config{Weights: testWeights()} }

// recordsOf wraps events into record values for driving the pure generation
// functions directly in tests. Each event's name is left empty (the events
// these tests use carry no named entity); a test that needs a resolved name
// exercises it through the real Ingest path instead.
func recordsOf(events []Event) []record {
	out := make([]record, len(events))
	for i, ev := range events {
		out[i] = record{ev: ev}
	}
	return out
}

// numberValue returns the value of the named "year in numbers" figure, or
// fails the test if the label is absent.
func numberValue(t interface{ Fatalf(string, ...any) }, nums []AnnualNumber, label string) int64 {
	for _, n := range nums {
		if n.Label == label {
			return n.Value
		}
	}
	t.Fatalf("annual number %q not found in %+v", label, nums)
	return 0
}

// hasMilestone reports whether the epilogue states the given milestone
// event (by EventID).
func hasMilestone(ep EpilogueReport, eventID string) bool {
	for _, m := range ep.Milestones {
		if m.EventID == eventID {
			return true
		}
	}
	return false
}
