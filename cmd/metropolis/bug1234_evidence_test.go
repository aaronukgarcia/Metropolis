package main

import (
	"os"
	"path/filepath"
	"testing"
)

// evidenceDir returns METRO_EVIDENCE_DIR, where a frame is saved on request.
func evidenceDir() string { return os.Getenv("METRO_EVIDENCE_DIR") }

func writeEvidence(t *testing.T, dir, text string) {
	t.Helper()
	name := os.Getenv("METRO_EVIDENCE_NAME")
	if name == "" {
		name = "bug1234-f3-120x28.txt"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatalf("write evidence: %v", err)
	}
}
