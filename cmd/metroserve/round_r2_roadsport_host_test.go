package main

import (
	"context"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-9995385628 port round r2: a hosted city under rules.LayRoad holding a
// built road and a queued road survives shutdown (with and without the final
// snapshot) and wakes to the same state as a city that never slept; the
// queued road then completes the same way.

func roadsPortHostCmds(prefix string, phase int) []protocol.Command {
	mk := func(id string, kind protocol.Kind, p protocol.CommandPayload) protocol.Command {
		return protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: protocol.CorrelationID(prefix + id), Kind: kind, Payload: p}
	}
	adv := func(id string, n int64) protocol.Command {
		return mk(id, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: n})
	}
	if phase == 1 {
		return []protocol.Command{
			mk("buy", protocol.KindBuy, protocol.BuyPayload{Cell: protocol.CellRef{X: 3, Y: 3}}),
			mk("road1", protocol.KindBuild, protocol.BuildPayload{Cell: protocol.CellRef{X: 5, Y: 3}, BuildingType: "road"}),
			adv("a1", 100), adv("a2", 100), adv("a3", 100),
			mk("road2", protocol.KindBuild, protocol.BuildPayload{Cell: protocol.CellRef{X: 6, Y: 3}, BuildingType: "road"}),
			adv("a4", 2),
		}
	}
	return []protocol.Command{adv("b1", 100), adv("b2", 100), adv("b3", 100)}
}

func TestRoundR2RoadsPort_HostedRoadCity_SurvivesShutdownAndWake(t *testing.T) {
	if rules.Effective() < rules.LayRoad {
		t.Fatalf("live version %d below LayRoad", rules.Effective())
	}
	for _, tc := range []struct {
		name   string
		budget time.Duration
	}{{"final snapshot", 30 * time.Second}, {"journal only", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			key := persist.CityKey{TenantID: persistTenantID, CityID: lane5City}
			p1, p2 := roadsPortHostCmds("rp-", 1), roadsPortHostCmds("rp-", 2)
			// Control: the digest sees the queued road (dropping it changes it),
			// so digest equality below is evidence the road order survived.
			noQueued := append(append([]protocol.Command{}, p1[:len(p1)-2]...), p1[len(p1)-1])
			if standaloneDigest(t, key, noQueued) == standaloneDigest(t, key, p1) {
				t.Fatal("control: the state digest does not see a queued road")
			}

			h, logs := newFinalHost(t, dir, tc.budget, time.Hour, nil)
			rc, err := h.GetOrCreate(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			submitAll(t, rc.Engine(), p1)
			before := rc.Composition().StateDigest()
			if want := standaloneDigest(t, key, p1); before != want {
				t.Fatalf("hosted city differs from the standalone control before shutdown")
			}
			_ = h.Close()
			snaps := len(snapshotIDs(t, dir, key))
			if tc.budget > 0 && snaps == 0 {
				t.Fatalf("no final snapshot written on shutdown\nlogs:\n%s", logs.String())
			}

			for wake := 1; wake <= 2; wake++ {
				h2, logs2 := newFinalHost(t, dir, tc.budget, time.Hour, nil)
				rc2, err := h2.GetOrCreate(context.Background(), key)
				if err != nil {
					t.Fatalf("wake %d refused: %v\nlogs:\n%s", wake, err, logs2.String())
				}
				cmds := p1
				if wake == 2 {
					cmds = append(append([]protocol.Command{}, p1...), p2...)
				}
				if got, want := rc2.Composition().StateDigest(), standaloneDigest(t, key, cmds); got != want {
					t.Fatalf("wake %d: woken road city differs from a city that never slept", wake)
				}
				if wake == 1 {
					submitAll(t, rc2.Engine(), p2) // the queued road completes after the wake
					if got, want := rc2.Composition().StateDigest(), standaloneDigest(t, key, append(append([]protocol.Command{}, p1...), p2...)); got != want {
						t.Fatal("the queued road did not continue the same way after the wake")
					}
				}
				_ = h2.Close()
			}
		})
	}
}
