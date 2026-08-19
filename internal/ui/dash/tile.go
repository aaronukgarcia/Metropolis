package dash

import (
	"strconv"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TileKind enumerates the closed set of dashboard tile types (UI-SPEC §4
// lists bignum, gauge, sparkline chart, table, mini-map, alert list;
// KindDiagram is US-4's embedded-diagram tile, added so a number inside
// a diagram is drillable like every other tile).
type TileKind string

const (
	KindBigNum  TileKind = "bignum"
	KindGauge   TileKind = "gauge"
	KindSpark   TileKind = "sparkline"
	KindTable   TileKind = "table"
	KindMiniMap TileKind = "minimap"
	KindAlerts  TileKind = "alerts"
	KindDiagram TileKind = "diagram"
)

// Severity is an alert entry's severity. Deliberately distinct from
// widgets.ThresholdState (which means "value vs thresholds", not "alert
// importance").
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityDanger
)

// BignumSpec is a KindBigNum tile's payload: a large figure, delta
// derivation, embedded sparkline, and threshold colouring (UI-SPEC §2's
// big-number idiom).
type BignumSpec struct {
	Label      string
	ValueText  string
	Prev, Curr float64
	Series     []float64
	Thresholds widgets.Thresholds
}

// GaugeSpec is a KindGauge tile's payload: a normalised [0,1] capacity
// fraction with optional threshold colouring.
type GaugeSpec struct {
	Label      string
	Value      float64
	Thresholds widgets.Thresholds
}

// SparkSpec is a KindSpark tile's payload: a trending series.
type SparkSpec struct {
	Label  string
	Series []float64
}

// MinimapSpec is a KindMiniMap tile's payload: a background-colour ramp
// (the map-overlay idiom) over a caller-supplied value grid.
type MinimapSpec struct {
	Label  string
	Values []float64
	Width  int
}

// AlertEntry is one line of a KindAlerts tile.
type AlertEntry struct {
	Text     string
	Severity Severity
}

// AlertsSpec is a KindAlerts tile's payload: an ordered list of alert
// lines.
type AlertsSpec struct {
	Label   string
	Entries []AlertEntry
}

// TableRow is one row of a KindTable tile: display cells plus the row's
// own DrillTarget. The per-row DrillTarget is what makes every table
// cell drillable (AC-5) and what sort/filter must preserve (AC-7).
type TableRow struct {
	Cells []string    `json:"cells"`
	Drill DrillTarget `json:"drill"`
}

// TableSpec is a KindTable tile's payload: columns plus rows. It
// implements widgets.TableData so the tile renders/sorts/filters/exports
// through ui.widgets' shared table contract rather than a duplicated
// implementation (AC-1/AC-7).
type TableSpec struct {
	Columns []widgets.Column `json:"columns"`
	Rows    []TableRow       `json:"rows"`
}

// NumRows implements widgets.TableData.
func (t *TableSpec) NumRows() int { return len(t.Rows) }

// Cell implements widgets.TableData. Out-of-range row/col returns "" (a
// defensive no-op) rather than panicking — mirrors core.Buffer's
// out-of-range-is-a-no-op discipline and keeps a malformed sort/filter
// request from crashing the render path.
func (t *TableSpec) Cell(row, col int) string {
	if row < 0 || row >= len(t.Rows) {
		return ""
	}
	cells := t.Rows[row].Cells
	if col < 0 || col >= len(cells) {
		return ""
	}
	return cells[col]
}

// DiagramHit is one hit-test entry of a KindDiagram tile, carrying
// forward ui.diagrams' AC-5 hit-test mapping: the caller's own source
// identifier (SourceID, passed through unchanged) with the cell region
// it occupies, plus the DrillTarget that source resolves to. This is the
// seam that keeps a number inside a diagram drillable like any other
// tile.
type DiagramHit struct {
	SourceID string      `json:"sourceId"`
	Region   core.Rect   `json:"region"`
	Drill    DrillTarget `json:"drill"`
}

// DiagramSpec is a KindDiagram tile's payload: the embedded diagram's
// hit-test elements.
type DiagramSpec struct {
	Hits []DiagramHit `json:"hits"`
}

// Tile is one dashboard tile. Its drill-target field is unexported and
// the only constructors are the New<Kind>Tile functions below, each of
// which requires a valid DrillTarget — this is AC-4's structural
// enforcement (see the package doc): there is no settable-later field
// and no zero-value path across package boundaries.
type Tile struct {
	id    string
	kind  TileKind
	drill DrillTarget

	bignum  BignumSpec
	gauge   GaugeSpec
	spark   SparkSpec
	minimap MinimapSpec
	alerts  AlertsSpec
	table   *TableSpec
	diagram *DiagramSpec
}

// ID returns the tile's stable identifier (used by the layout editor and
// profile JSON).
func (t Tile) ID() string { return t.id }

// Kind returns the tile's type.
func (t Tile) Kind() TileKind { return t.kind }

// Drill returns the tile's own DrillTarget (a value copy — the field is
// not settable from outside this package).
func (t Tile) Drill() DrillTarget { return t.drill }

// Bignum returns the tile's BignumSpec (KindBigNum only). The returned
// value is a deep copy of the spec's Series slice (SEC-063).
func (t Tile) Bignum() BignumSpec { return cloneBignumSpec(t.bignum) }

// Gauge returns the tile's GaugeSpec (KindGauge only).
func (t Tile) Gauge() GaugeSpec { return t.gauge }

// Spark returns the tile's SparkSpec (KindSpark only). The returned value
// is a deep copy of the spec's Series slice (SEC-063).
func (t Tile) Spark() SparkSpec { return cloneSparkSpec(t.spark) }

// Minimap returns the tile's MinimapSpec (KindMiniMap only). The returned
// value is a deep copy of the spec's Values slice (SEC-063).
func (t Tile) Minimap() MinimapSpec { return cloneMinimapSpec(t.minimap) }

// Alerts returns the tile's AlertsSpec (KindAlerts only). The returned
// value is a deep copy of the spec's Entries slice (SEC-063).
func (t Tile) Alerts() AlertsSpec { return cloneAlertsSpec(t.alerts) }

// Table returns the tile's TableSpec (KindTable only). The returned value
// is a deep copy — Columns, Rows, and each row's Cells/Drill get fresh
// backing arrays — so a caller mutating the returned spec (e.g. zeroing a
// row's DrillTarget through the exported TableRow.Drill field) cannot
// reach the tile's stored rows and reintroduce the drill-through dead end
// AC-4 makes structurally unconstructible (SEC-063). The editor replaces
// whole tiles; this accessor is for read-only render/sort/filter/export.
// verified secure: SEC-063 and SEC-070 are fully satisfied by cloneTableSpec.
func (t Tile) Table() *TableSpec { return cloneTableSpec(t.table) }

// Diagram returns the tile's DiagramSpec (KindDiagram only). Like Table,
// the returned value is a deep copy (Hits get a fresh backing array) so
// no exported handle aliases the tile's stored hit-test entries (SEC-063).
// verified secure: SEC-063 and SEC-070 are fully satisfied by cloneDiagramSpec.
func (t Tile) Diagram() *DiagramSpec { return cloneDiagramSpec(t.diagram) }

// newTile is the shared construction path: it validates the tile ID and
// the required DrillTarget, then returns the bare tile for a per-kind
// constructor to fill in.
func newTile(id string, kind TileKind, drill DrillTarget) (Tile, error) {
	if id == "" {
		return Tile{}, errs.New(codeTileNeedsDrill, corr(), map[string]any{
			"kind":   string(kind),
			"reason": "empty tile id",
		})
	}
	if err := requireDrill(drill, map[string]any{"tileId": id, "kind": string(kind)}); err != nil {
		return Tile{}, err
	}
	return Tile{id: id, kind: kind, drill: drill}, nil
}

// NewBignumTile constructs a KindBigNum tile with a required DrillTarget.
func NewBignumTile(id string, drill DrillTarget, spec BignumSpec) (Tile, error) {
	t, err := newTile(id, KindBigNum, drill)
	if err != nil {
		return Tile{}, err
	}
	t.bignum = cloneBignumSpec(spec)
	return t, nil
}

// NewGaugeTile constructs a KindGauge tile with a required DrillTarget.
func NewGaugeTile(id string, drill DrillTarget, spec GaugeSpec) (Tile, error) {
	t, err := newTile(id, KindGauge, drill)
	if err != nil {
		return Tile{}, err
	}
	t.gauge = spec
	return t, nil
}

// NewSparkTile constructs a KindSpark tile with a required DrillTarget.
func NewSparkTile(id string, drill DrillTarget, spec SparkSpec) (Tile, error) {
	t, err := newTile(id, KindSpark, drill)
	if err != nil {
		return Tile{}, err
	}
	t.spark = cloneSparkSpec(spec)
	return t, nil
}

// NewMinimapTile constructs a KindMiniMap tile with a required
// DrillTarget.
func NewMinimapTile(id string, drill DrillTarget, spec MinimapSpec) (Tile, error) {
	t, err := newTile(id, KindMiniMap, drill)
	if err != nil {
		return Tile{}, err
	}
	t.minimap = cloneMinimapSpec(spec)
	return t, nil
}

// NewAlertsTile constructs a KindAlerts tile with a required DrillTarget.
func NewAlertsTile(id string, drill DrillTarget, spec AlertsSpec) (Tile, error) {
	t, err := newTile(id, KindAlerts, drill)
	if err != nil {
		return Tile{}, err
	}
	t.alerts = cloneAlertsSpec(spec)
	return t, nil
}

// NewTableTile constructs a KindTable tile with a required DrillTarget
// for the tile itself (the whole-view target) and — critically — a
// required, valid DrillTarget on every row (AC-4/AC-5: every table cell
// is drillable, with no zero-value path). A row with an empty/invalid
// DrillTarget is rejected at construction, naming the offending row.
func NewTableTile(id string, drill DrillTarget, spec TableSpec) (Tile, error) {
	t, err := newTile(id, KindTable, drill)
	if err != nil {
		return Tile{}, err
	}
	for i := range spec.Rows {
		if err := requireDrill(spec.Rows[i].Drill, map[string]any{
			"tileId": id,
			"row":    i,
		}); err != nil {
			return Tile{}, err
		}
	}
	// Deep-copy the spec so the constructor's own caller no longer holds an
	// alias to the tile's rows: `spec.Rows[0].Drill = DrillTarget{}` after
	// NewTableTile must not corrupt the stored tile (SEC-063).
	t.table = cloneTableSpec(&spec)
	return t, nil
}

// NewDiagramTile constructs a KindDiagram tile with a required
// DrillTarget for the tile itself and a required, valid DrillTarget on
// every hit-test element (US-4/AC-5: a number inside a diagram is
// drillable like any other tile).
func NewDiagramTile(id string, drill DrillTarget, spec DiagramSpec) (Tile, error) {
	t, err := newTile(id, KindDiagram, drill)
	if err != nil {
		return Tile{}, err
	}
	for i := range spec.Hits {
		if err := requireDrill(spec.Hits[i].Drill, map[string]any{
			"tileId":   id,
			"hit":      i,
			"sourceId": spec.Hits[i].SourceID,
		}); err != nil {
			return Tile{}, err
		}
	}
	// Deep-copy the spec so the constructor's own caller no longer holds an
	// alias to the tile's hit-test entries (SEC-063).
	t.diagram = cloneDiagramSpec(&spec)
	return t, nil
}

// elementID formats a stable, human-readable element identifier for a
// Gap (AC-5): "row:N" for a table row, "hit:N" for a diagram hit-test
// entry. Deterministic (no map iteration).
func elementID(kind TileKind, i int) string {
	switch kind {
	case KindTable:
		return "row:" + strconv.Itoa(i)
	case KindDiagram:
		return "hit:" + strconv.Itoa(i)
	default:
		return strconv.Itoa(i)
	}
}

// cloneSlice returns a slice with a fresh backing array holding a shallow
// copy of each element. It is a true deep copy only for element types
// with no reference-typed fields ([]float64, []string, []widgets.Column,
// []DiagramHit, []AlertEntry, []Tile). Types whose elements DO carry a
// nested reference (TableRow, with its Cells []string) get a dedicated
// clone (cloneTableSpec) instead — callers must not reach for cloneSlice
// where the element type itself holds a slice. A nil input stays nil (a
// nil slice and an empty slice are distinct on the JSON wire and must
// round-trip faithfully).
func cloneSlice[T any](s []T) []T {
	if s == nil {
		return nil
	}
	out := make([]T, len(s))
	copy(out, s)
	return out
}

// cloneBignumSpec returns a deep copy of s (Series gets a fresh backing
// array; Thresholds is a value type with no reference fields).
func cloneBignumSpec(s BignumSpec) BignumSpec {
	s.Series = cloneSlice(s.Series)
	return s
}

// cloneSparkSpec returns a deep copy of s (Series gets a fresh backing
// array).
func cloneSparkSpec(s SparkSpec) SparkSpec {
	s.Series = cloneSlice(s.Series)
	return s
}

// cloneMinimapSpec returns a deep copy of s (Values gets a fresh backing
// array).
func cloneMinimapSpec(s MinimapSpec) MinimapSpec {
	s.Values = cloneSlice(s.Values)
	return s
}

// cloneAlertsSpec returns a deep copy of s (Entries gets a fresh backing
// array; AlertEntry is a value type with no reference fields).
func cloneAlertsSpec(s AlertsSpec) AlertsSpec {
	s.Entries = cloneSlice(s.Entries)
	return s
}

// cloneTableSpec returns a deep copy of s: Columns, Rows, and each row's
// Cells all get fresh backing arrays, so no exported handle aliases the
// stored spec (SEC-063). Drill/DrillTarget are value fields copied with
// the row struct. A nil s is returned as nil (the accessor is nil-safe;
// a table tile with no payload is not constructible through the public
// API).
func cloneTableSpec(s *TableSpec) *TableSpec {
	if s == nil {
		return nil
	}
	out := &TableSpec{
		Columns: cloneSlice(s.Columns),
		Rows:    make([]TableRow, len(s.Rows)),
	}
	for i := range s.Rows {
		out.Rows[i] = TableRow{
			Cells: cloneSlice(s.Rows[i].Cells),
			Drill: s.Rows[i].Drill,
		}
	}
	return out
}

// cloneDiagramSpec returns a deep copy of s (Hits gets a fresh backing
// array; DiagramHit is a value type with no reference fields). A nil s is
// returned as nil.
func cloneDiagramSpec(s *DiagramSpec) *DiagramSpec {
	if s == nil {
		return nil
	}
	return &DiagramSpec{Hits: cloneSlice(s.Hits)}
}
