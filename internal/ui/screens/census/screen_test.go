package census

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func newScreenWithData(t *testing.T, sub protocol.SubscriptionID) *Screen {
	t.Helper()
	s := New("corr-" + string(sub))
	s.BindSubscription(sub)
	s.ApplyDelta(protocolDelta(t, sub, fullPatch()))
	return s
}

func TestApplyDelta_PopulatesAllSubSurfaces(t *testing.T) {
	s := newScreenWithData(t, "sub-full")

	if !s.HaveData() {
		t.Fatal("HaveData() = false after a full patch")
	}
	if bands, have := s.AgeBandSeries(); !have || bands != [NumAgeBands]int64{1000, 2500, 2200, 1400, 600} {
		t.Errorf("AgeBandSeries() = %v, %v; want fixture values, have=true", bands, have)
	}
	if sex, have := s.SexSeries(); !have || sex != [NumSexBuckets]int64{3800, 3900} {
		t.Errorf("SexSeries() = %v, %v; want fixture values, have=true", sex, have)
	}
	if tiers, have := s.EducationTierSeries(); !have || tiers[2] != 1500 {
		t.Errorf("EducationTierSeries() = %v, %v; want fixture values, have=true", tiers, have)
	}
	if bwc, have := s.BlueWhiteCollarSplit(); !have || bwc.Blue != 4200 || bwc.White != 1800 {
		t.Errorf("BlueWhiteCollarSplit() = %v, %v; want fixture values, have=true", bwc, have)
	}
	if kpis, have := s.KPITiles(); !have || len(kpis) != 8 {
		t.Errorf("KPITiles() = %v, %v; want 8 tiles, have=true", kpis, have)
	}
	if src, ok := s.KPISource(KPIKeyHomeless); !ok || len(src.EntityIDs) != 3 {
		t.Errorf("KPISource(homeless) = %v, %v; want 3 entity ids, ok=true", src, ok)
	}
	if bio, have := s.SelectedBio(); !have || bio.GUID != "citizen:42" {
		t.Errorf("SelectedBio() = %v, %v; want fixture bio, have=true", bio, have)
	}
	if link, have := s.EducationCrimeLinkageView(); !have || link.Population != 8000 {
		t.Errorf("EducationCrimeLinkageView() = %v, %v; want fixture linkage, have=true", link, have)
	}
}

func TestApplyDelta_UnboundSubscriptionIsDroppedNotApplied(t *testing.T) {
	s := newScreenWithData(t, "sub-bound")

	bad := fullPatch()
	ageBands := [NumAgeBands]int64{9999, 9999, 9999, 9999, 9999}
	bad.AgeBands = &ageBands
	s.ApplyDelta(protocolDelta(t, "sub-ghost", bad))

	bands, _ := s.AgeBandSeries()
	if bands[0] == 9999 {
		t.Error("delta for an unknown subscription was applied (AC-11 violation)")
	}
}

func TestApplyDelta_MalformedPatchKeepsLastKnownGood(t *testing.T) {
	s := newScreenWithData(t, "sub-malformed")
	before, _ := s.AgeBandSeries()

	// Invalid JSON.
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-malformed", Patch: []byte("{not json")})
	// Wrong schema version.
	s.ApplyDelta(protocolDelta(t, "sub-malformed", wirePatch{SchemaVersion: 99}))

	after, _ := s.AgeBandSeries()
	if after != before {
		t.Error("malformed patch changed the screen's last-known-good state")
	}
}

func TestApplyDelta_OversizedPatchRejected(t *testing.T) {
	s := newScreenWithData(t, "sub-oversized")
	before, _ := s.AgeBandSeries()

	huge := make([]byte, maxPatchWireBytes+1)
	for i := range huge {
		huge[i] = 'a'
	}
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-oversized", Patch: huge})

	after, _ := s.AgeBandSeries()
	if after != before {
		t.Error("oversized patch was not rejected -- last-known-good state changed")
	}
}

func TestApplyDelta_AbsentSubSurfaceMarksUnavailable(t *testing.T) {
	s := newScreenWithData(t, "sub-absent")

	// A patch carrying only ageBands must mark every other sub-surface
	// unavailable and clear its previously-delivered data (AC-11: no
	// stale data).
	bands := [NumAgeBands]int64{1, 2, 3, 4, 5}
	s.ApplyDelta(protocolDelta(t, "sub-absent", wirePatch{SchemaVersion: 1, AgeBands: &bands}))

	if _, have := s.SexSeries(); have {
		t.Error("SexSeries() reported have=true after a patch that omitted it")
	}
	if _, have := s.EducationTierSeries(); have {
		t.Error("EducationTierSeries() reported have=true after a patch that omitted it")
	}
	if _, have := s.BlueWhiteCollarSplit(); have {
		t.Error("BlueWhiteCollarSplit() reported have=true after a patch that omitted it")
	}
	if _, have := s.KPITiles(); have {
		t.Error("KPITiles() reported have=true after a patch that omitted it")
	}
	if _, have := s.SelectedBio(); have {
		t.Error("SelectedBio() reported have=true after a patch that omitted it")
	}
	if _, have := s.EducationCrimeLinkageView(); have {
		t.Error("EducationCrimeLinkageView() reported have=true after a patch that omitted it")
	}
}

func TestApplyResult_RejectionSurfacedThenClearedOnAccept(t *testing.T) {
	s := New("corr-result")

	s.ApplyResult(protocol.CommandResult{
		CorrelationID: "corr-result",
		Accepted:      false,
		Error:         &protocol.ErrorRef{Code: "MET-G2701", Display: "unknown census object"},
	})
	if got := s.SelectionRejectedReason(); got != "unknown census object" {
		t.Errorf("SelectionRejectedReason() = %q, want the engine's rejection reason", got)
	}

	s.ApplyResult(protocol.CommandResult{CorrelationID: "corr-result", Accepted: true})
	if got := s.SelectionRejectedReason(); got != "" {
		t.Errorf("SelectionRejectedReason() = %q after an accepted result, want empty", got)
	}
}

func TestApplyResult_IgnoresMismatchedCorrelationID(t *testing.T) {
	s := New("corr-mine")
	s.ApplyResult(protocol.CommandResult{
		CorrelationID: "corr-someone-else",
		Accepted:      false,
		Error:         &protocol.ErrorRef{Code: "MET-G2701", Display: "not for this screen"},
	})
	if got := s.SelectionRejectedReason(); got != "" {
		t.Errorf("SelectionRejectedReason() = %q, want empty for a result addressed to a different correlation ID", got)
	}
}

func TestSelectKPI_SendsFixedOpString(t *testing.T) {
	s := New("corr-select-kpi")
	var got protocol.Command
	send := func(cmd protocol.Command) error {
		got = cmd
		return nil
	}
	if err := s.SelectKPI(send, KPIKeyHomeless); err != nil {
		t.Fatalf("SelectKPI: %v", err)
	}
	payload, ok := got.Payload.(protocol.DebugPayload)
	if !ok {
		t.Fatalf("Payload type = %T, want protocol.DebugPayload", got.Payload)
	}
	if payload.Op != opSelectKPI {
		t.Errorf("Op = %q, want %q (ASM-1193 fixed Op string convention)", payload.Op, opSelectKPI)
	}
	if payload.Args["key"] != KPIKeyHomeless {
		t.Errorf("Args[key] = %q, want %q", payload.Args["key"], KPIKeyHomeless)
	}
}

func TestSelectCitizen_SendsFixedOpString(t *testing.T) {
	s := New("corr-select-citizen")
	var got protocol.Command
	send := func(cmd protocol.Command) error {
		got = cmd
		return nil
	}
	if err := s.SelectCitizen(send, "citizen:42"); err != nil {
		t.Fatalf("SelectCitizen: %v", err)
	}
	payload, ok := got.Payload.(protocol.DebugPayload)
	if !ok {
		t.Fatalf("Payload type = %T, want protocol.DebugPayload", got.Payload)
	}
	if payload.Op != opSelectCitizen {
		t.Errorf("Op = %q, want %q (ASM-1193 fixed Op string convention)", payload.Op, opSelectCitizen)
	}
	if payload.Args["guid"] != "citizen:42" {
		t.Errorf("Args[guid] = %q, want %q", payload.Args["guid"], "citizen:42")
	}
}
