package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// Round r5 (port of lane 5 onto rule version 1): the committed rule fixture is a
// REAL version-0 persist directory (no pins in this test). The pre-check, the
// scan tool and the real restore must agree on it, with the live version at 1.

var fixtureKey = persist.CityKey{TenantID: "local", CityID: "default"}

const fixtureSrc = "../../internal/engine/compose/testdata/rules/v1"

func copyFixture(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "v1")
	err := filepath.WalkDir(fixtureSrc, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(fixtureSrc, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func precheckCode(t *testing.T, dir string) string {
	t.Helper()
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = compose.PrecheckRuleVersions(context.Background(), st, fixtureKey, 20261118, "real")
	if err == nil {
		return ""
	}
	var ee *errs.E
	if errors.As(err, &ee) {
		return ee.Code
	}
	t.Fatalf("precheck error without code: %v", err)
	return ""
}

func realRestoreCode(t *testing.T, dir string) (string, compose.RestoreReport) {
	t.Helper()
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := core.NewEngine(core.WithWorldSeed(20261118), core.WithPoolSize(1))
	c, err := compose.Wire(e, &compose.Deps{PersistStore: st, PersistCity: fixtureKey})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	rep, err := compose.RestoreLatestSnapshotOrGenesisReport(context.Background(), e, c, st, fixtureKey)
	if err != nil {
		var ee *errs.E
		if errors.As(err, &ee) {
			return ee.Code, rep
		}
		t.Fatalf("restore without a code: %v", err)
	}
	return "", rep
}

func scanText(t *testing.T, dir string) (int, string) {
	t.Helper()
	var out strings.Builder
	code := run([]string{dir}, &out, io.Discard)
	return code, out.String()
}

func TestRoundR5Lane5Port_RealFixture_OlderRules_AllAgree(t *testing.T) {
	if rules.Current < 1 {
		t.Skip("needs rule 1 wired")
	}
	dir := copyFixture(t)
	if got := precheckCode(t, dir); got != "" {
		t.Fatalf("precheck refused a version-0 fixture: %s", got)
	}
	code, out := scanText(t, dir)
	if code != exitClean || !strings.Contains(out, "frames per rule version: v0=") {
		t.Fatalf("scan exit %d:\n%s", code, out)
	}
	if !strings.Contains(strings.ToLower(out), "older") {
		t.Fatalf("scan does not say older rules:\n%s", out)
	}
	t.Logf("scan output:\n%s", out)
	c, rep := realRestoreCode(t, dir)
	if c != "" || !rep.OlderRules {
		t.Fatalf("real restore: code %q report %+v", c, rep)
	}
}

func TestRoundR5Lane5Port_RealFixture_NewerFrame_RefusedEverywhere_PrecheckWritesNothing(t *testing.T) {
	dir := copyFixture(t)
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	next := int(rules.Current) + 1
	rec := envelope(t, string(rune('0'+next)))
	if err := st.AppendJournal(context.Background(), fixtureKey, rec); err != nil {
		t.Fatal(err)
	}
	before := dirDigest(t, dir)
	if got := precheckCode(t, dir); got != "MET-G5610" {
		t.Fatalf("precheck: %q want MET-G5610", got)
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("the pre-check changed the persist directory")
	}
	code, out := scanText(t, dir)
	if code != exitFindings || !strings.Contains(out, "MET-G5610") {
		t.Fatalf("scan exit %d:\n%s", code, out)
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("the scan changed the persist directory")
	}
	if c, _ := realRestoreCode(t, dir); c != "MET-G5610" {
		t.Fatalf("real restore: %q want MET-G5610", c)
	}
}

func TestRoundR5Lane5Port_RealFixture_RetiredVersionZero_RefusedEverywhere(t *testing.T) {
	dir := copyFixture(t)
	rules.SetOldestSupportedForTest(t, 1)
	before := dirDigest(t, dir)
	if got := precheckCode(t, dir); got != "MET-G5611" {
		t.Fatalf("precheck: %q want MET-G5611", got)
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("the pre-check changed the persist directory")
	}
	code, out := scanText(t, dir)
	if code != exitFindings || !strings.Contains(out, "MET-G5611") {
		t.Fatalf("scan exit %d:\n%s", code, out)
	}
	if c, _ := realRestoreCode(t, dir); c != "MET-G5611" {
		t.Fatalf("real restore: %q want MET-G5611", c)
	}
}
