package router

// Registry error codes for ui.router (BOW MOD-115; ASM-1482). Range:
// V400-V499, claimed via `node tools/plan/add-error.js claim-range
// ui.router` (data/errors.json's "ranges.reserved" table) — this
// package's own dedicated block, following ui.screen.build/map/proj/
// trade/ticker/chrome's precedent of each guarded package claiming and
// owning its own V-layer range (GR#7). Every code below is registered in
// data/errors.json with real severity/module/message/remedy fields.
//
// NOTE (2026-08-19): originally claimed as V300-V399, but that block
// collided with an unmerged lane/ben claim for ui.screen.finance
// (verified by an independent round, first-claim-wins per the BUG-276
// arbitration precedent). Renumbered to V400-V499 / MET-V400..V402 before
// commit; data/errors.json now carries the authoritative V300-V399
// reservation for ui.screen.finance so this cannot recur.
const (
	// ErrRouteMiss: a CommandResult/Delta/Event whose key (CorrelationID,
	// SubscriptionID, or Event.Kind) matched no registered
	// RegisterResultHandler/BindSubscription/RegisterEventRoute entry —
	// raised instead of silently dropping the message (GR#1/GR#17; ICD
	// §8's "a routing-table miss ... must raise a registry-sourced MET-U
	// code rather than silently dropping the message"). Also raised, with
	// ctx["kind"]=="result-stale-pruned", when a registered result-owner
	// is evicted for exceeding pendingTTLTicks without ever receiving its
	// CommandResult (Tick-based staleness — GR#21, never time.Now).
	ErrRouteMiss = "MET-V400"

	// ErrDeltaGap: protocol.SeqTracker observed a gap (one or more
	// skipped Seq values) in a subscription's Delta stream — the
	// transport's evict-oldest drop policy (transport.go) discarded a
	// delta before this router observed it. Logged, never silently
	// swallowed (ICD §9's resilience contract).
	ErrDeltaGap = "MET-V401"

	// ErrReceiverPanic: a registered receiver (ResultReceiver/
	// DeltaReceiver/EventReceiver) panicked while handling a routed
	// message. Router.invoke{Result,Delta,Event}Receiver (router.go)
	// recovers the panic, raises this code with the receiver kind +
	// routing key + tick in context, increments PanicCount(), and
	// CONTINUES the drain loop -- one poisoned message must never crash
	// the whole UI process or stop routing subsequent messages (GR#1;
	// T2's lossy-by-design contract, ICD §5). The panic is the receiving
	// screen's own bug to fix, never the router's.
	ErrReceiverPanic = "MET-V403"
)

// ErrRouterCopied (MET-V402) is declared in copyguard.go, next to
// checkNotCopied — the one thing that produces it — following
// ui.screen.build's precedent (its errors.go's own comment) of keeping a
// copy-guard's error constant local to copyguard.go rather than this file.
