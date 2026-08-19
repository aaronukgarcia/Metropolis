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
// This module has one major outstanding architectural gap:
//  1. Sourcing online-delivery-share dynamically lacks a registered edge to engine.comms.
//
// Note: Sourcing the "whose trip is this" location and demographic details is now resolved
// via the registered outbound edge to engine.citizens.
//
// Both inputs are currently structured around marked, fallback placeholders.
//
// # Format Preference Weights (MOD-050 r4 verdict fix, GR#15):
//
// The four format preference weights (cornerShopWeight/marketHallWeight/
// supermarketWeight/retailParkWeight) are sourced from data/shopping.json,
// which LoadConfig reads every time production wires this module. The
// in-code defaults in api.go (defaultCornerShopWeight etc.) are a
// documented fallback for a genuinely absent field only -- see
// effectiveWeight's doc comment -- never a silent substitute for an
// explicit zero. An explicit zero disables that format outright: its
// trips redistribute to the remaining formats. LoadConfig validates
// every weight fail-closed (MET-G4704): negative and NaN values are
// rejected before s.cfg is ever updated.
package shopping
