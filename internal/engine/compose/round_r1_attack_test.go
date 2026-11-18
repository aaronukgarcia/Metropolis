package compose

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func TestRoundR1_DecoderVariants(t *testing.T) {
	plain, err := protocol.EncodeCommand(advCmd(3))
	if err != nil {
		t.Fatal(err)
	}
	p := string(plain)
	cases := map[string]string{
		"rv 1.0":        `{"rv":1.0,"cmd":` + p + `}`,
		"rv 1e0":        `{"rv":1e0,"cmd":` + p + `}`,
		"rv string":     `{"rv":"1","cmd":` + p + `}`,
		"rv 01":         `{"rv":01,"cmd":` + p + `}`,
		"rv 2^31":       `{"rv":2147483648,"cmd":` + p + `}`,
		"rv 2^63":       `{"rv":9223372036854775808,"cmd":` + p + `}`,
		"rv 0":          `{"rv":0,"cmd":` + p + `}`,
		"rv -1":         `{"rv":-1,"cmd":` + p + `}`,
		"nested":        `{"rv":1,"cmd":{"rv":1,"cmd":` + p + `}}`,
		"extra key":     `{"rv":1,"cmd":` + p + `,"x":1}`,
		"kind beside":   `{"rv":1,"cmd":` + p + `,"kind":"advanceTicks"}`,
		"rv only":       `{"rv":1}`,
		"cmd only":      `{"cmd":` + p + `}`,
		"cmd null":      `{"rv":1,"cmd":null}`,
		"trailing junk": `{"rv":1,"cmd":` + p + `} x`,
		"plain + rv":    strings.TrimSuffix(p, "}") + `,"rv":2}`,
		"plain + cmd":   strings.TrimSuffix(p, "}") + `,"cmd":{}}`,
	}
	for name, raw := range cases {
		f, err := decodeJournalFrame([]byte(raw), 0, 1)
		if err == nil {
			t.Errorf("%s: ACCEPTED as version %d kind %s", name, f.Version, f.Kind)
		} else {
			t.Logf("%s: refused: %v", name, err)
		}
	}
	// Not refused by design (observations, logged):
	soft := map[string]string{
		"duplicate rv":            `{"rv":1,"rv":5,"cmd":` + p + `}`,
		"uppercase RV":            `{"RV":4,"cmd":` + p + `}`,
		"case-folded Rv on plain": strings.TrimSuffix(p, "}") + `,"Rv":2}`,
		"padded rv":               `{"rv": 2 ,"cmd":` + p + `}`,
	}
	for name, raw := range soft {
		f, err := decodeJournalFrame([]byte(raw), 0, 1)
		t.Logf("SOFT %s: version=%d err=%v", name, f.Version, err)
	}
}

func TestRoundR1_TwoBoundaries(t *testing.T) {
	rules.SetCurrentForTest(t, 2)
	store := persist.NewMemStore()
	rc := newRuleCity(t, store, 0)
	rc.sessionA(t) // v0, tick 65
	if err := rc.c.PinRuleVersion(1); err != nil {
		t.Fatal(err)
	}
	rc.sessionB(t)           // v1, tick 105
	rc.snapshot(t, store, 5) // snapshot at v1
	if err := rc.c.PinRuleVersion(2); err != nil {
		t.Fatal(err)
	}
	cell := protocol.CellRef{X: 5, Y: 3}
	rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"})
	rc.advance(t, 13) // v2
	live := rc.outcome(t)
	dec := rc.probe.decisions

	ctrl := newRuleCity(t, nil, 0)
	ctrl.sessionA(t)
	_ = ctrl.c.PinRuleVersion(1)
	ctrl.sessionB(t)
	_ = ctrl.c.PinRuleVersion(2)
	ctrl.do(t, protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"})
	ctrl.advance(t, 13)
	if ctrl.outcome(t) != live || !reflect.DeepEqual(ctrl.probe.decisions, dec) {
		t.Fatal("control differs from live")
	}
	for _, c := range []struct {
		name string
		keep []int
		n    int
	}{{"genesis", nil, len(dec)}, {"snapshot between", []int{0}, 2}} {
		t.Run(c.name, func(t *testing.T) {
			src := cloneStore(t, store, c.keep...)
			_, _ = wake(t, src) // first wake may stamp the seed of record (BUG-508, pre-existing)
			fp := fingerprint(t, src)
			w, rep := wake(t, src)
			if w.outcome(t) != live {
				t.Fatalf("outcome %+v want %+v", w.outcome(t), live)
			}
			if got := w.probe.decisions; !reflect.DeepEqual(got, dec[len(dec)-c.n:]) {
				t.Fatalf("decisions %+v want %+v", got, dec[len(dec)-c.n:])
			}
			if rep.OlderRules || w.c.RuleVersion() != 2 {
				t.Fatalf("rep %+v live %d", rep, w.c.RuleVersion())
			}
			if fingerprint(t, src) != fp {
				t.Fatal("wake wrote to the store")
			}
			w.advance(t, 1)
			fr, _ := src.ReadJournal(context.Background(), ruleTestCity)
			if !bytes.HasPrefix(fr[len(fr)-1], []byte(`{"rv":2,`)) {
				t.Fatalf("post-wake frame not stamped 2: %s", fr[len(fr)-1])
			}
		})
	}
	// Upgrade: same history (ends at v2) under a build at 3: older rules, snapshot after restore.
	t.Run("upgrade then snapshot", func(t *testing.T) {
		rules.SetCurrentForTest(t, 3)
		src := cloneStore(t, store, 0)
		w, rep := wake(t, src)
		if !rep.OlderRules || rep.ReplayedVersion != 2 || w.c.RuleVersion() != 3 {
			t.Fatalf("rep %+v", rep)
		}
		w.snapshot(t, src, 1)
		ids, _ := src.ListSnapshots(context.Background(), ruleTestCity)
		data, _ := src.GetSnapshot(context.Background(), ruleTestCity, ids[len(ids)-1])
		v, err := snapshotRuleVersion(data)
		t.Logf("snapshot written after an older-rules restore carries ruleVersion=%d (err %v)", v, err)
		w2, rep2 := wake(t, src)
		t.Logf("second wake: rep=%+v outcome-equal=%v", rep2, w2.outcome(t) == live)
		if w2.outcome(t) != live {
			t.Fatal("second wake differs")
		}
	})
}

// Future frame in the PREFIX (before the newest snapshot): must refuse, never walk back.
func TestRoundR1_FutureFrameInPrefix(t *testing.T) {
	rules.SetCurrentForTest(t, 1)
	cr := buildCrossing(t)
	ctx := context.Background()
	fr, _ := cr.store.ReadJournal(ctx, ruleTestCity)
	ms := persist.NewMemStore()
	for i, f := range fr {
		if i == 6 { // a v1 frame of sessionB, before the "after" snapshot
			f = bytes.Replace(f, []byte(`{"rv":1,`), []byte(`{"rv":7,`), 1)
		}
		_ = ms.AppendJournal(ctx, ruleTestCity, f)
	}
	ids, _ := cr.store.ListSnapshots(ctx, ruleTestCity)
	for _, id := range ids {
		data, _ := cr.store.GetSnapshot(ctx, ruleTestCity, id)
		if _, err := ms.PutSnapshot(ctx, ruleTestCity, data); err != nil {
			t.Fatal(err)
		}
	}
	failClosed(t, ms, rules.ErrSavedUnderNewerRules, nil)
}

// Torn tail mid-envelope on a real DiskStore: BUG-1197 truncation must win.
func TestRoundR1_TornEnvelopeTail(t *testing.T) {
	rules.SetCurrentForTest(t, 1)
	dir := t.TempDir()
	ds, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rc := newRuleCity(t, ds, 1)
	rc.sessionA(t)
	rc.advance(t, 9)
	var jpath string
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(strings.ToLower(info.Name()), "journal") {
			jpath = p
		}
		return nil
	})
	if jpath == "" {
		t.Fatal("no journal file")
	}
	b, _ := os.ReadFile(jpath)
	if err := os.WriteFile(jpath, b[:len(b)-12], 0o644); err != nil {
		t.Fatal(err)
	}
	ds2, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	fr, err := ds2.ReadJournal(context.Background(), ruleTestCity)
	t.Logf("frames after tear: %d err=%v", len(fr), err)
	w, rep := wake(t, ds2)
	cl, _ := w.e.Clock()
	if cl.Tick() != 65 {
		t.Fatalf("tick %d want 65 (torn last AdvanceTicks dropped)", cl.Tick())
	}
	t.Logf("rep %+v", rep)
}

func TestRoundR1_SplitCarriesVersions(t *testing.T) {
	fr := []JournalFrame{{Command: advCmd(5)}, {Command: advCmd(10), Version: 1}, {Command: advCmd(3), Version: 2}}
	for _, c := range []struct {
		at   int64
		want []rules.Version
		n0   int64
	}{{4, []rules.Version{0, 1, 2}, 1}, {5, []rules.Version{1, 2}, 10}, {8, []rules.Version{1, 2}, 7}, {15, []rules.Version{2}, 3}, {16, []rules.Version{2}, 2}} {
		tail, err := splitJournalAtTick(fr, c.at, ruleTestCity, "r1")
		if err != nil {
			t.Fatal(err)
		}
		var got []rules.Version
		for _, f := range tail {
			got = append(got, f.Version)
		}
		if !reflect.DeepEqual(got, c.want) || tail[0].Payload.(protocol.AdvanceTicksPayload).N != c.n0 {
			t.Fatalf("at %d: versions %v first N %v", c.at, got, tail[0].Payload)
		}
	}
}
