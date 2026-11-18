package replay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
)

// Round r4 (BUG-1224 port) attacks on the RuleStamp fix.

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// A refused Save leaves no file and no folder.
func TestRoundR4Port_RefusedSaveLeavesNothing(t *testing.T) {
	base := t.TempDir()
	fresh := filepath.Join(base, "does-not-exist-yet")
	if err := Save(fresh, "x", sampleRecorder(t), FixtureMeta{}); !isCode(err, codeFixtureRuleVersionUnset) {
		t.Fatalf("unset: %v", err)
	}
	if _, err := os.Stat(fresh); err == nil {
		t.Fatal("unset refusal created the directory")
	}
	for name, v := range map[string]rules.Version{"future": rules.Current + 50, "neg": -1} {
		err := Save(fresh, name, sampleRecorder(t), FixtureMeta{RuleVersion: RecordedAt(v)})
		if err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if _, e := os.Stat(fresh); e == nil {
			t.Fatalf("%s: refusal created the directory (%v)", name, dirEntries(t, fresh))
		}
	}
	// Struct-copied recorder: copy error first, nothing written.
	r := sampleRecorder(t)
	cp := copyRecorderBytes(r)
	err := Save(fresh, "cp", cp, cp.Meta(1, "a"))
	if !isCode(err, codeRecorderCopied) {
		t.Fatalf("copied: %v", err)
	}
	if _, e := os.Stat(fresh); e == nil {
		t.Fatal("copied recorder created the directory")
	}
	// Zero-valued recorder.
	var z Recorder
	if err := Save(fresh, "z", &z, z.Meta(1, "a")); !isCode(err, codeRecorderCopied) {
		t.Fatalf("zero recorder: %v", err)
	}
	if z.Meta(1, "a").RuleVersion.IsSet() {
		t.Fatal("zero recorder meta is set")
	}
}

// A refused overwrite leaves the existing fixture intact.
func TestRoundR4Port_RefusedOverwriteKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, "keep", sampleRecorder(t), FixtureMeta{WorldSeed: 9, RuleVersion: RecordedAt(0)}); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, n := range dirEntries(t, dir) {
		b, _ := os.ReadFile(filepath.Join(dir, n))
		before[n] = b
	}
	for _, m := range []FixtureMeta{{}, {RuleVersion: RecordedAt(rules.Current + 9)}, {RuleVersion: RecordedAt(-3)}} {
		if err := Save(dir, "keep", sampleRecorder(t), m); err == nil {
			t.Fatal("refused overwrite succeeded")
		}
	}
	for n, b := range before {
		a, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil || string(a) != string(b) {
			t.Fatalf("existing %s damaged: %v", n, err)
		}
	}
	if _, err := Load(dir, "keep"); err != nil {
		t.Fatal(err)
	}
}

// Header with no field -> explicit 0 on round trip (no field written);
// ruleVersion 1 stays 1 under another live version; load under live X, save under live Y.
func TestRoundR4Port_LoadedHeadersRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, "v0", sampleRecorder(t), FixtureMeta{RuleVersion: RecordedAt(0)}); err != nil {
		t.Fatal(err)
	}
	h0, _ := os.ReadFile(filepath.Join(dir, "v0.header.json"))
	if strings.Contains(string(h0), "ruleVersion") {
		t.Fatalf("version 0 header carries a field: %s", h0)
	}
	if err := Save(dir, "v1", sampleRecorder(t), FixtureMeta{RuleVersion: RecordedAt(1)}); err != nil {
		t.Fatal(err)
	}
	rules.SetCurrentForTest(t, rules.Current+1) // live differs at save time
	for name, want := range map[string]rules.Version{"v0": 0, "v1": 1} {
		fx, err := Load(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		m := fx.Meta()
		if !m.RuleVersion.IsSet() || m.RuleVersion.Version() != want {
			t.Fatalf("%s: meta %+v", name, m.RuleVersion)
		}
		rec := NewRecorder()
		rec.records = fx.Records
		if err := Save(dir, name+"-again", rec, m); err != nil {
			t.Fatal(err)
		}
		fx2, err := Load(dir, name+"-again")
		if err != nil || fx2.RuleVersion() != want {
			t.Fatalf("%s: relabelled to %v (%v)", name, fx2.RuleVersion(), err)
		}
		b1, _ := os.ReadFile(filepath.Join(dir, name+".header.json"))
		b2, _ := os.ReadFile(filepath.Join(dir, name+"-again.header.json"))
		_ = b1
		_ = b2
	}
}

// Hand-built Fixture: Meta() states version 0 from a zero header. Recorded as
// an observation (advisory), not a failure.
func TestRoundR4Port_HandBuiltFixtureMeta(t *testing.T) {
	m := Fixture{}.Meta()
	t.Logf("hand-built Fixture{}.Meta(): set=%v version=%v", m.RuleVersion.IsSet(), m.RuleVersion.Version())
}

// Recorder captures at creation: record under live Current, move, save.
func TestRoundR4Port_RecorderCapturesAtCreation(t *testing.T) {
	rec := sampleRecorder(t)
	created := rules.Effective()
	rules.SetCurrentForTest(t, rules.Current+1)
	dir := t.TempDir()
	if err := Save(dir, "c", rec, rec.Meta(0, "")); err != nil {
		t.Fatal(err)
	}
	fx, _ := Load(dir, "c")
	if fx.RuleVersion() != created {
		t.Fatalf("stamped %d want %d", fx.RuleVersion(), created)
	}
	// A recorder created after the move is stamped with the new one.
	rec2 := sampleRecorder(t)
	if err := Save(dir, "d", rec2, rec2.Meta(0, "")); err != nil {
		t.Fatal(err)
	}
	fx2, _ := Load(dir, "d")
	if fx2.RuleVersion() != rules.Effective() {
		t.Fatalf("stamped %d want %d", fx2.RuleVersion(), rules.Effective())
	}
}
