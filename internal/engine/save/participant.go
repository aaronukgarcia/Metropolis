package save

import "github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"

// Participant is the contract a domain engine module registers its saved
// state through (AC-1, GR#20). Kind labels the shard this participant
// produces/consumes (matched against serialize.ShardMeta.Kind and used
// to route a loaded shard's records back to the right participant on
// Load). Source is called once per save to obtain a fresh
// serialize.RecordSource that streams this participant's current live
// state; Handler is called once per load to obtain a fresh
// serialize.RecordHandler that reconstructs that state from the records
// streamed back.
//
// Implementations must not buffer their whole record set before Source
// starts returning records, and must not buffer a whole shard before
// Handler finishes receiving it — the same one-record-at-a-time
// streaming contract int.serializer's own StateSerializer enforces
// (AC-7): this package never materialises a full []serialize.Record
// slice for a Kind before handing records to WriteShard, so a
// Participant that internally buffered everything first would defeat
// that property one layer up.
type Participant interface {
	// Kind returns this participant's stable shard label. Must be
	// non-empty and unique across a single registry/participant list.
	Kind() string

	// Source returns a fresh RecordSource streaming this participant's
	// current live state, one record at a time, for a save currently in
	// progress.
	Source() serialize.RecordSource

	// Handler returns a fresh RecordHandler that reconstructs this
	// participant's state from the records streamed back for a load
	// currently in progress.
	Handler() serialize.RecordHandler
}

// DefaultParticipants is the static, explicitly-coded registry of every
// domain engine module's Participant (AC-1) — a literal slice, never a
// reflect-driven auto-discovery over other engine packages (a
// reflection-based "walk the package graph and guess what to save"
// implementation would defeat AC-2's ability to name an exhaustive,
// enumerable registry and is explicitly rejected by this item's
// acceptance criteria).
//
// Empty as of this build: no domain engine module has registered a
// Participant yet (citizens/buildings/market/etc. land in later
// sprints, per the sprint plan). Each domain module, once it registers
// here, takes on the AC-2/AC-18 field-parity drift-test obligation
// documented in doc.go — see that obligation's doc comment for the
// opt-out exclusion-allowlist policy every such registration must
// follow.
//
// Tests in this package construct their own, separate participant
// lists (never appended to this var) to exercise SaveManual/Autosave/
// Milestone/Load against fixture state without waiting on a real domain
// module to exist — see fixture_test.go.
var DefaultParticipants = []Participant{}
