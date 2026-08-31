package wsserver

// shim.go — FEAT-1972079936 Phase 0 increment 2 (AC-3): the compat-shim
// mechanism that lets an in-window OLDER major connect and be served
// correctly, without scattering per-version branches through the rest of
// this package (docs/planning/acceptance/
// feat-1972079936-phase0-protocol-versioning.md's "Compat shim"
// definition).
//
// # Why keyed by OFFSET, not by absolute major number
//
// This build's protocol.CurrentWireVersion.Major is still 1 today — no
// real major-2 has shipped yet, so there is nothing to key an absolute
// "major 1's shim" registration against that would still make sense once
// a real major bump lands. Keying by "how many majors back from
// whatever CurrentWireVersion.Major happens to be right now" means this
// registry needs no changes when a real major bump eventually happens;
// only a NEW offset entry is added (offset 3, 4, ... as the window
// widens or a future major supersedes today's shim target), and an
// existing offset's shim keeps meaning the same relative thing.
//
// # The ONE concrete shim this increment ships
//
// Phase 0 has no real breaking wire-shape change to demonstrate yet (the
// envelope hasn't actually been broken by a shipped major bump) — so,
// exactly like the acceptance doc's own worked example ("a field added/
// renamed between major N-1 and N is translated by the shim"), this
// increment picks ONE deliberately-introduced, illustrative rename to
// prove the end-to-end MECHANISM: an offset-1 (one-major-back) client is
// modelled as having sent its Command envelope's correlation id under
// the legacy snake_case wire key "correlation_id" instead of this
// build's current camelCase "correlationId" (envelope.go's real,
// registered field name). adaptCommandIn rewrites that key to
// "correlationId" BEFORE protocol.DecodeCommand ever sees the bytes, so
// the decoded protocol.Command is indistinguishable from one a
// current-major client sent — see the determinism test
// (shim_test.go's TestShim_DeterminismAcrossOffsets) for the proof this
// AC-6 requires. adaptResultOut is the mirror-image shim applied to an
// outbound CommandResult notification, so that same older client also
// gets its response back in the shape it expects.
//
// Increment 3 (or a real future major bump) adds more entries to
// shimRegistry the SAME way — this file's shape does not need to change,
// only grow.
import "encoding/json"

// versionShim adapts wire-level JSON bytes between an in-window OLDER
// major and the shape this build's protocol.Command/CommandResult types
// actually speak (protocol.CurrentWireVersion's major). Either field may
// be nil if that direction needs no adaptation for this offset.
type versionShim struct {
	// adaptCommandIn rewrites a raw "command" JSON-RPC params payload FROM
	// the older major's wire shape INTO the shape protocol.DecodeCommand
	// expects (current major) — called BEFORE decode.
	adaptCommandIn func(raw json.RawMessage) (json.RawMessage, error)
	// adaptResultOut rewrites a raw CommandResult (already marshalled in
	// the current shape) INTO the older major's wire shape — called
	// AFTER encode, before the bytes are written to the socket.
	adaptResultOut func(raw json.RawMessage) (json.RawMessage, error)
}

// shimRegistry maps "how many majors back from CurrentWireVersion.Major"
// to the shim that adapts wire bytes for a connection negotiated at that
// offset. Offset 0 (current major) has no entry -- unshimmed by
// definition, never looked up (shimForOffset returns ok=false for
// offset<=0). Increment 2 adds exactly the one entry (offset 1) the
// acceptance doc's Increment 2 scope calls for; a real future major bump
// or increment 3 extends this map, never restructures it.
var shimRegistry = map[int]versionShim{
	1: {
		adaptCommandIn: func(raw json.RawMessage) (json.RawMessage, error) {
			return renameJSONField(raw, "correlation_id", "correlationId")
		},
		adaptResultOut: func(raw json.RawMessage) (json.RawMessage, error) {
			return renameJSONField(raw, "correlationId", "correlation_id")
		},
	},
}

// shimForOffset returns the shim registered for offset (majors back from
// current), and whether one exists. offset<=0 (current major, or a
// nonsensical negative offset) never has a shim -- current-major traffic
// is never shimmed, by definition.
func shimForOffset(offset int) (versionShim, bool) {
	if offset <= 0 {
		return versionShim{}, false
	}
	s, ok := shimRegistry[offset]
	return s, ok
}

// normalizeProtocolVersionField overwrites raw's top-level "protocolVersion"
// key with canonical, leaving every other key untouched (BUG-471, FEAT-
// 1972079936 Phase 0 inc3): a shimmed (in-window older-major) connection's
// wire bytes still carry the CLIENT's own declared protocolVersion (e.g.
// "1.0"), which adaptCommandIn's field-rename alone does not touch. If the
// field is absent, canonical is added -- a well-formed envelope always
// carries protocolVersion (envelope.go's Command.Validate requires it), so
// an absent field here means an already-malformed payload that decode will
// reject on its own merits; this function's job is only to canonicalize
// the tag when present, never to validate the rest of the envelope.
func normalizeProtocolVersionField(raw json.RawMessage, canonical string) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	canonicalBytes, err := json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	m["protocolVersion"] = canonicalBytes
	return json.Marshal(m)
}

// renameJSONField renames a top-level key of a JSON object from `from` to
// `to`, leaving every other key untouched and preserving each value's
// exact raw bytes (no re-marshal/re-typing of the value itself, so a
// number/string/nested object survives byte-for-byte). If `from` is
// absent, raw is returned unchanged (a shim must be a no-op on a payload
// that never had the legacy field to begin with -- not an error). If
// `to` is already present, `from`'s value does NOT overwrite it -- a
// well-formed payload from a single wire-shape family never carries both
// keys, and silently preferring one over the other would hide a genuinely
// malformed frame instead of surfacing it via the decode step that
// follows.
func renameJSONField(raw json.RawMessage, from, to string) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if v, ok := m[from]; ok {
		if _, exists := m[to]; !exists {
			m[to] = v
		}
		delete(m, from)
	}
	return json.Marshal(m)
}
