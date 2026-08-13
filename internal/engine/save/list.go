package save

import (
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// SaveSummary is the display metadata List returns for one discoverable
// bundle (US-6, AC-8) — everything a future F10 menu needs without
// opening a single shard file.
type SaveSummary struct {
	Path              string
	DisplayName       string
	SaveKind          SaveKind
	CreatedAtTick     int64
	GameMonth         int64
	AppVersion        string
	DebugTouched      bool
	MilestoneTierNum  int
	MilestoneTierName string
}

// List enumerates every discoverable bundle (manual, autosave,
// milestone) under root, returning display metadata sourced entirely
// from each bundle's header.json (int.serializer's Header) and
// save-meta.json (this package's Meta) — it never opens a shard file
// (AC-8). Bundles under root/.staging (in-flight, not yet promoted —
// AC-9) are never visible here by construction: List only walks the
// three discoverable subdirectories, not root itself.
//
// A bundle whose header/meta cannot be read or decoded (e.g. corrupted
// header.json) is skipped rather than failing the whole call — one bad
// entry must not hide every other save from the player; ListErrors
// (if non-nil) carries per-entry read failures for a caller that wants
// to surface them (e.g. a debug diagnostic), while the primary
// SaveSummary slice always reflects whatever DID read cleanly.
func List(root string) ([]SaveSummary, []error, error) {
	var out []SaveSummary
	var readErrs []error

	kinds := []string{manualSubdir, autosaveSubdir, milestoneSubdir}
	for _, sub := range kinds {
		dir := filepath.Join(root, sub)
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, errs.Wrap(ErrListFailed, "", err, map[string]any{"root": root, "dir": dir, "cause": err.Error()})
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			// BUG-158: a stray ".replaced-stage-<random>" sibling left
			// behind by a writeBundle save-over that crashed between
			// displacing the prior bundle and promoting the new one is a
			// fully-valid bundle on disk (header.json + save-meta.json
			// intact) but is NEVER a real, player-visible save slot —
			// filter it out here rather than letting readSummary treat it
			// as an independent phantom entry.
			if isReplacedSiblingName(e.Name()) {
				continue
			}
			bundleDir := filepath.Join(dir, e.Name())
			summary, err := readSummary(bundleDir)
			if err != nil {
				readErrs = append(readErrs, err)
				continue
			}
			out = append(out, summary)
		}
	}
	return out, readErrs, nil
}

// readSummary reads bundleDir's header.json and save-meta.json ONLY —
// no shard file is ever opened (AC-8's own check deletes every shard
// after writing a bundle and asserts List still succeeds; readSummary
// is what makes that true structurally, not just by convention).
func readSummary(bundleDir string) (SaveSummary, error) {
	header, err := serialize.ReadHeader(bundleDir)
	if err != nil {
		return SaveSummary{}, err
	}
	meta, err := ReadMeta(bundleDir)
	if err != nil {
		return SaveSummary{}, err
	}
	return SaveSummary{
		Path:              bundleDir,
		DisplayName:       meta.DisplayName,
		SaveKind:          meta.SaveKind,
		CreatedAtTick:     header.CreatedAtTick,
		GameMonth:         header.GameMonth,
		AppVersion:        header.AppVersion,
		DebugTouched:      header.DebugTouched(),
		MilestoneTierNum:  meta.MilestoneTierNumber,
		MilestoneTierName: meta.MilestoneTierName,
	}, nil
}
