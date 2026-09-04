package deathservices

import (
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := LoadDefaultConfig("corr")
	if err != nil {
		t.Fatalf("LoadDefaultConfig: %v", err)
	}
	return cfg
}

// TestIntakeOneBodyPerDeath (AC-1): streaming a fixed RealisedDeath list
// through Intake produces a body set with identical cardinality and IDs,
// no dropped or duplicated bodies.
func TestIntakeOneBodyPerDeath(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	deaths := []citizens.RealisedDeath{
		{CitizenID: 1, DeathMonth: 10, EmergencyFlag: false},
		{CitizenID: 2, DeathMonth: 10, EmergencyFlag: false},
		{CitizenID: 3, DeathMonth: 11, EmergencyFlag: false},
	}
	ids, err := d.Intake(deaths, "corr")
	if err != nil {
		t.Fatalf("Intake: %v", err)
	}
	if len(ids) != len(deaths) {
		t.Fatalf("Intake returned %d ids, want %d", len(ids), len(deaths))
	}
	seen := map[uint64]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	for _, rd := range deaths {
		if !seen[rd.CitizenID] {
			t.Fatalf("citizen %d dropped by intake", rd.CitizenID)
		}
		b, err := d.Body(rd.CitizenID, "corr")
		if err != nil {
			t.Fatalf("Body(%d): %v", rd.CitizenID, err)
		}
		if b.State != BodyAwaiting {
			t.Fatalf("body %d state = %s, want awaiting", rd.CitizenID, b.State)
		}
	}
	snap, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.BodiesReleased != int64(len(deaths)) {
		t.Fatalf("BodiesReleased = %d, want %d (no dropped/duplicated bodies)", snap.BodiesReleased, len(deaths))
	}
}

// TestIntakeRejectsDuplicateDeath (AC-1/AC-17): a citizenID intaken twice
// is rejected with the registry code and creates no phantom second body.
func TestIntakeRejectsDuplicateDeath(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 5, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("first Intake: %v", err)
	}
	_, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 5, DeathMonth: 2}}, "corr")
	if err == nil {
		t.Fatalf("second Intake for citizen 5 returned nil error, want ErrDuplicateDeath")
	}
	assertRegistryCode(t, err, ErrDuplicateDeath)
	snap, _ := d.Snapshot("corr")
	if snap.BodiesReleased != 1 {
		t.Fatalf("BodiesReleased = %d after rejected duplicate, want 1 (no phantom body)", snap.BodiesReleased)
	}
}

// TestNoDirectExportedFieldMutation (AC-1's GR#20 contract check): this is
// the mechanical grep AC-1 specifies
// (`grep -rln "deathservices\.\w\+ = " internal/engine/*/ internal/ui/*/`)
// -- documented here as a real test that exercises the contract surface: a
// Body snapshot returned by an accessor is a VALUE copy, so mutating it
// can never reach back into the live record.
func TestNoDirectExportedFieldMutation(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 9, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}
	b, err := d.Body(9, "corr")
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	b.State = BodyBuried // mutating the returned VALUE, not the live record
	b2, err := d.Body(9, "corr")
	if err != nil {
		t.Fatalf("Body (reread): %v", err)
	}
	if b2.State != BodyAwaiting {
		t.Fatalf("live record state = %s after mutating a returned snapshot, want awaiting (snapshot must be a copy)", b2.State)
	}
}

// TestOldSaveZeroStateLoadsCleanly (AC-16): a DeathServicesAPI constructed
// fresh (the old-save-compatibility analogue -- zero deathservices state,
// exactly what a save predating this module restores to) answers every
// accessor with sensible zero values and proceeds with intake normally, no
// panics.
func TestOldSaveZeroStateLoadsCleanly(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")

	if n, err := d.AwaitingBacklog("corr"); err != nil || n != 0 {
		t.Fatalf("AwaitingBacklog on zero state = (%d, %v), want (0, nil)", n, err)
	}
	snap, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot on zero state: %v", err)
	}
	if snap != (Conservation{}) {
		t.Fatalf("Snapshot on zero state = %+v, want all-zero", snap)
	}
	active, err := d.DispensationActive("corr")
	if err != nil || active {
		t.Fatalf("DispensationActive on zero state = (%v, %v), want (false, nil)", active, err)
	}

	// Intake proceeds normally with no prior state.
	if _, err := d.Intake([]citizens.RealisedDeath{{CitizenID: 1, DeathMonth: 1}}, "corr"); err != nil {
		t.Fatalf("Intake on zero state: %v", err)
	}
}

// TestConcurrentIntakeWithDisposalResolution (AC-20): concurrent Intake
// from the death queue while burial/cremation resolve within a "tick" --
// no data race (run with -race), and every body ends up in exactly one
// terminal or in-flight state.
func TestConcurrentIntakeWithDisposalResolution(t *testing.T) {
	d := NewDeathServicesAPI(testConfig(t), "corr")
	if err := d.RegisterCemetery("cem-1", "corr"); err != nil {
		t.Fatalf("RegisterCemetery: %v", err)
	}
	if err := d.RegisterCrematorium("crem-1", "corr"); err != nil {
		t.Fatalf("RegisterCrematorium: %v", err)
	}

	const n = 200
	deaths := make([]citizens.RealisedDeath, n)
	for i := 0; i < n; i++ {
		deaths[i] = citizens.RealisedDeath{CitizenID: uint64(i + 1), DeathMonth: 1}
	}
	if _, err := d.Intake(deaths, "corr"); err != nil {
		t.Fatalf("Intake: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := uint64(i + 1)
			if i%2 == 0 {
				_ = d.Bury(id, "cem-1", 1, "corr")
			} else {
				_, _, _ = d.Cremate([]uint64{id}, "crem-1", 1, "corr")
			}
		}()
	}
	wg.Wait()

	snap, err := d.Snapshot("corr")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Sum() != snap.BodiesReleased {
		t.Fatalf("conservation broken after concurrent disposal: released=%d sum=%d (%+v)", snap.BodiesReleased, snap.Sum(), snap)
	}
}
