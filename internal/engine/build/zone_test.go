package build

import (
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// buildingsJSONWithZones wraps a raw zones-array snippet in a schema-shaped
// buildings.json (version + meta + empty entries), for malformed-data tests.
func buildingsJSONWithZones(zonesJSON string) string {
	return `{"version":1,"meta":{"labourPerTick":1},"zones":` + zonesJSON + `,"entries":[]}`
}

// --- AC-8: catalogue-driven from buildings.json (data, not Go literals) --

func TestZoneCatalogueIsDataDriven(t *testing.T) {
	dir1 := t.TempDir()
	writeBuildings(t, dir1, fixtureBuildingsJSON(100, 45))
	b1, err := Load(dir1, testCorr())
	if err != nil {
		t.Fatalf("Load fixture A: %v", err)
	}

	dir2 := t.TempDir()
	writeBuildings(t, dir2, fixtureBuildingsJSON(200, 90))
	b2, err := Load(dir2, testCorr())
	if err != nil {
		t.Fatalf("Load fixture B: %v", err)
	}

	if got := zoneInfoFor(t, b1, ZoneDwelling); got.Materials != 100 || got.BaseLeadTimeDays != 45 {
		t.Errorf("fixture A dwelling = %+v, want materials 100 / lead 45", got)
	}
	if got := zoneInfoFor(t, b2, ZoneDwelling); got.Materials != 200 || got.BaseLeadTimeDays != 90 {
		t.Errorf("fixture B dwelling = %+v, want materials 200 / lead 90", got)
	}

	// The queue behaviour must reflect the changed data: submit an order
	// against each and check the materials budget and (seasonally-neutral
	// July) effective lead time track the fixture values.
	w := newOwnedWorld(t)
	s, err := season.LoadDefault(testCorr())
	if err != nil {
		t.Fatalf("season.LoadDefault: %v", err)
	}
	for _, b := range []*BuildAPI{b1, b2} {
		if err := b.SetWorld(w); err != nil {
			t.Fatalf("SetWorld: %v", err)
		}
		if err := b.SetSeason(s); err != nil {
			t.Fatalf("SetSeason: %v", err)
		}
	}

	id1, err := b1.SubmitBuildCommand(BuildCommand{
		Tile: tile00(), Local: local00(), OwnerID: testOwner, Zone: ZoneDwelling, Month: 6,
	})
	if err != nil {
		t.Fatalf("fixture A SubmitBuildCommand: %v", err)
	}
	o1 := orderByID(t, b1.Queue(), id1)
	if o1.MaterialsBillTotal != 100 || o1.LeadTimeRemaining != 45 {
		t.Errorf("fixture A order = materials %d / lead %d, want 100 / 45", o1.MaterialsBillTotal, o1.LeadTimeRemaining)
	}

	id2, err := b2.SubmitBuildCommand(BuildCommand{
		Tile: tile00(), Local: local00(), OwnerID: testOwner, Zone: ZoneDwelling, Month: 6,
	})
	if err != nil {
		t.Fatalf("fixture B SubmitBuildCommand: %v", err)
	}
	o2 := orderByID(t, b2.Queue(), id2)
	if o2.MaterialsBillTotal != 200 || o2.LeadTimeRemaining != 90 {
		t.Errorf("fixture B order = materials %d / lead %d, want 200 / 90", o2.MaterialsBillTotal, o2.LeadTimeRemaining)
	}
}

// --- AC-11: malformed buildings.json → registry error at load, no silent
// default substitution ---------------------------------------------------

func TestMalformedBuildingsNegativeLeadTimeRejected(t *testing.T) {
	dir := t.TempDir()
	writeBuildings(t, dir, buildingsJSONWithZones(
		`[{"id":"dwelling","name":"Dwelling","materialsBill":{"constructionMaterials":100},"labour":40,"baseLeadTimeDays":-5}]`,
	))
	_, err := Load(dir, testCorr())
	assertCode(t, err, ErrZoneDataInvalid)
}

func TestMalformedBuildingsMissingMaterialsBillRejected(t *testing.T) {
	dir := t.TempDir()
	writeBuildings(t, dir, buildingsJSONWithZones(
		`[{"id":"dwelling","name":"Dwelling","labour":40,"baseLeadTimeDays":45}]`,
	))
	_, err := Load(dir, testCorr())
	assertCode(t, err, ErrZoneDataInvalid)
}

func TestMalformedBuildingsUnrecognisedZoneTypeRejected(t *testing.T) {
	dir := t.TempDir()
	extra := `,{"id":"indutry","name":"Indutry","materialsBill":{"constructionMaterials":10},"labour":1,"baseLeadTimeDays":1}`
	content := strings.Replace(fixtureBuildingsJSON(100, 45), `],"entries":[]}`, extra+`],"entries":[]}`, 1)
	writeBuildings(t, dir, content)
	_, err := Load(dir, testCorr())
	assertCode(t, err, ErrZoneDataInvalid)
}

// The malformed-load error must be a real *errs.E carrying this package's
// code — never a nil error a caller could mistake for success.
func TestMalformedBuildingsErrorIsRegistrySourced(t *testing.T) {
	dir := t.TempDir()
	writeBuildings(t, dir, buildingsJSONWithZones(
		`[{"id":"dwelling","name":"Dwelling","materialsBill":{"constructionMaterials":100},"labour":40,"baseLeadTimeDays":-1}]`,
	))
	_, err := Load(dir, testCorr())
	if err == nil {
		t.Fatal("Load returned nil error for malformed buildings.json")
	}
	if _, ok := err.(*errs.E); !ok {
		t.Fatalf("Load returned non-registry error %T: %v", err, err)
	}
}
