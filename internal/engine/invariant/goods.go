package invariant

// GoodsInvariant checks the goods-conservation balance (§14, US-1): the
// tracked commodity/stock quantity for a tick (per §6/§8 JIT logistics,
// once those modules register their stocks) at the end of the tick must
// equal the quantity at the start of the tick plus every tracked
// production/consumption/in-transit-flow event for the tick.
//
// Balance identity: Closing - Opening == TrackedDelta, where
// TrackedDelta = production - consumption + net in-transit change for
// the tick. Until engine.market/engine.logistics (Sprint 4/6) register
// a real StockGoods reading each tick, this invariant's Ran field is
// false (AC-12) rather than false-flagging against an assumed-zero
// stock — see Snapshot.Reading.
type GoodsInvariant struct {
	stockCheck
}

// NewGoodsInvariant constructs the goods-conservation invariant.
func NewGoodsInvariant() GoodsInvariant {
	return GoodsInvariant{stockCheck{name: "goods", stock: StockGoods}}
}
