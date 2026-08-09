package core

import "strings"

// Profile is the terminal capability profile selected at startup
// (UI-SPEC §1: "capability probe selecting the right colour/mouse
// profile automatically").
type Profile struct {
	// Name identifies the profile for logs/diagnostics: "windows-terminal"
	// or "conhost".
	Name string
	// TrueColor is true when 24-bit colour is safe to emit.
	TrueColor bool
	// Mouse is true when mouse events should be enabled
	// (Screen.EnableMouse).
	Mouse bool
	// Colors is the effective palette size the render layer should
	// quantise to when TrueColor is false (16 for conhost's degraded
	// profile).
	Colors int
}

// WindowsTerminalProfile is the capability profile for Windows Terminal:
// truecolor, mouse, full Unicode (UI-SPEC §1).
var WindowsTerminalProfile = Profile{
	Name:      "windows-terminal",
	TrueColor: true,
	Mouse:     true,
	Colors:    1 << 24,
}

// ConhostProfile is the degraded capability profile for legacy conhost:
// 16-colour palette map, no mouse (UI-SPEC §1).
var ConhostProfile = Profile{
	Name:      "conhost",
	TrueColor: false,
	Mouse:     false,
	Colors:    16,
}

// CapabilitySource is the injectable terminal-environment source Probe
// reads from. The real implementation (OSEnv) reads os.Getenv; tests
// inject a fake map-backed source instead of mutating process
// environment variables, per AC-5 ("a mocked/injected
// terminal-capability source").
type CapabilitySource interface {
	Getenv(key string) string
}

// MapCapabilitySource is a CapabilitySource backed by a plain map, for
// tests.
type MapCapabilitySource map[string]string

// Getenv implements CapabilitySource.
func (m MapCapabilitySource) Getenv(key string) string { return m[key] }

// osEnvSource is the production CapabilitySource, reading the real
// process environment.
type osEnvSource struct{ getenv func(string) string }

// Getenv implements CapabilitySource.
func (o osEnvSource) Getenv(key string) string { return o.getenv(key) }

// NewOSCapabilitySource returns the production CapabilitySource, backed
// by getenv (pass os.Getenv at the call site — this package does not
// import "os" itself, keeping this file's only import stdlib-neutral
// besides "strings").
func NewOSCapabilitySource(getenv func(string) string) CapabilitySource {
	return osEnvSource{getenv: getenv}
}

// Probe selects a Profile from src. Detection order:
//
//  1. WT_SESSION set (Windows Terminal always sets this, regardless of
//     COLORTERM) -> WindowsTerminalProfile.
//  2. COLORTERM containing "truecolor" or "24bit" (the conventional
//     signal any truecolor-capable terminal sets) -> WindowsTerminalProfile.
//  3. Otherwise -> ConhostProfile, the safe degraded default: a terminal
//     that doesn't announce truecolor support gets treated as legacy
//     conhost rather than risking garbled output from colours it can't
//     render (UI-SPEC §1: degrade gracefully, never render garbage).
func Probe(src CapabilitySource) Profile {
	if src.Getenv("WT_SESSION") != "" {
		return WindowsTerminalProfile
	}
	ct := strings.ToLower(src.Getenv("COLORTERM"))
	if strings.Contains(ct, "truecolor") || strings.Contains(ct, "24bit") {
		return WindowsTerminalProfile
	}
	return ConhostProfile
}
