package main

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079936 Phase 1 inc4 — metroserve durable persistence + rehydrate.
//
// These tests exercise the setUpPersistence seam (persist.go) — the wiring
// AC-4 factors out of run() so persist+rehydrate is provable without booting
// the HTTP server. The acceptance bar is a lossless restart round-trip: a city
// persisted by engine A, rebuilt into a fresh engine B pointed at the same
// dir, must reach a byte-identical StateDigest.

const inc4Seed = uint64(20260831)

// newEngine constructs an engine at the fixed test seed with a single-citizen
// pool, matching inc2's persist round-trip tests (deterministic + fast).
func newEngine() *core.Engine {
	return core.NewEngine(core.WithWorldSeed(inc4Seed), core.WithPoolSize(1))
}

// rtCommands is a deterministic, fully-accepted command sequence mirroring
// inc2's persistjournal_test.rtCommands: clock advances (which run the phase
// pipeline so the build order below progresses) mixed with buy/zone/build, so
// the resulting StateDigest genuinely moves off the boot state.
func rtCommands() []protocol.Command {
	cell := protocol.CellRef{X: 3, Y: 3}
	mk := func(id string, kind protocol.Kind, payload protocol.CommandPayload) protocol.Command {
		return protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(id),
			Kind:            kind,
			Payload:         payload,
		}
	}
	return []protocol.Command{
		mk("rt-adv-1", protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 5}),
		mk("rt-buy", protocol.KindBuy, protocol.BuyPayload{Cell: cell}),
		mk("rt-zone", protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"}),
		mk("rt-build", protocol.KindBuild, protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"}),
		mk("rt-adv-2", protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 60}),
	}
}

// submitAll drives cmds through e.HandleCommand in order, failing the test if
// any command is rejected (the sequence is designed to be fully accepted).
func submitAll(t *testing.T, e *core.Engine, cmds []protocol.Command) {
	t.Helper()
	for i, cmd := range cmds {
		if res := e.HandleCommand(cmd); !res.Accepted {
			t.Fatalf("command %d (%s) rejected: %+v", i, cmd.Kind, res.Error)
		}
	}
}

// TestSetUpPersistence_RestartRoundTrip is the acceptance bar (AC-4): a city
// persisted by engine A rehydrates losslessly into a fresh engine B pointed at
// the SAME dir via setUpPersistence, and a THIRD engine C proves the restart is
// idempotent (the rehydrate guard did not re-append the replayed journal —
// restart-twice must not diverge). Prove-can-fail: a genesis engine that never
// rehydrated has a different digest, so the equality is meaningful.
func TestSetUpPersistence_RestartRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// Engine A: fresh boot with persistence on, submit the command sequence
	// (durably journaled), snapshot the digest.
	eA := newEngine()
	compA, storeA, err := setUpPersistence(eA, dir, "rt", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("setUpPersistence (A, fresh): %v", err)
	}
	if storeA == nil {
		t.Fatal("persist on: setUpPersistence returned a nil Store")
	}
	submitAll(t, eA, rtCommands())
	digestA := compA.StateDigest()

	// Engine B: fresh engine, SAME dir → rehydrate. Digest must match A.
	eB := newEngine()
	compB, _, err := setUpPersistence(eB, dir, "rt", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("setUpPersistence (B, rehydrate): %v", err)
	}
	digestB := compB.StateDigest()
	if digestA != digestB {
		t.Fatalf("restart is lossy: A digest %x != B digest %x", digestA, digestB)
	}

	// Engine C: rehydrate AGAIN. If the rehydrate guard failed to suppress the
	// replayed commands' re-append, B's rehydrate would have doubled the
	// journal and C would diverge. Restart-twice losslessness.
	eC := newEngine()
	compC, _, err := setUpPersistence(eC, dir, "rt", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("setUpPersistence (C, second rehydrate): %v", err)
	}
	if digestA != compC.StateDigest() {
		t.Fatalf("second restart diverged (journal double-append?): A %x != C %x", digestA, compC.StateDigest())
	}

	// Prove-can-fail: a genesis engine that was NEVER rehydrated has a
	// different digest — otherwise the equality checks above are vacuous.
	eGenesis := newEngine()
	compGenesis, err := compose.Wire(eGenesis, nil)
	if err != nil {
		t.Fatalf("Wire (genesis control): %v", err)
	}
	if compGenesis.StateDigest() == digestA {
		t.Fatal("a never-rehydrated genesis engine matched the persisted digest — the round-trip check cannot detect divergence (false-pass)")
	}
}

// TestSetUpPersistence_DefaultOff proves persistDir "" takes the compose.Wire(e,
// nil) path: no Store is created, and the engine still boots and accepts
// commands (byte-for-byte-unchanged default behaviour).
func TestSetUpPersistence_DefaultOff(t *testing.T) {
	e := newEngine()
	comp, store, err := setUpPersistence(e, "", "unused", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("setUpPersistence (default off): %v", err)
	}
	if store != nil {
		t.Fatalf("persist off must create no Store, got %T", store)
	}
	if comp == nil {
		t.Fatal("persist off returned a nil Composition")
	}
	// A nil-store run boots and processes commands normally.
	submitAll(t, e, rtCommands())
	_ = comp.StateDigest()
}

// TestSetUpPersistence_CityIsolation proves two different --city values under
// the SAME persist-dir keep independent journals: city A's restart rehydrates
// A's state, never B's.
func TestSetUpPersistence_CityIsolation(t *testing.T) {
	dir := t.TempDir()

	// City A: the base sequence.
	eA := newEngine()
	compA, _, err := setUpPersistence(eA, dir, "cityA", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("setUpPersistence (cityA): %v", err)
	}
	submitAll(t, eA, rtCommands())
	digestA := compA.StateDigest()

	// City B: a DIFFERENT sequence (one extra tick) so the two cities' states
	// genuinely differ.
	eB := newEngine()
	compB, _, err := setUpPersistence(eB, dir, "cityB", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("setUpPersistence (cityB): %v", err)
	}
	cmdsB := append(rtCommands(), protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("b-extra-adv"),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 1},
	})
	submitAll(t, eB, cmdsB)
	digestB := compB.StateDigest()

	if digestA == digestB {
		t.Fatal("test setup invalid: the two cities produced identical digests, so isolation cannot be observed")
	}

	// Restart city A: must rehydrate to A's digest, unaffected by city B.
	eA2 := newEngine()
	compA2, _, err := setUpPersistence(eA2, dir, "cityA", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("setUpPersistence (cityA restart): %v", err)
	}
	if compA2.StateDigest() != digestA {
		t.Fatalf("city isolation broken: cityA restart digest %x != %x (cross-loaded cityB?)", compA2.StateDigest(), digestA)
	}
}

// TestSetUpPersistence_CorruptJournalFatal proves a corrupt (undecodable)
// journal makes rehydrate FATAL — an error is returned, never a silent fresh
// start over a persisted city (the data loss this epic kills).
func TestSetUpPersistence_CorruptJournalFatal(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Plant a garbage frame directly on disk for city "corrupt".
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	city := persist.CityKey{TenantID: persistTenantID, CityID: "corrupt"}
	if err := disk.AppendJournal(ctx, city, []byte("{not a valid command frame")); err != nil {
		t.Fatalf("plant garbage frame: %v", err)
	}

	e := newEngine()
	_, _, err = setUpPersistence(e, dir, "corrupt", &bytes.Buffer{})
	if err == nil {
		t.Fatal("corrupt journal was NOT fatal — setUpPersistence silently started fresh over a persisted city (data loss)")
	}
}

// TestRun_CorruptJournalExitsNonZero proves the fatal corrupt-journal path
// propagates through run() to a non-zero exit (never a silently-fresh server).
func TestRun_CorruptJournalExitsNonZero(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	city := persist.CityKey{TenantID: persistTenantID, CityID: "corrupt"}
	if err := disk.AppendJournal(ctx, city, []byte("{garbage")); err != nil {
		t.Fatalf("plant garbage frame: %v", err)
	}

	// run() takes *os.File for stdout/stderr; give it real temp files. The
	// corrupt-journal error fires in setUpPersistence, BEFORE any port bind, so
	// run() returns non-zero without touching the network.
	outF, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outF.Close() }()
	errF, err := os.CreateTemp(t.TempDir(), "err")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = errF.Close() }()

	code := run([]string{"--persist-dir", dir, "--city", "corrupt", "--addr", "localhost:0"}, outF, errF)
	if code == 0 {
		t.Fatal("run() returned 0 on a corrupt journal — a persisted city was silently overwritten with a fresh start")
	}

	// The stderr message names the failure (aggressive error trapping, GR#1).
	stderrBytes, _ := os.ReadFile(errF.Name())
	if !bytes.Contains(stderrBytes, []byte("metroserve:")) {
		t.Fatalf("run() corrupt-journal exit did not log a metroserve error to stderr; got %q", stderrBytes)
	}
}
