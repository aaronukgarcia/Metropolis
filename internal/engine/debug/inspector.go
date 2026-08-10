package debug

import (
	"encoding/json"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// EntityLookup resolves ref — protocol.InspectEntityPayload's opaque
// "typed:id" convention, e.g. "citizen:482913" — to the domain entity
// value to dump, or an error if ref does not resolve. Injected by
// whichever module owns the referenced entity kind; this package only
// gates and marshals, it never resolves an entity itself (out of
// scope, see doc.go).
type EntityLookup func(ref string) (any, error)

// InspectEntity gates, resolves, and JSON-marshals ref via the injected
// EntityLookup (AC-7). Rejected with a registry-sourced error when
// debug is off (AC-9/AC-11), when no EntityLookup is configured, when
// the lookup itself fails, or when the resolved value cannot be
// marshalled — never a panic on a bad ref or a misbehaving lookup.
func (s *State) InspectEntity(correlationID, ref string) ([]byte, error) {
	if err := s.requireOn(correlationID, "entity-inspector"); err != nil {
		return nil, err
	}

	// SEC-020 wave 2: requireOn (above) already checked identity before
	// this point, but this is a SEPARATE s.mu.Lock() site reading a
	// SEPARATE shared field (entityLookup) — re-checked here, immediately
	// before this specific lock, rather than trusting an earlier check on
	// the same call to cover every subsequent lock site (Weakness
	// pattern #3).
	if err := s.checkNotCopied(correlationID, map[string]any{"ref": ref}); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if err := s.checkNotCopied(correlationID, map[string]any{"ref": ref}); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	lookup := s.entityLookup
	s.mu.Unlock()

	if lookup == nil {
		return nil, errs.New(ErrEntityLookupNotConfigured, correlationID, map[string]any{"ref": ref})
	}

	entity, err := lookup(ref)
	if err != nil {
		return nil, errs.Wrap(ErrEntityLookupFailed, correlationID, err, map[string]any{"ref": ref})
	}

	b, err := json.Marshal(entity)
	if err != nil {
		return nil, errs.Wrap(ErrEntityMarshalFailed, correlationID, err, map[string]any{"ref": ref})
	}
	return b, nil
}
