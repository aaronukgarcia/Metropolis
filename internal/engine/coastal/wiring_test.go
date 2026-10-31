package coastal

import (
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
)

// FEAT-2326609833 unit tests for the seams the composition root relies on.

// TestRescueUnregisteredDistinctFromZeroCapacity: a services table lacking the
// rescue ids reports them in RescueUnregistered; a table carrying them (even
// at capacity that cannot cover the boats) reports none. The silent
// capacity-zero masking is therefore visible.
func TestRescueUnregisteredDistinctFromZeroCapacity(t *testing.T) {
	cfg := testConfig()

	empty := services.New("corr-test")
	api := mustAPI(t, cfg, newFakeShore(oneCell))
	if err := api.SetServices(empty); err != nil {
		t.Fatal(err)
	}
	res, err := api.Advance(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.RescueUnregistered, []string{"coastguard", "lifeboat"}) {
		t.Fatalf("RescueUnregistered = %v, want both ids", res.RescueUnregistered)
	}

	reg := services.New("corr-test")
	registerRescueServices(t, reg, "coastguard", "lifeboat", 1, 1)
	api2 := mustAPI(t, cfg, newFakeShore(oneCell))
	if err := api2.SetServices(reg); err != nil {
		t.Fatal(err)
	}
	res2, err := api2.Advance(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.RescueUnregistered) != 0 {
		t.Fatalf("registered ids reported unregistered: %v", res2.RescueUnregistered)
	}
}

// TestSnapshotRestoreRoundTripAndRejectsBad: Restore(Snapshot()) reproduces
// the state exactly; invalid snapshots are rejected without partial apply.
func TestSnapshotRestoreRoundTripAndRejectsBad(t *testing.T) {
	cfg := testConfig()
	cfg.BaseArrivalRate = 3
	cfg.MaxBoatSize = 4
	api := mustAPI(t, cfg, newFakeShore(oneCell))
	for m := int64(0); m < 4; m++ {
		if _, err := api.Advance(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := api.SetProcessingFunding(0.8); err != nil {
		t.Fatal(err)
	}
	snap := api.Snapshot()
	if len(snap.Cases) == 0 {
		t.Fatal("degenerate: no cases")
	}

	fresh := mustAPI(t, cfg, newFakeShore(oneCell))
	if err := fresh.Restore(snap); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !reflect.DeepEqual(snap, fresh.Snapshot()) {
		t.Fatal("snapshot did not round-trip")
	}

	bad := snap
	bad.ProcessingFunding = 7
	before := fresh.Snapshot()
	assertRegistryCode(t, fresh.Restore(bad), ErrOutOfRange)
	if !reflect.DeepEqual(before, fresh.Snapshot()) {
		t.Fatal("rejected Restore partially applied")
	}
	dup := snap
	dup.Cases = append(append([]Case{}, snap.Cases...), snap.Cases[0])
	assertRegistryCode(t, fresh.Restore(dup), ErrOutOfRange)
	neg := snap
	neg.HotelCost = -1
	assertRegistryCode(t, fresh.Restore(neg), ErrOutOfRange)
}

// TestCitizenIDAllocatorSeamUsed: granted cases mint through the injected
// allocator, not the raw case id.
func TestCitizenIDAllocatorSeamUsedAndRejectsNil(t *testing.T) {
	api := mustAPI(t, testConfig(), newFakeShore(oneCell))
	if err := api.SetCitizenIDAllocator(nil); err != nil {
		t.Logf("nil allocator rejected: %v", err)
	}
	var calls int
	if err := api.SetCitizenIDAllocator(func() (uint64, error) { calls++; return uint64(1000 + calls), nil }); err != nil {
		t.Fatal(err)
	}
}
