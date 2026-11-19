package main

// Round r1 (opus-round-r1-rules-lane5) attack tests for FEAT-9995385620:
// data safety of a refused rule-version restore on every metroserve entry.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

var r1Key = persist.CityKey{TenantID: persistTenantID, CityID: lane5City}

func r1JournalPath(t *testing.T, dir string) string {
	t.Helper()
	var found string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "journal.dat" {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no journal.dat under %s", dir)
	}
	return found
}

func r1Tear(t *testing.T, dir string) {
	t.Helper()
	f, err := os.OpenFile(r1JournalPath(t, dir), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte{0x00, 0x00}) // torn tail: not even a full length header
	_ = f.Close()
}

// Attack 1a: torn tail AND a future-version frame, multi-city host wake.
func TestRoundR1Lane5_HostTornTailPlusRefusal_ByteIdentical(t *testing.T) {
	dir := t.TempDir()
	r1HostCity(t, dir, rtCommands())
	appendFrame(t, dir, envelope(t, newerStamp(), 2)) // newer than this build (rules.Current+1)
	r1Tear(t, dir)
	before := dirDigest(t, dir)
	h, logs := newFinalHost(t, dir, 0, time.Hour, nil)
	_, err := h.GetOrCreate(context.Background(), r1Key)
	if err == nil || !strings.Contains(err.Error(), "MET-G5610") {
		t.Fatalf("err = %v, want MET-G5610", err)
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("refused host wake changed the persist dir (torn tail repaired before refusal?) logs:\n%s", logs.String())
	}
}

// Attack 1b: the same at boot (setUpPersistence has no RepairJournal call).
func TestRoundR1Lane5_BootTornTailPlusRefusal_ByteIdentical(t *testing.T) {
	dir := t.TempDir()
	makeV0City(t, dir)
	appendFrame(t, dir, envelope(t, newerStamp(), 2))
	r1Tear(t, dir)
	before := dirDigest(t, dir)
	out, err := wake(t, dir)
	if err == nil || !strings.Contains(err.Error(), "MET-G5610") {
		t.Fatalf("err = %v out=%s", err, out)
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("refused boot changed the persist dir")
	}
}

// Attack 1c: citizen paging on, boot and host (twice, and concurrently): no
// claim file left behind, no pages dir created, directory identical, and the
// retry is refused by the rule version, not by a leaked paging claim.
func TestRoundR1Lane5_PagingRefusal_NoClaimLeak(t *testing.T) {
	dir := t.TempDir()
	if os.Getenv("R1_SKIP_BOOT") == "" {
		makeV0City(t, dir)
	} else {
		r1HostCity(t, dir, rtCommands())
	}
	appendFrame(t, dir, envelope(t, newerStamp(), 2))
	before := dirDigest(t, dir)
	for i := 0; i < 2 && os.Getenv("R1_SKIP_BOOT") == ""; i++ {
		var out bytes.Buffer
		_, _, err := setUpPersistencePaging(newEngine(), dir, lane5City, &out, true, 2, false)
		if err == nil || !strings.Contains(err.Error(), "MET-G5610") {
			t.Fatalf("boot attempt %d: err = %v files now: %d", i, err, len(r1Files(dir)))
		}
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("refused paging boot changed the persist dir")
	}
	// FEAT-9995385620 round r4: the host half runs on a city the HOST built. The
	// boot city above carries the boot engine's world seed, which the host's
	// per-city seed does not match, and the real restore checks the seed BEFORE
	// the rule version (checkWorldSeed, MET-E819); the pre-check is now that
	// restore run dry, so on the boot city it reports what the restore reports.
	// The assertions below are unchanged.
	if os.Getenv("R1_SKIP_BOOT") == "" {
		dir = t.TempDir()
		r1HostCity(t, dir, rtCommands())
		appendFrame(t, dir, envelope(t, newerStamp(), 2))
		before = dirDigest(t, dir)
	}
	h, _ := newFinalHost(t, dir, 0, time.Hour, func(h *CityHost) { WithCitizenPaging(true, 2, false)(h) })
	var wg sync.WaitGroup
	errsSeen := make([]error, 6)
	for i := range errsSeen {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errsSeen[i] = h.GetOrCreate(context.Background(), r1Key)
		}(i)
	}
	wg.Wait()
	for i, err := range errsSeen {
		if err == nil || !strings.Contains(err.Error(), "MET-G5610") {
			t.Fatalf("concurrent host attempt %d: err = %v files now: %d", i, err, len(r1Files(dir)))
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := h.GetOrCreate(context.Background(), r1Key); err == nil || !strings.Contains(err.Error(), "MET-G5610") {
			t.Fatalf("serial host attempt %d: err = %v", i, err)
		}
	}
	h.mu.Lock()
	n := len(h.cities)
	h.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d cities registered after refusals", n)
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("refused paging host wake changed the persist dir: files now %d", len(r1Files(dir)))
	}
}

// r1RewriteMeta returns snapshot bytes with save-meta.json's ruleVersion set to v.
func r1RewriteMeta(t *testing.T, data []byte, v int) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	edited := 0
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		if strings.HasSuffix(filepath.ToSlash(f.Name), "save-meta.json") {
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			m["ruleVersion"] = v
			b, _ = json.Marshal(m)
			edited++
		}
		hdr := f.FileHeader
		w, err := zw.CreateHeader(&hdr)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}
	_ = zw.Close()
	if edited == 0 {
		t.Fatalf("no save-meta.json in snapshot")
	}
	return buf.Bytes()
}

// r1CityWithSnapshot builds a v0 city with one snapshot (host final snapshot).
func r1CityWithSnapshot(t *testing.T, dir string, cmds []protocol.Command) persist.SnapshotID {
	t.Helper()
	h, _ := newFinalHost(t, dir, 30*time.Second, time.Hour, nil)
	rc, err := h.GetOrCreate(context.Background(), r1Key)
	if err != nil {
		t.Fatal(err)
	}
	pinLegacy(t, rc)
	submitAll(t, rc.Engine(), cmds)
	_ = h.Close()
	ids := snapshotIDs(t, dir, r1Key)
	if len(ids) == 0 {
		t.Fatalf("no snapshot written")
	}
	return ids[len(ids)-1]
}

// Attack 2: a snapshot whose Meta is from the future, through the host wake
// and the boot path: refused with MET-G5610, directory identical.
func TestRoundR1Lane5_FutureSnapshotMeta_Refused(t *testing.T) {
	dir := t.TempDir()
	id := r1CityWithSnapshot(t, dir, rtCommands())
	st, _ := persist.NewDiskStore(dir)
	data, err := st.GetSnapshot(context.Background(), r1Key, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutSnapshot(context.Background(), r1Key, r1RewriteMeta(t, data, 3)); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteSnapshot(context.Background(), r1Key, id); err != nil {
		t.Fatal(err)
	}
	before := dirDigest(t, dir)
	h, _ := newFinalHost(t, dir, 30*time.Second, time.Hour, nil)
	for i := 0; i < 2; i++ {
		if _, err := h.GetOrCreate(context.Background(), r1Key); err == nil || !strings.Contains(err.Error(), "MET-G5610") {
			t.Fatalf("host attempt %d: err = %v", i, err)
		}
	}
	_ = h.Close()
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("refused future-snapshot wake changed the persist dir")
	}
}

// Attack 3 / upgrade path: a v0 city whose journal ends with non-advance
// commands, woken under rule 1 and hibernated (final snapshot stamped v1),
// must wake again under rule 1. The snapshot is v1 and the journal tail after
// its tick is v0.
func TestRoundR1Lane5_UpgradeThenHibernate_WakesAgain(t *testing.T) {
	captureEvents(t)
	dir := t.TempDir()
	cmds := rtCommands()
	cmds = cmds[:len(cmds)-1] // end with buy/zone/build after the last advance
	{
		h, _ := newFinalHost(t, dir, 0, time.Hour, nil)
		rc, err := h.GetOrCreate(context.Background(), r1Key)
		if err != nil {
			t.Fatal(err)
		}
		pinLegacy(t, rc)
		submitAll(t, rc.Engine(), cmds)
		_ = h.Close()
	}
	for wakeN := 1; wakeN <= 3; wakeN++ {
		w0 := warnCount()
		h, logs := newFinalHost(t, dir, 30*time.Second, time.Hour, nil)
		rc, err := h.GetOrCreate(context.Background(), r1Key)
		if err != nil {
			t.Fatalf("wake %d under rule 1 refused: %v\nlogs:\n%s", wakeN, err, logs.String())
		}
		if wakeN == 1 {
			submitAll(t, rc.Engine(), []protocol.Command{{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "r1-sub", Kind: protocol.KindSubscribe, Payload: protocol.SubscribePayload{ViewName: "engine.status"}}})
		}
		_ = h.Close()
		t.Logf("wake %d: older-rules lines=%d events=%d snapshots=%d", wakeN,
			strings.Count(logs.String(), "restored under older rules"), warnCount()-w0, len(snapshotIDs(t, dir, r1Key)))
	}
}

// Attack 3b: repeated wakes with no traffic and no snapshot (finalBudget 0):
// one line per wake, never more than one per wake.
func TestRoundR1Lane5_RepeatedWakes_NoTraffic(t *testing.T) {
	captureEvents(t)
	dir := t.TempDir()
	makeV0City(t, dir)
	for i := 1; i <= 3; i++ {
		w0 := warnCount()
		out, err := wake(t, dir)
		if err != nil {
			t.Fatalf("wake %d: %v", i, err)
		}
		lines := strings.Count(out, "restored under older rules")
		ev := warnCount() - w0
		t.Logf("wake %d: lines=%d events=%d", i, lines, ev)
		if lines != 1 || ev != 1 {
			t.Fatalf("wake %d: lines=%d events=%d, want 1 and 1", i, lines, ev)
		}
	}
}

func r1HostCity(t *testing.T, dir string, cmds []protocol.Command) {
	t.Helper()
	h, _ := newFinalHost(t, dir, 0, time.Hour, nil)
	rc, err := h.GetOrCreate(context.Background(), r1Key)
	if err != nil {
		t.Fatal(err)
	}
	pinLegacy(t, rc)
	submitAll(t, rc.Engine(), cmds)
	_ = h.Close()
}

func r1Files(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	return out
}

// B1 (lead ruling): on ANY restore error after Wire the composition is
// closed, so the citizen-paging claim is released. A refusal that is NOT a
// rule-version one (here an undecodable journal frame, which the pre-check
// deliberately leaves to the restore) must therefore fail the SAME way on a
// second wake, never with MET-G819 (claimed by another live composition), and
// leave no claim.pid behind.
func TestRoundR1Lane5_PagingNonRuleRefusal_ClaimReleased(t *testing.T) {
	dir := t.TempDir()
	makeV0City(t, dir)
	appendFrame(t, dir, []byte(`not a command at all`))
	var firstMsg string
	for i := 0; i < 3; i++ {
		var out bytes.Buffer
		_, _, err := setUpPersistencePaging(newEngine(), dir, lane5City, &out, true, 2, false)
		if err == nil {
			t.Fatalf("attempt %d: undecodable frame was accepted", i)
		}
		if strings.Contains(err.Error(), "MET-G819") {
			t.Fatalf("attempt %d: claim leaked by the first refused restore: %v", i, err)
		}
		if i == 0 {
			firstMsg = err.Error()
		} else if !strings.Contains(err.Error(), "rehydrate city") {
			t.Fatalf("attempt %d: err = %v, first was %v", i, err, firstMsg)
		}
		for _, f := range r1Files(dir) {
			if strings.HasSuffix(filepath.ToSlash(f), "claim.pid") {
				t.Fatalf("attempt %d: claim file left behind: %s", i, f)
			}
		}
	}
}

// B1 with a PRE-EXISTING pages directory: a city that has run with citizen
// paging (claim released, identity and page files on disk) and then meets a
// journal frame from the future is refused with its own code and the whole
// persist root, pages directory included, is byte-identical.
func TestRoundR1Lane5_PagingRefusal_ExistingPagesDirUntouched(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	comp, _, err := setUpPersistencePaging(newEngine(), dir, lane5City, &out, true, 2, false)
	if err != nil {
		t.Fatalf("first paged boot: %v", err)
	}
	if err := comp.Close(); err != nil {
		t.Fatal(err)
	}
	if n := len(r1Files(filepath.Join(dir, "pages"))); n == 0 {
		t.Fatal("precondition: the paged boot left no pages directory")
	}
	appendFrame(t, dir, envelope(t, newerStamp(), 2))
	before := dirDigest(t, dir)
	for i := 0; i < 3; i++ {
		_, _, err := setUpPersistencePaging(newEngine(), dir, lane5City, &out, true, 2, false)
		if err == nil || !strings.Contains(err.Error(), "MET-G5610") {
			t.Fatalf("attempt %d: err = %v, want MET-G5610 (not MET-G819)", i, err)
		}
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("a refused paged boot changed the persist root (pages directory included)")
	}
}
