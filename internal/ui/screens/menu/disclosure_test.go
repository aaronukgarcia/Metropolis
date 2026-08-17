package menu

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// assertSanitizedDisplay asserts err is a registry-sourced *errs.E whose
// user-visible Display() one-liner carries the registry code and the
// correlation ID (GR#1) but none of the given sensitive tokens (the
// caller's absolute filesystem path, or the raw cause text). It returns the
// *errs.E so the caller can also assert against the wrapped cause.
func assertSanitizedDisplay(t *testing.T, err error, wantCode string, sensitive ...string) *errs.E {
	t.Helper()
	e, ok := err.(*errs.E)
	if !ok {
		t.Fatalf("error = %T (%v), want *errs.E with code %s (registry-sourced, GR#7)", err, err, wantCode)
	}
	if e.Code != wantCode {
		t.Fatalf("error code = %s, want %s", e.Code, wantCode)
	}
	if e.CorrelationID == "" {
		t.Fatalf("correlation ID is empty (GR#1)")
	}
	got := e.Display()
	for _, tok := range sensitive {
		if tok != "" && strings.Contains(got, tok) {
			t.Errorf("Display() leaks sensitive token %q: %q", tok, got)
		}
	}
	if !strings.Contains(got, e.Code) {
		t.Errorf("Display() %q does not carry the registry code %s", got, e.Code)
	}
	if !strings.Contains(got, e.CorrelationID) {
		t.Errorf("Display() %q does not carry the correlation ID %q", got, e.CorrelationID)
	}
	return e
}

// TestDisplayDoesNotLeakPathOrCause is SEC-224's regression check: for the
// three registry codes whose templates previously rendered {path}/{cause}
// (MET-U604 enumeration, MET-U605 write, MET-U608 read), the user-visible
// Display() one-liner must carry neither the caller's absolute filesystem
// path nor the raw cause (an *os.PathError / lister error string) — only
// the sanitized registry message plus code + correlation ID — while the raw
// cause stays on the wrapped error for errors.Is/As/Unwrap. Mirrors
// TestRefresh_CorruptHeaderErrorIsRegistrySourced (SEC-218/U609).
func TestDisplayDoesNotLeakPathOrCause(t *testing.T) {
	// U608 — profile READ: the caller's absolute path and the raw
	// *os.PathError ("open <path>: ...") must not reach Display().
	t.Run("U608_read_profile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nonexistent", "keymap.json")
		s := New("corr-u608")
		if _, err := s.LoadLayoutProfile(path); err == nil {
			t.Fatal("LoadLayoutProfile(nonexistent) returned nil error")
		} else {
			e := assertSanitizedDisplay(t, err, ErrProfileReadFailed, path)
			if cause := errors.Unwrap(e); cause != nil && strings.Contains(e.Display(), cause.Error()) {
				t.Errorf("Display() leaks the raw cause: %q (cause %q)", e.Display(), cause.Error())
			}
		}
	})

	// U608 — keymap profile READ (LoadKeymapFile): the keymap half of the
	// same sanitization contract, asserted per wrap site so a regression in
	// keymaps.go's wrap is caught even if layouts.go stays clean.
	t.Run("U608_read_keymap_profile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nonexistent", "keymap.json")
		s := New("corr-u608-km")
		g := newTestGrammar("corr-u608-km-g")
		if _, err := s.LoadKeymapFile(path, g); err == nil {
			t.Fatal("LoadKeymapFile(nonexistent) returned nil error")
		} else {
			e := assertSanitizedDisplay(t, err, ErrProfileReadFailed, path)
			if cause := errors.Unwrap(e); cause != nil && strings.Contains(e.Display(), cause.Error()) {
				t.Errorf("Display() leaks the raw cause: %q (cause %q)", e.Display(), cause.Error())
			}
		}
	})

	// U605 — profile WRITE: same sanitization on the write path.
	t.Run("U605_write_profile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nonexistent", "layout.json")
		s := New("corr-u605")
		err := s.SaveLayoutProfile(path, &LayoutProfile{Name: "x", Data: []byte(`{}`)})
		if err == nil {
			t.Fatal("SaveLayoutProfile(nonexistent dir) returned nil error")
		}
		e := assertSanitizedDisplay(t, err, ErrProfileWriteFailed, path)
		if cause := errors.Unwrap(e); cause != nil && strings.Contains(e.Display(), cause.Error()) {
			t.Errorf("Display() leaks the raw cause: %q (cause %q)", e.Display(), cause.Error())
		}
	})

	// U605 — keymap profile WRITE (SaveKeymapFile): the keymap half of the
	// write sanitization contract, asserted per wrap site. A selected keymap
	// is required first so the write path (not the no-selection path) is the
	// one exercised.
	t.Run("U605_write_keymap_profile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nonexistent", "keymap-out.json")
		s := New("corr-u605-km")
		g := newTestGrammar("corr-u605-km-g")
		km, err := keys.ParseKeymap([]byte(`{"version":1,"bindings":{}}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.SelectKeymap(km, g); err != nil {
			t.Fatalf("SelectKeymap: %v", err)
		}
		err = s.SaveKeymapFile(path)
		if err == nil {
			t.Fatal("SaveKeymapFile(nonexistent dir) returned nil error")
		}
		e := assertSanitizedDisplay(t, err, ErrProfileWriteFailed, path)
		if cause := errors.Unwrap(e); cause != nil && strings.Contains(e.Display(), cause.Error()) {
			t.Errorf("Display() leaks the raw cause: %q (cause %q)", e.Display(), cause.Error())
		}
	})

	// U604 — enumeration failure: the lister's raw cause (which embeds the
	// save-root path) must not reach Display(), and SaveListUnavailable must
	// hold a sanitized reason rather than err.Error().
	t.Run("U604_enumeration_failure", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "secret-root")
		s := New("corr-u604", WithSaveRoot(root), WithBundleLister(func(string) ([]string, error) {
			return nil, errors.New("open " + root + ": permission denied")
		}))
		err := s.Refresh()
		if err == nil {
			t.Fatal("Refresh() returned nil for a failing lister")
		}
		e := assertSanitizedDisplay(t, err, ErrSaveListFailed, root)
		if cause := errors.Unwrap(e); cause != nil && strings.Contains(e.Display(), cause.Error()) {
			t.Errorf("Display() leaks the raw cause: %q (cause %q)", e.Display(), cause.Error())
		}
		if got := s.SaveListUnavailable(); got == "" {
			t.Fatalf("SaveListUnavailable() = %q after enumeration failure, want non-empty", got)
		} else if strings.Contains(got, root) || strings.Contains(got, "permission denied") {
			t.Errorf("SaveListUnavailable() leaks the raw cause/path: %q", got)
		}
	})
}
