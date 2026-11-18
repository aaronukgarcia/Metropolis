package wsserver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// lifecycle_caps_test.go -- FEAT-9995385597 inc1 (AC-P1, AC-P5).

// TestDefaultServerCapabilities_NoDedup_NoLifecycleYet: this build's server
// must not advertise command.dedup (inc2 dedup is not live, AC-P1) and,
// because no server path emits the lifecycle notifications in this
// increment, must not advertise the lifecycle flags either.
func TestDefaultServerCapabilities_NoDedup_NoLifecycleYet(t *testing.T) {
	for _, banned := range []string{
		protocol.CapCommandDedup,
		protocol.CapLifecycleWaking,
		protocol.CapLifecycleDraining,
		protocol.CapLifecycleHibernating,
	} {
		if protocol.HasCapability(defaultServerCapabilities, banned) {
			t.Errorf("defaultServerCapabilities must not contain %q in FEAT-9995385597 inc1", banned)
		}
	}
}

// TestHandshake_ClientSessionID_DecodedAndOptional: the field decodes from
// the wire, and a handshake with or without it is accepted (legacy client
// unchanged).
func TestHandshake_ClientSessionID_DecodedAndOptional(t *testing.T) {
	var p handshakeParams
	if err := json.Unmarshal([]byte(`{"clientVersion":"v1","clientSessionId":"abc123","futureField":1}`), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.ClientSessionID != "abc123" {
		t.Fatalf("ClientSessionID = %q, want abc123", p.ClientSessionID)
	}
	b, _ := json.Marshal(handshakeParams{ClientVersion: "v1"})
	if string(b) != `{"clientVersion":"v1"}` {
		t.Fatalf("absent id must be omitted on the wire, got %s", b)
	}

	for _, id := range []string{"", "0123456789abcdef0123456789abcdef"} {
		url, _, cleanup := newTestServer(t, "v1.2.3", time.Second)
		conn := dial(t, url)
		_, res := sendHandshakeFull(t, conn, handshakeParams{ClientVersion: "v1.2.3", ClientSessionID: id})
		if !res.Accepted {
			t.Errorf("handshake with clientSessionId %q refused", id)
		}
		_ = conn.Close()
		cleanup()
	}
}
