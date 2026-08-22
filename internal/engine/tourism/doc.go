// Package tourism implements §44 Holiday Tourism — the visitor economy —
// for the Metropolis engine. It is code.json's engine.tourism module
// (GUID e245cc2c-7b37-4c6c-aeb1-e92fcaca5bf1, "visitor streams parallel to
// citizens; portfolio score decomposed").
//
// # Module key and spec refs
//
// Module key: engine.tourism (MOD-057). Spec refs: §44 (in full — the
// day-tripper/staying-visitor parallel population stream, the
// attraction-portfolio draw formula, the accommodation-stock caps, visitor
// spend/bed tax/queues/waste/policing spikes, and "August is a logistics
// boss-fight your July projections warn you about"); §21 (external
// commuting & housing — the holiday-let vs workforce-housing stock
// competition, AC-7 once unblocked); §39 (taxation — the tourist bed-tax
// line item, AC-8 once unblocked); §9 (seasonality — the seaside summer
// curve, consumed live from engine.season per AC-4).
//
// # The visitor economy (§44)
//
// Visitors are a parallel population stream, distinct from citizens:
// day-trippers (rail/coach/car; hours, small spend, large transport load)
// and staying visitors (nights × spend, accommodation-bound). The draw is
// the decomposed attraction-portfolio score (beach/promenade/pier +
// venues + events + landmarks/heritage + countryside/BDI) multiplied by
// reputation (engine.attract's Reputation(), lagged per AC-10), access
// (the domestic → continental → global reach ladder) and the seaside
// seasonal curve (engine.season's beach weight, never a hardcoded ×3).
// Accommodation stock (hotels, B&Bs, campsite/caravan, holiday lets) caps
// the realised staying-visitor count; overflow waits in an accommodation
// queue.
//
// # The August stress scenario (the S11 exit-gate clause, AC-13)
//
// §44's "August is a logistics boss-fight" is narrative; this package's
// scenario test turns it into a fail-able criterion. A fixture city runs
// July through September at a fixed visitor-generation configuration with
// engine.season's real summer multiplier live. It FAILS if any of these
// hold:
//
//  1. Conservation violation — an admitted visitor (day-tripper or
//     staying) silently vanishes or duplicates outside a documented
//     departure event (§5.2's conservation doctrine mirrored for
//     visitors). The invariant admitted == departed + present must hold.
//  2. Capacity-cap breach — the realised staying-visitor count exceeds the
//     summed accommodation-stock capacity (hotels + B&Bs + campsite +
//     holiday lets) at any point.
//  3. Backlog that never drains — the accommodation waitlist, having grown
//     through the August peak, fails to shrink to within 10% of its
//     pre-August (July) baseline by the end of September (a RELATIVE
//     recovery bound, never an absolute count or wall-clock duration).
//
// # Blocked mechanics (BUG-058)
//
// Three §44 mechanics are BLOCKED pending the BUG-058 findings that would
// register the missing code.json edges, and are deliberately NOT built or
// routed around (AC-17):
//
//   - AC-7 (holiday-let conversion → engine.households) — blocked on
//     BUG-058 finding #3 (no engine.tourism→engine.households edge).
//   - AC-8 (bed tax → engine.tax) — blocked on BUG-058 finding #4 (no
//     engine.tourism→engine.tax edge).
//   - AC-9 (café-culture portfolio term → engine.cafe) — blocked on
//     BUG-058 finding #2 (no engine.tourism→engine.cafe edge).
//
// This package imports none of engine.households, engine.tax, or
// engine.cafe while those findings remain open; when an edge lands, its
// AC (7/8/9) supersedes the AC-17 guard for that package.
//
// # Determinism (GR#21)
//
// This package never reads the wall clock (grep -rn "time\.Now\|time\.
// Since" internal/engine/tourism/*.go, excluding _test.go, returns no
// matches). Every draw-score, capacity and visitor-stream computation is a
// pure function of loaded data, current sim state, and (were any stochastic
// draw needed) a det.Stream keyed by hash(worldSeed, id, month, purpose) —
// the v1 model needs no stochastic draw, so determinism is structural
// rather than RNG-dependent. No result-affecting map iteration exists: the
// attraction and accommodation inventories are insertion-ordered slices,
// and the venue-mix array is summed in index order.
package tourism
