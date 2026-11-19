package main

// FEAT-9995385620 round r4: the rule-version pre-check is a dry-run restore.
// Whatever it decides, it leaves the persist root exactly as it found it
// (files AND directories, so a paging folder created and left empty counts),
// and a refused wake with citizen paging enabled creates nothing either.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// treeListing lists every directory and file under dir; a file carries its
// size and modification time, a directory only its existence (NTFS updates a
// directory's time lazily, so it is not evidence of a change), so an empty
// directory left behind or a touched file shows up.
func treeListing(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			lines = append(lines, fmt.Sprintf("%s dir", rel))
			return nil
		}
		lines = append(lines, fmt.Sprintf("%s size=%d mtime=%d", rel, info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func retireVersionZero(t *testing.T) {
	t.Helper()
	rules.SetOldestSupportedForTest(t, rules.RefuseOccupiedCell)
}

// A pre-check that PASSES (an older-rules city is fine) changes nothing.
func TestRoundR4Lane5_PrecheckPasses_RootByteIdentical(t *testing.T) {
	dir := t.TempDir()
	makeV0City(t, dir)
	// version 0 is still supported: the restore would succeed
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	beforeTree, beforeDigest := treeListing(t, dir), dirDigest(t, dir)
	if err := compose.PrecheckRuleVersions(context.Background(), st, r1Key, newEngine().WorldSeed(), ""); err != nil {
		t.Fatalf("pre-check refused a restorable city: %v", err)
	}
	if got := treeListing(t, dir); got != beforeTree {
		t.Fatalf("a passing pre-check changed the persist root:\nbefore:\n%s\nafter:\n%s", beforeTree, got)
	}
	if dirDigest(t, dir) != beforeDigest {
		t.Fatal("a passing pre-check changed file content")
	}
}

// A pre-check that REFUSES changes nothing either.
func TestRoundR4Lane5_PrecheckRefuses_RootByteIdentical(t *testing.T) {
	dir := t.TempDir()
	makeV0City(t, dir)
	retireVersionZero(t)
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	beforeTree := treeListing(t, dir)
	err = compose.PrecheckRuleVersions(context.Background(), st, r1Key, newEngine().WorldSeed(), "")
	if err == nil || !strings.Contains(err.Error(), "MET-G5611") {
		t.Fatalf("pre-check = %v, want MET-G5611", err)
	}
	if got := treeListing(t, dir); got != beforeTree {
		t.Fatalf("a refusing pre-check changed the persist root:\nbefore:\n%s\nafter:\n%s", beforeTree, got)
	}
}

// The real boot path with citizen paging ENABLED: a refused wake leaves the
// root, directories included, exactly as it was (no paging folder, no claim
// file, no identity file), and the same city wakes once the rule allows it.
func TestRoundR4Lane5_PagedBoot_RefusedWake_RootByteIdentical(t *testing.T) {
	dir := t.TempDir()
	makeV0City(t, dir)
	retireVersionZero(t)
	beforeTree, beforeDigest := treeListing(t, dir), dirDigest(t, dir)
	var out bytes.Buffer
	_, _, err := setUpPersistencePaging(newEngine(), dir, lane5City, &out, true, 2, false)
	if err == nil || !strings.Contains(err.Error(), "MET-G5611") {
		t.Fatalf("err = %v, want MET-G5611", err)
	}
	if got := treeListing(t, dir); got != beforeTree {
		t.Fatalf("a refused paged boot changed the persist root:\nbefore:\n%s\nafter:\n%s", beforeTree, got)
	}
	if dirDigest(t, dir) != beforeDigest {
		t.Fatal("a refused paged boot changed file content")
	}
}

// The hosted path with citizen paging enabled, a city the restore refuses
// only after walking back (the r3 history is built in the compose tests; here
// the plain retired city proves the host wiring), root identical.
func TestRoundR4Lane5_PagedHost_RefusedWake_RootByteIdentical(t *testing.T) {
	dir := t.TempDir()
	{
		// The v0 city is written by an UNPAGED host: a paged host keeps writing
		// page files in the background after Close, which would make any
		// before/after comparison of the tree meaningless.
		h, err := newCityHost(dir, hostTickDisabled, time.Hour, 5*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		h.engineOpts = testEngineOpts()
		h.logw = io.Discard
		rc, err := h.GetOrCreate(context.Background(), r1Key)
		if err != nil {
			t.Fatalf("GetOrCreate (v0 city): %v", err)
		}
		pinLegacy(t, rc)
		submitAll(t, rc.Engine(), rtCommands())
		_ = h.Close()
	}
	retireVersionZero(t)
	h, err := newCityHost(dir, hostTickDisabled, time.Hour, 5*time.Millisecond, WithCitizenPaging(true, 2, false))
	if err != nil {
		t.Fatal(err)
	}
	h.engineOpts = testEngineOpts()
	h.logw = io.Discard
	// Constructing a paged host itself touches the paging directory (found
	// while writing this test: independent of any city wake; so does its Close), so the baseline
	// is taken after the host exists and before the wake.
	before := treeListing(t, dir)
	_, gerr := h.GetOrCreate(context.Background(), r1Key)
	after := treeListing(t, dir) // before the host's own Close, which touches the paging directory
	_ = h.Close()
	if gerr == nil || !strings.Contains(gerr.Error(), "MET-G5611") {
		t.Fatalf("GetOrCreate = %v, want MET-G5611", gerr)
	}
	if got := after; got != before {
		t.Fatalf("a refused paged host wake changed the persist root:\nbefore:\n%s\nafter:\n%s", before, got)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}

// A non-rule failure in the dry run (an undecodable journal frame) is not the
// pre-check's to report: it returns nil and the real restore reports its own.
func TestRoundR4Lane5_NonRuleFailure_PrecheckNil_RealRestoreReportsItsOwn(t *testing.T) {
	dir := t.TempDir()
	makeV0City(t, dir)
	appendFrame(t, dir, []byte("this is not a command frame"))
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := compose.PrecheckRuleVersions(context.Background(), st, r1Key, newEngine().WorldSeed(), ""); err != nil {
		t.Fatalf("pre-check = %v, want nil for a non-rule failure", err)
	}
	_, err = wake(t, dir)
	if err == nil {
		t.Fatal("the real wake accepted a corrupt journal")
	}
	if strings.Contains(err.Error(), "MET-G561") {
		t.Fatalf("the real wake's error is a rule refusal: %v", err)
	}
	t.Logf("real wake error: %v", err)
}
