// Package shopping implements the household shopping trip generation & access module (MOD-050).
//
// Key: engine.shopping
// Cites: §37 Shopping & Grocery Access, and §48 Destination Leisure & Retail.
//
// # Three-Independent-Factor Access-Score Contract (AC-5):
//
// Every home cell gets a composite grocery access score computed as:
//
//	AccessScore = TimeFactor × PriceFactor × FreshnessFactor
//
// where:
//   - TimeFactor is the inverse of format travel time (shorter travel time is better)
//   - PriceFactor is the inverse of format price level (cheaper prices are better)
//   - FreshnessFactor is format freshness (higher freshness is better)
//
// All three factors move independently, proving all three are load-bearing.
//
// # Emergent Food Deserts (AC-6):
//
// Food deserts are not scripted special-case flags; they emerge naturally when a cell's
// composite access score falls below the threshold (e.g. 20.0) read off the ordinary
// AccessScore accessor, such as when the nearest supermarket closes and travel time rises.
//
// # Outstanding Gaps & Outbound Blockers (BUG-058):
//
// This module has two major outstanding architectural gaps:
//  1. Sourcing "whose trip is this" (household location, income, typology) lacks a registered
//     edge in code.json to any demand-generating source (like engine.households).
//  2. Sourcing online-delivery-share dynamically lacks a registered edge to engine.comms.
//
// Both inputs are currently structured around marked, fallback placeholders.
package shopping
