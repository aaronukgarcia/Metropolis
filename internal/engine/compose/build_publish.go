package compose

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ui.screen.build (F3): the "f3.build" view publisher, an adapter over
// engine.build's BuildAPI (ZoneCatalogue + Queue). Same one-file-per-
// integration convention and the same GR#20 discipline as
// services_publish.go: compose owns its OWN copy of the wire schema
// (the JSON tags of internal/ui/screens/build/wire.go), never imports
// the UI package.
//
// # Decision record: the catalogue is the zone list
//
// handleGameplay maps protocol.BuildPayload.BuildingType onto
// build.ZoneType ("the build module builds zones, not a separate
// building catalogue yet", compose.go KindBuild case). The screen's
// BuildOn only issues a command for an ID present in the published
// catalogue[]. Publishing data/buildings.json IDs would therefore let the
// player pick an entry the engine then rejects as an unknown zone, so the
// catalogue published here is exactly the zone catalogue: ID = zone slug,
// which the engine accepts. Every zone is published "unlocked" because
// engine.unlocks has no per-zone gate in baseline one; claiming
// "locked" would be invention.
//
// Per-order cell: BuildOrder carries (Tile, Local); the wire uses the
// protocol CellRef the commands use, which cellFromRef maps onto the
// start tile as (X=Col, Y=Row).
//
// Sub-surfaces deliberately NOT published (omitted via omitempty, so the
// screen renders them "unavailable" rather than a fabricated zero):
// landPrice and demolition (cursor-dependent; no cell context on a
// subscription).

const buildWireSchemaVersion = 1

// buildViewSubscriptionName mirrors ui.screen.build's ViewSubscriptionName
// VALUE ("f3.build"), duplicated independently (GR#20).
const buildViewSubscriptionName = "f3.build"

type buildWireZone struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Materials        int64  `json:"materials"`
	Labour           int64  `json:"labour"`
	BaseLeadTimeDays int64  `json:"baseLeadTimeDays"`
}

type buildWireCell struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type buildWireOrder struct {
	ID                 uint64        `json:"id"`
	Cell               buildWireCell `json:"cell"`
	Zone               string        `json:"zone"`
	MaterialsBillTotal int64         `json:"materialsBillTotal"`
	MaterialsDrawn     int64         `json:"materialsDrawn"`
	MaterialsRemaining int64         `json:"materialsRemaining"`
	LabourRemaining    int64         `json:"labourRemaining"`
	LeadTimeRemaining  int64         `json:"leadTimeRemaining"`
	Status             string        `json:"status"`
}

type buildWireCatalogueEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Section     string `json:"section"`
	CostRaw     string `json:"costRaw,omitempty"`
	CapacityRaw string `json:"capacityRaw,omitempty"`
	Notes       string `json:"notes,omitempty"`
	UnlockState string `json:"unlockState"`
}

type buildWirePatch struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Zones         *[]buildWireZone           `json:"zones,omitempty"`
	Queue         *[]buildWireOrder          `json:"queue,omitempty"`
	Catalogue     *[]buildWireCatalogueEntry `json:"catalogue,omitempty"`
}

// buildF3Patch reads BuildAPI's zone catalogue (already sorted by zone
// type) and queue (insertion / order-ID order) — no map is ranged here
// (GR#21) — and returns the full "f3.build" patch.
func (st *simState) buildF3Patch() (json.RawMessage, error) {
	zoneInfos := st.buildAPI.ZoneCatalogue()
	zones := make([]buildWireZone, 0, len(zoneInfos))
	catalogue := make([]buildWireCatalogueEntry, 0, len(zoneInfos))
	for _, z := range zoneInfos {
		zones = append(zones, buildWireZone{
			ID: string(z.Zone), Name: z.Name,
			Materials: z.Materials, Labour: z.Labour, BaseLeadTimeDays: z.BaseLeadTimeDays,
		})
		catalogue = append(catalogue, buildWireCatalogueEntry{
			ID: string(z.Zone), Name: z.Name, Section: "zone", UnlockState: "unlocked",
		})
	}

	orders := st.buildAPI.Queue()
	queue := make([]buildWireOrder, 0, len(orders))
	for _, o := range orders {
		queue = append(queue, buildWireOrder{
			ID:                 uint64(o.ID),
			Cell:               buildWireCell{X: o.Local.Col, Y: o.Local.Row},
			Zone:               string(o.Zone),
			MaterialsBillTotal: o.MaterialsBillTotal,
			MaterialsDrawn:     o.MaterialsDrawn,
			MaterialsRemaining: o.MaterialsRemaining,
			LabourRemaining:    o.LabourRemaining,
			LeadTimeRemaining:  o.LeadTimeRemaining,
			Status:             string(o.Status),
		})
	}

	raw, err := json.Marshal(buildWirePatch{
		SchemaVersion: buildWireSchemaVersion,
		Zones:         &zones,
		Queue:         &queue,
		Catalogue:     &catalogue,
	})
	if err != nil {
		return nil, errs.Wrap(ErrModuleFailed, st.cid, err, map[string]any{"module": "build", "accessor": "json.Marshal"})
	}
	return raw, nil
}
