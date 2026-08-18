// Package dispatch implements the unified emergency & care dispatch module (MOD-040).
//
// Key: engine.dispatch
// Cites: §26 Emergency & Care Dispatch Model (unified).
//
// # Fleet Conservation Identity
//
// For every unit type (fire, ambulance, air-ambulance, police), the following
// identity holds exactly at every tick:
//
//	TotalUnits == UnitsAvailable + UnitsEnRoute + UnitsOnScene + UnitsOutOfService
//
// Each term is independently sourced from its own bucket.
//
// # Air Ambulance Routing
//
// Air-ambulance routing is road-independent and ignores road-network travel time.
// It is limited strictly by weather conditions (grounded during storms).
//
// # Registered Edges and Collaborations
//
// The engine.projections edge is fully registered.
// The engine.invariant edge remains absent (pending collaborations gate configuration).
package dispatch
