package build

import (
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// newScreenWithData returns a Screen bound to sub with the full fixture
// patch already applied.
func newScreenWithData(t *testing.T, sub protocol.SubscriptionID) *Screen {
	t.Helper()
	s := New("corr-" + string(sub))
	s.BindSubscription(sub)
	s.ApplyDelta(protocolDelta(t, sub, fullPatch()))
	return s
}

// captureCommands returns a SendCommandFunc that appends every command it
// is handed to out.
func captureCommands(out *[]protocol.Command) SendCommandFunc {
	return func(c protocol.Command) error {
		*out = append(*out, c)
		return nil
	}
}

func TestSubscribe_SendsF3BuildView(t *testing.T) {
	s := New("corr-sub")
	var cmds []protocol.Command
	if err := s.Subscribe(captureCommands(&cmds)); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("Subscribe sent %d commands, want 1", len(cmds))
	}
	c := cmds[0]
	if c.Kind != protocol.KindSubscribe {
		t.Errorf("Kind = %s, want %s", c.Kind, protocol.KindSubscribe)
	}
	p, ok := c.Payload.(protocol.SubscribePayload)
	if !ok {
		t.Fatalf("Payload type = %T, want SubscribePayload", c.Payload)
	}
	if p.ViewName != ViewSubscriptionName {
		t.Errorf("ViewName = %q, want %q", p.ViewName, ViewSubscriptionName)
	}
}

func TestApplyDelta_PopulatesAllSubSurfaces(t *testing.T) {
	s := newScreenWithData(t, "sub-data")

	if !s.HaveData() {
		t.Error("HaveData() = false after applying a full patch")
	}
	zones, have := s.Zones()
	if !have || len(zones) != 8 {
		t.Fatalf("Zones() = %d/%v, want 8/true (all §34 zone classes)", len(zones), have)
	}
	if zones[0].Zone != "dwelling" || zones[7].Zone != "mining" {
		t.Errorf("zones = %+v, want dwelling..mining in order", zones)
	}
	queue, have := s.Queue()
	if !have || len(queue) != 2 {
		t.Fatalf("Queue() = %d/%v, want 2/true", len(queue), have)
	}
	if queue[0].ID != 1 || queue[0].MaterialsRemaining != 60 || queue[0].LabourRemaining != 20 || queue[0].LeadTimeRemaining != 15 {
		t.Errorf("queue[0] = %+v, want id=1 materialsRemaining=60 labourRemaining=20 leadTimeRemaining=15", queue[0])
	}
	catalogue, have := s.Catalogue()
	if !have || len(catalogue) != 3 {
		t.Fatalf("Catalogue() = %d/%v, want 3/true", len(catalogue), have)
	}
	if catalogue[0].Unlock != UnlockUnlocked || catalogue[1].Unlock != UnlockLocked || catalogue[2].Unlock != UnlockInProgress {
		t.Errorf("catalogue unlock states = %v/%v/%v, want unlocked/locked/in-progress", catalogue[0].Unlock, catalogue[1].Unlock, catalogue[2].Unlock)
	}
	price, have := s.LandPrice()
	if !have || price.PriceMicropounds != 1_250_000 {
		t.Fatalf("LandPrice() = %+v/%v, want 1250000/true", price, have)
	}
	dem, have := s.Demolition()
	if !have || dem.CompensationMicropounds != 600_000 {
		t.Fatalf("Demolition() = %+v/%v, want 600000/true", dem, have)
	}
}

func TestApplyDelta_UnknownSubscriptionDropped(t *testing.T) {
	s := New("corr-unknown")
	s.BindSubscription("sub-known")
	s.ApplyDelta(protocolDelta(t, "sub-known", fullPatch()))

	bad := fullPatch()
	queue := *bad.Queue
	queue[0].LeadTimeRemaining = 999
	bad.Queue = &queue
	s.ApplyDelta(protocolDelta(t, "sub-ghost", bad))

	queueOut, _ := s.Queue()
	if queueOut[0].LeadTimeRemaining == 999 {
		t.Error("delta for an unknown subscription was applied (SF-7 violation)")
	}
}

func TestApplyDelta_MalformedPatchKeepsLastKnownGood(t *testing.T) {
	s := newScreenWithData(t, "sub-malformed")
	queueBefore, _ := s.Queue()

	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-malformed", Patch: []byte("{not json")})
	s.ApplyDelta(protocolDelta(t, "sub-malformed", wirePatch{SchemaVersion: 99}))

	queueAfter, _ := s.Queue()
	if len(queueAfter) != len(queueBefore) || queueAfter[0].LeadTimeRemaining != queueBefore[0].LeadTimeRemaining {
		t.Error("malformed patch changed the screen's last-known-good state")
	}
}

func TestApplyDelta_AbsentSubSurfaceMarksUnavailable(t *testing.T) {
	s := newScreenWithData(t, "sub-absent")

	// A patch carrying only zones must mark queue/catalogue/landPrice/
	// demolition unavailable and clear previously-delivered data (SF-7).
	zones := []wireZone{{ID: "dwelling", Name: "Dwelling", Materials: 100, Labour: 40, BaseLeadTimeDays: 45}}
	s.ApplyDelta(protocolDelta(t, "sub-absent", wirePatch{SchemaVersion: 1, Zones: &zones}))

	if _, have := s.Queue(); have {
		t.Error("Queue() reported have=true after a patch that omitted queue")
	}
	if _, have := s.Catalogue(); have {
		t.Error("Catalogue() reported have=true after a patch that omitted catalogue")
	}
	if _, have := s.LandPrice(); have {
		t.Error("LandPrice() reported have=true after a patch that omitted landPrice")
	}
	if _, have := s.Demolition(); have {
		t.Error("Demolition() reported have=true after a patch that omitted demolition")
	}
	if _, have := s.Zones(); !have {
		t.Error("Zones() reported have=false after a patch that delivered zones")
	}
}

func TestApplyDelta_DefensiveCopies(t *testing.T) {
	s := newScreenWithData(t, "sub-copy")

	zones, _ := s.Zones()
	zones[0].Materials = 12345
	againZ, _ := s.Zones()
	if againZ[0].Materials == 12345 {
		t.Error("caller mutation of returned zone leaked into stored state")
	}

	queue, _ := s.Queue()
	queue[0].LeadTimeRemaining = 12345
	againQ, _ := s.Queue()
	if againQ[0].LeadTimeRemaining == 12345 {
		t.Error("caller mutation of returned order leaked into stored state")
	}

	catalogue, _ := s.Catalogue()
	catalogue[0].Name = "INJECTED"
	againC, _ := s.Catalogue()
	if againC[0].Name == "INJECTED" {
		t.Error("caller mutation of returned catalogue entry leaked into stored state")
	}
}

// --- command tests (BLD-1..BLD-4, BLD-7) -----------------------------

func TestBuyLand_IssuesBuyCommand(t *testing.T) {
	s := newScreenWithData(t, "sub-buy")
	var cmds []protocol.Command
	cell := protocol.CellRef{X: 2, Y: 3}
	if err := s.BuyLand(captureCommands(&cmds), cell); err != nil {
		t.Fatalf("BuyLand: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("BuyLand sent %d commands, want 1", len(cmds))
	}
	if cmds[0].Kind != protocol.KindBuy {
		t.Errorf("Kind = %s, want %s", cmds[0].Kind, protocol.KindBuy)
	}
	p, ok := cmds[0].Payload.(protocol.BuyPayload)
	if !ok {
		t.Fatalf("payload type = %T, want BuyPayload", cmds[0].Payload)
	}
	if p.Cell != cell {
		t.Errorf("BuyPayload.Cell = %+v, want %+v", p.Cell, cell)
	}
}

func TestZonePaint_OneCommandPerCell(t *testing.T) {
	s := newScreenWithData(t, "sub-zone")
	var cmds []protocol.Command
	cells := []protocol.CellRef{{X: 1, Y: 1}, {X: 1, Y: 2}, {X: 1, Y: 3}}
	if err := s.ZonePaint(captureCommands(&cmds), cells, "dwelling"); err != nil {
		t.Fatalf("ZonePaint: %v", err)
	}
	// BLD-2: exactly one command per painted cell — no silently dropped
	// subset.
	if len(cmds) != len(cells) {
		t.Fatalf("ZonePaint sent %d commands for %d cells, want one per cell", len(cmds), len(cells))
	}
	for i, c := range cmds {
		if c.Kind != protocol.KindZone {
			t.Errorf("cmd[%d].Kind = %s, want %s", i, c.Kind, protocol.KindZone)
		}
		p, ok := c.Payload.(protocol.ZonePayload)
		if !ok {
			t.Fatalf("cmd[%d] payload type = %T, want ZonePayload", i, c.Payload)
		}
		if p.Cell != cells[i] || p.ZoneType != "dwelling" {
			t.Errorf("cmd[%d] = %+v, want cell %+v zone dwelling", i, p, cells[i])
		}
	}
}

func TestZonePaint_UnknownZoneRejected(t *testing.T) {
	s := newScreenWithData(t, "sub-zone-unknown")
	var cmds []protocol.Command
	err := s.ZonePaint(captureCommands(&cmds), []protocol.CellRef{{X: 1, Y: 1}}, "spaceport")
	if err == nil {
		t.Fatal("ZonePaint(unknown zone) returned nil, want ErrUnknownZoneType")
	}
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrUnknownZoneType {
		t.Errorf("err code = %v, want %s (BLD-7: never a silent rejection)", err, ErrUnknownZoneType)
	}
	if len(cmds) != 0 {
		t.Errorf("ZonePaint sent %d commands on a rejected action, want 0", len(cmds))
	}
}

func TestBuildOn_IssuesCommandAndRejectsUnknown(t *testing.T) {
	s := newScreenWithData(t, "sub-build")
	var cmds []protocol.Command
	cell := protocol.CellRef{X: 4, Y: 4}
	if err := s.BuildOn(captureCommands(&cmds), cell, "footpath"); err != nil {
		t.Fatalf("BuildOn: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Kind != protocol.KindBuild {
		t.Fatalf("BuildOn = %v, want 1 KindBuild command", cmds)
	}
	p, ok := cmds[0].Payload.(protocol.BuildPayload)
	if !ok || p.Cell != cell || p.BuildingType != "footpath" {
		t.Fatalf("BuildPayload = %+v, want cell %+v building footpath", cmds[0].Payload, cell)
	}

	err := s.BuildOn(captureCommands(&cmds), cell, "fusion_pilot")
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrUnknownBuilding {
		t.Errorf("err = %v, want ErrUnknownBuilding (BLD-7)", err)
	}
	if len(cmds) != 1 {
		t.Errorf("BuildOn sent %d commands, want 1 (unknown building must not send)", len(cmds))
	}
}

func TestDemolish_CostConfirmAndCommit(t *testing.T) {
	s := newScreenWithData(t, "sub-demolish")
	cell := protocol.CellRef{X: 2, Y: 3}

	// BLD-4: DemolishCost surfaces the view's compensation BEFORE commit.
	cost, found := s.DemolishCost(cell)
	if !found || cost != 600_000 {
		t.Fatalf("DemolishCost(%+v) = %d/%v, want 600000/true", cell, cost, found)
	}

	// The Demolish command carries exactly the cell whose cost was shown.
	var cmds []protocol.Command
	if err := s.Demolish(captureCommands(&cmds), cell); err != nil {
		t.Fatalf("Demolish: %v", err)
	}
	if len(cmds) != 1 || cmds[0].Kind != protocol.KindDemolish {
		t.Fatalf("Demolish = %v, want 1 KindDemolish command", cmds)
	}
	p, ok := cmds[0].Payload.(protocol.DemolishPayload)
	if !ok || p.Cell != cell {
		t.Fatalf("DemolishPayload = %+v, want cell %+v", cmds[0].Payload, cell)
	}
}

func TestDemolish_NoReportedCostRejected(t *testing.T) {
	s := newScreenWithData(t, "sub-demolish-nocost")
	var cmds []protocol.Command

	// BLD-4: the confirm step is not skippable — a cell the view reports no
	// compensation for cannot be demolished (refused loudly, never a silent
	// deletion).
	cell := protocol.CellRef{X: 9, Y: 9}
	err := s.Demolish(captureCommands(&cmds), cell)
	if err == nil {
		t.Fatal("Demolish(no-cost cell) returned nil, want ErrUnknownStructure")
	}
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrUnknownStructure {
		t.Errorf("err code = %v, want %s", err, ErrUnknownStructure)
	}
	if len(cmds) != 0 {
		t.Errorf("Demolish sent %d commands on a rejected action, want 0", len(cmds))
	}
}

func TestErrorCodesAreRegistered(t *testing.T) {
	// GR#7: the codes this package raises must be well-formed and distinct,
	// and resolve against the registry (data/errors.json — enforced
	// mechanically by internal/foundation/errs' source-scan test; this
	// test only asserts the local constants are shaped and unique).
	codes := []string{ErrMalformedPatch, ErrUnknownSubscription, ErrScreenCopied, ErrUnknownZoneType, ErrUnknownStructure, ErrUnknownBuilding}
	seen := map[string]bool{}
	for _, c := range codes {
		if !strings.HasPrefix(c, "MET-V2") {
			t.Errorf("code %s does not start with MET-V2 (this package's reserved range)", c)
		}
		if seen[c] {
			t.Errorf("duplicate code %s", c)
		}
		seen[c] = true
	}
}
