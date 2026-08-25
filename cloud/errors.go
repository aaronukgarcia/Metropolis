package cloud

import (
	"errors"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/integration"
)

// Sentinel causes for the cloud tier's failure classes (typed so callers
// can errors.Is against them). These are CAUSES, not the surfaced error:
// per GR#7 the surfaced error is always a registry-sourced *errs.E built
// with cloudFailureCode below and wrapping the cause.
//
// cloud.azure has no MET range of its own yet (ICD §8 OD-2 — a cloud-layer
// range must be claimed before these transport failures can carry
// dedicated codes), so every transport / Blob failure maps to
// foundation.integration's resilience surface, the only registered
// remote-integration codes that exist today.
var (
	// ErrCloudUnavailable is the cause when the cloud transport cannot be
	// reached at all (endpoint unreachable, connection refused).
	ErrCloudUnavailable = errors.New("cloud.azure: cloud transport unavailable")

	// ErrThrottled is the cause when the cloud transport rejects a call
	// for rate/quota reasons.
	ErrThrottled = errors.New("cloud.azure: cloud transport throttled")

	// ErrBlobNotFound is the cause when a Restore names a durable object
	// that does not exist.
	ErrBlobNotFound = errors.New("cloud.azure: blob not found")

	// ErrCloudDisabled is the cause when a cloud operation is attempted on
	// a tier constructed with Config.Enabled == false.
	ErrCloudDisabled = errors.New("cloud.azure: cloud tier disabled")
)

// cloudFailureCode is the registry code every cloud transport/Blob failure
// surfaces as, until a dedicated cloud range is claimed (ICD §8 OD-2). It
// is integration.ErrRetriesExhausted — the terminal state of a remote
// call after the logical retry budget is spent — which is the accurate
// description of "the cloud call gave up" and, being an exported constant
// of foundation.integration, is consumed here rather than re-declared
// (GR#3).
const cloudFailureCode = integration.ErrRetriesExhausted

// cloudError wraps cause in the registry-sourced cloud failure code with
// the correlation ID attached (GR#1/GR#7). cause is preserved via
// errors.Is/errors.As, so a caller can still distinguish ErrBlobNotFound
// from ErrThrottled behind the shared registry code.
func cloudError(correlationID string, cause error, ctx map[string]any) error {
	return errs.Wrap(cloudFailureCode, correlationID, cause, ctx)
}
