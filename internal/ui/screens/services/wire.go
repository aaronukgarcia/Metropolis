package services

import "encoding/json"

const ViewSubscriptionName = "f4.services"
const wireSchemaVersion = 1
const maxPatchWireBytes = 1 << 20 // 1 MiB

type wireServiceSlider struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Step  float64 `json:"step"`
}

type wireCapacityDemand struct {
	ServiceID     string  `json:"serviceId"`
	Label         string  `json:"label"`
	CapacityUnits float64 `json:"capacityUnits"`
	DemandUnits   float64 `json:"demandUnits"`
}

type wireResponseTimeStat struct {
	ServiceID     string  `json:"serviceId"`
	Label         string  `json:"label"`
	MedianSeconds float64 `json:"medianSeconds"`
	P90Seconds    float64 `json:"p90Seconds"`
	SampleCount   int     `json:"sampleCount"`
}

type wireWaitingList struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`
	CurrentCount int       `json:"currentCount"`
	TrendHistory []float64 `json:"trendHistory,omitempty"`
}

// wirePieSlice is SVC-6's Public Service Pie slice: a benchmark
// per-1k-population ratio (§54) alongside the player's actual funding
// level. BLOCKED — see doc.go's SVC-6 note: engine.fiscal is not a
// registered code.json outbound edge for ui.screen.services (BUG-058
// candidate), so no engine can populate BenchmarkPer1k today. The wire
// field exists for forward compatibility only (mirrors ui.screen.trade's
// TRD-6 wireSafety pattern); ApplyDelta accepts it if ever sent, but
// nothing sends it yet, so PublicServicePie() always reports have=false.
type wirePieSlice struct {
	ServiceID      string  `json:"serviceId"`
	Label          string  `json:"label"`
	BenchmarkPer1k float64 `json:"benchmarkPer1k"`
	ActualFunding  float64 `json:"actualFunding"`
}

type wirePublicServicePie struct {
	Slices []wirePieSlice `json:"slices"`
}

type wirePatch struct {
	SchemaVersion    int                     `json:"schemaVersion"`
	Sliders          *[]wireServiceSlider    `json:"sliders,omitempty"`
	CapacityDemand   *[]wireCapacityDemand   `json:"capacityDemand,omitempty"`
	ResponseTimes    *[]wireResponseTimeStat `json:"responseTimes,omitempty"`
	WaitingLists     *[]wireWaitingList      `json:"waitingLists,omitempty"`
	PublicServicePie *wirePublicServicePie   `json:"publicServicePie,omitempty"`
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
