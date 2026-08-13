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
