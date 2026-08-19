package tourism

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/leisure"
	"github.com/aaronukgarcia/Metropolis/internal/engine/news"
	"github.com/aaronukgarcia/Metropolis/internal/engine/season"
)

// Dependency seams (GR#20 contract-first): engine.tourism consumes each
// registered outbound dependency through a narrow interface, so the
// composition root wires the real implementation and tests inject fakes.
// The compile-time assertions below prove the concrete implementation
// satisfies each seam AND keep the real engine.{attract,leisure,season,news}
// import alive in this package (the registered edges AC-3/AC-4 name — and
// the exact proof form the grep checks look for).

// AttractAPI is the narrow engine.attract surface tourism consumes: the
// §11 reputation term (AC-3). Reputation is never recomputed here — single
// source of truth (GR#3).
type AttractAPI interface {
	Reputation() float64
}

// LeisureAPI is the narrow engine.leisure surface tourism consumes for the
// venues portfolio term (AC-2): the citywide per-category venue capacity.
type LeisureAPI interface {
	VenueMix(district uint16, correlationID string) ([leisure.NumCategories]float64, error)
}

// SeasonAPI is the narrow engine.season surface tourism consumes for the
// §44 seaside seasonal curve (AC-4): the beach/indoor leisure-mix pair,
// whose Beach weight is the data-derived summer-peaking draw multiplier
// (never a hardcoded ×3 literal).
type SeasonAPI interface {
	LeisureMix(monthIndex int64) (season.LeisureWeights, error)
}

// NewsAPI is the narrow engine.news surface tourism consumes to SUPPLY an
// event (the registered engine.tourism→engine.news edge) — never the
// ticker copy, which is engine.news's own rendering job (out of scope).
type NewsAPI interface {
	Ingest(ev news.Event) (news.Story, error)
}

// Compile-time assertions: the concrete implementations satisfy the seams.
var (
	_ AttractAPI = (*attract.AttractAPI)(nil)
	_ LeisureAPI = (*leisure.LeisureAPI)(nil)
	_ SeasonAPI  = (*season.SeasonAPI)(nil)
	_ NewsAPI    = (*news.NewsAPI)(nil)
)
