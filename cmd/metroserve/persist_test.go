package main

import (
	"bytes"
	"context"
	"os"
	"strings"
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

// TestSetUpPersistence_GameModeRestartRefusesOnMismatch is BUG-737's round
// finding P1-4 headline scenario, made executable exactly as the round's
// own wording specified: "boot unlimited on a fresh persist dir, stop,
// reboot real". Engine A boots -game-mode "unlimited" against a fresh
// persist dir (durably stamping "unlimited" as the city's ORIGINATING
// mode via compose.Wire's own SetGameModeIfAbsent call). Engine B then
// attempts to boot the SAME city with "real" — this must REFUSE with a
// registry error naming BOTH modes (checkGameMode, snapshot.go), never
// silently proceed in either mode (the round's named P2: "a silent
// overrule").
func TestSetUpPersistence_GameModeRestartRefusesOnMismatch(t *testing.T) {
	dir := t.TempDir()

	eA := newEngine()
	compA, storeA, err := setUpPersistence(eA, dir, "modecity", &bytes.Buffer{}, "unlimited")
	if err != nil {
		t.Fatalf("setUpPersistence (A, unlimited): %v", err)
	}
	if got := compA.GameMode(); got != "unlimited" {
		t.Fatalf("compA.GameMode() = %q, want %q", got, "unlimited")
	}
	// Drive at least one real command so this city has genuine durable
	// history, not merely a mode/seed sidecar — the realistic "stop"
	// half of the scenario.
	submitAll(t, eA, rtCommands())

	city := persist.CityKey{TenantID: persistTenantID, CityID: "modecity"}
	recordedMode, ok, err := storeA.GameMode(context.Background(), city)
	if err != nil || !ok || recordedMode != "unlimited" {
		t.Fatalf("precondition: durable GameMode = (%q, %v, %v), want (\"unlimited\", true, nil)", recordedMode, ok, err)
	}

	eB := newEngine()
	_, _, err = setUpPersistence(eB, dir, "modecity", &bytes.Buffer{}, "real")
	if err == nil {
		t.Fatal("setUpPersistence (B, real) succeeded against a durably-unlimited city, want a refusal naming both modes")
	}
	msg := err.Error()
	if !containsAll(msg, "unlimited", "real") {
		t.Fatalf("mismatch error = %q, want it to name BOTH the recorded mode (unlimited) and the requested one (real)", msg)
	}

	// The durable record itself must be UNCHANGED by the refused attempt
	// — a silent overrule would have left "real" on record instead.
	recordedMode2, ok2, err := storeA.GameMode(context.Background(), city)
	if err != nil || !ok2 || recordedMode2 != "unlimited" {
		t.Fatalf("durable GameMode after the refused reboot = (%q, %v, %v), want unchanged (\"unlimited\", true, nil)", recordedMode2, ok2, err)
	}
}

// TestSetUpPersistence_GameModeDeletedSidecarRefusesNotRemode is BUG-737's
// round finding P1-4's second named scenario: deleting gamemode.json
// between boots on a city that was already Wired (has a recorded world
// seed) must REFUSE the next boot, never silently (re-)stamp whatever
// mode that boot happens to request.
func TestSetUpPersistence_GameModeDeletedSidecarRefusesNotRemode(t *testing.T) {
	dir := t.TempDir()

	eA := newEngine()
	_, storeA, err := setUpPersistence(eA, dir, "delcity", &bytes.Buffer{}, "unlimited")
	if err != nil {
		t.Fatalf("setUpPersistence (A, unlimited): %v", err)
	}
	submitAll(t, eA, rtCommands())

	city := persist.CityKey{TenantID: persistTenantID, CityID: "delcity"}
	disk, ok := storeA.(*persist.DiskStore)
	if !ok {
		t.Fatalf("storeA is %T, want *persist.DiskStore (setUpPersistence with a non-empty persistDir)", storeA)
	}
	sidecar := disk.GameModeSidecarPath(city)
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("gamemode.json sidecar missing before deletion: %v", err)
	}
	if err := os.Remove(sidecar); err != nil {
		t.Fatalf("delete gamemode.json: %v", err)
	}

	eB := newEngine()
	_, _, err = setUpPersistence(eB, dir, "delcity", &bytes.Buffer{}, "real")
	if err == nil {
		t.Fatal("setUpPersistence (B, real) succeeded against a city with a deleted gamemode.json sidecar, want a refusal — a missing sidecar on an already-Wired city must never be silently re-stamped")
	}
}

// TestSetUpPersistence_LegacyPersistDirMigratesWithWarning is the
// round-2 lead ruling's headline fix (2026-09-05): the LIVE Azure
// dogfood city scenario — a persist dir with a durably recorded world
// seed and real journal history, but NO gamemode.json and no epoch
// marker at all, because it was created before FEAT-143 existed. This
// simulates that dir by hand, going straight through the real
// *persist.DiskStore (never a test-only shortcut), and proves the
// FIRST post-upgrade boot succeeds (never MET-E820's "(missing)"
// refusal the original P1-4 design produced), while a SECOND boot with
// a genuinely different mode NOW correctly refuses — the city is fully
// migrated from its first touch onward.
func TestSetUpPersistence_LegacyPersistDirMigratesWithWarning(t *testing.T) {
	dir := t.TempDir()
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	city := persist.CityKey{TenantID: persistTenantID, CityID: "legacycity"}

	// Simulate the pre-FEAT-143 world: SetWorldSeedIfAbsent (BUG-488)
	// and a real journal record, exactly what a genuine dogfood city
	// would have — never SetGameModeIfAbsent/SetGameModeEpoch, since
	// those concepts did not exist when this hypothetical city was
	// created.
	if _, err := disk.SetWorldSeedIfAbsent(context.Background(), city, inc4Seed); err != nil {
		t.Fatalf("SetWorldSeedIfAbsent (simulate legacy): %v", err)
	}
	legacyCmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("legacy-pre-feat143"),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 1},
	}
	legacyRecord, err := protocol.EncodeCommand(legacyCmd)
	if err != nil {
		t.Fatalf("EncodeCommand (simulate legacy): %v", err)
	}
	if err := disk.AppendJournal(context.Background(), city, legacyRecord); err != nil {
		t.Fatalf("AppendJournal (simulate legacy): %v", err)
	}
	if _, ok, err := disk.GameMode(context.Background(), city); err != nil || ok {
		t.Fatalf("precondition: GameMode = (_, %v, %v), want ok=false (no mode ever recorded)", ok, err)
	}
	if has, err := disk.HasGameModeEpoch(context.Background(), city); err != nil || has {
		t.Fatalf("precondition: HasGameModeEpoch = (%v, %v), want false (never touched by mode-aware code)", has, err)
	}

	// First post-upgrade boot: real binary path (setUpPersistence),
	// requesting "unlimited". Must SUCCEED — the round-2 fix's entire
	// point — not refuse with MET-E820's "(missing)" mismatch the
	// original design produced against exactly this shape of dir.
	eA := newEngine()
	compA, _, err := setUpPersistence(eA, dir, "legacycity", &bytes.Buffer{}, "unlimited")
	if err != nil {
		t.Fatalf("setUpPersistence (legacy city, first post-upgrade boot) refused, want the one-time migration to succeed: %v", err)
	}
	if got := compA.GameMode(); got != "unlimited" {
		t.Fatalf("compA.GameMode() = %q, want %q", got, "unlimited")
	}

	// The migration must have durably stamped BOTH the mode and the
	// epoch, so this city is fully governed by the normal match/
	// mismatch rule from here on.
	recordedMode, ok, err := disk.GameMode(context.Background(), city)
	if err != nil || !ok || recordedMode != "unlimited" {
		t.Fatalf("after migration: GameMode = (%q, %v, %v), want (\"unlimited\", true, nil)", recordedMode, ok, err)
	}
	if has, err := disk.HasGameModeEpoch(context.Background(), city); err != nil || !has {
		t.Fatalf("after migration: HasGameModeEpoch = (%v, %v), want (true, nil)", has, err)
	}

	// Second boot, a genuinely DIFFERENT mode: now correctly refuses —
	// this city is no longer "legacy", it is "already migrated".
	eB := newEngine()
	_, _, err = setUpPersistence(eB, dir, "legacycity", &bytes.Buffer{}, "real")
	if err == nil {
		t.Fatal("setUpPersistence (legacy city, second boot, different mode) succeeded, want a refusal — the migration must be a ONE-TIME event, never a standing bypass")
	}
	if !containsAll(err.Error(), "unlimited", "real") {
		t.Fatalf("mismatch error = %q, want it to name both the migrated mode (unlimited) and the requested one (real)", err)
	}
}

// containsAll reports whether hay contains every one of needles as a
// substring (small local helper — this file has no existing multi-needle
// contains check to reuse).
func containsAll(hay string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(hay, n) {
			return false
		}
	}
	return true
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
