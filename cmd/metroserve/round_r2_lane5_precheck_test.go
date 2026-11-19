package main

// Round r2 (opus-round-r2-rules-lane5) attack tests for FEAT-9995385620:
// the read-only pre-check must agree with the restore, so a city whose
// restore is refused for its RULE VERSION leaves the persist root
// byte-identical on every metroserve entry.

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// r2CityFutureFramePlusBadSnapshot builds a city with one good snapshot, adds
// one undecodable snapshot payload and then a journal frame from a newer
// build. The restore checks the whole journal BEFORE it reads any snapshot,
// so it refuses with MET-G5610 whatever the snapshots hold.
func r2CityFutureFramePlusBadSnapshot(t *testing.T, dir string) {
	t.Helper()
	_ = r1CityWithSnapshot(t, dir, rtCommands())
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutSnapshot(context.Background(), r1Key, []byte("not a zip archive")); err != nil {
		t.Fatal(err)
	}
	appendFrame(t, dir, envelope(t, "2", 2)) // newer than this build (rules.Current+1)
}

// Host wake with a torn tail: the refusal is a rule-version one, so the
// torn tail must NOT have been repaired (truncated) first.
func TestRoundR2Lane5_HostFutureFrameBadSnapshotTorn_ByteIdentical(t *testing.T) {
	dir := t.TempDir()
	r2CityFutureFramePlusBadSnapshot(t, dir)
	r1Tear(t, dir)
	before := dirDigest(t, dir)
	h, logs := newFinalHost(t, dir, 0, time.Hour, nil)
	_, err := h.GetOrCreate(context.Background(), r1Key)
	_ = h.Close()
	if err == nil || !strings.Contains(err.Error(), "MET-G5610") {
		t.Fatalf("err = %v, want MET-G5610", err)
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("a rule-version refusal changed the persist root (pre-check passed, RepairJournal ran) logs:\n%s", logs.String())
	}
}

// Boot with citizen paging on and no pages directory yet: Wire must not run.
func TestRoundR2Lane5_PagedBootFutureFrameBadSnapshot_ByteIdentical(t *testing.T) {
	dir := t.TempDir()
	makeV0City(t, dir) // boot-path city (the host city has a different seed)
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutSnapshot(context.Background(), r1Key, []byte("not a zip archive")); err != nil {
		t.Fatal(err)
	}
	appendFrame(t, dir, envelope(t, "2", 2))
	before := dirDigest(t, dir)
	var out bytes.Buffer
	_, _, err = setUpPersistencePaging(newEngine(), dir, lane5City, &out, true, 2, false)
	if err == nil || !strings.Contains(err.Error(), "MET-G5610") {
		t.Fatalf("err = %v, want MET-G5610", err)
	}
	if after := dirDigest(t, dir); after != before {
		t.Fatalf("a rule-version refusal changed the persist root (pre-check passed, Wire ran): %d files now", len(r1Files(dir)))
	}
}
