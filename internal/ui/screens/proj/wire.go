package proj

import "encoding/json"

// ViewSubscriptionName is the one view F7 subscribes to (SF-1/SF-2):
// "f7.projections", per int.protocol's ValidateViewName grammar and this
// package's own v1 patch schema documented below.
const ViewSubscriptionName = "f7.projections"

// wireSchemaVersion is the only "f7.projections" schema version this
// package understands. A patch declaring any other value is treated as
// malformed (SF-7/PRJ-6 posture: logged via MET-V001 and dropped, never
// applied, never a panic) rather than guessed at — a future incompatible
// schema bump is expected to arrive as a new, explicitly-handled version,
// not a silent best-effort decode.
const wireSchemaVersion = 1

// maxPatchWireBytes bounds a single raw patch payload BEFORE it is ever
// unmarshalled, mirroring ui.screen.map's SEC-039 discipline (patch.go)
// and ui.screen.demo's maxPatchWireBytes — an oversized f7.projections
// payload cannot force an expensive allocation-heavy decode before this
// package gets a chance to reject it.
const maxPatchWireBytes = 1 << 20 // 1 MiB — generous for an aggregate curve feed

// The wire structs below mirror engine.projections' eventual
// "f7.projections" view schema field for field (same JSON tags),
// duplicated here rather than imported so this package never depends on
// internal/engine (GR#20, SF-1) — the schema is the contract, not the Go
// type that happens to produce it engine-side (the same convention as
// ui.screen.map's wirePatch and ui.screen.demo's wire* structs).
//
// Wire shape (json.Marshal of a full f7.projections patch):
//
//	{
//	  "schemaVersion": 1,
//	  "horizonMonths": 72,
//	  "curves": [
//	    {
//	      "key": "water.demand",
//	      "label": "Water demand",
//	      "status": "available",
//	      "history": [ ... ],
//	      "projection": [ ... ],
//	      "confidenceUpper": [ ... ],
//	      "confidenceLower": [ ... ],
//	      "thresholds": [ {"value": 123.0, "label": "capacity ceiling"} ],
//	      "markers": [ {"monthOffset": 18, "label": "school build"} ]
//	    }
//	  ],
//	  "crossings": [
//	    {
//	      "key": "refuse.ashford",
//	      "label": "Refuse export — Ashford",
//	      "status": "available",
//	      "internalDemand": [ ... ],
//	      "contractedCapacity": [ ... ],
//	      "crossingMonth": 24
//	    }
//	  ],
//	  "rateOutlook": {
//	    "status": "available",
//	    "history": [ ... ],
//	    "projection": [ ... ]
//	  }
//	}
//
// Field notes:
//   - status: "available" | "unavailable" | "not-unlocked". Any other
//     string decodes to StatusUnavailable with a reason naming the raw
//     value (types.go's CurveStatus doc).
//   - unavailableReason: present when status is "unavailable"/"not-
//     unlocked"; this package also honours it when status is absent/
//     unrecognised.
//   - crossingMonth: -1 means "no crossing within the horizon".
//   - All numeric series are []float64. An absent series (key omitted) is
//     an empty series (renders nothing), matching the map/demo screens'
//     "omitted key = zero value" decode posture.

type wireThreshold struct {
	Value float64 `json:"value"`
	Label string  `json:"label,omitempty"`
}

type wireMarker struct {
	MonthOffset int    `json:"monthOffset"`
	Label       string `json:"label,omitempty"`
}

type wireCurve struct {
	Key               string          `json:"key"`
	Label             string          `json:"label"`
	Status            string          `json:"status"`
	UnavailableReason string          `json:"unavailableReason,omitempty"`
	History           []float64       `json:"history,omitempty"`
	Projection        []float64       `json:"projection,omitempty"`
	ConfidenceUpper   []float64       `json:"confidenceUpper,omitempty"`
	ConfidenceLower   []float64       `json:"confidenceLower,omitempty"`
	Thresholds        []wireThreshold `json:"thresholds,omitempty"`
	Markers           []wireMarker    `json:"markers,omitempty"`
}

type wireCrossing struct {
	Key                string    `json:"key"`
	Label              string    `json:"label"`
	Status             string    `json:"status"`
	UnavailableReason  string    `json:"unavailableReason,omitempty"`
	InternalDemand     []float64 `json:"internalDemand,omitempty"`
	ContractedCapacity []float64 `json:"contractedCapacity,omitempty"`
	CrossingMonth      int       `json:"crossingMonth"`
}

type wireRateOutlook struct {
	Status            string    `json:"status"`
	UnavailableReason string    `json:"unavailableReason,omitempty"`
	History           []float64 `json:"history,omitempty"`
	Projection        []float64 `json:"projection,omitempty"`
}

type wirePatch struct {
	SchemaVersion int              `json:"schemaVersion"`
	HorizonMonths int              `json:"horizonMonths"`
	Curves        []wireCurve      `json:"curves,omitempty"`
	Crossings     []wireCrossing   `json:"crossings,omitempty"`
	RateOutlook   *wireRateOutlook `json:"rateOutlook,omitempty"`
}

// decodeWirePatch parses raw as a wirePatch. It returns an error for
// anything ApplyDelta should treat as malformed: an oversized wire payload
// (checked first, before json.Unmarshal — SEC-039), invalid JSON, or a
// schemaVersion this package doesn't understand. Beyond that it performs
// no other validation; per-field availability (status strings, marker
// index clamping) is handled by the apply/render path rather than decode.
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

// decodeStatus maps a wire status string to CurveStatus. "available" ->
// StatusAvailable; "not-unlocked" -> StatusNotUnlocked; anything else
// (including "" and unknown producer values) -> StatusUnavailable.
func decodeStatus(raw string) CurveStatus {
	switch raw {
	case "available":
		return StatusAvailable
	case "not-unlocked":
		return StatusNotUnlocked
	default:
		return StatusUnavailable
	}
}
