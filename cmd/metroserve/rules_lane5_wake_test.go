package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-9995385612 lane 5, criterion C18: the metroserve wake path.
//
// A city restored under older rules writes exactly one operator line and one
// registered WARN event (MET-G5614), built from the rule registry's own text.
// A restore refused by a rule-version check (MET-G5610 to MET-G5613) starts no
// city, changes no byte on disk, and reaches the operator with its code.
//
// Rules up to rules.Current are wired. "Older rules" is a city whose frames
// are version 0 (legacy, unstamped), written by pinning a fresh composition to
// version 0 (makeV0City), woken on today's build; "a newer build's frame" is
// stamped rules.Current+1.

const lane5City = "lane5"

// dirDigest hashes every file under dir (relative path and content, in sorted
// order) so a test can prove a directory is byte-identical before and after.
func dirDigest(t *testing.T, dir string) string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		rel, _ := filepath.Rel(dir, p)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		_, _ = h.Write([]byte(rel + "\x00"))
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// makeV0City persists a city under the legacy rules (version 0, plain
// frames): a fresh composition is pinned to version 0 before any command.
func makeV0City(t *testing.T, dir string) {
	t.Helper()
	e := newEngine()
	comp, _, err := setUpPersistence(e, dir, lane5City, io.Discard)
	if err != nil {
		t.Fatalf("setUpPersistence (fresh): %v", err)
	}
	if err := comp.PinRuleVersion(0); err != nil {
		t.Fatalf("PinRuleVersion(0): %v", err)
	}
	submitAll(t, e, rtCommands())
}

// pinLegacy makes a freshly created host city a legacy (version 0) city: the
// composition is pinned to version 0 before any command is submitted.
func pinLegacy(t *testing.T, rc *runningCity) {
	t.Helper()
	if err := rc.Composition().PinRuleVersion(0); err != nil {
		t.Fatalf("PinRuleVersion(0): %v", err)
	}
}

// makeCurrentCity persists a city under the live rules (stamped frames).
func makeCurrentCity(t *testing.T, dir string) {
	t.Helper()
	e := newEngine()
	if _, _, err := setUpPersistence(e, dir, lane5City, io.Discard); err != nil {
		t.Fatalf("setUpPersistence (fresh): %v", err)
	}
	submitAll(t, e, rtCommands())
}

// appendFrame appends one raw journal frame to the city's durable journal.
func appendFrame(t *testing.T, dir string, rec []byte) {
	t.Helper()
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	key := persist.CityKey{TenantID: persistTenantID, CityID: lane5City}
	if err := st.AppendJournal(context.Background(), key, rec); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}
}

// newerStamp is a journal stamp one past this build's live rule version: a
// frame from a newer build.
func newerStamp() string { return strconv.Itoa(int(rules.Effective()) + 1) }

// stampOf renders a named rule version as a journal stamp.
func stampOf(v rules.Version) string { return strconv.Itoa(int(v)) }

// envelope wraps a plain advance command as a version-n journal frame.
func envelope(t *testing.T, rv string, n int64) []byte {
	t.Helper()
	plain, err := protocol.EncodeCommand(advance("l5-env", n))
	if err != nil {
		t.Fatalf("EncodeCommand: %v", err)
	}
	return []byte(`{"rv":` + rv + `,"cmd":` + string(plain) + `}`)
}

// plainFrame is a version-0 frame.
func plainFrame(t *testing.T, n int64) []byte {
	t.Helper()
	plain, err := protocol.EncodeCommand(advance("l5-plain", n))
	if err != nil {
		t.Fatalf("EncodeCommand: %v", err)
	}
	return plain
}

var lane5Sink *syncBuf

// captureEvents routes registry events to a buffer for the test (the
// in-memory ring holds only 200 entries and wraps).
func captureEvents(t *testing.T) {
	t.Helper()
	lane5Sink = &syncBuf{}
	if err := errs.SetSink(errs.NewLogger(lane5Sink)); err != nil {
		t.Fatalf("SetSink: %v", err)
	}
	t.Cleanup(func() { _ = errs.SetSink(nil) })
}

// warnCount is how many MET-G5614 events were raised since captureEvents.
func warnCount() int {
	return strings.Count(lane5Sink.String(), "MET-G5614")
}

func wake(t *testing.T, dir string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	_, _, err := setUpPersistence(newEngine(), dir, lane5City, &out)
	return out.String(), err
}

// C18: restore under older rules: one stdout line, one WARN event, text from
// the registry; a second wake after live traffic is silent.
func TestLane5_Wake_OlderRules_OneLineOneEvent(t *testing.T) {
	captureEvents(t)
	dir := t.TempDir()
	makeV0City(t, dir)
	entry, ok := rules.Entry(rules.RefuseOccupiedCell)
	if !ok {
		t.Fatal("rule 1 missing from the registry")
	}

	before := warnCount()
	var out bytes.Buffer
	e := newEngine()
	if _, _, err := setUpPersistence(e, dir, lane5City, &out); err != nil {
		t.Fatalf("wake under rule 1: %v", err)
	}
	lines := 0
	for _, ln := range strings.Split(out.String(), "\n") {
		if strings.Contains(ln, "restored under older rules") {
			lines++
			if !strings.Contains(ln, entry.Old) || !strings.Contains(ln, entry.New) || !strings.Contains(ln, entry.Item) || !strings.Contains(ln, entry.Date) {
				t.Fatalf("operator line is not built from the registry text:\n%s", ln)
			}
			if want := "version 0 through " + strconv.Itoa(int(rules.Current)); !strings.Contains(ln, want) {
				t.Fatalf("operator line does not name %q:\n%s", want, ln)
			}
		}
	}
	if lines != 1 {
		t.Fatalf("want exactly one operator line, got %d in:\n%s", lines, out.String())
	}
	if !strings.Contains(out.String(), "rehydrated city") {
		t.Fatalf("the existing rehydrated line is gone:\n%s", out.String())
	}
	if got := warnCount() - before; got != 1 {
		t.Fatalf("want exactly one MET-G5614 event, got %d", got)
	}

	// Live traffic after the switch is stamped rule 1.
	submitAll(t, e, []protocol.Command{advance("l5-live", 3)})

	before = warnCount()
	out.Reset()
	if _, _, err := setUpPersistence(newEngine(), dir, lane5City, &out); err != nil {
		t.Fatalf("second wake: %v", err)
	}
	if strings.Contains(out.String(), "older rules") {
		t.Fatalf("second wake repeated the older-rules line:\n%s", out.String())
	}
	if got := warnCount() - before; got != 0 {
		t.Fatalf("second wake raised %d MET-G5614 events, want 0", got)
	}
}

// C18: a restore at the current rules (no older history) says nothing.
func TestLane5_Wake_CurrentRules_NoWarning(t *testing.T) {
	captureEvents(t)
	dir := t.TempDir()
	makeCurrentCity(t, dir)
	before := warnCount()
	out, err := wake(t, dir)
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if strings.Contains(out, "older rules") || warnCount() != before {
		t.Fatalf("a city at the current rules must be silent, got:\n%s", out)
	}
}

// C18 / C23: every refusal returns the registered code, starts no city and
// changes no byte on disk.
func TestLane5_Wake_Refusals_ByteIdentical(t *testing.T) {
	cases := []struct {
		name  string
		code  string
		setup func(t *testing.T, dir string)
	}{
		{"newer than this build", "MET-G5610", func(t *testing.T, dir string) {
			appendFrame(t, dir, envelope(t, newerStamp(), 2)) // one past the build (rules.Current+1)
		}},
		{"version goes backwards", "MET-G5612", func(t *testing.T, dir string) {
			appendFrame(t, dir, envelope(t, stampOf(rules.RefuseOccupiedCell), 2))
			appendFrame(t, dir, plainFrame(t, 2)) // 1 then 0
		}},
		{"malformed stamp (rv 0)", "MET-G5613", func(t *testing.T, dir string) {
			appendFrame(t, dir, envelope(t, "0", 2))
		}},
		{"malformed stamp (rv not a number)", "MET-G5613", func(t *testing.T, dir string) {
			appendFrame(t, dir, envelope(t, `"x"`, 2))
		}},
		{"retired version", "MET-G5611", func(t *testing.T, dir string) {
			rules.SetOldestSupportedForTest(t, rules.RefuseOccupiedCell) // the version-0 history is retired
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			makeV0City(t, dir)
			tc.setup(t, dir)
			before := dirDigest(t, dir)

			out, err := wake(t, dir)
			if err == nil {
				t.Fatalf("restore was not refused; output:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error does not carry %s: %v", tc.code, err)
			}
			if strings.Contains(out, "rehydrated city") {
				t.Fatalf("a refused restore printed the success line:\n%s", out)
			}
			if after := dirDigest(t, dir); after != before {
				t.Fatalf("persist directory changed on a refused restore (%s -> %s)", before, after)
			}
		})
	}
}

// C18 at the multi-city host (GetOrCreate -> buildCity -> the shared seam):
// older rules log one line; a refusal registers no city, carries its code and
// leaves the directory byte-identical.
func TestLane5_Host_OlderRulesAndRefusal(t *testing.T) {
	captureEvents(t)
	dir := t.TempDir()
	key := persist.CityKey{TenantID: persistTenantID, CityID: lane5City}
	{
		h, _ := newFinalHost(t, dir, 0, time.Hour, nil)
		rc, err := h.GetOrCreate(context.Background(), key)
		if err != nil {
			t.Fatalf("GetOrCreate (v0 city): %v", err)
		}
		pinLegacy(t, rc)
		submitAll(t, rc.Engine(), []protocol.Command{advance("l5-h1", 4), advance("l5-h2", 5)})
		_ = h.Close()
	}

	// Older rules: one line in the host log.
	{
		before := warnCount()
		h, logs := newFinalHost(t, dir, 0, time.Hour, nil)
		if _, err := h.GetOrCreate(context.Background(), key); err != nil {
			t.Fatalf("GetOrCreate under rule 1: %v", err)
		}
		if n := strings.Count(logs.String(), "restored under older rules"); n != 1 {
			t.Fatalf("host log has %d older-rules lines, want 1:\n%s", n, logs.String())
		}
		if got := warnCount() - before; got != 1 {
			t.Fatalf("host raised %d MET-G5614 events, want 1", got)
		}
		_ = h.Close()
	}

	// A refusal: the stored city carries a frame from a build newer than this one.
	appendFrame(t, dir, envelope(t, newerStamp(), 2))
	before := dirDigest(t, dir)
	h, logs := newFinalHost(t, dir, 0, time.Hour, nil)
	for i := 0; i < 2; i++ { // twice: a refusal must not leave a half-registered city
		_, err := h.GetOrCreate(context.Background(), key)
		if err == nil || !strings.Contains(err.Error(), "MET-G5610") {
			t.Fatalf("attempt %d: GetOrCreate err = %v, want a MET-G5610 refusal", i, err)
		}
	}
	if strings.Contains(logs.String(), "rehydrated city") {
		t.Fatalf("a refused city logged a success line:\n%s", logs.String())
	}
	h.mu.Lock()
	n := len(h.cities)
	h.mu.Unlock()
	if n != 0 {
		t.Fatalf("a refused city left %d entries registered", n)
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("persist directory changed on a refused host restore (%s -> %s)", before, after)
	}
}
