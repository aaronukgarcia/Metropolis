package menu

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// writeBundle creates a valid (header-only, zero-shard) save bundle at
// dir with the given Header, using int.serializer's own write path.
func writeBundle(t *testing.T, dir string, h serialize.Header) {
	t.Helper()
	if err := serialize.CreateBundleDir(dir); err != nil {
		t.Fatalf("CreateBundleDir(%q): %v", dir, err)
	}
	if err := serialize.WriteHeader(dir, h); err != nil {
		t.Fatalf("WriteHeader(%q): %v", dir, err)
	}
}

func TestRefresh_ListsSaveBundlesFromHeaders(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, filepath.Join(root, "newtown"), serialize.NewHeader(1001, 400, 5, "test-build"))
	writeBundle(t, filepath.Join(root, "oldtown"), serialize.NewHeader(1002, 120, 2, "test-build"))

	s := New("corr-list", WithSaveRoot(root))
	if err := s.Refresh(); err != nil {
		t.Fatalf("Refresh(): %v", err)
	}

	entries := s.SaveEntries()
	if len(entries) != 2 {
		t.Fatalf("SaveEntries() = %d entries, want 2", len(entries))
	}
	// Sorted by name ascending (deterministic render order, GR#21).
	if entries[0].Name != "newtown" || entries[1].Name != "oldtown" {
		t.Fatalf("SaveEntries() order = %q, %q, want newtown, oldtown", entries[0].Name, entries[1].Name)
	}
	if entries[0].WorldSeed != 1001 || entries[0].CreatedAtTick != 400 || entries[0].GameMonth != 5 {
		t.Errorf("newtown entry = %+v, want seed 1001 / tick 400 / month 5 (Header-sourced)", entries[0])
	}
	if !strings.Contains(entries[0].Summary, "seed 1001") {
		t.Errorf("newtown Summary = %q, want it to carry the seed", entries[0].Summary)
	}
	if s.SaveListUnavailable() != "" {
		t.Errorf("SaveListUnavailable() = %q, want empty (list is available)", s.SaveListUnavailable())
	}
}

func TestRefresh_NoSaveRootIsUnavailable(t *testing.T) {
	s := New("corr-noroot") // no WithSaveRoot
	_ = s.Refresh()         // lists nothing, but the reason is "not configured"
	if got := s.SaveListUnavailable(); got == "" {
		t.Fatalf("SaveListUnavailable() = %q, want a non-empty reason", got)
	}
}

func TestRefresh_CorruptHeaderSkippedNotFatal(t *testing.T) {
	root := t.TempDir()
	writeBundle(t, filepath.Join(root, "good"), serialize.NewHeader(1, 1, 1, "test"))
	badDir := filepath.Join(root, "bad")
	if err := serialize.CreateBundleDir(badDir); err != nil {
		t.Fatal(err)
	}
	// Corrupt header.json: not valid JSON.
	if err := os.WriteFile(serialize.HeaderPath(badDir), []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New("corr-corrupt", WithSaveRoot(root))
	if err := s.Refresh(); err != nil {
		t.Fatalf("Refresh(): %v", err)
	}
	entries := s.SaveEntries()
	if len(entries) != 1 || entries[0].Name != "good" {
		t.Fatalf("SaveEntries() = %+v, want exactly [good] (the corrupt slot must not hide the good one)", entries)
	}
	if errs := s.SaveListErrors(); len(errs) != 1 {
		t.Fatalf("SaveListErrors() = %d entries, want 1 (the corrupt slot's read failure)", len(errs))
	}
}

// TestRefresh_CorruptHeaderErrorIsRegistrySourced is SEC-218's regression
// check: a bundle whose header.json cannot be read must surface in
// SaveListErrors as a registry-sourced *errs.E (ErrSaveListEntryReadFailed)
// with a correlation ID — never the raw serialize *fmt.wrapError, which
// carries the save root's absolute filesystem path and no registry code
// (GR#7/GR#1). Mirrors TestLoadKeymapFile_ErrorsAreRegistrySourced (SEC-212).
func TestRefresh_CorruptHeaderErrorIsRegistrySourced(t *testing.T) {
	root := t.TempDir()
	badDir := filepath.Join(root, "bad")
	if err := serialize.CreateBundleDir(badDir); err != nil {
		t.Fatal(err)
	}
	// Corrupt header.json: not valid JSON.
	if err := os.WriteFile(serialize.HeaderPath(badDir), []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New("corr-corrupt-reg", WithSaveRoot(root))
	if err := s.Refresh(); err != nil {
		t.Fatalf("Refresh(): %v", err)
	}
	listErrs := s.SaveListErrors()
	if len(listErrs) != 1 {
		t.Fatalf("SaveListErrors() = %d entries, want 1", len(listErrs))
	}
	e, ok := listErrs[0].(*errs.E)
	if !ok {
		t.Fatalf("SaveListErrors()[0] = %T (%v), want *errs.E with code %s (registry-sourced, not the raw serialize error)",
			listErrs[0], listErrs[0], ErrSaveListEntryReadFailed)
	}
	if e.Code != ErrSaveListEntryReadFailed {
		t.Errorf("SaveListErrors()[0] code = %s, want %s", e.Code, ErrSaveListEntryReadFailed)
	}
	if e.CorrelationID == "" {
		t.Errorf("SaveListErrors()[0] correlation ID is empty (GR#1)")
	}
	if got := e.Display(); strings.Contains(got, root) {
		t.Errorf("SaveListErrors()[0] Display() leaks the absolute save-root path: %q", got)
	}
}

// TestLoad_CorruptBundleSurfacesSerializerErrorVerbatim is MEN-6's core
// check: loading a save whose shards fail ValidateBundle surfaces
// int.serializer's own error VERBATIM — never genericised into "load
// failed" and never re-derived by this screen.
func TestLoad_CorruptBundleSurfacesSerializerErrorVerbatim(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "corrupt")
	if err := serialize.CreateBundleDir(dir); err != nil {
		t.Fatal(err)
	}
	// A header that claims a shard exists, but no shard file is written —
	// ValidateBundle's validateShardFile then fails on the missing file.
	h := serialize.NewHeader(1, 1, 1, "test")
	h.ShardIndex = []serialize.ShardMeta{{
		Name: "citizens.0001", Kind: "citizen", Encoding: "ndjson+gzip",
		ByteSize: 10, SHA256: "0000000000000000000000000000000000000000000000000000000000000000",
	}}
	if err := serialize.WriteHeader(dir, h); err != nil {
		t.Fatal(err)
	}

	s := New("corr-load", WithSaveRoot(root))
	var sent bool
	err := s.Load(dir, func(cmd protocol.Command) error { sent = true; return nil })
	if err == nil {
		t.Fatalf("Load() returned nil for a corrupt bundle")
	}
	if sent {
		t.Fatalf("Load() sent a command for a corrupt bundle -- must fail before sending")
	}
	if strings.Contains(err.Error(), "load failed") {
		t.Fatalf("Load() genericised the serializer error into 'load failed': %v", err)
	}
	// The error must be the serializer's own validation error text.
	if !strings.Contains(err.Error(), "failed validation") && !strings.Contains(err.Error(), "opening") {
		t.Fatalf("Load() error %q does not carry int.serializer's own corruption text", err)
	}
}

// TestLoad_IncompatibleFormatVersionSurfacesSerializerErrorVerbatim is
// MEN-6's format-version half: a bundle with a newer MAJOR format version
// surfaces serialize.CheckFormatVersion's major-mismatch error verbatim.
func TestLoad_IncompatibleFormatVersionSurfacesSerializerErrorVerbatim(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "future")
	if err := serialize.CreateBundleDir(dir); err != nil {
		t.Fatal(err)
	}
	h := serialize.NewHeader(1, 1, 1, "test")
	h.FormatVersion = "2.0.0" // newer major than CurrentFormatVersion
	if err := serialize.WriteHeader(dir, h); err != nil {
		t.Fatal(err)
	}

	s := New("corr-version", WithSaveRoot(root))
	err := s.Load(dir, func(cmd protocol.Command) error { return nil })
	if err == nil {
		t.Fatalf("Load() returned nil for an incompatible format version")
	}
	if !strings.Contains(err.Error(), "not compatible") {
		t.Fatalf("Load() error %q does not carry serialize.CheckFormatVersion's 'not compatible' text", err)
	}
}

func TestLoad_ValidBundleSendsLoadCommand(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "valid")
	writeBundle(t, dir, serialize.NewHeader(7, 42, 3, "test"))

	s := New("corr-load-ok", WithSaveRoot(root))
	var got protocol.Command
	err := s.Load(dir, func(cmd protocol.Command) error { got = cmd; return nil })
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	p, ok := got.Payload.(protocol.DebugPayload)
	if !ok {
		t.Fatalf("Load command payload = %T, want protocol.DebugPayload", got.Payload)
	}
	if p.Op != opLoadSave {
		t.Errorf("Load op = %q, want %q", p.Op, opLoadSave)
	}
	if p.Args["path"] != dir {
		t.Errorf("Load path arg = %q, want %q", p.Args["path"], dir)
	}
}

func TestSaveAndDeleteSendCommands(t *testing.T) {
	s := New("corr-actions")

	var got protocol.Command
	if err := s.Save("holiday", func(cmd protocol.Command) error { got = cmd; return nil }); err != nil {
		t.Fatalf("Save(): %v", err)
	}
	if p := got.Payload.(protocol.DebugPayload); p.Op != opSaveGame || p.Args["name"] != "holiday" {
		t.Errorf("Save command = %+v, want op=%q name=holiday", p, opSaveGame)
	}

	if err := s.Delete("/saves/holiday", func(cmd protocol.Command) error { got = cmd; return nil }); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	if p := got.Payload.(protocol.DebugPayload); p.Op != opDeleteSave || p.Args["path"] != "/saves/holiday" {
		t.Errorf("Delete command = %+v, want op=%q path=/saves/holiday", p, opDeleteSave)
	}
}

// TestRefresh_EnumerationFailureIsRegistrySourced proves the enumeration
// failure path returns a registry-sourced error (MET-U604, GR#7) and marks
// the list unavailable — never a raw I/O error and never a fabricated
// empty list.
func TestRefresh_EnumerationFailureIsRegistrySourced(t *testing.T) {
	s := New("corr-list-fail", WithSaveRoot(t.TempDir()), WithBundleLister(func(string) ([]string, error) {
		return nil, errors.New("boom")
	}))

	err := s.Refresh()
	if err == nil {
		t.Fatalf("Refresh() returned nil for a failing lister")
	}
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("Refresh() error = %T, want *errs.E (registry-sourced)", err)
	}
	if e.Code != ErrSaveListFailed {
		t.Errorf("Refresh() error code = %s, want %s", e.Code, ErrSaveListFailed)
	}
	if got := s.SaveListUnavailable(); got == "" {
		t.Fatalf("SaveListUnavailable() = %q after an enumeration failure, want non-empty", got)
	}
}

// TestSaveEntries_SF3_OneSlotChanges is SF-3's differential check applied
// to the save browser: two roots differing only in one slot's CreatedAtTick
// must (a) change that slot's rendered row and (b) leave the OTHER slot's
// rendered row byte-identical. It drives the real serialize write path.
func TestSaveEntries_SF3_OneSlotChanges(t *testing.T) {
	build := func(t *testing.T, tickA, tickB int64) []string {
		t.Helper()
		root := t.TempDir()
		writeBundle(t, filepath.Join(root, "alpha"), serialize.NewHeader(50, tickA, 4, "test"))
		writeBundle(t, filepath.Join(root, "beta"), serialize.NewHeader(60, tickB, 4, "test"))
		s := New("corr-sf3", WithSaveRoot(root))
		if err := s.Refresh(); err != nil {
			t.Fatalf("Refresh(): %v", err)
		}
		buf := core.NewBuffer(90, 2)
		rect := core.Rect{X: 0, Y: 0, W: 90, H: 2}
		RenderSaves(buf, rect, s.SaveEntries(), tcell.StyleDefault)
		return renderedText(buf, rect)
	}

	linesA := build(t, 100, 200)
	linesB := build(t, 999, 200) // only alpha's tick mutated

	if linesA[0] == linesB[0] {
		t.Fatalf("alpha row unchanged after mutating its tick 100 -> 999: %q", linesB[0])
	}
	if linesA[1] != linesB[1] {
		t.Errorf("beta row changed even though untouched: %q -> %q", linesA[1], linesB[1])
	}
}
