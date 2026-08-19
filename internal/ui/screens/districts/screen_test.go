package districts

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

var testStyle = widgets.DefaultPalette.Style(widgets.TokenMoney)

// TestTaxSettings_AppliesFromDelta (AC-6): the tax-settings panel is
// scoped to the selected district and shows the fields sourced from the
// applied Delta, tracing to the fixture -- not a hardcoded copy.
func TestTaxSettings_AppliesFromDelta(t *testing.T) {
	s := New("corr-tax")
	s.BindSubscription("sub-1")
	s.SetSelectedDistrict("harbour")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: mustJSON(t, fullPatch())})

	settings, have := s.TaxSettings()
	if !have {
		t.Fatal("TaxSettings have=false after a valid delta")
	}
	if len(settings) != 3 {
		t.Fatalf("TaxSettings len = %d, want 3", len(settings))
	}

	buf := core.NewBuffer(80, 10)
	rect := core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderTaxSettings(buf, rect, settings, s.SelectedDistrict(), s.TaxRejectedReason(), true, testStyle)

	rows := renderedText(buf, rect)
	if !rowContains(rows, "Council Tax") || !rowContains(rows, "1.50") {
		t.Errorf("rendered rows do not trace to the harbour councilTax fixture row: %v", rows)
	}
	if rowContains(rows, "old-town") {
		t.Errorf("rendered rows include an old-town row despite selecting harbour: %v", rows)
	}
}

// TestTaxSettings_ChangingFixtureChangesRender (AC-6/AC-3-shaped
// false-pass guard): changing the fixture's multiplier changes the
// rendered rows without any code change -- proving live sourcing rather
// than a hardcoded render.
func TestTaxSettings_ChangingFixtureChangesRender(t *testing.T) {
	base := fullPatch()
	mutated := fullPatch()
	mutatedSettings := []wireDistrictTaxSetting{
		{DistrictID: "harbour", InstrumentID: "councilTax", InstrumentLabel: "Council Tax", Multiplier: 1.9, Rate: 10, RateMax: 20, EffectiveRate: 19},
	}
	mutated.TaxSettings = &mutatedSettings

	sA := New("corr-a")
	sA.BindSubscription("sub-a")
	sA.SetSelectedDistrict("harbour")
	sA.ApplyDelta(protocol.Delta{SubscriptionID: "sub-a", Patch: mustJSON(t, base)})

	sB := New("corr-b")
	sB.BindSubscription("sub-b")
	sB.SetSelectedDistrict("harbour")
	sB.ApplyDelta(protocol.Delta{SubscriptionID: "sub-b", Patch: mustJSON(t, mutated)})

	settingsA, _ := sA.TaxSettings()
	settingsB, _ := sB.TaxSettings()

	bufA := core.NewBuffer(80, 10)
	rectA := core.Rect{X: 0, Y: 0, W: 80, H: 10}
	RenderTaxSettings(bufA, rectA, settingsA, "harbour", "", true, testStyle)

	bufB := core.NewBuffer(80, 10)
	RenderTaxSettings(bufB, rectA, settingsB, "harbour", "", true, testStyle)

	if bufsEqual(bufA, bufB, rectA) {
		t.Error("rendered tax-settings panel unchanged after mutating the multiplier fixture from 1.50 to 1.90")
	}
}

// TestSetDistrictMultiplier_SendsCommandAndUpdatesFromDelta (AC-6): the
// command payload matches the intended value, and the rendered value only
// updates once the corresponding delta arrives -- never from a
// locally-mutated value held before the engine confirms it.
func TestSetDistrictMultiplier_SendsCommandAndUpdatesFromDelta(t *testing.T) {
	s := New("corr-set")
	s.BindSubscription("sub-1")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: mustJSON(t, fullPatch())})

	var sent protocol.Command
	err := s.SetDistrictMultiplier(func(c protocol.Command) error {
		sent = c
		return nil
	}, "harbour", "councilTax", 1.75)
	if err != nil {
		t.Fatalf("SetDistrictMultiplier: %v", err)
	}

	payload, ok := sent.Payload.(protocol.DebugPayload)
	if !ok {
		t.Fatalf("Payload type = %T, want protocol.DebugPayload", sent.Payload)
	}
	if payload.Op != opSetTaxMultiplier {
		t.Errorf("Op = %q, want %q", payload.Op, opSetTaxMultiplier)
	}
	if payload.Args["districtId"] != "harbour" || payload.Args["instrumentId"] != "councilTax" || payload.Args["multiplier"] != "1.75" {
		t.Errorf("command args = %+v, want districtId=harbour instrumentId=councilTax multiplier=1.75", payload.Args)
	}

	// The rendered value must NOT have changed yet -- only a Delta updates it.
	settings, _ := s.TaxSettings()
	for _, row := range settings {
		if row.DistrictID == "harbour" && row.InstrumentID == "councilTax" && row.Multiplier != 1.5 {
			t.Fatalf("Multiplier changed to %v before any Delta arrived -- SetDistrictMultiplier must not locally mutate state", row.Multiplier)
		}
	}

	// Now apply the confirming delta -- only then does the value change.
	updated := []wireDistrictTaxSetting{
		{DistrictID: "harbour", InstrumentID: "councilTax", InstrumentLabel: "Council Tax", Multiplier: 1.75, Rate: 10, RateMax: 20, EffectiveRate: 17.5},
	}
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: mustJSON(t, wirePatch{SchemaVersion: 1, TaxSettings: &updated})})
	settings, _ = s.TaxSettings()
	if len(settings) != 1 || settings[0].Multiplier != 1.75 {
		t.Fatalf("TaxSettings after confirming delta = %+v, want multiplier 1.75", settings)
	}
}

// TestSetDistrictMultiplier_RejectsInvalidLocally (MET-V603): a negative
// or non-finite multiplier is refused before it ever reaches the wire.
func TestSetDistrictMultiplier_RejectsInvalidLocally(t *testing.T) {
	s := New("corr-invalid")
	sendCalled := false
	send := func(protocol.Command) error { sendCalled = true; return nil }

	if err := s.SetDistrictMultiplier(send, "harbour", "councilTax", -1); err == nil {
		t.Error("negative multiplier accepted")
	}
	if err := s.SetDistrictMultiplier(send, "", "councilTax", 1.0); err == nil {
		t.Error("empty district accepted")
	}
	if err := s.SetDistrictMultiplier(send, "harbour", "", 1.0); err == nil {
		t.Error("empty instrument accepted")
	}
	if sendCalled {
		t.Error("send was called despite local validation failure")
	}
}

// TestCommandReject (AC-9): a command the engine rejects surfaces the
// rejection reason rather than silently no-op'ing.
func TestCommandReject(t *testing.T) {
	s := New("corr-reject")
	s.ApplyResult(protocol.CommandResult{
		CorrelationID: "corr-reject",
		Accepted:      false,
		Error:         &protocol.ErrorRef{Display: "effective rate exceeds the instrument's cap"},
	})
	if got := s.TaxRejectedReason(); got != "effective rate exceeds the instrument's cap" {
		t.Errorf("TaxRejectedReason = %q, want the rejection display text", got)
	}

	// A subsequent accepted result clears the reason.
	s.ApplyResult(protocol.CommandResult{CorrelationID: "corr-reject", Accepted: true})
	if got := s.TaxRejectedReason(); got != "" {
		t.Errorf("TaxRejectedReason after accept = %q, want empty", got)
	}
}

// TestApplyDelta_StaleSubscriptionDropped (AC-10): a delta for an unbound
// subscription is dropped and logged, not applied.
func TestApplyDelta_StaleSubscriptionDropped(t *testing.T) {
	s := New("corr-stale")
	// Note: no BindSubscription call.
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-unbound", Patch: mustJSON(t, fullPatch())})
	if s.HaveData() {
		t.Error("HaveData true after a delta for an unbound subscription")
	}
}

// TestApplyDelta_MalformedPatchDropped (AC-10 sibling): a malformed patch
// is dropped, keeping the screen's last-known-good state.
func TestApplyDelta_MalformedPatchDropped(t *testing.T) {
	s := New("corr-malformed")
	s.BindSubscription("sub-1")
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: mustJSON(t, fullPatch())})
	if !s.HaveData() {
		t.Fatal("setup: HaveData false after a valid delta")
	}
	before, _ := s.TaxSettings()

	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: []byte(`{not valid json`)})

	after, _ := s.TaxSettings()
	if len(after) != len(before) {
		t.Fatalf("TaxSettings changed after a malformed patch: before=%d after=%d", len(before), len(after))
	}
}

// TestApplyDelta_OversizedPatchDropped (AC-10 sibling): a patch larger
// than maxPatchWireBytes is rejected before JSON decode even runs.
func TestApplyDelta_OversizedPatchDropped(t *testing.T) {
	s := New("corr-oversized")
	s.BindSubscription("sub-1")
	huge := make([]byte, maxPatchWireBytes+1)
	for i := range huge {
		huge[i] = ' '
	}
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: huge})
	if s.HaveData() {
		t.Error("HaveData true after an oversized patch")
	}
}

// TestApplyDelta_WrongSchemaVersionDropped documents the schemaVersion
// guard.
func TestApplyDelta_WrongSchemaVersionDropped(t *testing.T) {
	s := New("corr-schema")
	s.BindSubscription("sub-1")
	raw := []byte(`{"schemaVersion":99,"taxSettings":[]}`)
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-1", Patch: raw})
	if s.HaveData() {
		t.Error("HaveData true after a wrong-schemaVersion patch")
	}
}
