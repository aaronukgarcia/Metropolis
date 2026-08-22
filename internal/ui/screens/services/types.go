package services

// ServiceSlider is a per-service-category funding control (SVC-1: police,
// fire, health, education, refuse, etc.). Value/Min/Max/Step are the
// slider's UI DISPLAY domain (arbitrary player-facing units — e.g. 0-1000
// or a percentage; ASM-250 flags that no spec text mandates slider bounds,
// so this screen renders whatever range the engine reports instead of
// inventing one). This display domain is deliberately NOT the engine's
// funding-level domain: internal/engine/services/api.go:266-292's
// ServicesAPI.SetFunding stores and hard-validates funding as a level in
// [0,1] (the codebase-wide funding-level convention), never a UI-scaled
// absolute. Screen.SetFunding (screen.go) is the seam that rescales a
// raw display-domain value into that [0,1] fraction before it ever
// reaches the wire — see normalizeFundingLevel.
type ServiceSlider struct {
	ID    string
	Label string
	Value float64
	Min   float64
	Max   float64
	Step  float64
}

// CapacityDemand is SVC-2's per-service capacity-vs-demand figure. Ratio
// returns DemandUnits/CapacityUnits clamped to [0,1] for widgets.Gauge (a
// value >1, meaning demand exceeds capacity, renders as a full/saturated
// gauge rather than an out-of-range one — the gauge cannot itself express
// "demand exceeds capacity", so RenderCapacityDemand also prints the raw
// figures alongside the bar).
type CapacityDemand struct {
	ServiceID     string
	Label         string
	CapacityUnits float64
	DemandUnits   float64
}

// Ratio returns d.DemandUnits/d.CapacityUnits clamped to [0,1]. A
// non-positive CapacityUnits (undefined ratio) returns 0 rather than
// dividing by zero.
func (d CapacityDemand) Ratio() float64 {
	if d.CapacityUnits <= 0 {
		return 0
	}
	r := d.DemandUnits / d.CapacityUnits
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}
	return r
}

// ResponseTimeStat is SVC-4's per-unit response-time distribution figure
// (§26's unified dispatch model: fire, ambulance, air ambulance, police),
// sourced from engine.dispatch's per-unit response data.
type ResponseTimeStat struct {
	ServiceID     string
	Label         string
	MedianSeconds float64
	P90Seconds    float64
	SampleCount   int
}

// WaitingList is SVC-5's waiting-list figure (e.g. hospital non-urgent
// care, §26), rendered with a 12-cell sparkline trend.
type WaitingList struct {
	ID           string
	Label        string
	CurrentCount int
	TrendHistory []float64
}

// PieSlice is SVC-6's Public Service Pie slice — a benchmark ratio (§54:
// per-1k-population targets) alongside the player's actual funding level.
// BLOCKED (see doc.go): no engine currently populates this; PieSlice
// exists so the render/wire plumbing is ready the day BUG-058 lands a
// registered source edge.
type PieSlice struct {
	ServiceID      string
	Label          string
	BenchmarkPer1k float64
	ActualFunding  float64
}

// PublicServicePieView is the full Public Service Pie view.
type PublicServicePieView struct {
	Slices []PieSlice
}
