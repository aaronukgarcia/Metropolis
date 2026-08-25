package solvergpu

// Error codes for the GPU sidecar (module key cloud.gpu, MOD-068).
//
// cloud.gpu has NO MET range of its own yet (cloud layer, milestone
// "future" — ICD §8, open decision OD-2: "claim a cloud error range before
// the sidecar lands"). Until a cloud-layer range is registered in
// data/errors.json, the sidecar surfaces already-registered foundation
// codes rather than minting a new one — GR#7 forbids raising an
// unregistered code (errs.New would silently degrade it to MET-F003 at
// runtime), and this build does not edit data/errors.json. The mapping is:
//
//   - oversized Request.Payload -> MET-F401 (foundation.solver; imported
//     via solver.ErrRequestPayloadTooLarge, the contract package's own
//     bound, single-sourced at the seam).
//   - sidecar/transport unavailable, name-lookup / re-authentication /
//     dial failure -> MET-F906 (foundation.integration's
//     ErrReconnectFailed, the registered code naming "the remote is
//     unreachable / reconnect failed").
//
// OD-2: when a cloud error range is claimed, these literals are the single
// place to point at the new codes.
const (
	// errSidecarUnavailable is the registry code the Backend (and its Stub)
	// raises when the GPU worker is absent, unreachable, or could not be
	// re-established. It maps to the registered foundation.integration code
	// MET-F906 until cloud.gpu claims its own range (ICD §8 OD-2).
	errSidecarUnavailable = "MET-F906"
)
