package main

// ADOPTED from the independent re-round-3 attacker (opus-reround3-bug737,
// BUG-737), per the lead's instruction on ACCEPT: these tests pin the
// never-silent property (exactly-one-warning count) that nothing else in
// this tree tests. Sourced from the attacker's archived
// metroserve_attack_rr3_test.go.txt, adapted in ONE place
// (TestRR3_C_TruthTable's "001_epoch_only_no_seed_no_mode" case) to match
// this round's own chosen fix for finding P2-1: SetGameModeEpoch now
// REFUSES on a seedless city (ErrGameModeEpochWithoutSeed) rather than
// materializing a fabricated world_seed:0 record, so that exact on-disk
// state can no longer be constructed through the public Store API at
// all — the adapted case instead hand-writes the raw sidecar bytes
// (simulating external tampering, the only way this shape could still
// arise) and asserts the CURRENT correct outcome for it.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// legacyCityDirRR3 builds an EXACT on-disk replica of the live Azure
// dogfood persist layout as the CURRENTLY DEPLOYED (pre-BUG-737) binary
// writes it: seed.json holding ONLY {"world_seed":N} (the pre-fix
// citySeed shape, no mode_epoch field at all), meta.json, journal.dat
// with a real encoded command record, and NO gamemode.json. Every file
// is written by hand, byte-for-byte, rather than through the NEW store
// API (which the author's own legacy test uses) -- the point of this
// attack is to exercise the real decode path against the real live file
// bytes.
func legacyCityDirRR3(t *testing.T, root, cityID string, seed uint64) (persist.CityKey, string) {
	t.Helper()
	city := persist.CityKey{TenantID: persistTenantID, CityID: cityID}

	// Locate the hashed city directory the same way the author's own
	// test-support accessor does, then write the live layout by hand.
	disk, err := persist.NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	dir := filepath.Dir(disk.GameModeSidecarPath(city))
	if dir == "." || dir == "" {
		t.Fatalf("could not resolve city dir for %+v", city)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir city dir: %v", err)
	}

	// seed.json EXACTLY as the live pre-BUG-737 binary marshals it.
	seedBytes, err := json.Marshal(struct {
		WorldSeed uint64 `json:"world_seed"`
	}{WorldSeed: seed})
	if err != nil {
		t.Fatalf("marshal legacy seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "seed.json"), seedBytes, 0o644); err != nil {
		t.Fatalf("write legacy seed.json: %v", err)
	}
	t.Logf("legacy seed.json bytes = %s", seedBytes)

	// A real journal record, appended through the store so the on-disk
	// framing is byte-identical to a live city's.
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("rr3-legacy"),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 1},
	}
	rec, err := protocol.EncodeCommand(cmd)
	if err != nil {
		t.Fatalf("EncodeCommand: %v", err)
	}
	if err := disk.AppendJournal(context.Background(), city, rec); err != nil {
		t.Fatalf("AppendJournal: %v", err)
	}

	// Preconditions: no gamemode.json, no epoch, seed readable.
	if _, err := os.Stat(filepath.Join(dir, "gamemode.json")); !os.IsNotExist(err) {
		t.Fatalf("precondition: gamemode.json exists (%v), want absent", err)
	}
	if got, ok, err := disk.WorldSeed(context.Background(), city); err != nil || !ok || got != seed {
		t.Fatalf("precondition: WorldSeed = (%d,%v,%v), want (%d,true,nil)", got, ok, err, seed)
	}
	if has, err := disk.HasGameModeEpoch(context.Background(), city); err != nil || has {
		t.Fatalf("precondition: HasGameModeEpoch = (%v,%v), want false", has, err)
	}
	return city, dir
}

// countCodeForCityRR3 counts entries carrying BOTH code and a "city" ctx
// value matching city exactly. errs.Recent() is a single GLOBAL,
// FIXED-CAPACITY (200) ring buffer shared by the entire test binary --
// this file's tests must run correctly inside the full cmd/metroserve
// package suite, not just in isolation, so a naive "after[before:]"
// slice comparison is unsound: once the ring is already at capacity
// (near-certain deep into a large package's test run), len(Recent())
// stays pinned at 200 on every call regardless of new pushes (the ring
// overwrites the oldest slot rather than growing), making
// after[before:] silently empty even when a genuine new entry WAS
// pushed. Diffing per-city COUNTS across two full snapshots, instead of
// slicing, stays correct under eviction as long as this exact city's
// own entries are not themselves evicted within one test (true here --
// each test uses its own unique city name specifically so its count
// cannot be confused with another test's).
func countCodeForCityRR3(entries []errs.Entry, code, city string) int {
	n := 0
	for _, e := range entries {
		if e.Code != code {
			continue
		}
		if c, ok := e.Ctx["city"]; ok && c == city {
			n++
		}
	}
	return n
}

// TestRR3_A_AzureReplicaBootsOnceWarnsOnce is attack (A): the exact live
// layout must boot in real mode with EXACTLY ONE MET-G5420, write the
// epoch, boot again silently, and refuse an unlimited boot afterwards.
func TestRR3_A_AzureReplicaBootsOnceWarnsOnce(t *testing.T) {
	root := t.TempDir()
	city, dir := legacyCityDirRR3(t, root, "azurecity", inc4Seed)
	cityCtxValue := persistTenantID + "/" + city.CityID

	before := countCodeForCityRR3(errs.Recent(), "MET-G5420", cityCtxValue)

	eA := newEngine()
	compA, storeA, err := setUpPersistence(eA, root, "azurecity", &bytes.Buffer{}, "real")
	if err != nil {
		t.Fatalf("BOOT 1 (real) against the exact live Azure layout REFUSED: %v", err)
	}
	if got := compA.GameMode(); got != "real" {
		t.Fatalf("boot1 GameMode = %q, want real", got)
	}
	// Count by DIFFING per-city occurrence counts across two full
	// errs.Recent() snapshots, never by slicing "after[before:]" -- see
	// countCodeForCityRR3's own doc comment for why: this test must
	// stay correct running inside the full cmd/metroserve package
	// suite, where the shared global ring buffer is very likely already
	// at its 200-entry capacity by the time this test runs.
	got := countCodeForCityRR3(errs.Recent(), "MET-G5420", cityCtxValue) - before
	if got != 1 {
		t.Fatalf("boot 1 raised MET-G5420 %d times for city %q, want exactly 1", got, cityCtxValue)
	}

	// The epoch AND the mode must now be durable.
	if has, err := storeA.HasGameModeEpoch(context.Background(), city); err != nil || !has {
		t.Fatalf("after boot 1: HasGameModeEpoch = (%v,%v), want true", has, err)
	}
	if m, ok, err := storeA.GameMode(context.Background(), city); err != nil || !ok || m != "real" {
		t.Fatalf("after boot 1: GameMode = (%q,%v,%v), want (real,true,nil)", m, ok, err)
	}
	// seed.json must STILL carry the original world seed.
	raw, err := os.ReadFile(filepath.Join(dir, "seed.json"))
	if err != nil {
		t.Fatalf("read seed.json after boot 1: %v", err)
	}
	t.Logf("seed.json after epoch write = %s", raw)
	var reread struct {
		WorldSeed uint64 `json:"world_seed"`
		ModeEpoch bool   `json:"mode_epoch"`
	}
	if err := json.Unmarshal(raw, &reread); err != nil {
		t.Fatalf("decode seed.json after boot 1: %v", err)
	}
	if reread.WorldSeed != inc4Seed {
		t.Fatalf("EPOCH WRITE CLOBBERED THE SEED: world_seed = %d, want %d", reread.WorldSeed, inc4Seed)
	}
	if !reread.ModeEpoch {
		t.Fatalf("mode_epoch not written to seed.json")
	}

	// BOOT 2, same mode: must succeed with NO warning.
	before2 := countCodeForCityRR3(errs.Recent(), "MET-G5420", cityCtxValue)
	eB := newEngine()
	if _, _, err := setUpPersistence(eB, root, "azurecity", &bytes.Buffer{}, "real"); err != nil {
		t.Fatalf("BOOT 2 (real) refused: %v", err)
	}
	if n := countCodeForCityRR3(errs.Recent(), "MET-G5420", cityCtxValue) - before2; n != 0 {
		t.Fatalf("boot 2 raised MET-G5420 %d times for city %q, want 0 (the migration must be ONE-TIME)", n, cityCtxValue)
	}

	// BOOT 3, unlimited: must refuse.
	eC := newEngine()
	_, _, err = setUpPersistence(eC, root, "azurecity", &bytes.Buffer{}, "unlimited")
	if err == nil {
		t.Fatal("BOOT 3 (unlimited) succeeded against a city migrated to real, want a refusal")
	}
	if !strings.Contains(err.Error(), "unlimited") || !strings.Contains(err.Error(), "real") {
		t.Fatalf("refusal %q does not name both modes", err)
	}
}

// TestRR3_A2_AzureReplicaBootedUnlimitedFirst is attack (A)'s mirror:
// the SAME live layout booted UNLIMITED first must also migrate (the
// legacy path is mode-agnostic), and a later real boot must refuse.
func TestRR3_A2_AzureReplicaBootedUnlimitedFirst(t *testing.T) {
	root := t.TempDir()
	legacyCityDirRR3(t, root, "azurecity2", inc4Seed)

	eA := newEngine()
	if _, _, err := setUpPersistence(eA, root, "azurecity2", &bytes.Buffer{}, "unlimited"); err != nil {
		t.Fatalf("legacy boot in unlimited refused: %v", err)
	}
	eB := newEngine()
	if _, _, err := setUpPersistence(eB, root, "azurecity2", &bytes.Buffer{}, "real"); err == nil {
		t.Fatal("second boot in real succeeded after a legacy unlimited migration, want refusal")
	}
}

// TestRR3_C_TruthTable enumerates the (seed present) x (gamemode
// present) x (epoch present) states reachable on disk and asserts each
// one's outcome, including the states the author's own tests do not
// cover: gamemode present + seed MISSING, and gamemode present but an
// EMPTY string.
func TestRR3_C_TruthTable(t *testing.T) {
	type tc struct {
		name       string
		seed       bool
		mode       string // "" => no gamemode.json at all
		epoch      bool
		wantRefuse bool
	}
	cases := []tc{
		{"000_fresh", false, "", false, false},
		// ADAPTED (this round's own P2-1 fix, 2026-09-05): the attacker's
		// original case here expected a refusal, on the theory that
		// SetGameModeEpoch on a seedless city fabricates world_seed:0
		// (round-2's bug). This round's chosen fix for P2-1 makes
		// SetGameModeEpoch REFUSE outright on a seedless city instead
		// (ErrGameModeEpochWithoutSeed) -- so that exact state can no
		// longer be constructed through the public Store API at all; see
		// internal/persist's TestRR3_C_EpochFirstFabricatesWorldSeedZero
		// (adapted identically) for the store-level proof of that
		// refusal. The only way this on-disk SHAPE can still exist is
		// external tampering (a hand-edited/corrupted seed.json carrying
		// ONLY mode_epoch, with no world_seed key at all), which this
		// case now constructs directly to observe what checkGameMode
		// actually does with it: WorldSeed's own ok=true/false signal is
		// FILE-presence-based (BUG-488's original contract), not
		// key-presence-based, so a seed.json that exists but happens to
		// be missing the "world_seed" key still decodes ok=true with
		// seed=0 (the Go zero value) -- checkGameMode therefore reads
		// this as "already Wired before" (a seed IS on record, just
		// zero), and with the tampered file's mode_epoch=true also
		// present, correctly lands on case 4 (already-migrated,
		// sidecar-equivalent-missing) and REFUSES -- the safe,
		// fail-closed outcome for a state this ambiguous, still true
		// after adapting for P2-1.
		{"001_epoch_only_no_seed_no_mode", false, "", true, true},
		{"010_mode_only_no_seed", false, "real", false, false},
		{"100_legacy_seed_only", true, "", false, false},
		{"101_seed+epoch_mode_missing", true, "", true, true},
		{"110_seed+mode_no_epoch", true, "real", false, false},
		{"111_all_present_match", true, "real", true, false},
		{"111_all_present_mismatch", true, "unlimited", true, true},
		{"empty_mode_string_sidecar", true, "EMPTY", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			cityID := "tt"
			city := persist.CityKey{TenantID: persistTenantID, CityID: cityID}
			disk, err := persist.NewDiskStore(root)
			if err != nil {
				t.Fatalf("NewDiskStore: %v", err)
			}
			dir := filepath.Dir(disk.GameModeSidecarPath(city))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if c.seed {
				if _, err := disk.SetWorldSeedIfAbsent(context.Background(), city, inc4Seed); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			if c.mode != "" {
				m := c.mode
				if m == "EMPTY" {
					m = ""
				}
				b, _ := json.Marshal(struct {
					GameMode string `json:"game_mode"`
				}{GameMode: m})
				if err := os.WriteFile(filepath.Join(dir, "gamemode.json"), b, 0o644); err != nil {
					t.Fatalf("write gamemode.json: %v", err)
				}
			}
			if c.epoch {
				if c.seed {
					// Normal API path: a seed is already on record, so
					// SetGameModeEpoch succeeds exactly as production
					// uses it.
					if err := disk.SetGameModeEpoch(context.Background(), city); err != nil {
						t.Fatalf("epoch: %v", err)
					}
				} else {
					// This shape (epoch marker, no seed) can no longer be
					// produced via the public API post-P2-1 -- hand-write
					// the raw sidecar to simulate external tampering, the
					// only way it could still arise.
					b, _ := json.Marshal(struct {
						ModeEpoch bool `json:"mode_epoch"`
					}{ModeEpoch: true})
					if err := os.WriteFile(filepath.Join(dir, "seed.json"), b, 0o644); err != nil {
						t.Fatalf("hand-write tampered seed.json: %v", err)
					}
				}
			}

			e := newEngine()
			_, _, err = setUpPersistence(e, root, cityID, &bytes.Buffer{}, "real")
			if c.wantRefuse && err == nil {
				t.Fatalf("case %s: boot SUCCEEDED, want refusal", c.name)
			}
			if !c.wantRefuse && err != nil {
				t.Fatalf("case %s: boot REFUSED (%v), want success", c.name, err)
			}
			if err != nil {
				t.Logf("case %s refusal: %v", c.name, err)
			}
		})
	}
}
