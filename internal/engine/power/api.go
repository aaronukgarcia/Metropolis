package power

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// PowerLine is one placed pylon span: a line segment between two
// tile-local cells, carrying its class tier's transmission capacity.
// It is the engine-side "world object" this slice publishes through
// "f1.viewport"'s powerLines field; the wire shape is compose's and the
// consumers' own mirrored concern (GR#20).
type PowerLine struct {
	ID           uint64     // monotonic placement order — the deterministic publish order
	Class        PylonClass // catalogue tier
	FromX, FromY int
	ToX, ToY     int
	CapacityMW   float64 // from the catalogue row at placement time
}

// Bounds is the inclusive-exclusive cell domain placements live in:
// a coordinate is valid when 0 <= x < Width and 0 <= y < Height
// (compose passes world.TileSizeCells for both). Kept as a parameter,
// not an import of internal/engine/world — this package consumes no
// unregistered module edge (GR#20).
type Bounds struct {
	Width, Height int
}

func (b Bounds) contains(x, y int) bool {
	return x >= 0 && x < b.Width && y >= 0 && y < b.Height
}

// PowerAPI holds placed pylon spans. Follows the house copy-guard
// discipline (SEC-020 family): mu is a sync.RWMutex VALUE while lines is
// a reference-type slice, so a struct copy would alias the original's
// state under its own independently-zeroed lock — checkNotCopied rejects
// every such call before mu is touched (see DepositMap.self's doc
// comment, which this field mirrors exactly).
type PowerAPI struct {
	cat    PylonCatalogue
	bounds Bounds

	mu    sync.RWMutex
	lines []PowerLine
	next  uint64

	self atomic.Pointer[PowerAPI]
}

// New constructs a *PowerAPI over cat, accepting placements inside b.
// A zero Bounds accepts nothing (fail-closed); compose always passes the
// real start-tile domain.
func New(cat PylonCatalogue, b Bounds) *PowerAPI {
	p := &PowerAPI{cat: cat, bounds: b}
	p.self.Store(p)
	return p
}

// checkNotCopied reports whether p is still the value New returned.
// Lock-free (atomic load), so it can run BEFORE mu is ever touched
// (SEC-016's pre-lock-ordering requirement).
func (p *PowerAPI) checkNotCopied(method string) error {
	if p.self.Load() != p {
		return errs.New(ErrPowerAPICopied, errs.NewCorrelationID(), map[string]any{"method": method})
	}
	return nil
}

// PlaceLine validates and records one pylon span of the named catalogue
// class between (fromX, fromY) and (toX, toY), returning the stored
// PowerLine. Rejections are registry-sourced and loud, never silent
// no-ops (GR#1/GR#7):
//
//   - unknown class name          -> ErrUnknownClass
//   - endpoint outside bounds     -> ErrPlacementInvalid
//   - degenerate segment (from==to) -> ErrPlacementInvalid
func (p *PowerAPI) PlaceLine(class string, fromX, fromY, toX, toY int, correlationID string) (PowerLine, error) {
	if err := p.checkNotCopied("PlaceLine"); err != nil {
		return PowerLine{}, err
	}

	c, ok := pylonClassByName(class)
	if !ok {
		return PowerLine{}, errs.New(ErrUnknownClass, correlationID, map[string]any{
			"class": class,
		})
	}
	tier, ok := p.cat.Tier(c)
	if !ok {
		return PowerLine{}, errs.New(ErrUnknownClass, correlationID, map[string]any{
			"class": class,
			"cause": "class known to the enum but absent from the loaded catalogue",
		})
	}
	if !p.bounds.contains(fromX, fromY) || !p.bounds.contains(toX, toY) {
		return PowerLine{}, errs.New(ErrPlacementInvalid, correlationID, map[string]any{
			"from":   []int{fromX, fromY},
			"to":     []int{toX, toY},
			"bounds": []int{p.bounds.Width, p.bounds.Height},
			"rule":   "endpoint outside the tile-local domain",
		})
	}
	if fromX == toX && fromY == toY {
		return PowerLine{}, errs.New(ErrPlacementInvalid, correlationID, map[string]any{
			"from": []int{fromX, fromY},
			"to":   []int{toX, toY},
			"rule": "degenerate segment: both endpoints identical",
		})
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.next++
	line := PowerLine{
		ID:         p.next,
		Class:      c,
		FromX:      fromX,
		FromY:      fromY,
		ToX:        toX,
		ToY:        toY,
		CapacityMW: tier.CapacityMW,
	}
	p.lines = append(p.lines, line)
	return line, nil
}

// Lines returns a copy of every placed span, ordered by placement ID —
// the deterministic publish order consumers see (GR#21: insertion into
// p.lines is already ID-ascending since next is monotonic under the
// lock; the explicit sort makes that contract true by construction, not
// by incidental append order).
func (p *PowerAPI) Lines(correlationID string) ([]PowerLine, error) {
	if err := p.checkNotCopied("Lines"); err != nil {
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]PowerLine, len(p.lines))
	copy(out, p.lines)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
