package compose

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

type r1SupStore struct {
	persist.Store
	off atomic.Bool
}

func (s *r1SupStore) AppendJournal(ctx context.Context, city persist.CityKey, rec []byte) error {
	if s.off.Load() {
		return nil
	}
	return s.Store.AppendJournal(ctx, city, rec)
}

func r1cmd(id string, kind protocol.Kind, p protocol.CommandPayload) protocol.Command {
	return protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID(id), Kind: kind, Payload: p}
}

func r1line(e *core.Engine, c *Composition, tag string) string {
	cl, _ := e.Clock()
	return fmt.Sprintf("%s digest=%x pop=%d treasury=%d tick=%d\n", tag, c.StateDigest(), c.Population(), c.Treasury(), cl.Tick())
}

func TestRoundR1_V0Session(t *testing.T) {
	out := os.Getenv("ROUND_OUT")
	if out == "" {
		t.Skip("ROUND_OUT unset")
	}
	_ = os.RemoveAll(out)
	ctx := context.Background()
	ds, err := persist.NewDiskStore(filepath.Join(out, "store"))
	if err != nil {
		t.Fatal(err)
	}
	var sum strings.Builder
	st := &r1SupStore{Store: ds}
	e := core.NewEngine(core.WithWorldSeed(persistTestSeed), core.WithPoolSize(1))
	c, err := Wire(e, &Deps{PersistStore: st})
	if err != nil {
		t.Fatal(err)
	}
	submitAll(t, e, rtCommands())
	if _, ok, err := c.MaybeSnapshotEvery(ctx, st, defaultPersistCity, 65); err != nil || !ok {
		t.Fatalf("snap1 %v %v", ok, err)
	}
	cell := protocol.CellRef{X: 4, Y: 3}
	submitAll(t, e, []protocol.Command{
		r1cmd("b-sub", protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "engine.status"}),
		r1cmd("b-zone", protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"}),
		r1cmd("b-build", protocol.KindBuild, protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"}),
		r1cmd("b-adv", protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 40}),
	})
	sum.WriteString(r1line(e, c, "live"))
	// eviction: final snapshot (cadence 1 forces)
	if _, ok, err := c.MaybeSnapshotEvery(ctx, st, defaultPersistCity, 1); err != nil || !ok {
		t.Fatalf("snap2 %v %v", ok, err)
	}
	// more commands after final snapshot, then wake
	submitAll(t, e, []protocol.Command{r1cmd("c-adv", protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 7})})
	sum.WriteString(r1line(e, c, "live2"))
	// wake
	st2 := &r1SupStore{Store: ds}
	e2 := core.NewEngine(core.WithWorldSeed(persistTestSeed), core.WithPoolSize(1))
	c2, err := Wire(e2, &Deps{PersistStore: st2})
	if err != nil {
		t.Fatal(err)
	}
	st2.off.Store(true)
	used, tick, err := RestoreLatestSnapshotOrGenesis(ctx, e2, c2, st2, defaultPersistCity)
	st2.off.Store(false)
	if err != nil {
		t.Fatal(err)
	}
	sum.WriteString(fmt.Sprintf("wake used=%v tick=%d\n", used, tick))
	sum.WriteString(r1line(e2, c2, "woken"))
	submitAll(t, e2, []protocol.Command{r1cmd("d-adv", protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 3})})
	if _, ok, err := c2.MaybeSnapshotEvery(ctx, st2, defaultPersistCity, 1); err != nil || !ok {
		t.Fatalf("snap3 %v %v", ok, err)
	}
	// genesis restore from a journal-only copy
	e3 := core.NewEngine(core.WithWorldSeed(persistTestSeed), core.WithPoolSize(1))
	c3, err := Wire(e3, nil)
	if err != nil {
		t.Fatal(err)
	}
	ms := persist.NewMemStore()
	fr, _ := ds.ReadJournal(ctx, defaultPersistCity)
	for _, f := range fr {
		_ = ms.AppendJournal(ctx, defaultPersistCity, f)
	}
	if _, _, err := RestoreLatestSnapshotOrGenesis(ctx, e3, c3, ms, defaultPersistCity); err != nil {
		t.Fatal(err)
	}
	sum.WriteString(r1line(e3, c3, "genesis"))
	// unpack every snapshot
	ids, _ := ds.ListSnapshots(ctx, defaultPersistCity)
	for _, id := range ids {
		data, _ := ds.GetSnapshot(ctx, defaultPersistCity, id)
		d := filepath.Join(out, "unz", string(id))
		if err := unzipDir(data, d); err != nil {
			t.Fatal(err)
		}
	}
	// hash every file
	var lines []string
	_ = filepath.Walk(out, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(out, p)
			lines = append(lines, fmt.Sprintf("%x %d %s", sha256.Sum256(b), len(b), filepath.ToSlash(rel)))
		}
		return nil
	})
	sort.Strings(lines)
	sum.WriteString(strings.Join(lines, "\n") + "\n")
	_ = os.WriteFile(out+".summary.txt", []byte(sum.String()), 0o644)
}
