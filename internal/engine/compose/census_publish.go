package compose

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/engine/census"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// FEAT-209 (docs/planning/acceptance/ui.screen.census.md): the fourth real
// UI delta-publishing vertical slice, "f6.census" — the Demographics/Census
// screen (F6). It publishes the three population splines (age band, sex,
// education tier), the blue/white-collar workforce split, the eight city
// KPI tiles, and the education→crime linkage report, all derived live from
// the composed engine.census observer (census_wire.go).
//
// This file mirrors services_publish.go / finance_publish.go /
// viewport_publish.go's one-file-per-integration convention exactly and,
// per the FEAT-208 design's §3.3, builds compose's OWN copy of the wire
// schema — the same JSON tags as ui.screen.census's wire.go, duplicated
// independently, NEVER importing internal/ui/screens/census (GR#20's
// engine-never-imports-ui half of the seam).

// censusWireSchemaVersion mirrors ui.screen.census/wire.go's
// wireSchemaVersion constant VALUE (1), kept as a separate, independently
// maintained value per the same GR#20/SF-1 discipline the other three
// publish files' identical constants follow.
const censusWireSchemaVersion = 1

// censusWireBlueWhiteCollar mirrors ui.screen.census/wire.go's
// wireBlueWhiteCollar.
type censusWireBlueWhiteCollar struct {
	Blue  int64 `json:"blue"`
	White int64 `json:"white"`
}

// censusWireKPITile mirrors ui.screen.census/wire.go's wireKPITile.
type censusWireKPITile struct {
	Key   string  `json:"key"`
	Value float64 `json:"value"`
}

// censusWireEducationCrimeLinkage mirrors ui.screen.census/wire.go's
// wireEducationCrimeLinkage.
type censusWireEducationCrimeLinkage struct {
	Population         int64   `json:"population"`
	MeanAttainment     float64 `json:"meanAttainment"`
	CrimeRate          float64 `json:"crimeRate"`
	UneducatedFraction float64 `json:"uneducatedFraction"`
	PolicyCoefficient  float64 `json:"policyCoefficient"`
}

// censusWirePatch is compose's own copy of ui.screen.census/wire.go's
// wirePatch — only the headline fields (ageBands, sexSeries,
// educationTiers, blueWhiteCollar, kpis, educationCrimeLinkage) are ever
// populated. kpiSources/selectedBio (the drill-in sub-surfaces) are
// deliberately left nil/omitted until the drill-in command path is wired
// (the ASM-1482 routing seam ui.screen.census/doc.go itself names as still
// open) — an omitted field decodes as "not sent", which the screen's
// ApplyDelta treats as "keep last-known-good / unavailable", never a
// fabricated empty figure.
type censusWirePatch struct {
	SchemaVersion         int                              `json:"schemaVersion"`
	AgeBands              *[5]int64                        `json:"ageBands,omitempty"`
	SexSeries             *[2]int64                        `json:"sexSeries,omitempty"`
	EducationTiers        *[8]int64                        `json:"educationTiers,omitempty"`
	BlueWhiteCollar       *censusWireBlueWhiteCollar       `json:"blueWhiteCollar,omitempty"`
	KPIs                  *[]censusWireKPITile             `json:"kpis,omitempty"`
	EducationCrimeLinkage *censusWireEducationCrimeLinkage `json:"educationCrimeLinkage,omitempty"`
}

// buildCensusPatch reads the composed engine.census observer's live
// snapshot and returns the "f6.census" patch. It runs on the subscription
// pump goroutine (subscribe.go's ViewPatchFunc contract), CONCURRENTLY with
// the phase pipeline's citizen mutations — safe only because every read
// goes through CensusAPI's own synchronization (Snapshot takes c.mu.RLock
// for the source pointers, then each source's own lock for its data) and
// the returned figures are derived as pure functions of that snapshot,
// never a plain simState field read (the same discipline compose.go's
// simState doc comment spells out for the other publish files).
//
// Full-derive rather than cached-LatestAggregates: the KPI values and the
// blue/white split are only computable from a *census.Snapshot, and
// CensusAPI does not store its last snapshot — only the aggregates the
// observer threads derive. Re-deriving on demand is correct and cheap at
// baseline-one population (64 seed citizens).
func (st *simState) buildCensusPatch() (json.RawMessage, error) {
	month, err := st.currentMonth()
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "census", "accessor": "clock.Month"})
	}
	snap, err := st.census.Snapshot(month, st.cid)
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "census", "accessor": "Snapshot"})
	}
	agg := st.census.Stats(snap)
	bwc := st.census.BlueWhiteCollar(snap)
	link := st.census.EducationCrimeLinkage(snap)

	ageBands := agg.AgeBands
	sex := agg.Sex
	tiers := agg.EducationTiers

	kpis := []censusWireKPITile{
		{Key: census.KPIKeyGDP, Value: float64(st.census.GDP(snap))},
		{Key: census.KPIKeyHappiness, Value: st.census.Happiness(snap)},
		{Key: census.KPIKeyLandValue, Value: float64(st.census.LandValue(snap))},
		{Key: census.KPIKeyHomeless, Value: float64(st.census.Homeless(snap))},
		{Key: census.KPIKeyInHospital, Value: float64(st.census.InHospital(snap))},
		{Key: census.KPIKeyOutOfWork, Value: float64(st.census.OutOfWork(snap))},
		{Key: census.KPIKeyUnfilledJobs, Value: float64(st.census.UnfilledJobs(snap))},
		{Key: census.KPIKeyJobSkillDemand, Value: float64(st.census.JobSkillDemand(snap))},
	}

	patch := censusWirePatch{
		SchemaVersion:  censusWireSchemaVersion,
		AgeBands:       &ageBands,
		SexSeries:      &sex,
		EducationTiers: &tiers,
		BlueWhiteCollar: &censusWireBlueWhiteCollar{
			Blue:  bwc.Blue,
			White: bwc.White,
		},
		KPIs: &kpis,
		EducationCrimeLinkage: &censusWireEducationCrimeLinkage{
			Population:         link.Population,
			MeanAttainment:     link.MeanAttainment,
			CrimeRate:          link.CrimeRate,
			UneducatedFraction: link.UneducatedFraction,
			PolicyCoefficient:  link.PolicyCoefficient,
		},
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		// Marshalling a plain struct of ints/strings/floats cannot fail;
		// unreachable in practice — mirrored on the other three publish
		// files' identical "cannot fail" branches. Per GR#1, degrade loudly
		// rather than panic.
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "census", "accessor": "json.Marshal"})
	}
	return raw, nil
}

// censusViewSubscriptionName mirrors internal/ui/screens/census/wire.go's
// ViewSubscriptionName constant VALUE ("f6.census") — duplicated
// independently as compose's own string literal, never imported from
// internal/ui/screens/census (GR#20's engine-never-imports-ui half of the
// seam; this file's own doc comment). Kept as its own named constant for
// the same reason servicesViewSubscriptionName / financeViewSubscriptionName
// / viewportViewSubscriptionName are.
const censusViewSubscriptionName = "f6.census"
