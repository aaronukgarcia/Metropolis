package compose

import (
	"bytes"
	"context"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// BUG-1224 round r2: the final (shutdown or eviction) snapshot reads the
// journal tail to decide whether it may be written. It once decoded each
// record as a bare command, so a journal of rule-version envelopes
// ({"rv":1,"cmd":...}) failed with "unknown command kind" and no final
// snapshot was ever written. These tests drive the real final-snapshot path
// over journals of version-1 frames and over a journal mixing version-0 and
// version-1 frames, and prove the snapshot is written and restores to the
// same state.

func finalSnapshotThenRestore(t *testing.T, rc *ruleCity, store persist.Store) {
	t.Helper()
	ctx := context.Background()
	want := rc.outcome(t)
	res, err := rc.c.FinalSnapshot(ctx, store, ruleTestCity)
	if err != nil {
		t.Fatalf("FinalSnapshot failed: %v", err)
	}
	if !res.Wrote || res.SkipReason != "" {
		t.Fatalf("final snapshot not written: %+v", res)
	}
	<-res.Done
	// Restore from the snapshot alone (the journal tail is empty).
	rules.SetLiveVersionForTest(t, rules.RefuseOccupiedCell)
	src := cloneStore(t, store, 0)
	back, rep := wake(t, src)
	if !rep.UsedSnapshot {
		t.Fatalf("restore did not use the final snapshot: %+v", rep)
	}
	if got := back.outcome(t); got != want {
		t.Fatalf("restored state differs:\n got %+v\nwant %+v", got, want)
	}
}

func TestBug1224R2_FinalSnapshot_VersionOneEnvelopes(t *testing.T) {
	rules.SetLiveVersionForTest(t, rules.RefuseOccupiedCell)
	store := persist.NewMemStore()
	rc := newRuleCity(t, store, rules.RefuseOccupiedCell)
	rc.sessionA(t)
	rc.sessionB(t)
	frames, err := store.ReadJournal(context.Background(), ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range frames {
		if !bytes.Contains(f, []byte(`"rv":1`)) {
			t.Fatalf("frame %d is not a version-1 envelope: %s", i, f)
		}
	}
	finalSnapshotThenRestore(t, rc, store)
}

func TestBug1224R2_FinalSnapshot_MixedVersionZeroAndOne(t *testing.T) {
	rules.SetLiveVersionForTest(t, rules.RefuseOccupiedCell)
	store := persist.NewMemStore()
	rc := newRuleCity(t, store, 0)
	rc.sessionA(t)
	if err := rc.c.PinRuleVersion(rules.RefuseOccupiedCell); err != nil {
		t.Fatal(err)
	}
	rc.sessionB(t)
	frames, err := store.ReadJournal(context.Background(), ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	var legacy, env int
	for _, f := range frames {
		if bytes.Contains(f, []byte(`"rv":`)) {
			env++
		} else {
			legacy++
		}
	}
	if legacy == 0 || env == 0 {
		t.Fatalf("journal is not mixed: %d legacy, %d envelope frames", legacy, env)
	}
	finalSnapshotThenRestore(t, rc, store)
}
