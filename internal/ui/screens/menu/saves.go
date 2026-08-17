package menu

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// stagingDirName is the single directory name defaultBundleLister skips —
// internal/engine/save's ".staging" in-flight bundle directory. It is a
// small, deliberate mirror of that private constant rather than a full
// reimplementation of engine.save's layout; the production composition
// root wires WithBundleLister to internal/engine/save's own listing
// anyway, so this default only has to be safe for a flat test root. See
// doc.go's "save-root enumeration seam" note and ASM-523.
const stagingDirName = ".staging"

// Refresh re-lists the save browser from the configured save root (MEN-1):
// it enumerates bundle directories (via the injected BundleLister, or
// defaultBundleLister) and reads each bundle's header through
// int.serializer's serialize.ReadHeader — which itself enforces the
// format-version check (serialize.CheckFormatVersion) — deriving Name /
// CreatedAtTick (timestamp) / GameMonth (sim-date) / Summary from the
// Header and the directory name. It never opens a shard file.
//
// A single bundle whose header cannot be read (corrupt header.json,
// incompatible format version) is skipped and recorded in SaveListErrors
// — one bad slot must not hide every other save (mirrors engine.save.List's
// posture). Enumeration failure (I/O on the root itself) marks the list
// "unavailable" (SF-7/MEN-7) rather than fabricating an empty list.
func (s *Screen) Refresh() error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Refresh"}); err != nil {
		return err
	}
	s.mu.Lock()
	root := s.saveRoot
	lister := s.bundleLister
	s.mu.Unlock()

	if root == "" {
		// No save root configured: the list is "unavailable" (MEN-7), not
		// empty and — critically — never "walk the current directory".
		s.mu.Lock()
		s.listFailed = ""
		s.saveEntries = nil
		s.saveListErrs = nil
		s.mu.Unlock()
		return nil
	}

	if lister == nil {
		lister = defaultBundleLister
	}
	dirs, err := lister(root)
	if err != nil && !os.IsNotExist(err) {
		wrapped := errs.Wrap(ErrSaveListFailed, s.correlationID, err, map[string]any{})
		s.mu.Lock()
		// SEC-224: the "unavailable" reason rendered for the player is the
		// registry message (sanitized), never err.Error() — which would leak
		// the save root's absolute path (GR#1).
		s.listFailed = wrapped.Msg
		s.saveEntries = nil
		s.saveListErrs = nil
		s.mu.Unlock()
		return wrapped
	}
	if os.IsNotExist(err) {
		// No save root yet = zero saves, not an error.
		dirs = nil
	}

	var entries []SaveEntry
	var readErrs []error
	for _, dir := range dirs {
		h, err := serialize.ReadHeader(dir)
		if err != nil {
			// SEC-218 (GR#7/GR#1): wrap the per-entry read failure in a
			// registry-sourced error with the screen's correlation ID,
			// mirroring the SEC-212 profile-read wrapping. serialize's raw
			// error embeds the bundle's absolute filesystem path (via %q on
			// dir) and no registry code; the raw cause is preserved on the
			// error via errors.Unwrap, but the rendered message carries only
			// the slot's base name — never the absolute path.
			readErrs = append(readErrs, errs.Wrap(ErrSaveListEntryReadFailed, s.correlationID, err, map[string]any{
				"slot": filepath.Base(dir),
			}))
			continue
		}
		entries = append(entries, saveEntryFromHeader(dir, h))
	}

	s.mu.Lock()
	s.listFailed = ""
	s.saveEntries = sortSaveEntries(entries)
	s.saveListErrs = readErrs
	s.mu.Unlock()
	return nil
}

// SaveEntries returns the last Refresh()'s save-slot list, sorted by name
// ascending (deterministic render order, GR#21). The returned slice is a
// copy — the caller may not mutate the screen's state through it.
func (s *Screen) SaveEntries() []SaveEntry {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SaveEntries"}); err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SaveEntries"}); err != nil {
		return nil
	}
	out := make([]SaveEntry, len(s.saveEntries))
	copy(out, s.saveEntries)
	return out
}

// SaveListErrors returns the per-entry header-read failures from the last
// Refresh() (bundles whose header.json could not be read). Empty when the
// last refresh read every bundle cleanly.
func (s *Screen) SaveListErrors() []error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SaveListErrors"}); err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SaveListErrors"}); err != nil {
		return nil
	}
	out := make([]error, len(s.saveListErrs))
	copy(out, s.saveListErrs)
	return out
}

// SaveListUnavailable returns a non-empty reason when the save browser
// cannot be shown (no save root configured, or the last enumeration
// failed), else "" (SF-7/MEN-7's "unavailable, not blank" state).
func (s *Screen) SaveListUnavailable() string {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SaveListUnavailable"}); err != nil {
		return "unavailable"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "SaveListUnavailable"}); err != nil {
		return "unavailable"
	}
	if s.saveRoot == "" {
		return "save root not configured"
	}
	return s.listFailed
}

// Load validates the bundle at path via int.serializer's
// serialize.ValidateBundle and, on success, issues a load command via send
// (MEN-1). On a corrupt/incompatible save it returns int.serializer's own
// error VERBATIM (MEN-6) — serialize.CheckFormatVersion's major-mismatch
// error for an incompatible format version, or ValidateBundle's corruption
// error for a shard mismatch — never re-derived, never genericised into
// "load failed".
func (s *Screen) Load(path string, send SendCommandFunc) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Load"}); err != nil {
		return err
	}
	if _, err := serialize.ValidateBundle(path); err != nil {
		return err
	}
	return send(opCommand(s.correlationID, opLoadSave, map[string]string{"path": path}))
}

// Save issues a save command for the current game under the given slot
// name (MEN-1). It only triggers the save — the save-bundle format and
// write mechanics are int.serializer/feat.saveux's (see ui.screen.menu.md's
// "Out of scope"); this screen does not write the bundle itself.
func (s *Screen) Save(name string, send SendCommandFunc) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Save"}); err != nil {
		return err
	}
	return send(opCommand(s.correlationID, opSaveGame, map[string]string{"name": name}))
}

// Delete issues a delete command for the save slot at path (MEN-1).
func (s *Screen) Delete(path string, send SendCommandFunc) error {
	if err := s.checkNotCopied(errs.NewCorrelationID(), map[string]any{"method": "Delete"}); err != nil {
		return err
	}
	return send(opCommand(s.correlationID, opDeleteSave, map[string]string{"path": path}))
}

// saveEntryFromHeader builds a SaveEntry from a bundle directory path and
// its already-read serialize.Header — the one place save-slot display
// metadata is derived from the Header (SF-2 traceability, GR#3).
func saveEntryFromHeader(dir string, h serialize.Header) SaveEntry {
	return SaveEntry{
		Name:          filepath.Base(dir),
		Path:          dir,
		CreatedAtTick: h.CreatedAtTick,
		GameMonth:     h.GameMonth,
		WorldSeed:     h.WorldSeed,
		AppVersion:    h.AppVersion,
		DebugTouched:  h.DebugTouched(),
		Summary:       summaryOf(h),
	}
}

// summaryOf derives the compact save summary from Header fields (world
// seed, plus a debug marker). It is derived from the Header, not invented,
// and never reads the wall clock (SF-8). Timestamp (CreatedAtTick) and
// sim-date (GameMonth) are rendered as their own columns by RenderSaves,
// so the summary line carries what those columns do not (the seed and the
// sticky debug flag).
func summaryOf(h serialize.Header) string {
	out := fmt.Sprintf("seed %d", h.WorldSeed)
	if h.DebugTouched() {
		out += " · debug"
	}
	return out
}

// defaultBundleLister walks root for directories that contain a
// header.json (via serialize.HeaderPath), skipping any directory named
// stagingDirName. It returns paths in sorted order (deterministic). This
// is the screen's own convenience default — the production composition
// root should wire WithBundleLister to internal/engine/save's listing
// (see doc.go's "save-root enumeration seam" note).
func defaultBundleLister(root string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == stagingDirName {
			return fs.SkipDir
		}
		if path == root {
			return nil
		}
		if _, err := os.Stat(serialize.HeaderPath(path)); err == nil {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	return dirs, nil
}
