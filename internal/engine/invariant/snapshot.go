package invariant

// StockName identifies one conserved stock (people, money, goods,
// vehicles, or a future module's own stock — US-5's extensibility
// seam). It is a plain string type, not a closed enum, precisely so a
// future module can introduce its own StockName without touching this
// package (see doc.go's "Extensibility" section).
type StockName string

// The four v1 conserved stocks (§14, AC-2).
const (
	StockPeople   StockName = "people"
	StockMoney    StockName = "money"
	StockGoods    StockName = "goods"
	StockVehicles StockName = "vehicles"
)

// StockReading is one stock's conservation-relevant data for a single
// tick, as reported by whatever module owns that stock (via the
// SnapshotProvider a caller wires — hook.go).
//
// Balance identity (see people.go/money.go/goods.go/vehicle.go for the
// stock-specific meaning of each field): a stock balances when
// Closing - Opening == TrackedDelta. Any other result is an unexplained
// change — the invariant's whole reason to exist.
type StockReading struct {
	// Registered is false when the owning module has not yet reported
	// this stock for the tick (AC-12: e.g. engine.market not yet real).
	// A Registered: false reading is always skipped, never treated as
	// "opens and closes at zero" — that would be exactly the "assumes
	// zero and false-flags" bug AC-12 forbids.
	Registered bool

	// Opening is the stock's total at the start of the tick.
	Opening int64

	// Closing is the stock's total at the end of the tick.
	Closing int64

	// TrackedDelta is the sum of every tracked flow the owning module
	// recorded for the tick — e.g. for people: births - deaths +
	// immigration - emigration; for money: income - expenditure; for
	// goods: production - consumption + net in-transit change; for
	// vehicles: spawns - despawns. See each stock's own file for its
	// exact accounting identity (AC-18).
	TrackedDelta int64

	// Suspects optionally names entities implicated in an imbalance
	// (AC-11's "affected entity IDs where applicable") — e.g. vehicle
	// IDs present in Closing's count with no matching spawn event. Never
	// required: nil is a valid, common value for a purely aggregate
	// mismatch. Copied verbatim into any resulting Violation.EntityIDs —
	// never interpolated into a diagnostic string (AC-11b, see
	// Violation's doc comment).
	Suspects []string
}

// Snapshot is one tick's world-state input to RunSuite: the tick index
// plus a per-stock reading. Built fresh each tick by whatever
// SnapshotProvider a caller wires (hook.go) — this package never
// constructs one itself from live world state, since real stock data is
// out of scope here (see doc.go).
type Snapshot struct {
	// Tick is the daily-tick index this snapshot describes
	// (core.Clock.Tick()).
	Tick int64

	// Readings holds one StockReading per stock a module has reported
	// for this tick. A StockName absent from this map is treated
	// identically to a StockReading{Registered: false} entry (AC-12) —
	// callers may either omit an unregistered stock's key entirely or
	// include it with Registered: false; both are read the same way by
	// every Invariant in this package.
	Readings map[StockName]StockReading
}

// NewSnapshot constructs an empty Snapshot for tick, ready for a caller
// to populate Readings.
func NewSnapshot(tick int64) Snapshot {
	return Snapshot{Tick: tick, Readings: make(map[StockName]StockReading)}
}

// Reading returns the StockReading for stock, and whether it is present
// AND Registered — the single place both checks ("was it reported at
// all" and "was it reported as registered") collapse into one boolean,
// used by every conservation invariant's Check (conservation.go).
func (s Snapshot) Reading(stock StockName) (StockReading, bool) {
	r, ok := s.Readings[stock]
	if !ok || !r.Registered {
		return StockReading{}, false
	}
	return r, true
}
