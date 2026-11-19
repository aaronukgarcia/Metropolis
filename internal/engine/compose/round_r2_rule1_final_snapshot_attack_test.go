package compose

// BUG-1224 round r2 (attacker opus-round-r2-bug1224-rule1): the final
// (shutdown/eviction) snapshot reads the journal tail through the
// envelope-aware decoder. These attacks hold the two edges the builder's
// tests did not: a journal of only legacy (version-0) frames written while
// the build's Current is 1, and a malformed rule-version envelope at the
// tail, which must fail closed (registered MET-G5613, nothing written, the
// previous snapshot kept) rather than write a snapshot over a journal the
// reader could not read.

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

func TestRoundR2Rule1_FinalSnapshot_LegacyOnlyJournalUnderCurrentOne(t *testing.T) {
	rules.SetLiveVersionForTest(t, rules.RefuseOccupiedCell)
	store := persist.NewMemStore()
	rc := newRuleCity(t, store, 0)
	rc.sessionA(t)
	rc.sessionB(t)
	frames, err := store.ReadJournal(context.Background(), ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range frames {
		if bytes.Contains(f, []byte(`"rv":`)) {
			t.Fatalf("frame %d is an envelope; the city was pinned to version 0: %s", i, f)
		}
	}
	finalSnapshotThenRestore(t, rc, store)
}

func TestRoundR2Rule1_FinalSnapshot_MalformedEnvelopeTailFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		edit func([]byte) []byte
	}{
		{"rv-quoted", func(b []byte) []byte { return bytes.Replace(b, []byte(`"rv":1`), []byte(`"rv":"1"`), 1) }},
		{"rv-zero", func(b []byte) []byte { return bytes.Replace(b, []byte(`"rv":1`), []byte(`"rv":0`), 1) }},
		{"extra-key", func(b []byte) []byte { return bytes.Replace(b, []byte(`{"rv":1,`), []byte(`{"rv":1,"x":1,`), 1) }},
		{"cmd-null", func(b []byte) []byte {
			i := bytes.Index(b, []byte(`"cmd":`))
			return append(append([]byte{}, b[:i]...), []byte(`"cmd":null}`)...)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules.SetLiveVersionForTest(t, rules.RefuseOccupiedCell)
			ctx := context.Background()
			store := persist.NewMemStore()
			rc := newRuleCity(t, store, rules.RefuseOccupiedCell)
			rc.sessionA(t)
			rc.snapshot(t, store, 1) // the previous snapshot that must survive
			before, err := store.ListSnapshots(ctx, ruleTestCity)
			if err != nil || len(before) == 0 {
				t.Fatalf("no previous snapshot: %v %v", before, err)
			}
			frames, err := store.ReadJournal(ctx, ruleTestCity)
			if err != nil {
				t.Fatal(err)
			}
			last := frames[len(frames)-1]
			if !bytes.HasPrefix(last, []byte(`{"rv":1,`)) {
				t.Fatalf("last frame is not a version-1 envelope: %s", last)
			}
			bad := tc.edit(last)
			if bytes.Equal(bad, last) {
				t.Fatalf("edit did not change the frame: %s", last)
			}
			if err := store.AppendJournal(ctx, ruleTestCity, bad); err != nil {
				t.Fatal(err)
			}
			res, ferr := rc.c.FinalSnapshot(ctx, store, ruleTestCity)
			if ferr == nil {
				t.Fatalf("final snapshot over a malformed envelope did not fail: %+v", res)
			}
			if !strings.Contains(ferr.Error(), rules.ErrMalformedRuleStamp) {
				t.Fatalf("error is not the registered malformed-stamp code %s: %v", rules.ErrMalformedRuleStamp, ferr)
			}
			if res.Wrote {
				t.Fatalf("a snapshot was written over an unreadable journal: %+v", res)
			}
			after, err := store.ListSnapshots(ctx, ruleTestCity)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(before) || after[len(after)-1] != before[len(before)-1] {
				t.Fatalf("snapshots changed: before %v after %v", before, after)
			}
		})
	}
}
