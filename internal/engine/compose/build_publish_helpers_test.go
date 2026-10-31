package compose

import (
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

func sendSubscribe(t *testing.T, transport *protocol.InProcTransport, corr protocol.CorrelationID, view string) protocol.CommandResult {
	t.Helper()
	if err := transport.SendCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   corr,
		Kind:            protocol.KindSubscribe,
		Payload:         protocol.SubscribePayload{ViewName: view},
	}); err != nil {
		t.Fatalf("SendCommand(Subscribe): %v", err)
	}
	select {
	case r := <-transport.Results():
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("hang guard: no Subscribe result")
	}
	panic("unreachable")
}
