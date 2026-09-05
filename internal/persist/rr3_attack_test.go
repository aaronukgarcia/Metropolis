package persist

// ADOPTED from the independent re-round-3 attacker (opus-reround3-bug737,
// BUG-737), per the lead's instruction on ACCEPT: these tests pin
// properties nothing else in this tree tests directly. Sourced from the
// attacker's archived persist_attack_rr3_test.go.txt.
//
// TestRR3_C_EpochFirstFabricatesWorldSeedZero is ADAPTED (this round's
// own fix for finding P2-1, 2026-09-05): the original test asserted the
// round-2 BUG it found -- SetGameModeEpoch on a seedless DiskStore
// city SUCCEEDED and fabricated world_seed:0, while MemStore correctly
// stayed seedless, a Disk/Mem parity break. This round's chosen fix
// (offered as one of two options: "represent the seed as a pointer/
// omitempty ... or refuse SetGameModeEpoch without a seed on both
// stores") is the latter: BOTH stores now REFUSE
// (ErrGameModeEpochWithoutSeed) rather than one fabricating a seed. The
// adapted test proves the NEW parity property directly -- both stores
// refuse identically, and neither one records ANY seed as a side effect
// of the refused call.
//
// Every other test below is UNCHANGED from the archived file: they were
// already written expecting the correct (post-fix) outcome and pass
// against this round's implementation without modification.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestRR3_B_EpochWriteDropsUnknownSeedFields is attack (B)'s second half:
// SetGameModeEpoch performs a DECODE-MODIFY-REENCODE merge of seed.json
// through the closed citySeed struct, so any field the running binary
// does not know about is silently DESTROYED by the rewrite. Proven with
// a seed.json carrying a plausible future/foreign field.
func TestRR3_B_EpochWriteDropsUnknownSeedFields(t *testing.T) {
	root := t.TempDir()
	s, err := NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	city := CityKey{TenantID: "t", CityID: "extrafields"}
	dir := filepath.Dir(s.GameModeSidecarPath(city))
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := []byte(`{"world_seed":4242,"lineage_id":"abc-123","created_by":"v1.2.3"}`)
	if err := os.WriteFile(filepath.Join(dir, "seed.json"), original, 0o644); err != nil {
		t.Fatalf("write seed.json: %v", err)
	}

	if err := s.SetGameModeEpoch(context.Background(), city); err != nil {
		t.Fatalf("SetGameModeEpoch: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "seed.json"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	t.Logf("before = %s", original)
	t.Logf("after  = %s", after)

	var m map[string]any
	if err := json.Unmarshal(after, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["world_seed"] != float64(4242) {
		t.Fatalf("world_seed lost/changed: %v", m["world_seed"])
	}
	for _, k := range []string{"lineage_id", "created_by"} {
		if _, ok := m[k]; !ok {
			t.Errorf("RR3 FINDING (B): the epoch merge DESTROYED unknown seed.json field %q -- decode/re-encode through the closed citySeed struct is not a byte-preserving merge", k)
		}
	}
}

// TestRR3_C_EpochFirstFabricatesWorldSeedZero (ADAPTED, see file doc
// comment above): proves this round's ACTUAL fix for finding P2-1 --
// both DiskStore and MemStore refuse SetGameModeEpoch identically on a
// seedless city, and neither fabricates a seed as a side effect.
func TestRR3_C_EpochFirstFabricatesWorldSeedZero(t *testing.T) {
	ctx := context.Background()
	city := CityKey{TenantID: "t", CityID: "epoch-first"}

	disk, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	dErr := disk.SetGameModeEpoch(ctx, city)
	if dErr == nil {
		t.Fatal("DiskStore.SetGameModeEpoch on a seedless city succeeded, want ErrGameModeEpochWithoutSeed")
	}
	dSeed, dOK, err := disk.WorldSeed(ctx, city)
	if err != nil {
		t.Fatalf("disk WorldSeed: %v", err)
	}
	if dOK {
		t.Fatalf("RR3 FINDING (C/E), if reintroduced: DiskStore fabricated a world seed (%d) as a side effect of a REFUSED SetGameModeEpoch call", dSeed)
	}

	mem := NewMemStore()
	mErr := mem.SetGameModeEpoch(ctx, city)
	if mErr == nil {
		t.Fatal("MemStore.SetGameModeEpoch on a seedless city succeeded, want ErrGameModeEpochWithoutSeed")
	}
	mSeed, mOK, err := mem.WorldSeed(ctx, city)
	if err != nil {
		t.Fatalf("mem WorldSeed: %v", err)
	}
	if mOK {
		t.Fatalf("MemStore fabricated a world seed (%d) as a side effect of a REFUSED SetGameModeEpoch call", mSeed)
	}

	t.Logf("DiskStore WorldSeed after refused epoch = (%d, %v); MemStore = (%d, %v)", dSeed, dOK, mSeed, mOK)
	if dOK != mOK {
		t.Errorf("RR3 FINDING (C/E), if reintroduced: DiskStore/MemStore PARITY BREAK -- ok=%v (disk) vs ok=%v (mem) after an identically-refused SetGameModeEpoch call", dOK, mOK)
	}

	// And the consequence this fix closes: a later genuine seed stamp
	// must NOT be blocked -- the refused epoch call left nothing behind.
	recorded, err := disk.SetWorldSeedIfAbsent(ctx, city, 999)
	if err != nil {
		t.Fatalf("disk SetWorldSeedIfAbsent: %v", err)
	}
	if recorded != 999 {
		t.Errorf("RR3 FINDING (C/E) consequence, if reintroduced: SetWorldSeedIfAbsent(999) returned %d -- a fabricated seed record permanently blocked the real seed stamp", recorded)
	}
}

// TestRR3_C_EpochPreservesRecordedSeed is the positive control for (B):
// in the ORDER production actually uses (seed stamped first, epoch
// after), the world seed must survive the merge intact.
func TestRR3_C_EpochPreservesRecordedSeed(t *testing.T) {
	ctx := context.Background()
	for _, tcName := range []string{"disk", "mem"} {
		t.Run(tcName, func(t *testing.T) {
			var s Store
			if tcName == "disk" {
				d, err := NewDiskStore(t.TempDir())
				if err != nil {
					t.Fatalf("NewDiskStore: %v", err)
				}
				s = d
			} else {
				s = NewMemStore()
			}
			city := CityKey{TenantID: "t", CityID: "seed-then-epoch"}
			if _, err := s.SetWorldSeedIfAbsent(ctx, city, 7777); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if err := s.SetGameModeEpoch(ctx, city); err != nil {
				t.Fatalf("epoch: %v", err)
			}
			got, ok, err := s.WorldSeed(ctx, city)
			if err != nil || !ok || got != 7777 {
				t.Fatalf("WorldSeed after epoch = (%d,%v,%v), want (7777,true,nil)", got, ok, err)
			}
			has, err := s.HasGameModeEpoch(ctx, city)
			if err != nil || !has {
				t.Fatalf("HasGameModeEpoch = (%v,%v), want true", has, err)
			}
		})
	}
}

// TestRR3_E_MemStoreEpochNoModeParity checks the five-case parity the
// round asked for at the store level: an empty-string epoch/mode
// combination behaves identically on both stores.
func TestRR3_E_MemStoreEpochNoModeParity(t *testing.T) {
	ctx := context.Background()
	city := CityKey{TenantID: "t", CityID: "parity"}
	disk, err := NewDiskStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	mem := NewMemStore()
	for _, s := range []Store{disk, mem} {
		if _, err := s.SetWorldSeedIfAbsent(ctx, city, 11); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if _, err := s.SetGameModeIfAbsent(ctx, city, ""); err != nil {
			t.Fatalf("empty mode: %v", err)
		}
		if _, ok, err := s.GameMode(ctx, city); err != nil || ok {
			t.Fatalf("%T GameMode after empty stamp = ok %v err %v, want ok=false", s, ok, err)
		}
		if has, err := s.HasGameModeEpoch(ctx, city); err != nil || has {
			t.Fatalf("%T HasGameModeEpoch = (%v,%v), want false", s, has, err)
		}
		if err := s.SetGameModeEpoch(ctx, city); err != nil {
			t.Fatalf("epoch: %v", err)
		}
		if has, err := s.HasGameModeEpoch(ctx, city); err != nil || !has {
			t.Fatalf("%T HasGameModeEpoch after set = (%v,%v), want true", s, has, err)
		}
		// Epoch write must not resurrect a mode.
		if _, ok, err := s.GameMode(ctx, city); err != nil || ok {
			t.Fatalf("%T GameMode after epoch = ok %v err %v, want ok=false", s, ok, err)
		}
	}
}
