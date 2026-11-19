package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

const scanCityID = "scan"

var scanKey = persist.CityKey{TenantID: defaultTenant, CityID: scanCityID}

func dirDigest(t *testing.T, dir string) string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, p) // directories too: a new empty directory is a change
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		rel, _ := filepath.Rel(dir, p)
		_, _ = h.Write([]byte(rel + "\x00"))
		if st, _ := os.Stat(p); st != nil && !st.IsDir() {
			data, _ := os.ReadFile(p)
			_, _ = h.Write(data)
		}
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func cmd(id string, n int64) protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(id),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: n},
	}
}

// makeCity builds a real legacy city (journal and one snapshot, rule version
// 0) the way metroserve does: a fresh composition is pinned to version 0.
func makeCity(t *testing.T, dir string) {
	t.Helper()
	const legacy = true
	store, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	e := core.NewEngine(core.WithWorldSeed(20261118), core.WithPoolSize(1))
	c, err := compose.Wire(e, &compose.Deps{PersistStore: store, PersistCity: scanKey})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if legacy {
		if err := c.PinRuleVersion(0); err != nil {
			t.Fatalf("PinRuleVersion(0): %v", err)
		}
	}
	for i, n := range []int64{5, 7, 9} {
		if res := e.HandleCommand(cmd("scan-"+string(rune('a'+i)), n)); !res.Accepted {
			t.Fatalf("command rejected: %+v", res.Error)
		}
	}
	if _, ok, err := c.MaybeSnapshotEvery(context.Background(), store, scanKey, 1); err != nil || !ok {
		t.Fatalf("snapshot: ok=%v err=%v", ok, err)
	}
	if res := e.HandleCommand(cmd("scan-d", 3)); !res.Accepted {
		t.Fatalf("command rejected: %+v", res.Error)
	}
}

func appendRaw(t *testing.T, dir string, rec []byte) {
	t.Helper()
	store, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendJournal(context.Background(), scanKey, rec); err != nil {
		t.Fatal(err)
	}
}

func envelope(t *testing.T, rv string) []byte {
	t.Helper()
	plain, err := protocol.EncodeCommand(cmd("scan-env", 2))
	if err != nil {
		t.Fatal(err)
	}
	return []byte(`{"rv":` + rv + `,"cmd":` + string(plain) + `}`)
}

func scan(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestScan_CleanCity_ReportShape(t *testing.T) {
	dir := t.TempDir()
	makeCity(t, dir)
	code, out, errs := scan(t, dir)
	if code != exitClean {
		t.Fatalf("exit %d, want 0; out:\n%s\nerr:%s", code, out, errs)
	}
	for _, want := range []string{
		"journalscan: tenant=local cities=1 (read-only)",
		"city scan",
		"snapshots: 1",
		": rule version 0, tick ",
		"journal frames: 4, first tick 0, last tick 0, advanced ticks 24",
		"frames per rule version: v0=4",
		"status: ok",
		"result: clean",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report lacks %q:\n%s", want, out)
		}
	}
	t.Logf("report:\n%s", out)
}

func TestScan_NeverWrites_AndIsStable(t *testing.T) {
	dir := t.TempDir()
	makeCity(t, dir)
	appendRaw(t, dir, envelope(t, "2")) // a finding path too
	before := dirDigest(t, dir)
	c1, o1, _ := scan(t, dir)
	c2, o2, _ := scan(t, dir)
	if c1 != exitFindings || c2 != exitFindings {
		t.Fatalf("exit %d/%d, want 2/2:\n%s", c1, c2, o1)
	}
	if o1 != o2 {
		t.Fatalf("report is not byte-stable:\n%s\n---\n%s", o1, o2)
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("the scan changed the directory (%s -> %s)", before, after)
	}
}

func TestScan_MissingRoot_IsNotCreated(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "nope")
	code, _, errs := scan(t, root)
	if code != exitCannot || !strings.Contains(errs, "not an existing directory") {
		t.Fatalf("exit %d err %q", code, errs)
	}
	if _, err := os.Stat(root); err == nil {
		t.Fatal("the scan created the missing root")
	}
}

func TestScan_Findings_CarryRegisteredCodes(t *testing.T) {
	cases := []struct {
		name  string
		code  string
		setup func(t *testing.T, dir string)
	}{
		{"newer than this build", "MET-G5610", func(t *testing.T, dir string) { appendRaw(t, dir, envelope(t, "2")) }},
		{"malformed stamp", "MET-G5613", func(t *testing.T, dir string) { appendRaw(t, dir, envelope(t, "0")) }},
		{"backwards", "MET-G5612", func(t *testing.T, dir string) {
			appendRaw(t, dir, envelope(t, "1"))
			plain, _ := protocol.EncodeCommand(cmd("scan-back", 2))
			appendRaw(t, dir, plain)
		}},
		{"retired", "MET-G5611", func(t *testing.T, dir string) {
			rules.SetOldestSupportedForTest(t, 1)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			makeCity(t, dir)
			tc.setup(t, dir)
			code, out, _ := scan(t, dir)
			if code != exitFindings {
				t.Fatalf("exit %d, want 2:\n%s", code, out)
			}
			if !strings.Contains(out, "finding: "+tc.code) || !strings.Contains(out, "status: FINDINGS") {
				t.Fatalf("no %s finding:\n%s", tc.code, out)
			}
		})
	}
}

func TestScan_CrossesVersions_CountsPerRule(t *testing.T) {
	dir := t.TempDir()
	makeCity(t, dir) // version 0
	appendRaw(t, dir, envelope(t, "1"))
	appendRaw(t, dir, envelope(t, "1"))
	code, out, _ := scan(t, dir)
	if code != exitClean || !strings.Contains(out, "frames per rule version: v0=4 v1=2") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestScan_CityFilterAndEmptyTenant(t *testing.T) {
	dir := t.TempDir()
	makeCity(t, dir)
	_, out, _ := scan(t, "-city", "other", dir)
	if !strings.Contains(out, "cities=0") {
		t.Fatalf("filter did not exclude:\n%s", out)
	}
	_, out, _ = scan(t, "-tenant", "nobody", dir)
	if !strings.Contains(out, "cities=0") || !strings.Contains(out, "result: clean") {
		t.Fatalf("empty tenant:\n%s", out)
	}
}
