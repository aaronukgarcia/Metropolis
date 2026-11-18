package replay

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
)

// Round r3 (BUG-1224 port) attacks on the unstated-version stamp.

// A load-then-save round trip of a version-0 fixture, copying the loaded
// version into the meta the natural way, must keep the label 0.
func TestRoundR3Port_RoundTripKeepsVersionZero(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, "v0", sampleRecorder(t), FixtureMeta{WorldSeed: 3, AppVersion: "r3", RuleVersion: RecordedAt(0)}); err != nil {
		t.Fatal(err)
	}
	fx, err := Load(dir, "v0")
	if err != nil {
		t.Fatal(err)
	}
	if fx.RuleVersion() != 0 {
		t.Fatalf("setup: %d", fx.RuleVersion())
	}
	rec := NewRecorder()
	rec.records = fx.Records
	meta := fx.Meta()
	if err := Save(dir, "v0again", rec, meta); err != nil {
		t.Fatal(err)
	}
	fx2, err := Load(dir, "v0again")
	if err != nil {
		t.Fatal(err)
	}
	if fx2.RuleVersion() != 0 {
		t.Fatalf("round trip relabelled a version-0 fixture as %d", fx2.RuleVersion())
	}
}

// Stamped at save time, not record time: record, move live, save.
func TestRoundR3Port_StampIsSaveTimeNotRecordTime(t *testing.T) {
	rec := sampleRecorder(t) // "recorded" under live Current
	rules.SetCurrentForTest(t, rules.Current+1)
	dir := t.TempDir()
	if err := Save(dir, "late", rec, rec.Meta(0, "")); err != nil {
		t.Fatal(err)
	}
	fx, _ := Load(dir, "late")
	t.Logf("recorded under %d, saved under %d, stamped %d", rules.Current, rules.Effective(), fx.RuleVersion())
	if fx.RuleVersion() != rules.Current {
		t.Fatalf("stamped %d, but the commands were recorded and judged under %d", fx.RuleVersion(), rules.Current)
	}
}
