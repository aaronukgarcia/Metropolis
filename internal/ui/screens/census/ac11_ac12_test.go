package census

// AC-11 (error handling -- unknown/stale subscription dropped and
// logged) / AC-12 (engine rejection surfaces as an explicit "unavailable"
// state, never a silent zero).

import (
	"testing"
)

// TestAC12_KPIUnavailableRendersExplicitState proves a KPI source the
// engine rejected (Unavailable=true on the wire) surfaces as an explicit
// "unavailable" pane, never a silently-rendered zero EntityIDs/LineValue.
func TestAC12_KPIUnavailableRendersExplicitState(t *testing.T) {
	s := New("corr-kpi-unavailable")
	s.BindSubscription("sub-kpi-unavailable")
	kpiSources := []wireKPISource{
		{Key: KPIKeyHomeless, Unavailable: true, Reason: "MET-G2702: unknown key"},
	}
	s.ApplyDelta(protocolDelta(t, "sub-kpi-unavailable", wirePatch{SchemaVersion: 1, KPISources: &kpiSources}))

	src, ok := s.KPISource(KPIKeyHomeless)
	if !ok {
		t.Fatal("KPISource ok=false, want true (the source WAS sent, just marked unavailable)")
	}
	if !src.Unavailable {
		t.Fatal("KPISource.Unavailable = false, want true")
	}
	if src.LineValue != 0 || len(src.EntityIDs) != 0 {
		t.Errorf("an unavailable source should carry no fabricated data: LineValue=%d EntityIDs=%v", src.LineValue, src.EntityIDs)
	}

	rows := renderCitizenBioRows(t, CitizenBio{}, false) // sanity: helper works
	_ = rows

	buf, rect := renderKPISourceInto(src, true)
	if !rowContains(renderedText(buf, rect), "unavailable") {
		t.Error("RenderKPISource did not render \"unavailable\" for a rejected KPI source")
	}
}

// TestAC12_BioUnavailableRendersExplicitState proves a citizen bio the
// engine rejected surfaces as an explicit "unavailable" pane, never a
// zero-value bio the player could mistake for a real empty citizen.
func TestAC12_BioUnavailableRendersExplicitState(t *testing.T) {
	s := New("corr-bio-unavailable")
	s.BindSubscription("sub-bio-unavailable")
	bio := wireCitizenBio{GUID: "citizen:999999", Unavailable: true, Reason: "MET-G2701: unknown object"}
	s.ApplyDelta(protocolDelta(t, "sub-bio-unavailable", wirePatch{SchemaVersion: 1, SelectedBio: &bio}))

	got, have := s.SelectedBio()
	if !have {
		t.Fatal("SelectedBio have=false, want true (the bio WAS sent, just marked unavailable)")
	}
	if !got.Unavailable {
		t.Fatal("SelectedBio.Unavailable = false, want true")
	}
	if got.Education.Attainment != 0 || got.Income != 0 {
		t.Errorf("an unavailable bio should carry no fabricated data: %+v", got)
	}

	rows := renderCitizenBioRows(t, got, true)
	if !rowContains(rows, "unavailable") {
		t.Error("RenderCitizenBio did not render \"unavailable\" for a rejected bio query")
	}
}

// TestAC11_UnknownSubscriptionDroppedNotAppliedSecondPatch proves a
// second Delta on a subscription that was bound then unbound is dropped.
func TestAC11_UnboundAfterUnsubscribeDropped(t *testing.T) {
	s := newScreenWithData(t, "sub-unsub")
	s.UnbindSubscription("sub-unsub")

	bad := fullPatch()
	bands := [NumAgeBands]int64{7, 7, 7, 7, 7}
	bad.AgeBands = &bands
	s.ApplyDelta(protocolDelta(t, "sub-unsub", bad))

	got, _ := s.AgeBandSeries()
	if got == bands {
		t.Error("delta applied after UnbindSubscription -- AC-11 violation")
	}
}
