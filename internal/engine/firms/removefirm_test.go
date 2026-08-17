package firms

import (
	"testing"
)

// This file is the SEC-159 regression suite for RemoveFirm — the genuine
// compensating inverse of RegisterFirm. The same-package tests below can read
// the unexported foundedEvents culture-index ledger directly, which the
// external feat.pharmacampus end-to-end regression (TestPharmaWinRollbackAgainstRealFirmsAPI)
// cannot; together they pin every effect RegisterFirm has and RemoveFirm must
// reverse.

// TestRemoveFirmIsGenuineInverse proves RemoveFirm reverses RegisterFirm's four
// effects — registry entry, foundedCount, EventFounded lifecycle event, and the
// foundedEvents culture-index entry — with no EventFailed and no failedCount++.
func TestRemoveFirmIsGenuineInverse(t *testing.T) {
	api := newTestAPI(t, 1)

	firm, err := api.RegisterFirm("anchor", 5, "SUP3")
	if err != nil {
		t.Fatalf("RegisterFirm: %v", err)
	}
	id := FirmID(firm.ID)

	// Pre-state: exactly one registration's worth of effects.
	if got := api.FoundedCount(); got != 1 {
		t.Fatalf("foundedCount after register = %d, want 1", got)
	}
	if got := api.FailedCount(); got != 0 {
		t.Fatalf("failedCount after register = %d, want 0", got)
	}
	if got := len(api.Firms()); got != 1 {
		t.Fatalf("firms after register = %d, want 1", got)
	}
	if got := api.Events(); len(got) != 1 || got[0].Kind != EventFounded || got[0].FirmID != id {
		t.Fatalf("events after register = %v, want [EventFounded(id)]", got)
	}
	if got := len(api.foundedEvents); got != 1 || api.foundedEvents[0].FirmID != id {
		t.Fatalf("foundedEvents after register = %v, want one entry for id", api.foundedEvents)
	}

	if err := api.RemoveFirm(id); err != nil {
		t.Fatalf("RemoveFirm: %v", err)
	}

	// Post-state: the registration is fully undone — as if it never happened.
	if got := api.FoundedCount(); got != 0 {
		t.Fatalf("foundedCount after remove = %d, want 0", got)
	}
	if got := api.FailedCount(); got != 0 {
		t.Fatalf("failedCount after remove = %d, want 0 (RemoveFirm must not touch the failed ledger)", got)
	}
	if got := len(api.Firms()); got != 0 {
		t.Fatalf("firms after remove = %d, want 0 (firm leaked)", got)
	}
	if got := api.Events(); len(got) != 0 {
		t.Fatalf("events after remove = %v, want none (EventFounded not retracted)", got)
	}
	if got := len(api.foundedEvents); got != 0 {
		t.Fatalf("foundedEvents after remove = %v, want none (culture-index entry not retracted)", api.foundedEvents)
	}
}

// TestRemoveFirmUnknownID rejects an unknown FirmID with ErrFirmNotFound rather
// than silently succeeding — a compensating inverse that no-ops on an unknown id
// would let a caller believe it unwound a registration it never made (SEC-159).
func TestRemoveFirmUnknownID(t *testing.T) {
	api := newTestAPI(t, 1)
	if err := api.RemoveFirm(FirmID(1)); !hasCode(err, ErrFirmNotFound) {
		t.Fatalf("RemoveFirm(unknown) = %v, want ErrFirmNotFound", err)
	}
	if got := api.FoundedCount(); got != 0 {
		t.Fatalf("unknown RemoveFirm moved foundedCount to %d, want 0", got)
	}
	if got := len(api.Firms()); got != 0 {
		t.Fatalf("unknown RemoveFirm left %d firms, want 0", got)
	}
}

// TestRemoveFirmRetractsOnlyTheTargetsEvent pins that RemoveFirm retracts only
// the target firm's EventFounded, leaving a sibling firm's lifecycle events and
// the failed ledger untouched (the SEC-140 distinction between the compensating
// inverse and the §32 insolvency path Fail).
func TestRemoveFirmRetractsOnlyTheTargetsEvent(t *testing.T) {
	api := newTestAPI(t, 1)

	a, err := api.RegisterFirm("a", 1, "SUP3")
	if err != nil {
		t.Fatalf("RegisterFirm(a): %v", err)
	}
	b, err := api.RegisterFirm("b", 1, "SUP3")
	if err != nil {
		t.Fatalf("RegisterFirm(b): %v", err)
	}
	if _, err := api.Fail(FirmID(a.ID)); err != nil {
		t.Fatalf("Fail(a): %v", err)
	}
	// a founded, b founded, a failed: foundedCount==2, failedCount==1.
	if got := api.FoundedCount(); got != 2 {
		t.Fatalf("foundedCount = %d, want 2", got)
	}
	if got := api.FailedCount(); got != 1 {
		t.Fatalf("failedCount = %d, want 1", got)
	}

	if err := api.RemoveFirm(FirmID(b.ID)); err != nil {
		t.Fatalf("RemoveFirm(b): %v", err)
	}

	if got := api.FoundedCount(); got != 1 {
		t.Fatalf("foundedCount after RemoveFirm(b) = %d, want 1 (b's founding undone, a's preserved)", got)
	}
	if got := api.FailedCount(); got != 1 {
		t.Fatalf("failedCount after RemoveFirm(b) = %d, want 1 (failed ledger untouched)", got)
	}
	// b's EventFounded is retracted; a's [EventFounded, EventFailed] remain.
	events := api.Events()
	if len(events) != 2 {
		t.Fatalf("events after RemoveFirm(b) = %v, want [EventFounded(a), EventFailed(a)]", events)
	}
	for _, e := range events {
		if e.FirmID != FirmID(a.ID) {
			t.Fatalf("event for firm %d after RemoveFirm(b), want only firm a's events: %v", e.FirmID, events)
		}
	}
	if got := len(api.foundedEvents); got != 1 || api.foundedEvents[0].FirmID != FirmID(a.ID) {
		t.Fatalf("foundedEvents after RemoveFirm(b) = %v, want one entry for a", api.foundedEvents)
	}
}

// TestRemoveFirmRetractsLastFoundedEventForReusedID (SEC-204): a reused FirmID
// must have its LAST EventFounded retracted, not the first. Fail deletes the
// firm without retracting its founding events, so RegisterFirm at the same
// month re-derives the freed id; RemoveFirm must then undo the SECOND founding,
// leaving the original [EventFounded, EventFailed] — never [EventFailed,
// EventFounded] with a trailing founding for a firm that no longer exists.
func TestRemoveFirmRetractsLastFoundedEventForReusedID(t *testing.T) {
	api := newTestAPI(t, 1)

	first, err := api.RegisterFirm("stage", 5, "SUP3")
	if err != nil {
		t.Fatalf("RegisterFirm #1: %v", err)
	}
	id := FirmID(first.ID)

	// Fail frees the firm but leaves its EventFounded (and culture-index entry)
	// in the ledgers — the id is now re-derivable.
	if _, err := api.Fail(id); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	// Register the SAME name at the SAME month: firmIDForLocked re-derives the
	// now-free id (a pure function of seed/founder/month/purpose, not of a
	// consumed-id ledger).
	second, err := api.RegisterFirm("stage", 5, "SUP3")
	if err != nil {
		t.Fatalf("RegisterFirm #2: %v", err)
	}
	if FirmID(second.ID) != id {
		t.Fatalf("RegisterFirm #2 derived id %d, want the freed id %d", second.ID, id)
	}

	// Pre-remove: [EventFounded, EventFailed, EventFounded] for the reused id.
	if events := api.Events(); len(events) != 3 {
		t.Fatalf("events before remove = %v, want 3 (found, failed, found)", events)
	}

	if err := api.RemoveFirm(id); err != nil {
		t.Fatalf("RemoveFirm: %v", err)
	}

	// The LAST EventFounded is retracted, leaving the original founding
	// followed by the failure — emission order preserved.
	got := api.Events()
	if len(got) != 2 || got[0].Kind != EventFounded || got[1].Kind != EventFailed {
		t.Fatalf("events after remove = %v, want [EventFounded, EventFailed]", got)
	}
	if got[0].FirmID != id || got[1].FirmID != id {
		t.Fatalf("events after remove = %v, want both events for the reused id", got)
	}
	// The culture index retains exactly the original founding's entry.
	if got := len(api.foundedEvents); got != 1 || api.foundedEvents[0].FirmID != id {
		t.Fatalf("foundedEvents after remove = %v, want one entry for id", api.foundedEvents)
	}
	// foundedCount reflects one real founding (the failed original); the second
	// registration is fully undone.
	if got := api.FoundedCount(); got != 1 {
		t.Fatalf("foundedCount after remove = %d, want 1", got)
	}
}
