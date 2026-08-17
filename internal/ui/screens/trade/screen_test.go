package trade

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

func TestSubscribe_SendsF5TradeView(t *testing.T) {
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
	contracts, have := s.Contracts()
	if !have || len(contracts) != 2 {
		t.Fatalf("Contracts() = %d/%v, want 2/true", len(contracts), have)
	}
	if contracts[0].ID != "c-1" || contracts[0].Commodity != "grain" {
		t.Errorf("contracts[0] = %+v, want c-1/grain", contracts[0])
	}
	junctions, have := s.Junctions()
	if !have || len(junctions) != 1 || len(junctions[0].Approaches) != 2 {
		t.Fatalf("Junctions() = %d/%v, want 1 junction with 2 approaches", len(junctions), have)
	}
	if junctions[0].Approaches[0].TruckCount != 12 || junctions[0].Approaches[0].WaitSeconds != 45 {
		t.Errorf("approach[0] = %+v, want truckCount 12 waitSeconds 45", junctions[0].Approaches[0])
	}
	warehouse, have := s.Warehouse()
	if !have || len(warehouse) != 2 {
		t.Fatalf("Warehouse() = %d/%v, want 2/true", len(warehouse), have)
	}
	port, have := s.Port()
	if !have || !port.Unlocked || port.Berths != 4 {
		t.Fatalf("Port() = %+v/%v, want unlocked berths=4", port, have)
	}
	balance, have := s.Balance()
	if !have || len(balance.Imports.ByCommodity) != 1 || len(balance.Exports.ByArtery) != 1 {
		t.Fatalf("Balance() = %+v/%v, want 1 import commodity + 1 export artery", balance, have)
	}
	safety, have := s.Safety()
	if !have || len(safety) != 1 || safety[0].Corridor != "port-refinery" {
		t.Fatalf("Safety() = %+v/%v, want 1 corridor", safety, have)
	}
}

func TestApplyDelta_UnknownSubscriptionDropped(t *testing.T) {
	s := New("corr-unknown")
	s.BindSubscription("sub-known")
	s.ApplyDelta(protocolDelta(t, "sub-known", fullPatch()))

	// A delta for an unbound subscription must be dropped, not applied.
	bad := fullPatch()
	contracts := *bad.Contracts
	contracts[0].PricePerUnitMicropounds = 999_000_000
	bad.Contracts = &contracts
	s.ApplyDelta(protocolDelta(t, "sub-ghost", bad))

	contractsOut, _ := s.Contracts()
	if contractsOut[0].PricePerUnitMicropounds == 999_000_000 {
		t.Error("delta for an unknown subscription was applied (SF-7 violation)")
	}
}

func TestApplyDelta_MalformedPatchKeepsLastKnownGood(t *testing.T) {
	s := newScreenWithData(t, "sub-malformed")
	contractsBefore, _ := s.Contracts()

	// Invalid JSON.
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-malformed", Patch: []byte("{not json")})
	// Wrong schema version.
	s.ApplyDelta(protocolDelta(t, "sub-malformed", wirePatch{SchemaVersion: 99}))

	contractsAfter, _ := s.Contracts()
	if len(contractsAfter) != len(contractsBefore) || contractsAfter[0].PricePerUnitMicropounds != contractsBefore[0].PricePerUnitMicropounds {
		t.Error("malformed patch changed the screen's last-known-good state")
	}
}

func TestApplyDelta_AbsentSubSurfaceMarksUnavailable(t *testing.T) {
	s := newScreenWithData(t, "sub-absent")

	// A patch carrying only contracts must mark the port unavailable and
	// clear its previously-delivered data (SF-7: no stale data).
	contracts := []wireContract{{ID: "c-9", Commodity: "grain", TermMonths: 3, Status: "active"}}
	s.ApplyDelta(protocolDelta(t, "sub-absent", wirePatch{SchemaVersion: 1, Contracts: &contracts}))

	if _, have := s.Port(); have {
		t.Error("Port() reported have=true after a patch that omitted the port (SF-7: should be unavailable, not stale)")
	}
	if _, have := s.Balance(); have {
		t.Error("Balance() reported have=true after a patch that omitted balance")
	}
	if _, have := s.Contracts(); !have {
		t.Error("Contracts() reported have=false after a patch that delivered contracts")
	}
}

func TestApplyDelta_DefensiveCopies(t *testing.T) {
	s := newScreenWithData(t, "sub-copy")

	// Mutating the returned contracts slice must not corrupt stored state.
	contracts, _ := s.Contracts()
	contracts[0].PricePerUnitMicropounds = 12345
	_ = append(contracts, ImportContract{ID: "injected"})
	again, _ := s.Contracts()
	if len(again) != 2 {
		t.Fatalf("stored contracts length changed to %d after caller mutation", len(again))
	}
	if again[0].PricePerUnitMicropounds == 12345 {
		t.Error("caller mutation of returned contract leaked into stored state")
	}

	// Mutating a returned junction's approaches must not corrupt stored state.
	junctions, _ := s.Junctions()
	junctions[0].Approaches[0].TruckCount = 999
	againJ, _ := s.Junctions()
	if againJ[0].Approaches[0].TruckCount == 999 {
		t.Error("caller mutation of returned approach leaked into stored state")
	}

	// Mutating a returned balance flow must not corrupt stored state.
	balance, _ := s.Balance()
	balance.Imports.ByCommodity[0].TonnesPerDay = 999
	againB, _ := s.Balance()
	if againB.Imports.ByCommodity[0].TonnesPerDay == 999 {
		t.Error("caller mutation of returned balance flow leaked into stored state")
	}
}

func TestCancellationPenalty(t *testing.T) {
	s := newScreenWithData(t, "sub-penalty")

	if p, found := s.CancellationPenalty("c-1"); !found || p != 1500000 {
		t.Errorf("CancellationPenalty(c-1) = %d/%v, want 1500000/true", p, found)
	}
	if p, found := s.CancellationPenalty("c-2"); !found || p != 0 {
		t.Errorf("CancellationPenalty(c-2) = %d/%v, want 0/true (penalty-free)", p, found)
	}
	if _, found := s.CancellationPenalty("nope"); found {
		t.Error("CancellationPenalty(nope) found=true, want false")
	}
}

func TestCancelContract_SurfacesPenaltyInCommand(t *testing.T) {
	s := newScreenWithData(t, "sub-cancel")
	var cmds []protocol.Command
	if err := s.CancelContract(captureCommands(&cmds), "c-1"); err != nil {
		t.Fatalf("CancelContract: %v", err)
	}
	if len(cmds) != 1 {
		t.Fatalf("CancelContract sent %d commands, want 1", len(cmds))
	}
	p, ok := cmds[0].Payload.(protocol.DebugPayload)
	if !ok {
		t.Fatalf("payload type = %T, want DebugPayload", cmds[0].Payload)
	}
	if p.Op != opCancelContract {
		t.Errorf("Op = %q, want %q", p.Op, opCancelContract)
	}
	if p.Args["contractId"] != "c-1" {
		t.Errorf("Args[contractId] = %q, want c-1", p.Args["contractId"])
	}
	if p.Args["penaltyMicropounds"] != "1500000" {
		t.Errorf("Args[penaltyMicropounds] = %q, want 1500000 (TRD-7: penalty carried, not silently charged)", p.Args["penaltyMicropounds"])
	}
}

func TestCancelContract_UnknownContractRejected(t *testing.T) {
	s := newScreenWithData(t, "sub-cancel-unknown")
	var cmds []protocol.Command
	err := s.CancelContract(captureCommands(&cmds), "nope")
	if err == nil {
		t.Fatal("CancelContract(nope) returned nil, want ErrUnknownContract")
	}
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrUnknownContract {
		t.Errorf("err code = %v, want %s (TRD-7: never a silent rejection)", err, ErrUnknownContract)
	}
	if len(cmds) != 0 {
		t.Errorf("CancelContract sent %d commands on a rejected action, want 0", len(cmds))
	}
}

func TestCreateContract_IssuesCommand(t *testing.T) {
	s := newScreenWithData(t, "sub-create")
	var cmds []protocol.Command
	if err := s.CreateContract(captureCommands(&cmds), "grain", 24, 30_000_000); err != nil {
		t.Fatalf("CreateContract: %v", err)
	}
	p, ok := cmds[0].Payload.(protocol.DebugPayload)
	if !ok || p.Op != opCreateContract {
		t.Fatalf("payload = %+v, want create-contract DebugPayload", cmds[0].Payload)
	}
	if p.Args["commodity"] != "grain" || p.Args["termMonths"] != "24" {
		t.Errorf("Args = %v, want commodity=grain termMonths=24", p.Args)
	}
}

func TestCreateContract_EmptyCommodityRejected(t *testing.T) {
	s := New("corr-empty")
	var cmds []protocol.Command
	err := s.CreateContract(captureCommands(&cmds), "", 12, 100)
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrUnknownCommodity {
		t.Errorf("err = %v, want ErrUnknownCommodity", err)
	}
	if len(cmds) != 0 {
		t.Error("CreateContract sent a command despite an empty commodity")
	}
}

func TestSetBuffer_IssuesCommandAndRejectsUnknown(t *testing.T) {
	s := newScreenWithData(t, "sub-buffer")
	var cmds []protocol.Command
	if err := s.SetBuffer(captureCommands(&cmds), "grain", 40); err != nil {
		t.Fatalf("SetBuffer: %v", err)
	}
	p, ok := cmds[0].Payload.(protocol.DebugPayload)
	if !ok || p.Op != opSetBuffer {
		t.Fatalf("payload = %+v, want set-buffer DebugPayload", cmds[0].Payload)
	}
	if p.Args["commodity"] != "grain" || p.Args["bufferTonnesPerDay"] != "40" {
		t.Errorf("Args = %v, want commodity=grain bufferTonnesPerDay=40", p.Args)
	}

	// Unknown commodity is refused loudly.
	err := s.SetBuffer(captureCommands(&cmds), "gold", 40)
	e, ok := err.(*errs.E)
	if !ok || e.Code != ErrUnknownCommodity {
		t.Errorf("err = %v, want ErrUnknownCommodity", err)
	}
	if len(cmds) != 1 {
		t.Errorf("SetBuffer sent %d commands, want 1 (unknown commodity must not send)", len(cmds))
	}
}

func TestErrorCodesAreRegistered(t *testing.T) {
	// GR#7: the codes this package raises must resolve against the registry
	// (data/errors.json). ErrUnknownSubscription is not exercised by the
	// screen's own logic path here, but it must still be a valid registry
	// code string (MET-V1xx) and distinct from the others.
	codes := []string{ErrMalformedPatch, ErrUnknownSubscription, ErrScreenCopied, ErrUnknownContract, ErrUnknownCommodity}
	seen := map[string]bool{}
	for _, c := range codes {
		if !strings.HasPrefix(c, "MET-V1") {
			t.Errorf("code %s does not start with MET-V1 (this package's reserved range)", c)
		}
		if seen[c] {
			t.Errorf("duplicate code %s", c)
		}
		seen[c] = true
	}
}
