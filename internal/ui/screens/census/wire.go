package census

import "encoding/json"

const ViewSubscriptionName = "f6.census"
const wireSchemaVersion = 1
const maxPatchWireBytes = 1 << 20 // 1 MiB

// wireBlueWhiteCollar mirrors engine.census.BlueWhiteCollar (AC-4).
type wireBlueWhiteCollar struct {
	Blue  int64 `json:"blue"`
	White int64 `json:"white"`
}

// wireKPITile mirrors one of AC-5's eight city KPIs.
type wireKPITile struct {
	Key   string  `json:"key"`
	Value float64 `json:"value"`
}

// wireKPISource mirrors AC-6's per-KPI drill-in resolution
// (engine.census.SourceResolution). Unavailable/Reason carry AC-12's
// engine-rejection surfacing for a KPI query the engine refused.
type wireKPISource struct {
	Key         string   `json:"key"`
	EntityIDs   []uint64 `json:"entityIds,omitempty"`
	LineValue   int64    `json:"lineValue"`
	Unavailable bool     `json:"unavailable,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// wireEducationStage mirrors engine.census.StageView.
type wireEducationStage struct {
	Stage      string `json:"stage"`
	StartMonth int64  `json:"startMonth"`
	EndMonth   int64  `json:"endMonth"`
}

// wireCitizenBio mirrors AC-7's cradle-to-grave citizen bio
// (engine.census.CitizenBio). Unavailable/Reason carry AC-12's
// engine-rejection surfacing for a bio query the engine refused.
type wireCitizenBio struct {
	GUID        string               `json:"guid"`
	ID          uint64               `json:"id"`
	BirthMonth  int64                `json:"birthMonth"`
	Sex         string               `json:"sex"`
	Attainment  int64                `json:"attainment"`
	Schooling   int64                `json:"schooling"`
	Stages      []wireEducationStage `json:"stages,omitempty"`
	IndustryTie string               `json:"industryTie,omitempty"`
	State       string               `json:"state"`
	Sector      string               `json:"sector"`
	Workplace   uint64               `json:"workplace"`
	Household   uint64               `json:"household"`
	Partner     uint64               `json:"partner"`
	Home        uint64               `json:"home"`
	Retirement  int64                `json:"retirement"`
	Income      int64                `json:"income"`
	Unavailable bool                 `json:"unavailable,omitempty"`
	Reason      string               `json:"reason,omitempty"`
}

// wireEducationCrimeLinkage mirrors engine.census.EducationCrimeLinkage
// (AC-8).
type wireEducationCrimeLinkage struct {
	Population         int64   `json:"population"`
	MeanAttainment     float64 `json:"meanAttainment"`
	CrimeRate          float64 `json:"crimeRate"`
	UneducatedFraction float64 `json:"uneducatedFraction"`
	PolicyCoefficient  float64 `json:"policyCoefficient"`
}

// wirePatch is the "f6.census" view's full patch shape. Every field is a
// pointer/omitempty so a partial patch (only some sub-surfaces changed)
// decodes cleanly — ApplyDelta (screen.go) treats a nil field as "this
// sub-surface was not sent in this patch" and a present-but-empty one as
// "this sub-surface is now empty", mirroring ui.screen.services' wirePatch
// convention exactly.
type wirePatch struct {
	SchemaVersion         int                        `json:"schemaVersion"`
	AgeBands              *[NumAgeBands]int64        `json:"ageBands,omitempty"`
	SexSeries             *[NumSexBuckets]int64      `json:"sexSeries,omitempty"`
	EducationTiers        *[NumEducationTiers]int64  `json:"educationTiers,omitempty"`
	BlueWhiteCollar       *wireBlueWhiteCollar       `json:"blueWhiteCollar,omitempty"`
	KPIs                  *[]wireKPITile             `json:"kpis,omitempty"`
	KPISources            *[]wireKPISource           `json:"kpiSources,omitempty"`
	SelectedBio           *wireCitizenBio            `json:"selectedBio,omitempty"`
	EducationCrimeLinkage *wireEducationCrimeLinkage `json:"educationCrimeLinkage,omitempty"`
}

func decodeWirePatch(raw json.RawMessage) (wirePatch, error) {
	if len(raw) > maxPatchWireBytes {
		return wirePatch{}, errPatchTooLarge(len(raw), maxPatchWireBytes)
	}
	var p wirePatch
	if err := json.Unmarshal(raw, &p); err != nil {
		return wirePatch{}, err
	}
	if p.SchemaVersion != wireSchemaVersion {
		return wirePatch{}, errUnsupportedSchemaVersion(p.SchemaVersion)
	}
	return p, nil
}
