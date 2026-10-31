package debug

import "testing"

// Destructive round r2 (FEAT-2326609837, GR#23): DebugOn is a new guarded
// exported method with its own mu.Lock site. A struct copy of a Screen
// whose flag returns true must report false (Collect's degrade posture),
// and the original must be unaffected.
func TestDebugOn_StructCopy_ReportsFalse(t *testing.T) {
	orig := NewScreen(nil, "corr-setup", WithDebugFlag(func() bool { return true }))
	if !orig.DebugOn() {
		t.Fatal("original.DebugOn() = false with a true flag")
	}
	cp := screenByteCopy(orig)
	if cp.DebugOn() {
		t.Fatal("copy.DebugOn() = true: the copy guard does not cover DebugOn")
	}
	if !orig.DebugOn() {
		t.Fatal("original.DebugOn() = false after the copy call")
	}
}
