package projections

import "testing"

// --- AC-4: decision markers show a step before the decision lands --------

func TestDecisionMarkerStepAppearsBeforeCompletion(t *testing.T) {
	api := NewProjectionsAPI()
	provider := fakeProvider{def: 100}
	if err := api.RegisterCurveProvider("test.schoolCapacity", provider); err != nil {
		t.Fatalf("RegisterCurveProvider: %v", err)
	}
	if err := api.SetCurrentMonth(0); err != nil {
		t.Fatalf("SetCurrentMonth: %v", err)
	}

	before, err := api.Curve("test.schoolCapacity", 0, 10)
	if err != nil {
		t.Fatalf("Curve (before enqueue): %v", err)
	}

	if err := api.EnqueueDecision(Decision{
		ID:              "school-build-1",
		Type:            "education.newSchool",
		CurveKey:        "test.schoolCapacity",
		CompletionMonth: 6,
		Delta:           50,
		FuseYears:       0, // short-fuse, no Slow-Fuse payload required
	}); err != nil {
		t.Fatalf("EnqueueDecision: %v", err)
	}

	after, err := api.Curve("test.schoolCapacity", 0, 10)
	if err != nil {
		t.Fatalf("Curve (after enqueue): %v", err)
	}

	// Current (month-0) value is unchanged.
	if after[0].Value != before[0].Value {
		t.Errorf("month 0 value changed after enqueueing a FUTURE decision: before=%v after=%v", before[0].Value, after[0].Value)
	}
	// The completion month's value differs (the step landed).
	if after[6].Value == before[6].Value {
		t.Errorf("month 6 (the decision's completion month) is unchanged after enqueue: before=%v after=%v", before[6].Value, after[6].Value)
	}
	if after[6].Value != before[6].Value+50 {
		t.Errorf("month 6 value = %v, want %v (base %v + delta 50)", after[6].Value, before[6].Value+50, before[6].Value)
	}
	// A month before completion is still unaffected.
	if after[5].Value != before[5].Value {
		t.Errorf("month 5 (before completion) changed: before=%v after=%v", before[5].Value, after[5].Value)
	}

	// Cancelling removes the step.
	if err := api.CancelDecision("school-build-1"); err != nil {
		t.Fatalf("CancelDecision: %v", err)
	}
	afterCancel, err := api.Curve("test.schoolCapacity", 0, 10)
	if err != nil {
		t.Fatalf("Curve (after cancel): %v", err)
	}
	if afterCancel[6].Value != before[6].Value {
		t.Errorf("month 6 value after cancel = %v, want the original %v (step not removed)", afterCancel[6].Value, before[6].Value)
	}
}

func TestCancelUnknownDecisionRejected(t *testing.T) {
	api := NewProjectionsAPI()
	err := api.CancelDecision("nonexistent")
	assertCode(t, err, ErrUnknownDecision)
}

// --- AC-5: the Slow-Fuse gate is decision-type-agnostic -------------------
//
// fakeUnknownDecisionType is deliberately NOT one of A5's five named
// examples (education, planning quality, rehabilitation, BDI, debt) —
// this is the load-bearing case the AC's own "Lazy implementation this
// rejects" note names: a hardcoded per-type if/else list would pass
// every test exercising the five named types and then silently fail to
// protect this one.
const fakeUnknownDecisionType = "feat.experimentalWidgetProgram"

func TestSlowFuseGateRejectsUnknownDecisionTypeWithoutPayload(t *testing.T) {
	api := NewProjectionsAPI()
	err := api.EnqueueDecision(Decision{
		ID:        "widget-1",
		Type:      fakeUnknownDecisionType,
		FuseYears: 6, // > slowFuseThresholdYears
		// Consequence deliberately nil.
	})
	assertCode(t, err, ErrSlowFuseMissingPayload)
}

func TestSlowFuseGateAcceptsUnknownDecisionTypeWithPayload(t *testing.T) {
	api := NewProjectionsAPI()
	err := api.EnqueueDecision(Decision{
		ID:        "widget-2",
		Type:      fakeUnknownDecisionType,
		FuseYears: 6,
		Consequence: &ProjectedConsequence{
			Description: "widget program capacity rises in year 6",
		},
	})
	if err != nil {
		t.Fatalf("EnqueueDecision with a valid payload was rejected: %v", err)
	}
}

func TestSlowFuseGateDoesNotApplyBelowThreshold(t *testing.T) {
	api := NewProjectionsAPI()
	err := api.EnqueueDecision(Decision{
		ID:        "widget-3",
		Type:      fakeUnknownDecisionType,
		FuseYears: 2, // <= slowFuseThresholdYears: no payload required
	})
	if err != nil {
		t.Fatalf("EnqueueDecision with FuseYears below the threshold was rejected: %v", err)
	}
}

// --- AC-10: SlowFuse rejection is registry-sourced, never a silent pass --

func TestSlowFuseConfirmationRejectsMissingPayload(t *testing.T) {
	api := NewProjectionsAPI()
	err := api.EnqueueDecision(Decision{
		ID:        "debt-restructure-1",
		Type:      "engine.finance.debtRestructure",
		FuseYears: 7,
	})
	assertCode(t, err, ErrSlowFuseMissingPayload)

	// The decision must not have been silently accepted despite the
	// missing payload — it should not show up as a queued step.
	if err := api.CancelDecision("debt-restructure-1"); err == nil {
		t.Error("CancelDecision succeeded for a decision that should have been rejected at EnqueueDecision, not silently queued")
	}
}
