package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lifecycle_test.go -- FEAT-9995385597 inc1 section A: round trip, unknown
// field tolerance, validation and golden fixtures for the three lifecycle
// notifications (AC-P1, AC-P2, AC-T5). The TS side decodes the same fixture
// files in webconsole/test/protocol-lifecycle-wire.test.mjs (parity).

func readLifecycleGolden(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "lifecycle", name))
	if err != nil {
		t.Fatalf("read lifecycle golden %s: %v", name, err)
	}
	return b
}

func TestLifecycle_Golden_ByteIdentical(t *testing.T) {
	cases := []struct {
		file string
		v    any
	}{
		{"waking.golden.json", WakingParams{Phase: WakeReplaying, Pct: 62}},
		{"waking_failed.golden.json", WakingParams{Phase: WakeFailed, Pct: 40, Code: "MET-P030", Ctx: map[string]any{"city": "default"}}},
		{"draining.golden.json", DrainingParams{ReconnectAfterMs: 4000, Reason: DrainDeploy}},
		{"hibernating.golden.json", HibernatingParams{Reason: HibernateIdle}},
	}
	for _, c := range cases {
		got, err := json.Marshal(c.v)
		if err != nil {
			t.Fatalf("%s: marshal: %v", c.file, err)
		}
		if want := string(readLifecycleGolden(t, c.file)); string(got) != want {
			t.Errorf("%s diverged from golden:\n got:  %s\n want: %s", c.file, got, want)
		}
	}
}

func TestLifecycle_RoundTrip_FromGolden(t *testing.T) {
	w, err := DecodeWakingParams(readLifecycleGolden(t, "waking_failed.golden.json"))
	if err != nil || w.Phase != WakeFailed || w.Code != "MET-P030" || w.Ctx["city"] != "default" {
		t.Fatalf("waking decode = %+v, %v", w, err)
	}
	d, err := DecodeDrainingParams(readLifecycleGolden(t, "draining.golden.json"))
	if err != nil || d.ReconnectAfterMs != 4000 || d.Reason != DrainDeploy {
		t.Fatalf("draining decode = %+v, %v", d, err)
	}
	h, err := DecodeHibernatingParams(readLifecycleGolden(t, "hibernating.golden.json"))
	if err != nil || h.Reason != HibernateIdle {
		t.Fatalf("hibernating decode = %+v, %v", h, err)
	}
}

func TestLifecycle_UnknownFieldsTolerated(t *testing.T) {
	if _, err := DecodeWakingParams([]byte(`{"phase":"queued","pct":0,"futureField":{"x":1}}`)); err != nil {
		t.Errorf("waking with unknown field: %v", err)
	}
	if _, err := DecodeDrainingParams([]byte(`{"reconnectAfterMs":0,"reason":"limit","futureField":1}`)); err != nil {
		t.Errorf("draining with unknown field: %v", err)
	}
	if _, err := DecodeHibernatingParams([]byte(`{"reason":"operator","futureField":"x"}`)); err != nil {
		t.Errorf("hibernating with unknown field: %v", err)
	}
}

func TestLifecycle_Validation_RejectsBadValues(t *testing.T) {
	bad := []struct {
		name string
		err  error
	}{
		{"waking unknown phase", WakingParams{Phase: "dreaming", Pct: 10}.Validate()},
		{"waking pct negative", WakingParams{Phase: WakeQueued, Pct: -1}.Validate()},
		{"waking pct over 100", WakingParams{Phase: WakeFetching, Pct: 101}.Validate()},
		{"waking ready below 100", WakingParams{Phase: WakeReady, Pct: 99}.Validate()},
		{"draining negative", DrainingParams{ReconnectAfterMs: -1, Reason: DrainDeploy}.Validate()},
		{"draining hidden is not a server reason", DrainingParams{ReconnectAfterMs: 0, Reason: "hidden"}.Validate()},
		{"hibernating hidden is not a server reason", HibernatingParams{Reason: "hidden"}.Validate()},
		{"hibernating empty", HibernatingParams{}.Validate()},
	}
	for _, c := range bad {
		if c.err == nil {
			t.Errorf("%s: want error, got nil", c.name)
		}
	}
	for _, r := range []DrainReason{DrainDeploy, DrainShutdown, DrainLimit, DrainBudget} {
		if err := (DrainingParams{Reason: r}).Validate(); err != nil {
			t.Errorf("drain reason %s: %v", r, err)
		}
	}
	if _, err := DecodeWakingParams([]byte(`{"phase":"queued","pct":"x"}`)); err == nil {
		t.Error("waking with string pct: want decode error")
	}
}

func TestLifecycle_MethodCapabilityMapping(t *testing.T) {
	for method, want := range map[string]Capability{
		MethodServerWaking:      CapLifecycleWaking,
		MethodServerDraining:    CapLifecycleDraining,
		MethodServerHibernating: CapLifecycleHibernating,
	} {
		got, ok := LifecycleMethodCapability(method)
		if !ok || got != want {
			t.Errorf("%s -> %q,%v want %q", method, got, ok, want)
		}
		if !strings.HasPrefix(string(got), "lifecycle.") {
			t.Errorf("capability %q should be lifecycle.*", got)
		}
	}
	if _, ok := LifecycleMethodCapability("result"); ok {
		t.Error("result is not a lifecycle method")
	}
}

func TestLifecycle_WireVersionUnchanged(t *testing.T) {
	// AC-P1: no version bump; the flags are the opt-in.
	if CurrentWireVersion.Major != 1 || CurrentWireVersion.Minor != 0 {
		t.Fatalf("CurrentWireVersion = %v, want 1.0 (AC-P1: capabilities, no bump)", CurrentWireVersion)
	}
}
