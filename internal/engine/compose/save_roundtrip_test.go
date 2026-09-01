package compose

import (
	"bytes"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079941 AC-6 — the ultimate test: a full composed engine driven
// through a deterministic multi-domain command sequence must round-trip
// through Save/Load with byte-identical per-module state for every one of
// the seven modules that implement the save.Participant contract.

const roundTripSeed = uint64(20260901)

// buildComposition constructs a fresh composed engine at the fixed
// round-trip seed.
func buildComposition(t *testing.T) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	comp, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, comp
}

// driveMultiDomain issues a deterministic gameplay sequence that touches
// world (Buy), build (Zone+Build), finance/citizens/refuse/traffic (the
// tick pipeline) and unlocks (direct mutators — no gameplay command routes
// to unlocks in baseline one). The same sequence run against two
// same-seed compositions produces identical state.
func driveMultiDomain(t *testing.T, e *core.Engine, comp *Composition) {
	t.Helper()
	cid := errs.NewCorrelationID()
	cells := []protocol.CellRef{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 0, Y: 1}}

	// Buy purchases the whole start tile once; every cell below is within
	// it, so a second Buy would be rejected "already owned".
	buy := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("rt-buy"),
		Kind:            protocol.KindBuy,
		Payload:         protocol.BuyPayload{Cell: cells[0]},
	}
	if res := e.HandleCommand(buy); !res.Accepted {
		t.Fatalf("Buy rejected: %+v", res.Error)
	}

	for i, cell := range cells {
		zone := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID("rt-zone"),
			Kind:            protocol.KindZone,
			Payload:         protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"},
		}
		if res := e.HandleCommand(zone); !res.Accepted {
			t.Fatalf("Zone %d rejected: %+v", i, res.Error)
		}
		buildCmd := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID("rt-build"),
			Kind:            protocol.KindBuild,
			Payload:         protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"},
		}
		if res := e.HandleCommand(buildCmd); !res.Accepted {
			t.Fatalf("Build %d rejected: %+v", i, res.Error)
		}
	}

	// Advance three months so the daily/monthly tick pipeline mutates
	// finance/citizens/refuse/traffic/build state.
	if err := e.AdvanceTicks(cid, 3*int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	// unlocks has no baseline-one hook or command — mutate it directly so
	// its saved state is non-default and the round-trip is meaningful for
	// it too.
	if err := comp.state.unlocks.AwardConstructionXP(2_500_000, cid); err != nil {
		t.Fatalf("unlocks.AwardConstructionXP: %v", err)
	}
	if err := comp.state.unlocks.AwardPopulationXP(750, cid); err != nil {
		t.Fatalf("unlocks.AwardPopulationXP: %v", err)
	}
}

// participantStreams drains every participant's Source into a stable
// []byte keyed by shard Kind — the exact record stream save.WriteShard
// would persist, so equality here is a direct proof the module's SAVED
// state is identical, independent of StateDigest's coverage.
func participantStreams(t *testing.T, comp *Composition) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	for _, p := range comp.Participants() {
		src := p.Source()
		var buf bytes.Buffer
		for {
			rec, ok, err := src()
			if err != nil {
				t.Fatalf("participant %q Source error: %v", p.Kind(), err)
			}
			if !ok {
				break
			}
			buf.WriteString(rec.Kind)
			buf.WriteByte(0)
			buf.Write(rec.Data)
			buf.WriteByte('\n')
		}
		out[p.Kind()] = buf.Bytes()
	}
	return out
}

// TestSaveRoundTrip_PerModuleStateIsByteIdentical is the make-or-break
// proof for AC-6: after Save on a driven composition and Load into a fresh
// one, EVERY one of the (now twelve) participants' saved record streams
// matches byte-for-byte. A mismatch names the module whose state did not
// round-trip.
func TestSaveRoundTrip_PerModuleStateIsByteIdentical(t *testing.T) {
	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)
	streamsA := participantStreams(t, compA)

	// Sanity: the driven composition is genuinely non-trivial (some module
	// state moved off its post-Wire default), so an all-empty "round trip"
	// cannot pass vacuously. Twelve participants as of FEAT-1972079947: the
	// seven stateful modules + crime + the stateless market/consumption
	// scaffolds (empty-but-conformant, Aaron 2026-09-01) + attract (closes
	// the LoadAt cross-month-boundary gap, FEAT-1972079947) + the
	// compose-owned ledger participant.
	if len(streamsA) != 12 {
		t.Fatalf("expected 12 participants, got %d", len(streamsA))
	}

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, compB := buildComposition(t)
	if err := compB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	streamsB := participantStreams(t, compB)

	for kind, a := range streamsA {
		b, ok := streamsB[kind]
		if !ok {
			t.Errorf("module %q: present in saved composition, absent after load", kind)
			continue
		}
		if !bytes.Equal(a, b) {
			t.Errorf("module %q: saved state did NOT round-trip (len %d -> %d)", kind, len(a), len(b))
		}
	}
}

// TestSaveRoundTrip_ProveCanFail proves the per-module equality check has
// teeth: a composition loaded from the SAME bundle but then advanced one
// extra tick diverges from the saved streams, so the equality assertion
// above is not vacuously true.
func TestSaveRoundTrip_ProveCanFail(t *testing.T) {
	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)
	streamsA := participantStreams(t, compA)

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	eB, compB := buildComposition(t)
	if err := compB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Mutate the loaded composition — one extra month of ticks.
	if err := eB.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	streamsB := participantStreams(t, compB)

	diverged := false
	for kind, a := range streamsA {
		if !bytes.Equal(a, streamsB[kind]) {
			diverged = true
			break
		}
	}
	if !diverged {
		t.Fatal("prove-can-fail: an extra tick after load produced byte-identical module streams — the equality check cannot detect a real difference")
	}
}

// TestSaveRoundTrip_StateDigestRoundTripsExactly is the FEAT-1972079943
// HEADLINE: after Save→Load into a FRESH composition, the FULL composed
// engine's StateDigest() — every observable it hashes, including crime and the
// composition root's OWN conservation/liveness ledgers — is byte-identical to
// the original. This is the property the prior build could not achieve: crime
// was not a save participant, and the compose-owned durable ledgers
// (moneyFlows, netMigration, consumptionDelivered, vitalBirths/Deaths, and the
// people/money opening+delta pairs) were saved by nothing. Crime is now the
// 8th participant and a compose-owned ledger participant serializes the durable
// fields; the derived mirrors (treasury/citizenWealth) are recomputed from the
// restored finance ledger in Composition.Load. Together the full digest now
// round-trips EXACTLY at the load point.
//
// This is a STATE SNAPSHOT: the digest matches at the load point, but Load does
// NOT restore the engine clock (see TestSaveRoundTrip_IsSnapshotNotTickContinuous).
// The clock-relative BUG-288 ledger-closing trackers are therefore deliberately
// NOT serialized — restoring them onto a tick-0 engine would freeze the ledger
// close (FEAT-1972079944 restores the clock; FEAT-1972079936 inc3's journal
// tail supplies continuation).
func TestSaveRoundTrip_StateDigestRoundTripsExactly(t *testing.T) {
	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)
	digestA := compA.StateDigest()

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	_, compB := buildComposition(t)
	if err := compB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	digestB := compB.StateDigest()

	if digestA != digestB {
		// Surface the compose-owned ledger fields on both sides to localise
		// any residual divergence to a concrete field.
		t.Errorf("full StateDigest did NOT round-trip: A=%x B=%x", digestA, digestB)
		t.Logf("compose ledger A: treasury=%d citizenWealth=%d moneyFlows=%d netMigration=%d consumption=%f vitalBirths=%d vitalDeaths=%d",
			compA.Treasury(), compA.CitizenWealth(), compA.MoneyFlows(), compA.NetMigration(), compA.ConsumptionDelivered(), compA.VitalBirths(), compA.VitalDeaths())
		t.Logf("compose ledger B: treasury=%d citizenWealth=%d moneyFlows=%d netMigration=%d consumption=%f vitalBirths=%d vitalDeaths=%d",
			compB.Treasury(), compB.CitizenWealth(), compB.MoneyFlows(), compB.NetMigration(), compB.ConsumptionDelivered(), compB.VitalBirths(), compB.VitalDeaths())
	}
}

// TestSaveRoundTrip_IsSnapshotNotTickContinuous documents the SNAPSHOT
// boundary explicitly (FEAT-1972079943 verdict): Composition.Save/Load restore
// STATE — the loaded StateDigest matches the original AT the load point — but
// do NOT restore the engine clock. A composition saved at tick>0 loads onto a
// tick-0 engine. This is a positive assertion of the known limitation (not a
// hidden divergence): state is snapshot-exact, the clock is not, so a
// standalone loaded composition is not tick-continuous with the original.
// Clock restoration is FEAT-1972079944; the journal-tail replay
// (FEAT-1972079936 inc3) supplies continuation on top of this state snapshot.
func TestSaveRoundTrip_IsSnapshotNotTickContinuous(t *testing.T) {
	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)

	clockA, err := eA.Clock()
	if err != nil {
		t.Fatalf("Clock (A): %v", err)
	}
	if clockA.Tick() == 0 {
		t.Fatalf("precondition: driven composition A should be at tick>0, got tick=0 — the snapshot-boundary assertion would be vacuous")
	}
	digestA := compA.StateDigest()

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	eB, compB := buildComposition(t)
	if err := compB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// STATE is restored: the digest matches at the load point.
	if compB.StateDigest() != digestA {
		t.Errorf("state snapshot: loaded StateDigest did NOT match original at the load point: A=%x B=%x", digestA, compB.StateDigest())
	}

	// The CLOCK is NOT restored: the loaded engine is at tick 0, documenting
	// the snapshot's known limitation (clock restoration is FEAT-1972079944).
	clockB, err := eB.Clock()
	if err != nil {
		t.Fatalf("Clock (B): %v", err)
	}
	if clockB.Tick() != 0 {
		t.Errorf("snapshot boundary: expected loaded engine clock at tick 0 (Load restores state, not the clock — FEAT-1972079944), got tick=%d", clockB.Tick())
	}
}

// TestSaveRoundTrip_StateDigestProveCanFail proves the exact-round-trip
// assertion above has teeth: a composition loaded from the SAME bundle but
// then advanced one extra month diverges in StateDigest, so the equality
// assertion is not vacuously true (e.g. a StateDigest that ignored the
// restored fields would match trivially).
func TestSaveRoundTrip_StateDigestProveCanFail(t *testing.T) {
	eA, compA := buildComposition(t)
	driveMultiDomain(t, eA, compA)
	digestA := compA.StateDigest()

	dir := t.TempDir()
	if err := compA.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	eB, compB := buildComposition(t)
	if err := compB.Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Mutate the loaded composition — one extra month of ticks moves module
	// AND compose-owned state, so its digest must differ from the saved one.
	if err := eB.AdvanceTicks(errs.NewCorrelationID(), int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	if compB.StateDigest() == digestA {
		t.Fatal("prove-can-fail: an extra month after load produced an identical StateDigest — the exact-round-trip assertion cannot detect a real difference")
	}
}
