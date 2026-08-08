package errs

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRegistry(t *testing.T, dir, content string) string {
	t.Helper()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dataDir, "errors.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	return path
}

const validRegistry = `{
  "version": 1,
  "codes": {
    "MET-F900": {
      "severity": "error",
      "module": "foundation.errors",
      "message": "test message {thing}",
      "remedy": "test remedy"
    }
  }
}`

func TestLoadRegistry_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := writeRegistry(t, dir, validRegistry)
	t.Setenv(registryPathEnv, path)
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	entries, err := loadRegistry()
	if err != nil {
		t.Fatalf("loadRegistry: %v", err)
	}
	e, ok := entries["MET-F900"]
	if !ok {
		t.Fatalf("expected MET-F900 to be present")
	}
	if e.Module != "foundation.errors" {
		t.Errorf("module = %q", e.Module)
	}
}

func TestLoadRegistry_MissingFile(t *testing.T) {
	t.Setenv(registryPathEnv, filepath.Join(t.TempDir(), "nope.json"))
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	if _, err := loadRegistry(); err == nil {
		t.Fatal("expected error for missing registry file")
	}
}

func TestLoadRegistry_InvalidCodeFormat(t *testing.T) {
	dir := t.TempDir()
	path := writeRegistry(t, dir, `{"version":1,"codes":{"BAD-CODE":{"severity":"error","module":"m","message":"m","remedy":"r"}}}`)
	t.Setenv(registryPathEnv, path)
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	if _, err := loadRegistry(); err == nil {
		t.Fatal("expected error for invalid code format")
	}
}

func TestLoadRegistry_MissingRequiredField(t *testing.T) {
	dir := t.TempDir()
	path := writeRegistry(t, dir, `{"version":1,"codes":{"MET-F901":{"severity":"error","module":"m","message":"m","remedy":""}}}`)
	t.Setenv(registryPathEnv, path)
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	if _, err := loadRegistry(); err == nil {
		t.Fatal("expected error for missing remedy field")
	}
}

func TestLoadRegistry_DuplicateCodeDetected(t *testing.T) {
	dir := t.TempDir()
	// Hand-build JSON with a literal duplicate key — Go's json package
	// would otherwise silently collapse this to "last value wins" via a
	// plain map unmarshal, which is exactly what decodeCodes must catch.
	content := `{"version":1,"codes":{
		"MET-F902":{"severity":"error","module":"m","message":"first","remedy":"r"},
		"MET-F902":{"severity":"error","module":"m","message":"second","remedy":"r"}
	}}`
	path := writeRegistry(t, dir, content)
	t.Setenv(registryPathEnv, path)
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	if _, err := loadRegistry(); err == nil {
		t.Fatal("expected error for duplicate code")
	}
}

func TestLoadRegistry_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := writeRegistry(t, dir, `{not valid json`)
	t.Setenv(registryPathEnv, path)
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	if _, err := loadRegistry(); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestLoadRegistry_CachedAfterFirstLoad(t *testing.T) {
	dir := t.TempDir()
	path := writeRegistry(t, dir, validRegistry)
	t.Setenv(registryPathEnv, path)
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	entries1, err := loadRegistry()
	if err != nil {
		t.Fatalf("loadRegistry: %v", err)
	}
	// Overwrite the file; cached load should NOT see this change.
	if err := os.WriteFile(path, []byte(`{"version":1,"codes":{}}`), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	entries2, err := loadRegistry()
	if err != nil {
		t.Fatalf("loadRegistry (cached): %v", err)
	}
	if len(entries1) != len(entries2) {
		t.Errorf("expected cached registry to be unchanged, got %d vs %d entries", len(entries1), len(entries2))
	}
}

func TestLoadRegistry_RealDataFile(t *testing.T) {
	// No env override: exercises the executable/CWD upward-search path
	// against the real repo data/errors.json.
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	entries, err := loadRegistry()
	if err != nil {
		t.Fatalf("loadRegistry against real data/errors.json: %v", err)
	}
	for _, want := range []string{"MET-F001", "MET-F002", "MET-F003", "MET-F004"} {
		if _, ok := entries[want]; !ok {
			t.Errorf("expected real registry to contain %s", want)
		}
	}
}
