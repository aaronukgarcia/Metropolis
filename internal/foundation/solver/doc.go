// Package solver defines the solver contract: the CPU/GPU/cloud
// interchangeable offload seam (CPU v1 always works -> GPU sidecar local
// acceleration -> Azure cloud). One interface, three backends; the engine
// cannot tell them apart except by latency.
//
// Module key: int.solver (see code.json)
// Spec ref:   §15; A4; A9; M0-ENG §1
package solver
