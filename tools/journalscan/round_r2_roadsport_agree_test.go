package main

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-9995385628 port round r2: under rules.LayRoad the dry-run pre-check,
// the static journalscan verdict and the real restore must agree on a journal
// that mixes legacy frames, frames stamped at the live version and a road
// build, and on that journal with a newer build's frame or snapshot.

func roadsPortCmd(id string, kind protocol.Kind, p protocol.CommandPayload) protocol.Command {
	return protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID(id), Kind: kind, Payload: p}
}

// craftRoadCity records version-0 frames, then switches to the live rules
// (LayRoad), buys a tile, lays a road and snapshots; a road and an advance
// follow the snapshot.
func craftRoadCity(t *testing.T, dir string) {
	t.Helper()
	if rules.Effective() < rules.LayRoad {
		t.Fatalf("this build's live version %d is below LayRoad", rules.Effective())
	}
	store, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := core.NewEngine(core.WithWorldSeed(20261118), core.WithPoolSize(1))
	c, err := compose.Wire(e, &compose.Deps{PersistStore: store, PersistCity: scanKey})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.PinRuleVersion(0); err != nil {
		t.Fatal(err)
	}
	if res := e.HandleCommand(cmd("rp-a", 3)); !res.Accepted {
		t.Fatalf("v0 advance refused: %+v", res.Error)
	}
	if err := c.PinRuleVersion(rules.LayRoad); err != nil {
		t.Fatal(err)
	}
	home := protocol.CellRef{X: 3, Y: 3}
	steps := []protocol.Command{
		roadsPortCmd("rp-buy", protocol.KindBuy, protocol.BuyPayload{Cell: home}),
		roadsPortCmd("rp-road1", protocol.KindBuild, protocol.BuildPayload{Cell: protocol.CellRef{X: 5, Y: 3}, BuildingType: "road"}),
		cmd("rp-b", 40),
	}
	for _, s := range steps {
		if res := e.HandleCommand(s); !res.Accepted {
			t.Fatalf("%s refused: %+v", s.CorrelationID, res.Error)
		}
	}
	if _, ok, err := c.MaybeSnapshotEvery(context.Background(), store, scanKey, 1); err != nil || !ok {
		t.Fatalf("snapshot: ok=%v err=%v", ok, err)
	}
	for _, s := range []protocol.Command{
		roadsPortCmd("rp-road2", protocol.KindBuild, protocol.BuildPayload{Cell: protocol.CellRef{X: 6, Y: 3}, BuildingType: "road"}),
		cmd("rp-c", 5),
	} {
		if res := e.HandleCommand(s); !res.Accepted {
			t.Fatalf("%s refused: %+v", s.CorrelationID, res.Error)
		}
	}
}

func roadsPortPrecheck(t *testing.T, dir string) string {
	t.Helper()
	st, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = compose.PrecheckRuleVersions(context.Background(), st, scanKey, 20261118, "")
	if err == nil {
		return ""
	}
	var ee *errs.E
	if errors.As(err, &ee) {
		return ee.Code
	}
	t.Fatalf("precheck error without a code: %v", err)
	return ""
}

func TestRoundR2RoadsPort_LiveRoadJournal_AllThreeAgree(t *testing.T) {
	cases := []struct {
		name, want string
		mutate     func(t *testing.T, dir string)
	}{
		{"legacy, live and road frames", "", func(*testing.T, string) {}},
		{"plus a newer build's frame", "MET-G5610", func(t *testing.T, dir string) {
			appendRaw(t, dir, envelope(t, strconv.Itoa(int(rules.Current)+1)))
		}},
		{"snapshot from a newer build", "MET-G5610", func(t *testing.T, dir string) {
			restampSnapshot(t, dir, int(rules.Current)+1)
		}},
		{"a frame stamped at the live version", "", func(t *testing.T, dir string) {
			appendRaw(t, dir, envelope(t, stampOf(rules.LayRoad)))
		}},
		{"snapshot stamped above its tail", "MET-G5612", func(t *testing.T, dir string) {
			appendRaw(t, dir, envelope(t, stampOf(rules.RefuseOccupiedCell)))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Three identical copies: the restore may write the seed stamp.
			dirs := [3]string{t.TempDir(), t.TempDir(), t.TempDir()}
			for _, d := range dirs {
				craftRoadCity(t, d)
				tc.mutate(t, d)
			}
			pre := roadsPortPrecheck(t, dirs[0])
			tool := toolVerdict(t, dirs[1])
			real := restoreVerdict(t, dirs[2])
			if pre != real || tool != real || real != tc.want {
				t.Fatalf("precheck %q, journalscan %q, restore %q; want all %q", pre, tool, real, tc.want)
			}
		})
	}
}
