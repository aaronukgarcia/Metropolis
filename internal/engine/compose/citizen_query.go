package compose

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/persona"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-2326609842 -- the citizen dummy-passport's engine half: a READ-ONLY query
// answerer over the REAL engine.citizens.CitizensAPI. Deliberately plain
// functions plus one simState method -- no new mutex-bearing *Wiring type (the
// astgate flood lesson): a read-only query owns no state.
//
// # Where the answers come from
//
//   - KindQueryCitizen -> CitizensAPI.PassportRecord: the indexed rowOf path
//     (O(1), transparently pages a shard back in), never a scan. The persona
//     (name / GUID / avatar) is derived from the id (internal/foundation/
//     persona), not stored.
//   - KindQueryOccupants -> CitizensAPI.OccupantsPage: the lazily-built,
//     month-stamped occupant index (see citizens/occupants.go for the cost
//     justification); page size is capped by the protocol codec.
//
// # Honest finding recorded with this wiring
//
// Nothing in the composed engine assigns a citizen a Home cell or a Workplace
// ref today (spawnCitizens mints citizens with neither), so on a real
// composed city every passport reads "no fixed address" and every occupants
// list is empty until a housing/employment assignment lands. The query is
// correct for records that carry them (tests seed such records); it does NOT
// fabricate an address or a job to look populated (AC-3, AC-4).

// ErrCitizenNotFound (MET-G5600, owner feat.compositionroot): the queried id is
// not a live citizen on this city -- dead, never minted, or unknown.
const ErrCitizenNotFound = "MET-G5600"

// ErrCitizenQueryFailed (MET-G5602, owner feat.compositionroot): the composed
// engine could not answer a citizen query (citizens not wired, or the answer
// could not be encoded).
const ErrCitizenQueryFailed = "MET-G5602"

// worldGridWidthCells is the width of the world grid a citizens.CellRef indexes
// (row-major: index = y*width + x): TilesPerSide tiles of TileSizeCells cells.
const worldGridWidthCells = world.TilesPerSide * world.TileSizeCells

// handleQuery is the injected core.QueryCommandHandler.
func (st *simState) handleQuery(cmd protocol.Command) (json.RawMessage, error) {
	if st.citizens == nil {
		return nil, errs.New(ErrCitizenQueryFailed, st.cid, map[string]any{"kind": string(cmd.Kind), "cause": "engine.citizens is not wired"})
	}
	switch p := cmd.Payload.(type) {
	case protocol.QueryCitizenPayload:
		pp, err := citizenPassport(st.citizens, st.cid, p.ID)
		if err != nil {
			return nil, err
		}
		return encodeQueryResult(st.cid, cmd.Kind, pp)
	case protocol.QueryOccupantsPayload:
		page, err := occupantPage(st.citizens, st.cid, p)
		if err != nil {
			return nil, err
		}
		return encodeQueryResult(st.cid, cmd.Kind, page)
	default:
		return nil, errs.New(ErrCitizenQueryFailed, st.cid, map[string]any{"kind": string(cmd.Kind), "cause": "payload type does not match the query kind"})
	}
}

func encodeQueryResult(cid string, kind protocol.Kind, v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, errs.Wrap(ErrCitizenQueryFailed, cid, err, map[string]any{"kind": string(kind), "cause": "result encoding failed"})
	}
	return raw, nil
}

// homeRefOfCell maps a protocol map cell onto the citizens.CellRef linear
// index; ok is false for a cell outside the world grid.
func homeRefOfCell(c protocol.CellRef) (uint32, bool) {
	if c.X < 0 || c.Y < 0 || c.X >= worldGridWidthCells || c.Y >= worldGridWidthCells {
		return 0, false
	}
	return uint32(c.Y)*uint32(worldGridWidthCells) + uint32(c.X), true
}

// cellOfHomeRef is homeRefOfCell's inverse.
func cellOfHomeRef(h citizens.CellRef) protocol.CellRef {
	return protocol.CellRef{X: int(uint32(h) % worldGridWidthCells), Y: int(uint32(h) / worldGridWidthCells)}
}

// citizenPassport builds the passport for id from the live record.
func citizenPassport(api *citizens.CitizensAPI, cid string, id uint64) (protocol.CitizenPassport, error) {
	rec, month, ok := api.PassportRecord(id, cid)
	if !ok {
		return protocol.CitizenPassport{}, errs.New(ErrCitizenNotFound, cid, map[string]any{"id": id, "cause": "not a live citizen"})
	}
	per := persona.Of(id)
	ageMonths := month - rec.BirthMonth
	ageYears := ageMonths / 12
	if ageMonths < 0 {
		ageYears = 0
	}
	pp := protocol.CitizenPassport{
		ID:                id,
		GUID:              per.GUID,
		Name:              per.Name,
		Avatar:            per.Avatar,
		Month:             month,
		BirthMonth:        rec.BirthMonth,
		AgeMonths:         ageMonths,
		AgeYears:          ageYears,
		Sex:               sexLabel(rec.Sex),
		District:          rec.District,
		Employment:        employmentLabel(rec.EmploymentState),
		Sector:            sectorLabel(rec.Sector),
		Workplace:         rec.Workplace,
		School:            rec.School,
		Stage:             stageLabel(rec.Stage),
		Household:         rec.Household,
		HasPartner:        rec.Partner != 0,
		Partner:           rec.Partner,
		ChildCount:        int(rec.ChildCount),
		HealthBand:        healthLabel(rec.HealthBand),
		WealthMicropounds: rec.Wealth,
		Satisfaction:      [5]int32{rec.SatHousing, rec.SatServices, rec.SatEnvironment, rec.SatLeisureFit, rec.SatCommute},
	}
	if rec.Home != 0 {
		cell := cellOfHomeRef(rec.Home)
		pp.HasHome = true
		pp.HomeCell = cell
		pp.AddressLabel = persona.AddressLabel(cell.X, cell.Y)
	}
	return pp, nil
}

// occupantPage answers a (codec-bounded) occupants query.
func occupantPage(api *citizens.CitizensAPI, cid string, p protocol.QueryOccupantsPayload) (protocol.OccupantPage, error) {
	limit := p.Limit
	if limit == 0 {
		limit = protocol.OccupantsPageCap
	}
	var kind citizens.OccupantKind
	var ref uint32
	switch p.By {
	case protocol.OccupantsByHome:
		kind = citizens.OccupantsHome
		r, ok := homeRefOfCell(p.Cell)
		if !ok {
			return protocol.OccupantPage{}, errs.New(protocol.ErrQueryOutOfBounds, cid, map[string]any{"kind": string(protocol.KindQueryOccupants), "cause": "home cell outside the world grid"})
		}
		ref = r
	case protocol.OccupantsByWorkplace:
		kind = citizens.OccupantsWorkplace
		ref = p.Ref
	default:
		return protocol.OccupantPage{}, errs.New(protocol.ErrQueryOutOfBounds, cid, map[string]any{"kind": string(protocol.KindQueryOccupants), "cause": "by must be home or workplace"})
	}
	ids, total, hasMore, err := api.OccupantsPage(kind, ref, p.AfterID, limit, cid)
	if err != nil {
		return protocol.OccupantPage{}, errs.Wrap(ErrCitizenQueryFailed, cid, err, map[string]any{"kind": string(protocol.KindQueryOccupants), "cause": "occupant index read failed"})
	}
	page := protocol.OccupantPage{By: p.By, AfterID: p.AfterID, NextAfterID: p.AfterID, Total: total, HasMore: hasMore, Entries: make([]protocol.OccupantEntry, 0, len(ids))}
	for _, id := range ids {
		page.Entries = append(page.Entries, protocol.OccupantEntry{ID: id, GUID: persona.GUID(id), Name: persona.Name(id)})
	}
	if n := len(ids); n > 0 {
		page.NextAfterID = ids[n-1]
	}
	return page, nil
}

// Label tables: the engine's enum names, one rendering for every surface.
func sexLabel(s citizens.Sex) string {
	if s == citizens.SexMale {
		return "male"
	}
	return "female"
}

func healthLabel(h citizens.HealthBand) string {
	switch h {
	case citizens.HealthCritical:
		return "critical"
	case citizens.HealthPoor:
		return "poor"
	case citizens.HealthFair:
		return "fair"
	case citizens.HealthGood:
		return "good"
	case citizens.HealthVeryGood:
		return "very good"
	case citizens.HealthExcellent:
		return "excellent"
	}
	return "unknown"
}

func employmentLabel(e citizens.EmploymentState) string {
	switch e {
	case citizens.EmploymentNone:
		return "none"
	case citizens.EmploymentStudent:
		return "student"
	case citizens.EmploymentEmployed:
		return "employed"
	case citizens.EmploymentUnemployed:
		return "unemployed"
	case citizens.EmploymentRetired:
		return "retired"
	case citizens.EmploymentOffMap:
		return "employed off-map"
	}
	return "unknown"
}

func sectorLabel(s citizens.Sector) string {
	switch s {
	case citizens.SectorNone:
		return "none"
	case citizens.SectorPrimary:
		return "primary"
	case citizens.SectorSecondary:
		return "secondary"
	case citizens.SectorTertiary:
		return "tertiary"
	case citizens.SectorPublic:
		return "public"
	}
	return "unknown"
}

func stageLabel(s citizens.Stage) string {
	switch s {
	case citizens.StageNone:
		return "none"
	case citizens.StageNursery:
		return "nursery"
	case citizens.StagePrimary:
		return "primary school"
	case citizens.StageSecondary:
		return "secondary school"
	case citizens.StageSixthForm:
		return "sixth form"
	case citizens.StageTechnical:
		return "technical college"
	case citizens.StageUniversity:
		return "university"
	case citizens.StageAdultEd:
		return "adult education"
	}
	return "unknown"
}
