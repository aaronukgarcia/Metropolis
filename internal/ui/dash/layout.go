package dash

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// Layout is an ordered widget grid for one screen: a screen key plus a
// slice of tiles. Tile order is the render order and the drill-walk
// order — a slice, never a map, so ordering is deterministic (AC-12).
type Layout struct {
	screen string
	tiles  []Tile
}

// NewLayout returns an empty layout for screen.
func NewLayout(screen string) Layout {
	return Layout{screen: screen}
}

// Screen returns the layout's screen key (e.g. "f1").
func (l Layout) Screen() string { return l.screen }

// Len returns the number of tiles.
func (l Layout) Len() int { return len(l.tiles) }

// Tiles returns the layout's tiles in render order as a defensive copy
// of the slice. The caller may reorder/append the returned slice without
// affecting the layout's own order (SEC-005: an exported mutable slice
// whose order is a contract is a hole, not an accessor). The Tile values
// themselves are copies; their spec slices are shared read-only (see
// Tile's spec accessors — the editor replaces whole tiles, it does not
// mutate a live tile's spec in place).
func (l Layout) Tiles() []Tile {
	out := make([]Tile, len(l.tiles))
	copy(out, l.tiles)
	return out
}

// clone returns a copy of l whose tiles slice has its own backing array,
// so a caller that mutates the returned layout (AddTile/RemoveTile/
// MoveTile) reorders/truncates its own slice, never l's (SEC-005/SEC-063:
// a shallow struct copy shares the tiles slice's backing array). The Tile
// values are copied one level deep; their spec pointers/slices are still
// shared with l, but are unreachable as mutable state through the public
// API because every spec accessor (Table/Diagram/Bignum/Spark/Minimap/
// Alerts) returns its own deep copy.
func (l Layout) clone() Layout {
	return Layout{screen: l.screen, tiles: cloneSlice(l.tiles)}
}

// FindTile returns a copy of the tile with the given id, if present.
func (l Layout) FindTile(id string) (Tile, bool) {
	for _, t := range l.tiles {
		if t.id == id {
			return t, true
		}
	}
	return Tile{}, false
}

// AddTile appends t to the layout. It re-validates t's DrillTarget
// fail-closed (AC-4): because Tile.drill is unexported, a caller can
// only obtain a Tile through the validating New<Kind>Tile constructors,
// but AddTile checks again on the way in so the "no zero-value path"
// invariant holds at the boundary too, not merely at construction.
func (l *Layout) AddTile(t Tile) error {
	if err := requireDrill(t.drill, map[string]any{"tileId": t.id, "kind": string(t.kind)}); err != nil {
		return err
	}
	// A tile ID that already exists would make RemoveTile/MoveTile/Drill
	// ambiguous and break profile round-trip identity (two tiles with the
	// same id collapse on load). Reject rather than silently shadow.
	for _, existing := range l.tiles {
		if existing.id == t.id {
			return errs.New(codeTileNeedsDrill, corr(), map[string]any{
				"tileId": t.id,
				"reason": "duplicate tile id",
			})
		}
	}
	l.tiles = cloneSlice(l.tiles)
	l.tiles = append(l.tiles, t)
	return nil
}

// RemoveTile removes the tile with the given id. It returns a
// registry-sourced error (MET-U604) if no such tile exists — a loud
// miss, not a silent no-op (GR#1).
func (l *Layout) RemoveTile(id string) error {
	for i, t := range l.tiles {
		if t.id == id {
			l.tiles = cloneSlice(l.tiles)
			l.tiles = append(l.tiles[:i], l.tiles[i+1:]...)
			return nil
		}
	}
	return errs.New(codeUnknownTile, corr(), map[string]any{"tileId": id})
}

// MoveTile moves the tile with the given id to position to, shifting the
// tiles in between. to is clamped into [0, len-1]. It returns
// MET-U604 if no such tile exists.
func (l *Layout) MoveTile(id string, to int) error {
	from := -1
	for i, t := range l.tiles {
		if t.id == id {
			from = i
			break
		}
	}
	if from < 0 {
		return errs.New(codeUnknownTile, corr(), map[string]any{"tileId": id})
	}
	if len(l.tiles) <= 1 {
		return nil
	}
	if to < 0 {
		to = 0
	}
	if to >= len(l.tiles) {
		to = len(l.tiles) - 1
	}
	if to == from {
		return nil
	}
	l.tiles = cloneSlice(l.tiles)
	t := l.tiles[from]
	l.tiles = append(l.tiles[:from], l.tiles[from+1:]...)
	// to is the tile's requested final index, not an index into the
	// post-removal slice: after the remove, the elements that followed from
	// have already shifted left into their final places, so inserting the
	// removed tile at to on the now-one-shorter slice lands it exactly at
	// final index to. Subtracting one for the forward case would insert it
	// one position early (an adjacent forward move becomes a no-op).
	insertAt := to
	l.tiles = append(l.tiles[:insertAt], append([]Tile{t}, l.tiles[insertAt:]...)...)
	return nil
}

// tileWire is the JSON shape of one Tile on the wire (profile JSON). It
// mirrors Tile but with every field exported so encoding/json can
// round-trip it; UnmarshalLayout reconstructs the unexported-field Tile
// from it, re-validating the drill targets (so a hand-edited or corrupt
// profile cannot smuggle a zero-drill tile past AC-4).
type tileWire struct {
	ID    string      `json:"id"`
	Kind  TileKind    `json:"kind"`
	Drill DrillTarget `json:"drill"`

	Bignum  *BignumSpec  `json:"bignum,omitempty"`
	Gauge   *GaugeSpec   `json:"gauge,omitempty"`
	Spark   *SparkSpec   `json:"spark,omitempty"`
	Minimap *MinimapSpec `json:"minimap,omitempty"`
	Alerts  *AlertsSpec  `json:"alerts,omitempty"`
	Table   *TableSpec   `json:"table,omitempty"`
	Diagram *DiagramSpec `json:"diagram,omitempty"`
}

// layoutWire is the JSON shape of a whole saved layout profile. Name is
// the player-facing profile name — read by ui.screen.menu's
// LoadLayoutProfile (which peeks the top-level "name" key and otherwise
// treats the document as opaque) — distinct from Screen, the F-screen
// key the layout is for.
type layoutWire struct {
	Name   string     `json:"name,omitempty"`
	Screen string     `json:"screen"`
	Tiles  []tileWire `json:"tiles"`
}

// Marshal serializes l to profile JSON (AC-3's save path). It is the
// inverse of UnmarshalLayout. The profile name is left empty; use
// MarshalProfile to write a named profile.
func Marshal(l Layout) ([]byte, error) {
	return MarshalProfile("", l)
}

// MarshalProfile serializes l to profile JSON under the given player
// name, the shape ui.screen.menu's LoadLayoutProfile reads back.
func MarshalProfile(name string, l Layout) ([]byte, error) {
	w := layoutWire{Name: name, Screen: l.screen, Tiles: make([]tileWire, 0, len(l.tiles))}
	for _, t := range l.tiles {
		tw := tileWire{ID: t.id, Kind: t.kind, Drill: t.drill}
		switch t.kind {
		case KindBigNum:
			s := t.bignum
			tw.Bignum = &s
		case KindGauge:
			s := t.gauge
			tw.Gauge = &s
		case KindSpark:
			s := t.spark
			tw.Spark = &s
		case KindMiniMap:
			s := t.minimap
			tw.Minimap = &s
		case KindAlerts:
			s := t.alerts
			tw.Alerts = &s
		case KindTable:
			tw.Table = t.table
		case KindDiagram:
			tw.Diagram = t.diagram
		}
		w.Tiles = append(w.Tiles, tw)
	}
	return json.Marshal(w)
}

// UnmarshalLayout parses a saved layout profile JSON blob into a Layout.
// It re-validates every tile and every element's DrillTarget, returning
// a registry-sourced error (MET-U601) on malformed JSON OR on a
// structurally-corrupt layout (a tile or element with a missing/invalid
// drill target) — a corrupt profile is rejected, never half-loaded.
func UnmarshalLayout(data []byte) (Layout, error) {
	var w layoutWire
	if err := json.Unmarshal(data, &w); err != nil {
		return Layout{}, errs.Wrap(codeMalformedProfile, corr(), err, map[string]any{"cause": err.Error()})
	}
	l := Layout{screen: w.Screen}
	for _, tw := range w.Tiles {
		t, err := tileFromWire(tw)
		if err != nil {
			return Layout{}, errs.Wrap(codeMalformedProfile, corr(), err, map[string]any{
				"tileId": tw.ID,
				"kind":   string(tw.Kind),
				"cause":  err.Error(),
			})
		}
		l.tiles = append(l.tiles, t)
	}
	return l, nil
}

// tileFromWire rebuilds a Tile from its wire shape, re-running the same
// constructor validation the public API uses (empty id, invalid drill,
// invalid element drills all fail closed).
func tileFromWire(tw tileWire) (Tile, error) {
	t, err := newTile(tw.ID, tw.Kind, tw.Drill)
	if err != nil {
		return Tile{}, err
	}
	switch tw.Kind {
	case KindBigNum:
		if tw.Bignum != nil {
			t.bignum = *tw.Bignum
		}
	case KindGauge:
		if tw.Gauge != nil {
			t.gauge = *tw.Gauge
		}
	case KindSpark:
		if tw.Spark != nil {
			t.spark = *tw.Spark
		}
	case KindMiniMap:
		if tw.Minimap != nil {
			t.minimap = *tw.Minimap
		}
	case KindAlerts:
		if tw.Alerts != nil {
			t.alerts = *tw.Alerts
		}
	case KindTable:
		if tw.Table == nil {
			return Tile{}, errs.New(codeMalformedProfile, corr(), map[string]any{
				"tileId": tw.ID, "reason": "table tile missing table payload", "cause": "table tile missing table payload",
			})
		}
		for i := range tw.Table.Rows {
			if err := requireDrill(tw.Table.Rows[i].Drill, map[string]any{"tileId": tw.ID, "row": i}); err != nil {
				return Tile{}, err
			}
		}
		t.table = cloneTableSpec(tw.Table)
	case KindDiagram:
		if tw.Diagram == nil {
			return Tile{}, errs.New(codeMalformedProfile, corr(), map[string]any{
				"tileId": tw.ID, "reason": "diagram tile missing diagram payload", "cause": "diagram tile missing diagram payload",
			})
		}
		for i := range tw.Diagram.Hits {
			if err := requireDrill(tw.Diagram.Hits[i].Drill, map[string]any{"tileId": tw.ID, "hit": i}); err != nil {
				return Tile{}, err
			}
		}
		t.diagram = cloneDiagramSpec(tw.Diagram)
	default:
		return Tile{}, errs.New(codeMalformedProfile, corr(), map[string]any{
			"tileId": tw.ID, "kind": string(tw.Kind), "reason": "unknown tile kind", "cause": "unknown tile kind",
		})
	}
	return t, nil
}

// LoadProfile loads a saved layout profile. On malformed or corrupt JSON
// it returns the shipped default layout for the screen alongside a
// registry-sourced error (MET-U601) — AC-10: the dashboard is never left
// unrenderable, and the fallback is the default, not a corrupted or
// partial load.
func LoadProfile(data []byte, screen string) (Layout, error) {
	l, err := UnmarshalLayout(data)
	if err != nil {
		return DefaultLayout(screen), err
	}
	return l, nil
}

// DefaultLayout returns the shipped default dashboard layout for screen.
// For "f1" it returns the Overview (F1's right rail) dashboard instance
// UI-SPEC §4 names ("the Overview (F1's right rail) is just a dashboard
// instance"): a fixed tile set covering the bignum, gauge, sparkline,
// table, mini-map and alert-list types, each bound to a drill target.
// For any other screen it returns a valid, empty layout (a screen that
// has not shipped a default yet is an empty dashboard, not an error).
//
// The tile set is data derived from this function, not a constant the
// caller must trust (GR#15): the AC-2 test loads it and asserts the
// returned tile kinds, so a change to the shipped set is caught by CI,
// not by a player finding a missing tile.
func DefaultLayout(screen string) Layout {
	l := Layout{screen: screen}
	if screen != "f1" {
		return l
	}
	// Each tile's DrillTarget uses int.protocol's entity-scoped view-name
	// grammar so whole-entity drill-through already resolves today (see
	// the package doc's "What a DrillTarget can name").
	must := func(t Tile, err error) Tile {
		if err != nil {
			// A shipped default with a bad target is a programming error
			// caught at package-test time, never a runtime condition to
			// paper over.
			panic("dash: default f1 layout: " + err.Error())
		}
		return t
	}
	l.tiles = append(l.tiles, must(NewBignumTile("f1.population",
		DrillTarget{ViewName: "f1.viewport"},
		BignumSpec{Label: "Population", ValueText: "0", Series: []float64{0}},
	)))
	l.tiles = append(l.tiles, must(NewGaugeTile("f1.jobs",
		DrillTarget{ViewName: "f1.viewport"},
		GaugeSpec{Label: "Job capacity", Value: 0},
	)))
	l.tiles = append(l.tiles, must(NewSparkTile("f1.cash",
		DrillTarget{ViewName: "f2.ledger"},
		SparkSpec{Label: "Cash trend", Series: []float64{0}},
	)))
	l.tiles = append(l.tiles, must(NewTableTile("f1.ledger",
		DrillTarget{ViewName: "f2.ledger"},
		TableSpec{
			Columns: []widgets.Column{{Title: "Line", Width: 8}, {Title: "Amount", Width: 12}},
			Rows: []TableRow{
				{Cells: []string{"Balance", "0"}, Drill: DrillTarget{ViewName: "f2.ledger"}},
			},
		},
	)))
	l.tiles = append(l.tiles, must(NewMinimapTile("f1.minimap",
		DrillTarget{ViewName: "f1.viewport"},
		MinimapSpec{Label: "Overview", Values: []float64{0}, Width: 1},
	)))
	l.tiles = append(l.tiles, must(NewAlertsTile("f1.alerts",
		DrillTarget{ViewName: "f1.viewport"},
		AlertsSpec{Label: "Alerts"},
	)))
	return l
}
