// Package dispatch implements the unified emergency & care dispatch module (MOD-040).
//
// Key: engine.dispatch
// Cites: §26 Emergency & Care Dispatch Model (unified).
//
// # Unified Priority-Tiered Dispatch Queue (AC-2):
//
// The module maintains a unified FIFO priority-tiered dispatch queue. High severity
// (Severity 3, e.g. life-threatening fire/medical) incidents are prioritized and
// inserted at the front of the queue, while lower severity (Severity 1) incidents
// are dispatched behind them in FIFO order.
//
// # Autonomy-Era Optimization (AC-7):
//
// Late-era autonomous ambulance, fire, and police vehicle deployments reduce
// emergency response travel delay by 30%, which is modeled as a direct scaling
// on the computed response times.
package dispatch
