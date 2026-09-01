package converge

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFixture_SaveLoadRoundTrip_MatchingPasses proves the fixture path
// end to end: a trajectory saved to disk and reloaded compares as a
// pass against the in-memory trajectory it was saved from — the
// "matching fixture = pass" half of the prove-can-fail pair.
func TestFixture_SaveLoadRoundTrip_MatchingPasses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "finance-fixture.json")

	ref := sampleTrajectory()
	if err := SaveFixture(path, "finance", ref); err != nil {
		t.Fatalf("SaveFixture: %v", err)
	}

	domain, loaded, err := LoadFixture(path)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if domain != "finance" {
		t.Fatalf("expected domain=finance, got %q", domain)
	}

	contract := Contract{
		"treasury": {Tier: TierExact},
		"netWorth": {Tier: TierExact},
	}
	rep := Compare("finance", ref, loaded, contract)
	if !rep.Pass {
		t.Fatalf("expected round-tripped fixture to match, got diffs: %v", rep.Diffs)
	}
}

// TestFixture_MutatedFixtureValue_Fails proves the other half: a
// fixture with one deliberately divergent value fails Compare, with the
// diff naming exactly the mutated field/tick — the gate has teeth
// against a real divergent fixture, not just a hand-wavy "loads OK".
func TestFixture_MutatedFixtureValue_Fails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "finance-fixture-divergent.json")

	ref := sampleTrajectory()
	mutated := sampleTrajectory()
	mutated[2].Values["netWorth"] = mutated[2].Values["netWorth"] + 500_000 // deliberate divergence

	if err := SaveFixture(path, "finance", mutated); err != nil {
		t.Fatalf("SaveFixture: %v", err)
	}
	domain, loaded, err := LoadFixture(path)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if domain != "finance" {
		t.Fatalf("expected domain=finance, got %q", domain)
	}

	contract := Contract{
		"treasury": {Tier: TierExact},
		"netWorth": {Tier: TierExact},
	}
	rep := Compare("finance", ref, loaded, contract)
	if rep.Pass {
		t.Fatalf("expected the mutated fixture to fail parity, got Pass=true")
	}
	found := false
	for _, d := range rep.Diffs {
		if d.Field == "netWorth" && d.Tick == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a diff naming netWorth at tick 3, got: %v", rep.Diffs)
	}
}

// TestLoadFixture_MissingFile_ReturnsRegistryError proves a missing
// fixture file fails loudly (codeFixtureLoadFailed) rather than
// returning a silently-empty Trajectory.
func TestLoadFixture_MissingFile_ReturnsRegistryError(t *testing.T) {
	_, _, err := LoadFixture(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatal("expected an error loading a missing fixture, got nil")
	}
}

// TestLoadFixture_MalformedJSON_ReturnsRegistryError proves malformed
// fixture bytes fail decode distinctly from a missing file.
func TestLoadFixture_MalformedJSON_ReturnsRegistryError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := writeFile(path, "{not valid json"); err != nil {
		t.Fatalf("test setup: %v", err)
	}
	_, _, err := LoadFixture(path)
	if err == nil {
		t.Fatal("expected an error decoding malformed fixture JSON, got nil")
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
