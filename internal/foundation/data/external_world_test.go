package data

import (
	"reflect"
	"testing"
)

// specExternalPoolIDs are the three off-map job pools §21 names
// verbatim (london/ashford/dover) — a checked-in reference list, not an
// invented total (GR#15), used by the real-file test the way
// naming_corpus_test.go's specCitedPlaceNames is.
var specExternalPoolIDs = map[string]bool{
	"london": true, "ashford": true, "dover": true,
}

// TestExternalWorld_RealFile_LoadsAndPopulates proves the committed
// data/external_world.json (not a synthetic fixture) round-trips
// through the rich ExternalWorld type: the three named pools, their
// capacity curves, int64 wages, transport gating, and the file's
// convention notes are all captured.
func TestExternalWorld_RealFile_LoadsAndPopulates(t *testing.T) {
	dir := realDataDir(t)
	e, err := LoadExternalWorld(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("LoadExternalWorld(real data/external_world.json): %v", err)
	}

	if e.Version != 1 {
		t.Errorf("Version = %d, want 1", e.Version)
	}
	if e.Comment == "" {
		t.Error("top-level $comment not captured")
	}
	if e.EraConvention == "" {
		t.Error("eraConvention not captured")
	}
	if e.MoneyConvention == "" {
		t.Error("moneyConvention not captured")
	}

	if len(e.Profiles) != len(specExternalPoolIDs) {
		t.Errorf("len(profiles) = %d, want %d (§21 names three pools)", len(e.Profiles), len(specExternalPoolIDs))
	}
	have := make(map[string]ExternalProfile, len(e.Profiles))
	for _, p := range e.Profiles {
		have[p.ID] = p
	}
	for id := range specExternalPoolIDs {
		p, ok := have[id]
		if !ok {
			t.Errorf("spec-cited pool %q not present in profiles", id)
			continue
		}
		if len(p.CapacityByEra) == 0 {
			t.Errorf("%s has empty capacityByEra", id)
		}
		// §21 names era 5 as the external-rail unlock tier; every pool
		// gates externalRail at exactly that tier.
		for _, tr := range p.TransportRequirement {
			if tr.Channel == "externalRail" && tr.AvailableFromTier != 5 {
				t.Errorf("%s externalRail availableFromTier = %d, want 5", id, tr.AvailableFromTier)
			}
		}
	}

	// Spot-check London's wage (int64 micro-pounds, transcribed from the
	// file — never a float).
	london := have["london"]
	if london.WageMicropounds != 2900000000 {
		t.Errorf("london wageMicropounds = %d, want 2900000000", london.WageMicropounds)
	}
}

// TestExternalWorld_RepeatedLoadDeepEqual is the GR#21 determinism check:
// loading the real file twice yields structurally identical values.
func TestExternalWorld_RepeatedLoadDeepEqual(t *testing.T) {
	dir := realDataDir(t)
	e1, err := LoadExternalWorld(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	e2, err := LoadExternalWorld(dir, testCorrelationID())
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if !reflect.DeepEqual(e1, e2) {
		t.Error("repeated LoadExternalWorld of the same file produced non-equal structs")
	}
}

// --- mutation tests: each proves Validate() rejects a specific
// malformation the old flat skeleton silently accepted ---------------------

// TestExternalWorld_CapacityDecreaseRejected proves a pool whose
// capacity shrinks across eras is rejected (A6's "bounded and slowly
// growing" mechanism).
func TestExternalWorld_CapacityDecreaseRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileExternalWorld, `{"version":1,"profiles":[{"id":"london","name":"London","capacityByEra":[{"era":1,"capacity":500},{"era":2,"capacity":400}],"wageMicropounds":2900000000,"transportRequirement":[{"channel":"motorway","availableFromTier":1}]}]}`)

	_, err := LoadExternalWorld(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "non-decreasing")
}

// TestExternalWorld_NonPositiveWageRejected proves a non-positive wage is
// rejected (the money convention requires a positive int64 micro-pound
// wage).
func TestExternalWorld_NonPositiveWageRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileExternalWorld, `{"version":1,"profiles":[{"id":"london","name":"London","capacityByEra":[{"era":1,"capacity":500}],"wageMicropounds":0,"transportRequirement":[{"channel":"motorway","availableFromTier":1}]}]}`)

	_, err := LoadExternalWorld(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "wageMicropounds")
}

// TestExternalWorld_ExternalRailUngatedRejected proves an externalRail
// channel gated below its §21-named unlock tier is rejected.
func TestExternalWorld_ExternalRailUngatedRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileExternalWorld, `{"version":1,"profiles":[{"id":"london","name":"London","capacityByEra":[{"era":1,"capacity":500}],"wageMicropounds":2900000000,"transportRequirement":[{"channel":"externalRail","availableFromTier":3}]}]}`)

	_, err := LoadExternalWorld(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "externalRail")
}

// TestExternalWorld_EmptyProfilesRejected proves an empty profiles list is
// rejected.
func TestExternalWorld_EmptyProfilesRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, FileExternalWorld, `{"version":1,"profiles":[]}`)

	_, err := LoadExternalWorld(dir, testCorrelationID())
	assertPlaceholderCode(t, err, CodeSchemaInvalid, "profiles")
}
