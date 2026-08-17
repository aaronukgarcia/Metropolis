package build

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// ViewSubscriptionName is the one view F3 subscribes to (SF-1/SF-2):
// "f3.build", per int.protocol's ValidateViewName grammar and this
// package's own v1 patch schema documented below.
const ViewSubscriptionName = "f3.build"

// wireSchemaVersion is the only "f3.build" schema version this package
// understands. A patch declaring any other value is treated as malformed
// (SF-7/BLD-8 posture: logged via MET-V200 and dropped, never applied,
// never a panic) rather than guessed at — a future incompatible schema
// bump is expected to arrive as a new, explicitly-handled version, not a
// silent best-effort decode.
const wireSchemaVersion = 1

// maxPatchWireBytes bounds a single raw patch payload BEFORE it is ever
// unmarshalled, mirroring ui.screen.map's SEC-039 discipline and
// ui.screen.trade's/ui.screen.proj's maxPatchWireBytes — an oversized
// f3.build payload cannot force an expensive allocation-heavy decode
// before this package gets a chance to reject it. The full building
// catalogue is the largest sub-surface; 1 MiB is generous for an
// aggregate build feed.
const maxPatchWireBytes = 1 << 20 // 1 MiB

// The wire structs below mirror the eventual "f3.build" view schema field
// for field (same JSON tags), duplicated here rather than imported so this
// package never depends on internal/engine (GR#20, SF-1) — the schema is
// the contract, not the Go type that happens to produce it engine-side
// (the same convention as ui.screen.trade's wire*.go, ui.screen.map's
// wirePatch, and ui.screen.proj's wire* structs).
//
// Wire shape (json.Marshal of a full f3.build patch):
//
//	{
//	  "schemaVersion": 1,
//	  "zones": [
//	    {"id":"dwelling","name":"Dwelling","materials":100,"labour":40,"baseLeadTimeDays":45}
//	  ],
//	  "queue": [
//	    {"id":1,"cell":{"x":2,"y":3},"zone":"dwelling",
//	     "materialsBillTotal":100,"materialsDrawn":40,"materialsRemaining":60,
//	     "labourRemaining":20,"leadTimeRemaining":15,"status":"in-progress"}
//	  ],
//	  "catalogue": [
//	    {"id":"footpath","name":"Footpath","section":"R","costRaw":"20k",
//	     "capacityRaw":"","notes":"walk/cycle only","unlockState":"unlocked"}
//	  ],
//	  "landPrice": {"cell":{"x":2,"y":3},"priceMicropounds":1250000},
//	  "demolition": {"cell":{"x":2,"y":3},"compensationMicropounds":600000}
//	}
//
// Cell references are the protocol's own CellRef (x,y grid offsets,
// internal/protocol/commands.go) — reused verbatim rather than a bespoke
// parallel wire-cell type (GR#3). The engine's two-level (tile, local)
// addressing is its own vocabulary; this UI layer addresses cells as the
// commands do (ASM-1447).
//
// Presence semantics (SF-7/BLD-8): every sub-surface field is optional and
// carried as a POINTER so "absent" (view has not delivered it) is distinct
// from "present but empty" (delivered, legitimately no rows). An absent
// sub-surface renders "unavailable", never blank and never stale — the
// screen clears any previously-delivered data for that sub-surface.

type wireZone struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Materials        int64  `json:"materials"`
	Labour           int64  `json:"labour"`
	BaseLeadTimeDays int64  `json:"baseLeadTimeDays"`
}

type wireBuildOrder struct {
	ID                 uint64           `json:"id"`
	Cell               protocol.CellRef `json:"cell"`
	Zone               string           `json:"zone"`
	MaterialsBillTotal int64            `json:"materialsBillTotal"`
	MaterialsDrawn     int64            `json:"materialsDrawn"`
	MaterialsRemaining int64            `json:"materialsRemaining"`
	LabourRemaining    int64            `json:"labourRemaining"`
	LeadTimeRemaining  int64            `json:"leadTimeRemaining"`
	Status             string           `json:"status"`
}

type wireCatalogueEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Section     string `json:"section"`
	CostRaw     string `json:"costRaw,omitempty"`
	CapacityRaw string `json:"capacityRaw,omitempty"`
	Notes       string `json:"notes,omitempty"`
	UnlockState string `json:"unlockState"`
}

type wireLandPrice struct {
	Cell             protocol.CellRef `json:"cell"`
	PriceMicropounds int64            `json:"priceMicropounds"`
}

type wireDemolition struct {
	Cell                    protocol.CellRef `json:"cell"`
	CompensationMicropounds int64            `json:"compensationMicropounds"`
}

type wirePatch struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Zones         *[]wireZone           `json:"zones,omitempty"`
	Queue         *[]wireBuildOrder     `json:"queue,omitempty"`
	Catalogue     *[]wireCatalogueEntry `json:"catalogue,omitempty"`
	LandPrice     *wireLandPrice        `json:"landPrice,omitempty"`
	Demolition    *wireDemolition       `json:"demolition,omitempty"`
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

// decodeBuildOrderStatus maps a wire status string to BuildOrderStatus.
// The four engine.build status strings are the contract; anything else
// (including "" and unknown producer values) is treated as in-progress —
// an unknown status is still an order the player has queued, and showing
// it as visibly in the queue is less wrong than hiding it as if complete.
func decodeBuildOrderStatus(raw string) BuildOrderStatus {
	switch raw {
	case "materials-pending":
		return StatusMaterialsPending
	case "labour-pending":
		return StatusLabourPending
	case "complete":
		return StatusComplete
	default:
		return StatusInProgress
	}
}

// decodeUnlockState maps a wire unlock-state string to UnlockState. Only
// the three documented states are recognised; anything else (including ""
// and the explicit "unavailable") degrades to UnlockUnavailable — the
// SF-7/BLD-8 posture that a figure whose source data is absent renders
// "unavailable", never a fabricated state.
func decodeUnlockState(raw string) UnlockState {
	switch raw {
	case "locked":
		return UnlockLocked
	case "in-progress":
		return UnlockInProgress
	case "unlocked":
		return UnlockUnlocked
	default:
		return UnlockUnavailable
	}
}
