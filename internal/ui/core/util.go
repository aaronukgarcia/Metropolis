package core

// trySendEvictOldest sends v on ch without blocking. If ch is already
// full, the oldest queued value is evicted (a non-blocking receive) to
// make room, then the send is retried, bounded by cap(ch)+1 attempts so
// a reader draining concurrently can never turn this into an infinite
// loop. Mirrors internal/protocol/transport.go's identically-named
// helper and its policy rationale (UI-SPEC §1: "the last frame stands"
// generalises to "the newest message stands" for any UI-owned queue)
// but is reimplemented here rather than imported, since it is
// unexported in protocol and this package's queues (InputMsg) are a
// distinct concern from protocol's wire messages.
//
// Returns true if v was enqueued (whether or not an older value had to
// be evicted first), false only if ch has zero capacity and is
// momentarily contended so no slot could be claimed within the bounded
// attempts.
func trySendEvictOldest[T any](ch chan T, v T) bool {
	attempts := cap(ch) + 1
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		select {
		case ch <- v:
			return true
		default:
		}
		select {
		case <-ch:
			// evicted the oldest queued value; retry the send
		default:
			// drained concurrently between the two selects; retry directly
		}
	}
	return false
}
