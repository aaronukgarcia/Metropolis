package core

import "testing"

func TestProbe_WindowsTerminal(t *testing.T) {
	src := MapCapabilitySource{"WT_SESSION": "some-guid"}
	got := Probe(src)
	if got.Name != WindowsTerminalProfile.Name || !got.TrueColor || !got.Mouse {
		t.Fatalf("Probe(WT_SESSION set) = %+v, want %+v", got, WindowsTerminalProfile)
	}
}

func TestProbe_TruecolorViaColorterm(t *testing.T) {
	src := MapCapabilitySource{"COLORTERM": "truecolor"}
	got := Probe(src)
	if got.Name != WindowsTerminalProfile.Name {
		t.Fatalf("Probe(COLORTERM=truecolor) = %+v, want windows-terminal profile", got)
	}
}

func TestProbe_Conhost(t *testing.T) {
	src := MapCapabilitySource{}
	got := Probe(src)
	if got.Name != ConhostProfile.Name || got.TrueColor || got.Mouse || got.Colors != 16 {
		t.Fatalf("Probe(no capability env) = %+v, want %+v", got, ConhostProfile)
	}
}

func TestProbe_ColortermJunkFallsBackToConhost(t *testing.T) {
	src := MapCapabilitySource{"COLORTERM": "yes"}
	got := Probe(src)
	if got.Name != ConhostProfile.Name {
		t.Fatalf("Probe(COLORTERM=yes) = %+v, want conhost (safe degraded default)", got)
	}
}
