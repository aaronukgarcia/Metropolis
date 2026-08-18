// Package parking implements the space accounting per destination module (MOD-051).
//
// Key: engine.parking
// Cites: §38 Parking.
//
// # Five Instrument Types (AC-2):
//
// The module implements five parking types with distinct land footprints:
//  1. OnStreet: linear frontage (6.0 linear metres per space)
//  2. Surface: dedicated lot (15.0 sq metres per space)
//  3. MultiStorey: structured deck (3.0 sq metres per space, smaller than surface)
//  4. ParkAndRide: peripheral lot (12.0 sq metres per space)
//  5. Workplace: internal allocated (10.0 sq metres per space)
//
// # Land-Accounting Identity (AC-3):
//
// Land footprint is reconciled according to:
//
//	TotalLandFootprint = SpaceCount × FootprintPerSpace(InstrumentType)
//
// Reconciles independently against WorldAPI's zoning records.
//
// # Gaps & Outbound Blockers:
//
//   - Workplace allocated parking (AC-4) is tracked and calculated locally
//     pending structural integration with real workplace buildings.
//   - Cruising-traffic (AC-7) and residential overspill (AC-8) generate computed
//     metrics returned to callers, pending real engine.traffic integration.
package parking
