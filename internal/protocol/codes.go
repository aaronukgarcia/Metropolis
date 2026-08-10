package protocol

// Registry error codes for the protocol package's own source (module key
// "protocol"). Range: P000-P009, declared in data/errors.json's
// "ranges.reserved" table — distinct from P090-P099, which belongs to
// internal/engine/stub (a CONSUMER of this seam, not this package's own
// source; see that package's codes.go doc comment). The code below IS
// registered there with real severity/module/message/remedy fields
// (GR#7); internal/foundation/errs's source-scan test guards against this
// ever drifting out of sync, and against another module's range
// accidentally overlapping this one (BUG-008's root cause).
const (
	// ErrTransportCopied: SendCommand, Close, SendResult, SendEvent,
	// SendDelta, Commands, Results, Events, or Deltas was called on an
	// InProcTransport value that is not the one NewInProcTransport
	// constructed — i.e. a struct copy (SEC-020 wave 1: 't2 := *t' is
	// legal, unsafe-free, reflect-free Go, and defeats closeMu's
	// per-instance safety because the copy gets its OWN closeMu but
	// ALIASES the original's cmdCh/resultCh/eventCh/deltaCh/closed/
	// closeOnce — a copy's Close() can then race the original's in-flight
	// sends and reopen BUG-007's send-on-closed-channel panic). See
	// InProcTransport.self's doc comment (transport.go).
	ErrTransportCopied = "MET-P000"

	// ErrSeqTrackerCopied: Observe or Reset was called on a SeqTracker
	// value that is not the one NewSeqTracker constructed — i.e. a
	// struct copy (SEC-020 wave 1: 't2 := *t' is legal, unsafe-free,
	// reflect-free Go, and defeats mu's per-instance safety because the
	// copy gets its OWN mu but ALIASES the original's last map — fatal
	// concurrent map access, same class as SEC-003/SEC-019). See
	// SeqTracker.self's doc comment (subscription.go).
	ErrSeqTrackerCopied = "MET-P001"

	// ErrSubscriptionAllocatorCopied: Allocate was called on a
	// SubscriptionAllocator value that is not the one
	// NewSubscriptionAllocator constructed — i.e. a struct copy (SEC-023,
	// SEC-020 wave 1's sibling hunt: 'a2 := *a' is legal, unsafe-free,
	// reflect-free Go and produces a second, independently-incrementing
	// counter starting from the same point, so the copy and the original
	// hand out COLLIDING SubscriptionIDs rather than crashing or
	// hanging). See SubscriptionAllocator.self's doc comment
	// (subscription.go).
	ErrSubscriptionAllocatorCopied = "MET-P002"
)
