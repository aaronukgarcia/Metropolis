package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BUG-1250: a rule fixture is a persist directory whose tenant and city
// folders are 64-character hashes, which made one committed path 206
// characters long and broke checkouts on Windows. In the repository the two
// hashed folders are stored under the short names below, with names.txt
// recording the real ones; every reader first materialises the real layout in
// a temp dir (materializeRuleFixture) and every generator writes the short
// layout (writeShortRuleFixture).
const (
	fixtureTenantShort = "tenant"
	fixtureCityShort   = "city"
	fixtureNamesFile   = "names.txt"
)

// materializeRuleFixture copies the short-layout fixture at src into a fresh
// temp dir under its real hashed folder names and returns that dir, ready for
// persist.NewDiskStore and CheckRuleFixture.
func materializeRuleFixture(t *testing.T, src string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(src, fixtureNamesFile))
	if err != nil {
		t.Fatalf("fixture %s: %v", src, err)
	}
	var tenant, city string
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "tenant":
			tenant = v
		case "city":
			city = v
		}
	}
	if tenant == "" || city == "" {
		t.Fatalf("fixture %s: %s does not name tenant and city", src, fixtureNamesFile)
	}
	dst := t.TempDir()
	realCity := filepath.Join(dst, tenant, city)
	if err := os.MkdirAll(realCity, 0o755); err != nil {
		t.Fatal(err)
	}
	copyDir(t, filepath.Join(src, fixtureTenantShort, fixtureCityShort), realCity)
	return dst
}

// writeShortRuleFixture replaces the short-layout fixture at dstShort with
// the contents of the real persist directory realDir (exactly one tenant
// folder holding exactly one city folder).
func writeShortRuleFixture(t *testing.T, realDir, dstShort string) {
	t.Helper()
	tenants, err := os.ReadDir(realDir)
	if err != nil || len(tenants) != 1 || !tenants[0].IsDir() {
		t.Fatalf("generated fixture %s: want exactly one tenant folder (%v)", realDir, err)
	}
	cities, err := os.ReadDir(filepath.Join(realDir, tenants[0].Name()))
	if err != nil || len(cities) != 1 || !cities[0].IsDir() {
		t.Fatalf("generated fixture %s: want exactly one city folder (%v)", realDir, err)
	}
	if err := os.RemoveAll(dstShort); err != nil {
		t.Fatal(err)
	}
	short := filepath.Join(dstShort, fixtureTenantShort, fixtureCityShort)
	if err := os.MkdirAll(short, 0o755); err != nil {
		t.Fatal(err)
	}
	copyDir(t, filepath.Join(realDir, tenants[0].Name(), cities[0].Name()), short)
	names := "tenant=" + tenants[0].Name() + "\ncity=" + cities[0].Name() + "\n"
	if err := os.WriteFile(filepath.Join(dstShort, fixtureNamesFile), []byte(names), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFixtureLayout_Materialize proves the short layout restores the real
// one: a short fixture built from a real directory materialises back to the
// same file bytes under the same hashed names.
func TestFixtureLayout_Materialize(t *testing.T) {
	real := filepath.Join(t.TempDir(), "r")
	rc := filepath.Join(real, strings.Repeat("a", 64), strings.Repeat("b", 64), "snapshots")
	if err := os.MkdirAll(rc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rc, "x.bin"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	short := filepath.Join(t.TempDir(), "s")
	writeShortRuleFixture(t, real, short)
	got := materializeRuleFixture(t, short)
	b, err := os.ReadFile(filepath.Join(got, strings.Repeat("a", 64), strings.Repeat("b", 64), "snapshots", "x.bin"))
	if err != nil || string(b) != "payload" {
		t.Fatalf("materialised fixture lost the file: %q %v", b, err)
	}
}

// TestFixtureLayout_NoLongTestdataPaths fails if any file under a testdata
// folder of the module is committed at a path longer than 150 characters
// (relative to the module root): such a path breaks Windows checkouts.
func TestFixtureLayout_NoLongTestdataPaths(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	const limit = 150
	longest := 0
	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "temp", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if !strings.Contains("/"+rel, "/testdata/") {
			return nil
		}
		if len(rel) > longest {
			longest = len(rel)
		}
		if len(rel) > limit {
			t.Errorf("testdata path is %d characters (limit %d): %s", len(rel), limit, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("longest testdata path: %d characters", longest)
}
