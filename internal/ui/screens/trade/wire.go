package trade

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// ViewSubscriptionName is the one view F5 subscribes to (SF-1/SF-2):
// "f5.trade", per int.protocol's ValidateViewName grammar and this
// package's own v1 patch schema documented below.
const ViewSubscriptionName = "f5.trade"

// wireSchemaVersion is the only "f5.trade" schema version this package
// understands. A patch declaring any other value is treated as malformed
// (SF-7/TRD-8 posture: logged via MET-V100 and dropped, never applied,
// never a panic) rather than guessed at — a future incompatible schema
// bump is expected to arrive as a new, explicitly-handled version, not a
// silent best-effort decode.
const wireSchemaVersion = 1

// maxPatchWireBytes bounds a single raw patch payload BEFORE it is ever
// unmarshalled, mirroring ui.screen.map's SEC-039 discipline and
// ui.screen.proj's maxPatchWireBytes — an oversized f5.trade payload
// cannot force an expensive allocation-heavy decode before this package
// gets a chance to reject it.
const maxPatchWireBytes = 1 << 20 // 1 MiB — generous for an aggregate trade feed

// The wire structs below mirror the eventual "f5.trade" view schema field
// for field (same JSON tags), duplicated here rather than imported so this
// package never depends on internal/engine (GR#20, SF-1) — the schema is
// the contract, not the Go type that happens to produce it engine-side
// (the same convention as ui.screen.map's wirePatch and ui.screen.proj's
// wire* structs).
//
// Wire shape (json.Marshal of a full f5.trade patch):
//
//	{
//	  "schemaVersion": 1,
//	  "contracts": [
//	    {"id":"c-1","commodity":"grain","termMonths":12,"monthsRemaining":8,
//	     "cancellationPenaltyMicropounds":1500000,"pricePerUnitMicropounds":45,
//	     "status":"active"}
//	  ],
//	  "junctions": [
//	    {"junctionId":"junction:14","label":"M20/A20 gyratory",
//	     "approaches":[{"approachId":"north","cargo":"freight",
//	                     "truckCount":12,"waitSeconds":45}]}
//	  ],
//	  "warehouse": [
//	    {"commodity":"grain","stockTonnes":1200,"capacityTonnes":2000,
//	     "bufferTonnesPerDay":25,"flowTonnesPerDay":18}
//	  ],
//	  "port": {
//	    "unlocked": true, "berths": 4, "craneRateTonnesPerHour": 40,
//	    "operatingHoursPerDay": 16, "customsThroughputTonnesPerDay": 1500,
//	    "smugglingRisk": 0.35
//	  },
//	  "balance": {
//	    "imports": {
//	      "byCommodity": [{"key":"grain","tonnesPerDay":40,"valuePerDayMicropounds":1800000}],
//	      "byArtery":    [{"key":"sea","tonnesPerDay":60,"valuePerDayMicropounds":2700000}]
//	    },
//	    "exports": { ... same shape ... }
//	  },
//	  "safety": {
//	    "corridors": [
//	      {"corridor":"port-refinery","pipelineCapacityTonnesPerDay":500,
//	       "truckMovementsPerDay":120,"leakRisk":0.02}
//	    ]
//	  }
//	}
//
// Presence semantics (SF-7/TRD-8): every sub-surface field is optional and
// carried as a POINTER so "absent" (view has not delivered it) is distinct
// from "present but empty" (delivered, legitimately no rows). An absent
// sub-surface renders "unavailable", never blank and never stale — the
// screen clears any previously-delivered data for that sub-surface.

type wireContract struct {
	ID                             string `json:"id"`
	Commodity                      string `json:"commodity"`
	TermMonths                     int    `json:"termMonths"`
	MonthsRemaining                int    `json:"monthsRemaining"`
	CancellationPenaltyMicropounds int64  `json:"cancellationPenaltyMicropounds"`
	PricePerUnitMicropounds        int64  `json:"pricePerUnitMicropounds"`
	Status                         string `json:"status"`
}

type wireApproach struct {
	ApproachID  string `json:"approachId"`
	Cargo       string `json:"cargo"`
	TruckCount  int    `json:"truckCount"`
	WaitSeconds int    `json:"waitSeconds"`
}

type wireJunction struct {
	JunctionID string         `json:"junctionId"`
	Label      string         `json:"label"`
	Approaches []wireApproach `json:"approaches,omitempty"`
}

type wireWarehouse struct {
	Commodity          string `json:"commodity"`
	StockTonnes        int64  `json:"stockTonnes"`
	CapacityTonnes     int64  `json:"capacityTonnes"`
	BufferTonnesPerDay int64  `json:"bufferTonnesPerDay"`
	FlowTonnesPerDay   int64  `json:"flowTonnesPerDay"`
}

type wirePort struct {
	Unlocked                      bool    `json:"unlocked"`
	Berths                        int64   `json:"berths"`
	CraneRateTonnesPerHour        int64   `json:"craneRateTonnesPerHour"`
	OperatingHoursPerDay          int64   `json:"operatingHoursPerDay"`
	CustomsThroughputTonnesPerDay int64   `json:"customsThroughputTonnesPerDay"`
	SmugglingRisk                 float64 `json:"smugglingRisk"`
}

type wireTradeFlow struct {
	Key                    string `json:"key"`
	TonnesPerDay           int64  `json:"tonnesPerDay"`
	ValuePerDayMicropounds int64  `json:"valuePerDayMicropounds"`
}

type wireLedger struct {
	ByCommodity []wireTradeFlow `json:"byCommodity,omitempty"`
	ByArtery    []wireTradeFlow `json:"byArtery,omitempty"`
}

type wireBalance struct {
	Imports *wireLedger `json:"imports,omitempty"`
	Exports *wireLedger `json:"exports,omitempty"`
}

type wireCorridor struct {
	Corridor                     string  `json:"corridor"`
	PipelineCapacityTonnesPerDay int64   `json:"pipelineCapacityTonnesPerDay"`
	TruckMovementsPerDay         int64   `json:"truckMovementsPerDay"`
	LeakRisk                     float64 `json:"leakRisk"`
}

type wireSafety struct {
	Corridors []wireCorridor `json:"corridors,omitempty"`
}

type wirePatch struct {
	SchemaVersion int              `json:"schemaVersion"`
	Contracts     *[]wireContract  `json:"contracts,omitempty"`
	Junctions     *[]wireJunction  `json:"junctions,omitempty"`
	Warehouse     *[]wireWarehouse `json:"warehouse,omitempty"`
	Port          *wirePort        `json:"port,omitempty"`
	Balance       *wireBalance     `json:"balance,omitempty"`
	Safety        *wireSafety      `json:"safety,omitempty"`
}

// decodeWirePatch parses raw as a wirePatch. It returns an error for
// anything ApplyDelta should treat as malformed: an oversized wire payload
// (checked first, before json.Unmarshal — SEC-039), invalid JSON, or a
// schemaVersion this package doesn't understand. Beyond that it performs
// no other validation; per-field availability is handled by the apply/
// render path rather than decode.
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

// decodeContractStatus maps a wire status string to ContractStatus.
// "cancelled" -> StatusCancelled; anything else (including "" and unknown
// producer values) -> StatusActive. Deliberate: an unknown status is still
// a contract the player holds, and treating it as active (and thus visible
// and cancelable) is less wrong than hiding it as if cancelled.
func decodeContractStatus(raw string) ContractStatus {
	switch raw {
	case "cancelled":
		return StatusCancelled
	default:
		return StatusActive
	}
}

// decodeCargo maps a wire cargo string to a widgets.CargoKind, reusing the
// widget library's closed cargo taxonomy (GR#3 — no bespoke parallel enum).
// Unknown values fall back to CargoGeneral, matching widgets.CargoKind.
// Glyph's own default-on-unknown posture.
func decodeCargo(raw string) widgets.CargoKind {
	switch raw {
	case "fuel":
		return widgets.CargoFuel
	case "passenger":
		return widgets.CargoPassenger
	case "waste":
		return widgets.CargoWaste
	case "freight":
		return widgets.CargoFreight
	default:
		return widgets.CargoGeneral
	}
}
