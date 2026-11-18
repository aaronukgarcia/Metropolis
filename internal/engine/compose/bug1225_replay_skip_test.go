package compose

import (
	"context"
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// TestBUG1225_ReplaySkipsSubscribeAndUnsubscribe pins the replay skip in
// replayCommands: a journal holding Subscribes (live and dead owner) and
// Unsubscribes, interleaved with gameplay and ticks, must restore (snapshot
// plus tail, and genesis) to exactly the state of a control that never saw a
// Subscribe, with no subscription and no live owner re-created.
//
// Red under (M1) the whole skip removed (subscriptions re-created) and
// (M2) only the Unsubscribe half removed (restore rejected, MET-G811).
func TestBUG1225_ReplaySkipsSubscribeAndUnsubscribe(t *testing.T) {
	for _, seed := range []uint64{roundTripSeed, 4103} {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			ctx := context.Background()
			mk := func(store persist.Store, city persist.CityKey) (*core.Engine, *Composition) {
				e := core.NewEngine(core.WithWorldSeed(seed), core.WithPoolSize(1))
				d := &Deps{Coastal: quietCoastal(t)}
				if store != nil {
					d.PersistStore, d.PersistCity = store, city
				}
				c, err := Wire(e, d)
				if err != nil {
					t.Fatal(err)
				}
				return e, c
			}
			n := 0
			cmd := func(e *core.Engine, owner uint64, k protocol.Kind, p protocol.CommandPayload) protocol.CommandResult {
				n++
				return e.HandleCommand(protocol.Command{ProtocolVersion: protocol.ProtocolVersion,
					CorrelationID: protocol.CorrelationID(fmt.Sprintf("r4-%d", n)), Kind: k, Payload: p, Owner: owner})
			}
			sub := func(e *core.Engine, owner uint64) {
				if r := cmd(e, owner, protocol.KindSubscribe, protocol.SubscribePayload{ViewName: "engine.status"}); !r.Accepted {
					t.Fatalf("subscribe rejected %+v", r.Error)
				}
			}
			unsub := func(e *core.Engine, id string) {
				if r := cmd(e, 0, protocol.KindUnsubscribe, protocol.UnsubscribePayload{SubscriptionID: protocol.SubscriptionID(id)}); !r.Accepted {
					t.Fatalf("unsubscribe %s rejected: %+v", id, r.Error)
				}
			}
			run := func(e *core.Engine, c *Composition, store persist.Store, city persist.CityKey, withSubs bool) {
				var o1, o2 uint64
				if withSubs {
					o1 = e.NewSubscriptionOwner()
					o2 = e.NewSubscriptionOwner()
					for i := 0; i < 5; i++ {
						sub(e, o1) // sub-1..sub-5
					}
					sub(e, 0) // sub-6
				}
				driveGameplayOnly(t, e) // Buy + Build on three distinct cells
				if withSubs {
					sub(e, o2) // sub-7
				}
				advanceViaCommand(t, e, 160)
				if withSubs {
					e.DetachSubscriptionOwner(o1) // o1 is now a dead owner
					sub(e, o1)                    // dead-owner Subscribes: journaled, create nothing
					sub(e, o1)
					unsub(e, "sub-6") // Unsubscribe of an id that exists
				}
				advanceViaCommand(t, e, 200)
				if store != nil && c != nil {
					if _, ok, err := c.MaybeSnapshot(ctx, store, city); err != nil || !ok {
						t.Fatalf("snapshot ok=%v err=%v", ok, err)
					}
				}
				if withSubs {
					sub(e, o2)
					sub(e, 0)
					unsub(e, "sub-7") // tail Unsubscribe of an id that exists
				}
				advanceViaCommand(t, e, 7)
				if withSubs {
					e.DetachSubscriptionOwner(o2)
					sub(e, o2) // tail dead-owner Subscribe
				}
				advanceViaCommand(t, e, 13)
			}

			eRef, cRef := mk(nil, persist.CityKey{})
			run(eRef, cRef, nil, persist.CityKey{}, false)

			city := persist.CityKey{TenantID: "t", CityID: fmt.Sprintf("r4-%d", seed)}
			snapStore := persist.NewMemStore()
			eL, cL := mk(snapStore, city)
			run(eL, cL, snapStore, city, true)
			genStore := persist.NewMemStore()
			// Genesis session: its own store, no snapshot ever taken.
			eG, cG := mk(genStore, city)
			run(eG, cG, nil, city, true)

			count := func(cmds []JournalFrame, k protocol.Kind) int {
				c := 0
				for _, x := range cmds {
					if x.Kind == k {
						c++
					}
				}
				return c
			}
			// Journal-content assertions (a vacuous test is the defect).
			whole, err := RestoreCommands(ctx, genStore, city)
			if err != nil {
				t.Fatal(err)
			}
			if count(whole, protocol.KindSubscribe) < 5 || count(whole, protocol.KindUnsubscribe) < 2 {
				t.Fatalf("genesis journal lacks session traffic: %d subscribe, %d unsubscribe",
					count(whole, protocol.KindSubscribe), count(whole, protocol.KindUnsubscribe))
			}
			ids, err := snapStore.ListSnapshots(ctx, city)
			if err != nil || len(ids) == 0 {
				t.Fatalf("no snapshot: %v", err)
			}
			data, err := snapStore.GetSnapshot(ctx, city, ids[len(ids)-1])
			if err != nil {
				t.Fatal(err)
			}
			journal, err := RestoreCommands(ctx, snapStore, city)
			if err != nil {
				t.Fatal(err)
			}
			_, tail, ok, err := tryRestoreCandidate(data, journal, city, cL.state.cid, seed)
			if err != nil || !ok {
				t.Fatalf("tail extraction ok=%v err=%v", ok, err)
			}
			if count(tail, protocol.KindSubscribe) < 2 || count(tail, protocol.KindUnsubscribe) < 1 {
				t.Fatalf("snapshot tail lacks session traffic: %d subscribe, %d unsubscribe (tail %d cmds)",
					count(tail, protocol.KindSubscribe), count(tail, protocol.KindUnsubscribe), len(tail))
			}

			for _, tc := range []struct {
				name  string
				store persist.Store
				snap  bool
			}{{"snapshot+tail", snapStore, true}, {"genesis", genStore, false}} {
				eR, cR := mk(nil, city)
				used, _, err := RestoreLatestSnapshotOrGenesis(ctx, eR, cR, tc.store, city)
				if err != nil {
					t.Fatalf("%s restore failed: %v", tc.name, err)
				}
				if used != tc.snap {
					t.Fatalf("%s: usedSnapshot=%v", tc.name, used)
				}
				if cR.StateDigest() != cRef.StateDigest() || cR.PopulationHash() != cRef.PopulationHash() ||
					cR.Population() != cRef.Population() || cR.Treasury() != cRef.Treasury() {
					t.Fatalf("%s: restored state diverges from the never-subscribed control", tc.name)
				}
				if got := eR.SubscriptionCount(); got != 0 {
					t.Fatalf("%s: restored engine holds %d subscriptions", tc.name, got)
				}
				if got := eR.LiveSubscriptionOwners(); got != 0 {
					t.Fatalf("%s: restored engine holds %d live owners", tc.name, got)
				}
			}
		})
	}
}
