package debug

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// fixtureEntity is a known fixture entity (AC-7: "inspects a known
// fixture entity and asserts the JSON output round-trips the entity's
// known fields").
type fixtureEntity struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Population int    `json:"population"`
}

// TestInspectEntityRoundTrips is AC-7.
func TestInspectEntityRoundTrips(t *testing.T) {
	want := fixtureEntity{Kind: "firm", ID: "firm:9001", Population: 42}
	s := NewState(WithHeader(newTestHeader()), WithEntityLookup(func(ref string) (any, error) {
		if ref != "firm:9001" {
			t.Fatalf("EntityLookup called with ref=%q, want %q", ref, "firm:9001")
		}
		return want, nil
	}))
	if err := s.Enable(SourceFlag, "corr-setup"); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	b, err := s.InspectEntity("corr-inspect", "firm:9001")
	if err != nil {
		t.Fatalf("InspectEntity: %v", err)
	}

	var got fixtureEntity
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("json.Unmarshal(InspectEntity output): %v", err)
	}
	if got != want {
		t.Fatalf("InspectEntity round-trip = %+v, want %+v", got, want)
	}
}

// TestInspectEntityNotConfigured: InspectEntity refuses rather than
// silently returning empty JSON when no EntityLookup is wired.
func TestInspectEntityNotConfigured(t *testing.T) {
	s := enabledState(t)
	_, err := s.InspectEntity("corr-noconf", "citizen:1")
	if err == nil {
		t.Fatalf("InspectEntity with no lookup configured: got nil error, want ErrEntityLookupNotConfigured")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrEntityLookupNotConfigured {
		t.Fatalf("InspectEntity with no lookup configured: got %v, want code %s", err, ErrEntityLookupNotConfigured)
	}
}

// TestInspectEntityLookupFailure: a failing lookup is wrapped, not
// swallowed.
func TestInspectEntityLookupFailure(t *testing.T) {
	lookupErr := errors.New("unknown ref")
	s := NewState(WithHeader(newTestHeader()), WithEntityLookup(func(ref string) (any, error) {
		return nil, lookupErr
	}))
	if err := s.Enable(SourceFlag, "corr-setup"); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	_, err := s.InspectEntity("corr-fail", "citizen:nope")
	if err == nil {
		t.Fatalf("InspectEntity with failing lookup: got nil error, want ErrEntityLookupFailed")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrEntityLookupFailed {
		t.Fatalf("InspectEntity with failing lookup: got %v, want code %s", err, ErrEntityLookupFailed)
	}
	if !errors.Is(err, lookupErr) {
		t.Fatalf("InspectEntity with failing lookup: wrapped cause not retrievable via errors.Is")
	}
}

// TestInspectEntityMarshalFailure: an entity value that cannot be
// JSON-marshalled is surfaced as ErrEntityMarshalFailed, not a panic.
func TestInspectEntityMarshalFailure(t *testing.T) {
	s := NewState(WithHeader(newTestHeader()), WithEntityLookup(func(ref string) (any, error) {
		return make(chan int), nil // channels are not JSON-marshalable
	}))
	if err := s.Enable(SourceFlag, "corr-setup"); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	_, err := s.InspectEntity("corr-marshal", "citizen:weird")
	if err == nil {
		t.Fatalf("InspectEntity with unmarshalable entity: got nil error, want ErrEntityMarshalFailed")
	}
	var e *errs.E
	if !errors.As(err, &e) || e.Code != ErrEntityMarshalFailed {
		t.Fatalf("InspectEntity with unmarshalable entity: got %v, want code %s", err, ErrEntityMarshalFailed)
	}
}
