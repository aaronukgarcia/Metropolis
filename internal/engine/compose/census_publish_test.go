package compose

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	uicensus "github.com/aaronukgarcia/Metropolis/internal/ui/screens/census"
)

// FEAT-209's engine-side proof set for the "f6.census" view
// (census_publish.go): it is REGISTERED, its Subscribe is ACCEPTED, and
// the delta it publishes carries real, NON-EMPTY population data — the
// three things that, like BUG-323's "f1.viewport" before it, are only a
// fix when true together. A view that registers and then publishes an
// empty patch renders identically to no view at all, so "the subscription
// succeeded" is explicitly NOT what any assertion here settles for: the
// load-bearing assertion is that the delivered age-band spline sums to the
// real 64-citizen seed population.

// TestCensusViewSubscriptionName_MatchesUIScreenConstant guards the two
// independently-maintained copies of "f6.census" (this package's
// censusViewSubscriptionName and ui.screen.census's ViewSubscriptionName)
// against drifting apart in VALUE — the same GR#20/SF-1 discipline the
// finance/services/viewport name-match tests apply. This test file is the
// only place in this package that imports internal/ui/screens/census;
// production code never does.
func TestCensusViewSubscriptionName_MatchesUIScreenConstant(t *testing.T) {
	if censusViewSubscriptionName != uicensus.ViewSubscriptionName {
		t.Fatalf("censusViewSubscriptionName = %q, want %q (ui.screen.census's own ViewSubscriptionName)", censusViewSubscriptionName, uicensus.ViewSubscriptionName)
	}
}

// TestCensusView_IsRegistered pins the registration itself, by name, in
// compose's fixed view-registration slice — the same shape as
// TestViewportView_IsRegistered, so deleting the entry names the symptom.
func TestCensusView_IsRegistered(t *testing.T) {
	for _, n := range RegisteredViewNames() {
		if n == censusViewSubscriptionName {
			return
		}
	}
	t.Fatalf("%q is not in compose's registered view set %v — with it absent, engine.core rejects the census screen's Subscribe and F6 renders as unavailable panes (FEAT-209)", censusViewSubscriptionName, RegisteredViewNames())
}

// wireCensusTestEngine builds a real compose.Wire'd engine (default deps —
// engine.census is constructed internally by Wire and its citizens source
// wired against the composed *citizens.CitizensAPI) and a live subscription
// pump, mirroring wireFinanceTestEngine's shape exactly.
func wireCensusTestEngine(t *testing.T) (*core.Engine, *protocol.InProcTransport, context.CancelFunc) {
	t.Helper()
	cid := errs.NewCorrelationID()

	e := core.NewEngine()
	if _, err := Wire(e, &Deps{CorrelationID: cid}); err != nil {
		t.Fatalf("Wire: %v", err)
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := e.StartSubscriptionPump(ctx, transport); err != nil {
		cancel()
		t.Fatalf("StartSubscriptionPump: %v", err)
	}
	go func() { _ = e.RunCommandLoop(ctx, transport) }()

	return e, transport, cancel
}

// TestCensusView_EndToEnd_DeltaCarriesRealPopulation is the core
// engine-side CONTENT proof: Subscribe("f6.census") against a REAL
// compose.Wire'd engine is accepted, and the delivered patch's age-band
// spline sums to the real 64-citizen seed population (Wire's
// seedCitizenCount) rather than to zero. An empty patch here — the exact
// failure a registered-but-empty view produces — would fail the first
// assertion below, on purpose.
func TestCensusView_EndToEnd_DeltaCarriesRealPopulation(t *testing.T) {
	_, transport, cancel := wireCensusTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uicensus.ViewSubscriptionName)

	var patch censusWirePatch
	if err := json.Unmarshal(delta.Patch, &patch); err != nil {
		t.Fatalf("unmarshalling f6.census patch: %v", err)
	}
	if patch.SchemaVersion != censusWireSchemaVersion {
		t.Fatalf("patch schemaVersion = %d, want %d (ui.screen.census's decodeWirePatch drops any other version outright)", patch.SchemaVersion, censusWireSchemaVersion)
	}
	if patch.AgeBands == nil {
		t.Fatal("ageBands is absent from the wire patch — buildCensusPatch always sets this field; an absent spline renders as 'unavailable'")
	}
	if patch.SexSeries == nil {
		t.Fatal("sexSeries is absent from the wire patch")
	}
	if patch.EducationTiers == nil {
		t.Fatal("educationTiers is absent from the wire patch")
	}
	if patch.BlueWhiteCollar == nil {
		t.Fatal("blueWhiteCollar is absent from the wire patch")
	}
	if patch.KPIs == nil || len(*patch.KPIs) != 8 {
		t.Fatalf("kpis absent or not 8 entries (got %v) — F6's eight KPI tiles must all be present", patch.KPIs)
	}
	if patch.EducationCrimeLinkage == nil {
		t.Fatal("educationCrimeLinkage is absent from the wire patch")
	}

	// The load-bearing assertion: the population content is REAL, not an
	// empty/zero placeholder. Every seed citizen is born at month 0, so the
	// whole population sits in the first age band (0-17); the spline must
	// sum to exactly seedCitizenCount.
	var pop int64
	for i, v := range patch.AgeBands {
		pop += v
		if i == 0 && v != seedCitizenCount {
			t.Errorf("ageBands[0] = %d, want %d (every seed citizen is born at month 0)", v, seedCitizenCount)
		}
	}
	if pop != seedCitizenCount {
		t.Fatalf("sum(ageBands) = %d, want %d (the real seed population) — a registered-but-empty view would publish zero here", pop, seedCitizenCount)
	}

	var sexPop int64
	for _, v := range patch.SexSeries {
		sexPop += v
	}
	if sexPop != seedCitizenCount {
		t.Errorf("sum(sexSeries) = %d, want %d", sexPop, seedCitizenCount)
	}

	var tierPop int64
	for _, v := range patch.EducationTiers {
		tierPop += v
	}
	if tierPop != seedCitizenCount {
		t.Errorf("sum(educationTiers) = %d, want %d", tierPop, seedCitizenCount)
	}

	// The linkage report's population must match too — the same snapshot.
	if patch.EducationCrimeLinkage.Population != seedCitizenCount {
		t.Errorf("educationCrimeLinkage.population = %d, want %d", patch.EducationCrimeLinkage.Population, seedCitizenCount)
	}
}

// TestCensusView_EndToEnd_RoundTripsThroughUIScreenDecoder proves compose's
// independently-maintained schema copy (census_publish.go) actually
// round-trips through ui.screen.census's own ApplyDelta/accessor path — the
// UI half of the seam the engine can never import (GR#20). The screen's
// AgeBandSeries() must report have=true with a non-zero population, which
// is the state the render layer turns into visible glyphs.
func TestCensusView_EndToEnd_RoundTripsThroughUIScreenDecoder(t *testing.T) {
	_, transport, cancel := wireCensusTestEngine(t)
	defer cancel()
	defer func() { _ = transport.Close() }()

	_, delta := subscribeAndAwaitFirstDelta(t, transport, uicensus.ViewSubscriptionName)

	scr := uicensus.New(errs.NewCorrelationID())
	id := protocol.SubscriptionID("sub-census")
	scr.BindSubscription(id)
	scr.ApplyDelta(protocol.Delta{SubscriptionID: id, Patch: delta.Patch})

	bands, have := scr.AgeBandSeries()
	if !have {
		t.Fatal("ui.screen.census AgeBandSeries() reported have=false after ApplyDelta — the published patch did not carry an ageBands field the UI decoder recognised")
	}
	var pop int64
	for _, v := range bands {
		pop += v
	}
	if pop != seedCitizenCount {
		t.Fatalf("AgeBandSeries() sums to %d, want %d (seed population) — the wire schema did not round-trip through ui.screen.census's decoder", pop, seedCitizenCount)
	}

	if kpis, ok := scr.KPITiles(); !ok || len(kpis) != 8 {
		t.Fatalf("KPITiles() = (%d tiles, ok=%v), want 8 tiles, ok=true", len(kpis), ok)
	}
}
