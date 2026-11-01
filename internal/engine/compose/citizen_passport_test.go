package compose

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/persona"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-2326609842 -- AC-driven tests of the citizen dummy-passport query seam
// against a REAL composed engine. Composed citizens carry Home=0/Workplace=0
// (FEAT-2326609845 owns assigning them), so AC tests SEED records that carry
// them through the public SeedColdRecords seam and ALSO pin the honest
// empty-composition behaviour.

const (
	passportSeed   = uint64(2326609842)
	passportIDBase = uint64(7_000_000_000) // far above any spawned or fertility id
)

func passportWire(t *testing.T, deps *Deps) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(passportSeed), core.WithPoolSize(1))
	comp, err := Wire(e, deps)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	return e, comp
}

func passportQuery(t *testing.T, e *core.Engine, kind protocol.Kind, p protocol.CommandPayload) protocol.CommandResult {
	t.Helper()
	cmd := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "passport-q", Kind: kind, Payload: p}
	if err := cmd.Validate(); err != nil {
		t.Fatalf("query command invalid: %v", err)
	}
	return e.HandleCommand(cmd)
}

func passportOf(t *testing.T, e *core.Engine, id uint64) protocol.CitizenPassport {
	t.Helper()
	res := passportQuery(t, e, protocol.KindQueryCitizen, protocol.QueryCitizenPayload{ID: id})
	if !res.Accepted {
		t.Fatalf("QueryCitizen %d refused: %+v", id, res.Error)
	}
	var pp protocol.CitizenPassport
	if err := json.Unmarshal(res.Result, &pp); err != nil {
		t.Fatalf("decode passport: %v (%s)", err, res.Result)
	}
	return pp
}

func occupantsOf(t *testing.T, e *core.Engine, p protocol.QueryOccupantsPayload) protocol.OccupantPage {
	t.Helper()
	res := passportQuery(t, e, protocol.KindQueryOccupants, p)
	if !res.Accepted {
		t.Fatalf("QueryOccupants refused: %+v", res.Error)
	}
	var pg protocol.OccupantPage
	if err := json.Unmarshal(res.Result, &pg); err != nil {
		t.Fatalf("decode page: %v (%s)", err, res.Result)
	}
	return pg
}

func seededRecord(id uint64, home citizens.CellRef, workplace uint32) citizens.ColdRecord {
	return citizens.ColdRecord{
		ID: id, BirthMonth: -480, Sex: citizens.SexFemale, ChildCount: 2,
		Home: home, District: 3, Workplace: workplace,
		HealthBand: citizens.HealthGood, Wealth: 123456,
		EmploymentState: citizens.EmploymentEmployed, Sector: citizens.SectorTertiary,
		SatHousing: 10, SatServices: 20, SatEnvironment: 30, SatLeisureFit: 40, SatCommute: 50,
	}
}

func homeOfCell(x, y int) citizens.CellRef {
	r, _ := homeRefOfCell(protocol.CellRef{X: x, Y: y})
	return citizens.CellRef(r)
}

// AC-1/AC-2/AC-3/AC-4: a seeded citizen's passport carries ID-derived persona,
// address from the Home cell, job from Workplace, and the record's own fields.
func TestPassport_SeededCitizen_AC1234(t *testing.T) {
	e, comp := passportWire(t, nil)
	id := passportIDBase + 1
	home := homeOfCell(12, 27)
	if err := comp.state.citizens.SeedColdRecords([]citizens.ColdRecord{seededRecord(id, home, 9)}, "pp"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	pp := passportOf(t, e, id)
	per := persona.Of(id)
	if pp.ID != id || pp.GUID != per.GUID || pp.Name != per.Name || pp.Avatar != per.Avatar {
		t.Errorf("persona not ID-derived: %+v vs %+v", pp, per)
	}
	if back, ok := persona.ParseGUID(pp.GUID); !ok || back != id {
		t.Errorf("GUID does not round-trip to the id: %s", pp.GUID)
	}
	if !pp.HasHome || pp.HomeCell != (protocol.CellRef{X: 12, Y: 27}) || pp.AddressLabel != persona.AddressLabel(12, 27) {
		t.Errorf("address wrong: %+v", pp)
	}
	if pp.Workplace != 9 || pp.Employment != "employed" || pp.Sector != "tertiary" {
		t.Errorf("job wrong: %+v", pp)
	}
	const wantWealth, wantChildren, wantDistrict = 123456, 2, 3
	if pp.Sex != "female" || pp.HealthBand != "good" || pp.WealthMicropounds != wantWealth || pp.ChildCount != wantChildren || pp.District != wantDistrict {
		t.Errorf("record fields wrong: %+v", pp)
	}
	if pp.Satisfaction != [5]int32{10, 20, 30, 40, 50} {
		t.Errorf("satisfaction axes wrong: %v", pp.Satisfaction)
	}
	if pp.AgeMonths != pp.Month+480 || pp.AgeYears != pp.AgeMonths/12 {
		t.Errorf("age not derived from month/birth: %+v", pp)
	}
	// AC-2: identical on a second ask, byte-for-byte.
	a := passportQuery(t, e, protocol.KindQueryCitizen, protocol.QueryCitizenPayload{ID: id}).Result
	b := passportQuery(t, e, protocol.KindQueryCitizen, protocol.QueryCitizenPayload{ID: id}).Result
	if string(a) != string(b) {
		t.Errorf("passport not stable across asks:\n%s\n%s", a, b)
	}
}

// AC-3/AC-4 honesty: a citizen with no Home / no Workplace says so.
func TestPassport_NoHomeNoJob_Honest(t *testing.T) {
	e, comp := passportWire(t, nil)
	id := passportIDBase + 2
	rec := seededRecord(id, 0, 0)
	rec.EmploymentState, rec.Sector = citizens.EmploymentNone, citizens.SectorNone
	if err := comp.state.citizens.SeedColdRecords([]citizens.ColdRecord{rec}, "pp"); err != nil {
		t.Fatal(err)
	}
	pp := passportOf(t, e, id)
	if pp.HasHome || pp.AddressLabel != "" || pp.HomeCell != (protocol.CellRef{}) {
		t.Errorf("fabricated an address: %+v", pp)
	}
	if pp.Workplace != 0 || pp.Employment != "none" {
		t.Errorf("fabricated a job: %+v", pp)
	}
}

// Empty-composition reality: spawned citizens have neither Home nor Workplace
// today, and the passport / listings say so rather than invent (FEAT-2326609845).
func TestPassport_RealComposedCity_NoHomeAndEmptyOccupants(t *testing.T) {
	e, comp := passportWire(t, nil)
	if comp.Population() == 0 {
		t.Skip("composition spawned no citizens")
	}
	var found uint64
	for id := uint64(1); id < 5000 && found == 0; id++ {
		if passportQuery(t, e, protocol.KindQueryCitizen, protocol.QueryCitizenPayload{ID: id}).Accepted {
			found = id
		}
	}
	if found == 0 {
		t.Skip("no spawned citizen id in 1..4999")
	}
	pp := passportOf(t, e, found)
	if pp.HasHome {
		t.Logf("composition now assigns homes (FEAT-2326609845 landed?): %+v", pp)
	} else if pp.AddressLabel != "" {
		t.Errorf("no-home passport carries an address label: %+v", pp)
	}
	res := passportQuery(t, e, protocol.KindQueryOccupants, protocol.QueryOccupantsPayload{By: protocol.OccupantsByWorkplace, Ref: 4242, Limit: 10})
	if !res.Accepted || !strings.Contains(string(res.Result), `"entries":[]`) {
		t.Errorf("empty workplace must encode entries as []: %s", res.Result)
	}
	pg := occupantsOf(t, e, protocol.QueryOccupantsPayload{By: protocol.OccupantsByWorkplace, Ref: 4242, Limit: 10})
	if pg.Total != 0 || len(pg.Entries) != 0 || pg.HasMore {
		t.Errorf("empty workplace should list nothing: %+v", pg)
	}
}

// AC-5: occupants are paged, stable ascending, Total is a count, HasMore right,
// and the page cap is enforced at the codec (over-cap refused by Validate).
func TestOccupants_PagingAndCap_AC5(t *testing.T) {
	e, comp := passportWire(t, nil)
	home := homeOfCell(5, 6)
	const n = 120
	recs := make([]citizens.ColdRecord, 0, n+1)
	for i := n; i >= 1; i-- { // seeded DESCENDING: page order must still ascend
		recs = append(recs, seededRecord(passportIDBase+100+uint64(i), home, 77))
	}
	recs = append(recs, seededRecord(passportIDBase+500, homeOfCell(5, 7), 78)) // neighbour cell
	if err := comp.state.citizens.SeedColdRecords(recs, "pp"); err != nil {
		t.Fatal(err)
	}
	var all []uint64
	after := uint64(0)
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("paging did not terminate")
		}
		pg := occupantsOf(t, e, protocol.QueryOccupantsPayload{By: protocol.OccupantsByHome, Cell: protocol.CellRef{X: 5, Y: 6}, AfterID: after})
		if pg.Total != n {
			t.Fatalf("Total = %d want %d", pg.Total, n)
		}
		if len(pg.Entries) > protocol.OccupantsPageCap {
			t.Fatalf("page of %d exceeds cap %d", len(pg.Entries), protocol.OccupantsPageCap)
		}
		for _, en := range pg.Entries {
			all = append(all, en.ID)
			if en.GUID != persona.GUID(en.ID) || en.Name != persona.Name(en.ID) {
				t.Errorf("entry persona wrong: %+v", en)
			}
		}
		if pg.AfterID != after || (len(pg.Entries) > 0 && pg.NextAfterID != pg.Entries[len(pg.Entries)-1].ID) {
			t.Errorf("cursor echo wrong: afterID %d (want %d) nextAfterID %d", pg.AfterID, after, pg.NextAfterID)
		}
		if wantMore := len(all) < n; pg.HasMore != wantMore {
			t.Errorf("HasMore wrong after %d of %d collected: %v", len(all), n, pg.HasMore)
		}
		if !pg.HasMore {
			break
		}
		after = pg.NextAfterID
	}
	if len(all) != n {
		t.Fatalf("collected %d occupants want %d", len(all), n)
	}
	for i := 1; i < len(all); i++ {
		if all[i] <= all[i-1] {
			t.Fatalf("not strictly ascending at %d: %d after %d", i, all[i], all[i-1])
		}
	}
	// Workplace key is a different relation: 78 holds only the neighbour.
	wp := occupantsOf(t, e, protocol.QueryOccupantsPayload{By: protocol.OccupantsByWorkplace, Ref: 78, Limit: 10})
	if wp.Total != 1 || len(wp.Entries) != 1 || wp.Entries[0].ID != passportIDBase+500 {
		t.Errorf("workplace listing wrong: %+v", wp)
	}
	// Past-the-end afterID: empty page, honest total.
	end := occupantsOf(t, e, protocol.QueryOccupantsPayload{By: protocol.OccupantsByHome, Cell: protocol.CellRef{X: 5, Y: 6}, AfterID: passportIDBase + 1_000_000})
	if len(end.Entries) != 0 || end.Total != n || end.HasMore || end.NextAfterID != end.AfterID {
		t.Errorf("past-end page wrong: %+v", end)
	}
	// Over-cap is refused (not clamped) before the engine runs.
	over := protocol.Command{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "c", Kind: protocol.KindQueryOccupants,
		Payload: protocol.QueryOccupantsPayload{By: protocol.OccupantsByHome, Limit: protocol.OccupantsPageCap + 1}}
	if err := over.Validate(); err == nil {
		t.Error("over-cap page passed Validate")
	}
}

// A home cell outside the world grid is refused with the bounds code, not an empty list.
func TestOccupants_CellOutsideGridRefused(t *testing.T) {
	e, _ := passportWire(t, nil)
	res := passportQuery(t, e, protocol.KindQueryOccupants, protocol.QueryOccupantsPayload{By: protocol.OccupantsByHome, Cell: protocol.CellRef{X: worldGridWidthCells + 5, Y: 0}, Limit: 5})
	if res.Accepted || res.Error == nil || res.Error.Code != protocol.ErrQueryOutOfBounds {
		t.Errorf("want %s refusal, got %+v", protocol.ErrQueryOutOfBounds, res)
	}
}

// AC-7: unknown / never-minted ids are refused with a registry code, never zeros.
func TestPassport_UnknownIDRefused(t *testing.T) {
	e, _ := passportWire(t, nil)
	for _, id := range []uint64{passportIDBase + 999_999, 1<<63 + 12345, protocol.MaxQueryCitizenID} {
		res := passportQuery(t, e, protocol.KindQueryCitizen, protocol.QueryCitizenPayload{ID: id})
		if res.Accepted || res.Error == nil || res.Error.Code != ErrCitizenNotFound {
			t.Errorf("id %d: want %s refusal, got %+v", id, ErrCitizenNotFound, res)
		}
		if len(res.Result) != 0 {
			t.Errorf("id %d: refusal carries a Result body", id)
		}
	}
}

// A citizen who dies is refused afterwards (and drops out of the occupant listing).
func TestPassport_DeadCitizenRefusedAndDelisted(t *testing.T) {
	e, comp := passportWire(t, nil)
	home := homeOfCell(9, 9)
	a, b := passportIDBase+10, passportIDBase+11
	if err := comp.state.citizens.SeedColdRecords([]citizens.ColdRecord{seededRecord(a, home, 5), seededRecord(b, home, 5)}, "pp"); err != nil {
		t.Fatal(err)
	}
	const wantPreDeath = 2
	if pg := occupantsOf(t, e, protocol.QueryOccupantsPayload{By: protocol.OccupantsByHome, Cell: protocol.CellRef{X: 9, Y: 9}}); pg.Total != wantPreDeath {
		t.Fatalf("pre-death total %d", pg.Total)
	}
	if err := comp.state.citizens.ApplyLifeEventCommand(citizens.LifeEventCommand{CorrelationID: "pp", Kind: citizens.LifeEventDeath, CitizenID: a}); err != nil {
		t.Fatalf("death: %v", err)
	}
	res := passportQuery(t, e, protocol.KindQueryCitizen, protocol.QueryCitizenPayload{ID: a})
	if res.Accepted || res.Error == nil || res.Error.Code != ErrCitizenNotFound {
		t.Errorf("dead citizen answered: %+v", res)
	}
	pg := occupantsOf(t, e, protocol.QueryOccupantsPayload{By: protocol.OccupantsByHome, Cell: protocol.CellRef{X: 9, Y: 9}})
	if len(pg.Entries) != 1 || pg.Entries[0].ID != b {
		t.Errorf("dead occupant still listed: %+v", pg)
	}
}

// A paged-out (cold, on disk) citizen still answers with an unchanged identity.
func TestPassport_PagedOutCitizenAnswers(t *testing.T) {
	e, comp := passportWire(t, &Deps{CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: t.TempDir()}})
	home := homeOfCell(3, 4)
	const n = 400
	recs := make([]citizens.ColdRecord, 0, n)
	for i := 0; i < n; i++ {
		recs = append(recs, seededRecord(passportIDBase+2000+uint64(i), home, 6))
	}
	if err := comp.state.citizens.SeedColdRecords(recs, "pp"); err != nil {
		t.Fatal(err)
	}
	ref := map[uint64]protocol.CitizenPassport{}
	for _, i := range []int{0, 137, 399} {
		id := passportIDBase + 2000 + uint64(i)
		ref[id] = passportOf(t, e, id)
	}
	// Drive a month so the amortised sweep evicts shards under the tight budget.
	if err := e.AdvanceTicks("pp-drive", int64(core.DailyTicksPerMonth)); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}
	for id, b := range ref {
		a := passportOf(t, e, id)
		if a.GUID != b.GUID || a.Name != b.Name || a.Avatar != b.Avatar || a.HomeCell != b.HomeCell || a.Workplace != b.Workplace || a.WealthMicropounds != b.WealthMicropounds {
			t.Errorf("citizen %d changed identity/address across paging:\n%+v\n%+v", id, b, a)
		}
	}
	if pg := occupantsOf(t, e, protocol.QueryOccupantsPayload{By: protocol.OccupantsByHome, Cell: protocol.CellRef{X: 3, Y: 4}}); pg.Total == 0 {
		t.Error("occupant listing lost the paged-out residents")
	}
}

// Queries are read-only: they never move the population hash or the state digest.
func TestQueries_DoNotMutateState(t *testing.T) {
	e, comp := passportWire(t, nil)
	id := passportIDBase + 30
	if err := comp.state.citizens.SeedColdRecords([]citizens.ColdRecord{seededRecord(id, homeOfCell(1, 1), 2)}, "pp"); err != nil {
		t.Fatal(err)
	}
	before := comp.PopulationHash()
	digest := comp.StateDigest()
	for i := 0; i < 20; i++ {
		passportOf(t, e, id)
		occupantsOf(t, e, protocol.QueryOccupantsPayload{By: protocol.OccupantsByHome, Cell: protocol.CellRef{X: 1, Y: 1}})
	}
	if comp.PopulationHash() != before || comp.StateDigest() != digest {
		t.Error("query changed composed state")
	}
}

// The protocol's id upper bound repeats citizens' fertility namespace base; pin them equal.
func TestQueryCitizenIDBoundMatchesNamespace(t *testing.T) {
	if protocol.MaxQueryCitizenID != citizens.FertilityChildIDBase+(1<<40) {
		t.Errorf("MaxQueryCitizenID %d drifted from FertilityChildIDBase %d + 1<<40", protocol.MaxQueryCitizenID, citizens.FertilityChildIDBase)
	}
}

// The compose-raised codes are registry-sourced (render, not unregistered).
func TestPassportCodes_RenderFromRegistry(t *testing.T) {
	for _, code := range []string{ErrCitizenNotFound, ErrCitizenQueryFailed} {
		var e *errs.E
		if !errors.As(errs.New(code, "c", map[string]any{"id": 1, "kind": "k", "cause": "x"}), &e) {
			t.Fatal("not *errs.E")
		}
		msg := e.Error()
		if !strings.Contains(msg, code) || strings.Contains(strings.ToLower(msg), "unregistered") {
			t.Errorf("code %s renders oddly: %q", code, msg)
		}
	}
}
