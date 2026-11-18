package compose

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-9995385612 lane 2 test helpers. No module is rule-versioned yet
// (BUG-1224's refusal is lane 4), so the mechanism is proved end to end with
// a TEST-ONLY versioned module (ruleProbe) switching on a test rule
// constant. Nothing here touches the production registry.

// probeDecision is what the probe judged for one accepted command.
type probeDecision struct {
	Kind    protocol.Kind
	Version rules.Version
	NewRule bool
}

// ruleProbe is a fake versioned module AND a core.CommandJournaler: the
// engine calls ObserveCommand while it accepts each command, so what the
// probe records is exactly the version the command was judged under.
type ruleProbe struct {
	get       func() rules.Version
	decisions []probeDecision
}

func (p *ruleProbe) SetRuleVersionSource(f func() rules.Version) { p.get = f }
func (p *ruleProbe) HasRuleVersionSource() bool                  { return p.get != nil }

func (p *ruleProbe) ObserveCommand(cmd protocol.Command) error {
	active := rules.Active(p.get)
	p.decisions = append(p.decisions, probeDecision{Kind: cmd.Kind, Version: active, NewRule: rules.Has(active, rules.RefuseOccupiedCell)})
	return nil
}

var _ core.CommandJournaler = (*ruleProbe)(nil)

// deadModule is registered as versioned but never receives a getter.
type deadModule struct{}

func (deadModule) SetRuleVersionSource(func() rules.Version) {}
func (deadModule) HasRuleVersionSource() bool                { return false }

// frames0 wraps commands as version-0 frames.
func frames0(cmds ...protocol.Command) []JournalFrame {
	out := make([]JournalFrame, len(cmds))
	for i, c := range cmds {
		out[i] = JournalFrame{Command: c}
	}
	return out
}

// guardStore suppresses AppendJournal while replaying, as metroserve's
// rehydrateGuardStore does, and counts the suppressed attempts.
type guardStore struct {
	persist.Store
	replaying atomic.Bool
	attempts  atomic.Int64
}

func (g *guardStore) AppendJournal(ctx context.Context, city persist.CityKey, rec []byte) error {
	if g.replaying.Load() {
		g.attempts.Add(1)
		return nil
	}
	return g.Store.AppendJournal(ctx, city, rec)
}

const ruleTestSeed = uint64(20261118)

var ruleTestCity = defaultPersistCity

// ruleCity is one wired engine with a probe.
type ruleCity struct {
	e     *core.Engine
	c     *Composition
	probe *ruleProbe
	guard *guardStore
}

// newRuleCity wires an engine with a probe as journaler. store nil means no
// persistence. pin < 0 leaves the live version at rules.Current.
func newRuleCity(t *testing.T, store persist.Store, pin rules.Version) *ruleCity {
	t.Helper()
	probe := &ruleProbe{}
	e := core.NewEngine(core.WithWorldSeed(ruleTestSeed), core.WithPoolSize(1))
	d := &Deps{Coastal: quietCoastal(t), CommandJournaler: probe}
	rc := &ruleCity{e: e, probe: probe}
	if store != nil {
		rc.guard = &guardStore{Store: store}
		d.PersistStore, d.PersistCity = rc.guard, ruleTestCity
	}
	c, err := Wire(e, d)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	rc.c = c
	if err := c.state.rv.wire(c.state.cid, "test.probe", probe); err != nil {
		t.Fatalf("rv.wire: %v", err)
	}
	if pin >= 0 {
		if err := c.PinRuleVersion(pin); err != nil {
			t.Fatalf("PinRuleVersion: %v", err)
		}
	}
	return rc
}

// wake wires a fresh engine over store and restores it, replay appends
// suppressed, then lets appends through again.
func wake(t *testing.T, store persist.Store) (*ruleCity, RestoreReport) {
	t.Helper()
	rc := newRuleCity(t, store, -1)
	rc.guard.replaying.Store(true)
	rep, err := RestoreLatestSnapshotOrGenesisReport(context.Background(), rc.e, rc.c, store, ruleTestCity)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	rc.guard.replaying.Store(false)
	return rc, rep
}

var ruleCorr atomic.Int64

func (rc *ruleCity) do(t *testing.T, kind protocol.Kind, payload protocol.CommandPayload) {
	t.Helper()
	res := rc.e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(fmt.Sprintf("rv-%d", ruleCorr.Add(1))),
		Kind:            kind,
		Payload:         payload,
	})
	if !res.Accepted {
		t.Fatalf("%s rejected: %+v", kind, res.Error)
	}
}

func (rc *ruleCity) advance(t *testing.T, n int64) {
	t.Helper()
	rc.do(t, protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: n})
}

// sessionA is the rtCommands sequence (tick 65).
func (rc *ruleCity) sessionA(t *testing.T) {
	t.Helper()
	cell := protocol.CellRef{X: 3, Y: 3}
	rc.advance(t, 5)
	rc.do(t, protocol.KindBuy, protocol.BuyPayload{Cell: cell})
	rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"})
	rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"})
	rc.advance(t, 60)
}

// sessionB zones and builds a second cell and advances 40 ticks.
func (rc *ruleCity) sessionB(t *testing.T) {
	t.Helper()
	cell := protocol.CellRef{X: 4, Y: 3}
	rc.do(t, protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"})
	rc.do(t, protocol.KindBuild, protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"})
	rc.advance(t, 40)
}

func (rc *ruleCity) snapshot(t *testing.T, store persist.Store, cadence int64) {
	t.Helper()
	if _, ok, err := rc.c.MaybeSnapshotEvery(context.Background(), store, ruleTestCity, cadence); err != nil || !ok {
		t.Fatalf("snapshot cadence %d: ok=%v err=%v", cadence, ok, err)
	}
}

// outcome is the state a restore must reproduce.
type outcome struct {
	digest [32]byte
	pop    int64
	money  int64
	tick   int64
}

func (rc *ruleCity) outcome(t *testing.T) outcome {
	t.Helper()
	clock, err := rc.e.Clock()
	if err != nil {
		t.Fatal(err)
	}
	return outcome{
		digest: rc.c.StateDigest(),
		pop:    int64(rc.c.Population()),
		money:  int64(rc.c.Treasury()),
		tick:   clock.Tick(),
	}
}

// fingerprint hashes everything a store holds for the city.
func fingerprint(t *testing.T, store persist.Store) [32]byte {
	t.Helper()
	ctx := context.Background()
	h := sha256.New()
	frames, err := store.ReadJournal(ctx, ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		_, _ = fmt.Fprintf(h, "J%d:", len(f))
		h.Write(f)
	}
	ids, err := store.ListSnapshots(ctx, ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		data, err := store.GetSnapshot(ctx, ruleTestCity, id)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(h, "S%s:%d:", id, len(data))
		h.Write(data)
	}
	seed, ok, err := store.WorldSeed(ctx, ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(h, "W%d:%v", seed, ok)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// cloneStore copies the journal and the snapshots at positions keep
// (oldest-first indexes) into a new MemStore.
func cloneStore(t *testing.T, src persist.Store, keep ...int) persist.Store {
	t.Helper()
	ctx := context.Background()
	dst := persist.NewMemStore()
	frames, err := src.ReadJournal(ctx, ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		if err := dst.AppendJournal(ctx, ruleTestCity, f); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := src.ListSnapshots(ctx, ruleTestCity)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range keep {
		data, err := src.GetSnapshot(ctx, ruleTestCity, ids[i])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := dst.PutSnapshot(ctx, ruleTestCity, data); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}
