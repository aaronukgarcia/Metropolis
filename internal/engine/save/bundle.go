package save

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Directory layout under a save root (AC-9's staging design, ASM #3):
//
//	<root>/
//	  manual/<name>/          -- one bundle dir per SaveManual call
//	  autosave/<seq>/         -- zero-padded sequence, rolling 10-deep
//	  milestone/<NN-slug>/    -- one bundle dir per Tier crossed
//	  .staging/<random>/      -- write target before promotion; List/Load
//	                             never look here
const (
	manualSubdir    = "manual"
	autosaveSubdir  = "autosave"
	milestoneSubdir = "milestone"
	stagingSubdir   = ".staging"

	// metaFileName is this package's own sidecar file name (AC-6),
	// distinct from int.serializer's header.json.
	metaFileName = "save-meta.json"
)

func manualDir(root, name string) string {
	return filepath.Join(root, manualSubdir, name)
}

func autosaveDir(root string, seq int) string {
	return filepath.Join(root, autosaveSubdir, fmt.Sprintf("%06d", seq))
}

var tierSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// tierSlug lowercases and hyphenates a tier name for use as a directory
// component, e.g. "Small Town" -> "small-town".
func tierSlug(name string) string {
	s := tierSlugRe.ReplaceAllString(strings.ToLower(name), "-")
	return strings.Trim(s, "-")
}

func milestoneDir(root string, tier Tier) string {
	return filepath.Join(root, milestoneSubdir, fmt.Sprintf("%02d-%s", tier.Number, tierSlug(tier.Name)))
}

func stagingRoot(root string) string {
	return filepath.Join(root, stagingSubdir)
}

// replacedSuffixRe matches the ".replaced-stage-<random>" suffix
// writeBundle appends to a prior promoted bundle's directory name while
// displacing it during a save-over (see writeBundle's displacedDir,
// built as finalDir + ".replaced-" + filepath.Base(stagingDir), and
// newStagingDir's "stage-*" MkdirTemp pattern above). A directory whose
// name matches this pattern is a crash-stranded displaced sibling — not
// a real, player-visible save slot under any of manual/autosave/
// milestone's real naming conventions — never anything else (BUG-158:
// List, list.go, filters these out; writeBundle reaps them on the next
// save to the same slot). The pattern is anchored to the exact suffix
// writeBundle produces (GR#3 — one definition, shared by both write and
// read sides) rather than a loose "contains .replaced-" check, so a
// legitimate manual-save name a player chose can never collide with it.
var replacedSuffixRe = regexp.MustCompile(`\.replaced-stage-[0-9A-Za-z]+$`)

// replacedMarker is the literal marker substring replacedSuffixRe is
// built around, shared here (GR#3 — one definition) so SaveManual's
// entry-point validation (BUG-159) can reject it as a plain substring
// check rather than duplicating/approximating the regex. A substring
// check is deliberately broader than an exact suffix match: it also
// catches a name where the marker text appears mid-string (e.g. as a
// prefix of a longer player-chosen name), which the anchored regex
// alone would let through today but could still collide with a FUTURE
// internal use of the same marker text — belt and braces against the
// exact class of bug BUG-159 found.
const replacedMarker = ".replaced-stage-"

// unsafeSaveNameChars are the path-separator characters explicitly
// rejected by isUnsafeSaveName, regardless of build GOOS: '/' and '\'
// are BOTH meaningful path separators to filepath on Windows (this
// project's primary target platform per CLAUDE.md), and ':' additionally
// introduces a Windows drive letter (e.g. "C:\evil") or NTFS alternate
// data stream. Checking all three explicitly, rather than relying solely
// on filepath.Separator/filepath.IsAbs (which are single-OS-aware and
// would silently miss a "/"-separated traversal on a Windows build, or a
// "\"-separated one on a POSIX build), keeps the check correct
// regardless of which OS actually built this binary.
const unsafeSaveNameChars = `/\:`

// maxSaveNameLen is the longest manual save name isUnsafeSaveName will
// accept (BUG-161). This project's planning docs do not define a
// save-name length convention of their own, so this uses 255 --
// the common filesystem filename-component limit (NTFS's MAX_PATH
// component limit, and ext4/most POSIX filesystems' NAME_MAX), which
// is already generous for a player-typed save name while still giving
// a concrete, documented bound to reject against BEFORE any filesystem
// call, rather than letting an arbitrarily long name reach
// writeBundleLocked and fail late with a raw OS error (e.g. Windows'
// "filename... syntax is incorrect" for a too-long component).
const maxSaveNameLen = 255

// isUnsafeSaveName reports whether name (a caller-supplied manual save
// name, not yet turned into a path) is unsafe to join, unmodified, into
// a save bundle path via filepath.Join (BUG-160), or is otherwise the
// kind of degenerate input that should never reach writeBundleLocked's
// real filesystem I/O in the first place (BUG-161). Rejects: an empty
// name, or a name that is empty (or consists ENTIRELY of whitespace)
// after strings.TrimSpace; a bare "." or ".." component outright
// (filepath.Base leaves both of these unchanged, so a naive
// "name != filepath.Base(name)" check alone would NOT catch them); a
// name containing any path separator or drive-letter colon anywhere in
// it -- which also inherently rejects a leading/trailing separator, an
// embedded ".." traversal component (e.g. "../../evil"), an absolute
// path, and a Windows drive-letter or UNC path; a name containing a NUL
// byte or any other C0 control character (bytes 0x00-0x1F -- tab,
// newline, BEL, ESC, backspace, etc; os/most filesystems either treat
// these as invalid path characters outright or accept them but produce
// confusing, un-typeable save slots); and a name longer than
// maxSaveNameLen. SaveManual rejects any such name at its entry point
// -- before it is ever joined into manualDir's filepath.Join call or
// reaches any filesystem call -- so a player-chosen name can never
// escape the configured save root, and a degenerate one can never
// trigger real staged-bundle I/O only to fail late with a raw,
// untyped OS error (BUG-161).
func isUnsafeSaveName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return true
	}
	if strings.TrimSpace(name) == "" {
		return true
	}
	if len(name) > maxSaveNameLen {
		return true
	}
	if strings.ContainsAny(name, unsafeSaveNameChars) {
		return true
	}
	for _, r := range name {
		// Covers NUL (the original BUG-160 check) and every other C0
		// control character (0x00-0x1F) in one pass -- unicode.IsControl
		// would also reject C1 controls and other Unicode-defined control
		// runes, but an explicit byte-range check on the rune value keeps
		// this in the same explicit, auditable style as
		// unsafeSaveNameChars above and matches exactly what BUG-161's
		// report called out (C0 controls specifically).
		if r >= 0x00 && r <= 0x1F {
			return true
		}
	}
	return false
}

// isReservedSaveName reports whether name (a caller-supplied manual
// save name, not yet turned into a path) contains the internal
// ".replaced-stage-" crash-recovery marker bundle.go's writeBundle uses
// to tag a displaced sibling during a save-over (BUG-158). SaveManual
// rejects any such name at its entry point (BUG-159) — before it is
// ever joined into a path or written to disk — so the marker pattern
// can never collide with a real, player-chosen save name the way
// isReplacedSiblingName/reapDisplacedSiblings' pattern-based detection
// implicitly assumed it never would.
func isReservedSaveName(name string) bool {
	return strings.Contains(name, replacedMarker)
}

// isReplacedSiblingName reports whether name (a single path component,
// e.g. from os.ReadDir) is a stray ".replaced-stage-<random>" sibling
// directory left behind by a writeBundle save-over that crashed between
// displacing the prior bundle and promoting the new one (BUG-158) —
// never a real save slot.
func isReplacedSiblingName(name string) bool {
	return replacedSuffixRe.MatchString(name)
}

// replacedSiblingGlob returns the glob pattern matching any stray
// ".replaced-stage-*" sibling of finalDir, for writeBundle's reap sweep
// to search finalDir's own parent directory (BUG-158).
func replacedSiblingGlob(finalDir string) string {
	return finalDir + ".replaced-stage-*"
}

// reapDisplacedSiblings removes any stray ".replaced-stage-<random>"
// sibling(s) of finalDir left behind by a prior writeBundle save-over
// that crashed between the displace and promote renames (BUG-158) — a
// fully-valid, orphaned bundle that would otherwise accumulate disk
// usage forever and (before List's own filtering) show up as a phantom
// duplicate save entry. Best-effort: a glob or per-entry RemoveAll
// failure is swallowed rather than failing the caller's real save — a
// leftover displaced sibling is forensic clutter, not a correctness
// hazard, and must never block a genuine SaveManual/Autosave/Milestone
// call.
func reapDisplacedSiblings(finalDir string) {
	matches, err := filepath.Glob(replacedSiblingGlob(finalDir))
	if err != nil {
		return
	}
	for _, m := range matches {
		_ = os.RemoveAll(m)
	}
}

// newStagingDir creates and returns a fresh, uniquely-named directory
// under root/.staging. The name is a random suffix (os.MkdirTemp) — it
// is never part of a promoted bundle's final path or content, so its
// non-determinism has no bearing on AC-14's byte-determinism guarantee
// (checked against the final, promoted bundle only).
func newStagingDir(root, correlationID string) (string, error) {
	base := stagingRoot(root)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", errs.Wrap(ErrStagingCreateFailed, correlationID, err, map[string]any{"root": root, "cause": err.Error()})
	}
	dir, err := os.MkdirTemp(base, "stage-*")
	if err != nil {
		return "", errs.Wrap(ErrStagingCreateFailed, correlationID, err, map[string]any{"root": root, "cause": err.Error()})
	}
	return dir, nil
}

// WriteMeta marshals m as indented JSON to dir/save-meta.json.
func WriteMeta(dir string, m Meta) error {
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(filepath.Join(dir, metaFileName), encoded, 0o644)
}

// ReadMeta reads and decodes dir/save-meta.json.
func ReadMeta(dir string) (Meta, error) {
	raw, err := os.ReadFile(filepath.Join(dir, metaFileName))
	if err != nil {
		return Meta{}, err
	}
	var m Meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return Meta{}, err
	}
	return m, nil
}

// listAutosaveSeqs returns the sequence numbers of every promoted
// autosave bundle under root, ascending (oldest first). A directory
// entry whose name does not parse as a plain non-negative integer is
// skipped rather than aborting the whole listing — a foreign/hand-
// placed entry under autosave/ must not break retention accounting.
func listAutosaveSeqs(root string) ([]int, error) {
	dir := filepath.Join(root, autosaveSubdir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var seqs []int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n, convErr := strconv.Atoi(e.Name())
		if convErr != nil || n < 0 {
			continue
		}
		seqs = append(seqs, n)
	}
	sort.Ints(seqs)
	return seqs, nil
}

// nextAutosaveSeq derives the next autosave sequence number from what is
// already on disk (a runtime query, not a hand-maintained counter —
// GR#15) so a *Manager restarting mid-session still rotates correctly.
func nextAutosaveSeq(root string) (int, error) {
	seqs, err := listAutosaveSeqs(root)
	if err != nil {
		return 0, err
	}
	if len(seqs) == 0 {
		return 0, nil
	}
	return seqs[len(seqs)-1] + 1, nil
}
