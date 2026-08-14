// Command metctl is the operator/CLI counterpart to metropolis: headless
// control, fixture/save export-import, and (later) H-HEADLESS scenario
// scripting (M0-ENG §2 harness strategy). Its export/import surface is the
// CLI consumer named in int.serializer ("metctl export").
//
// With no arguments it keeps the M0 skeleton behaviour: prints the build
// identity and exits. Two subcommands are implemented on top of that:
//
//	metctl export <save-dir> [-out dir]
//	metctl verify <save-dir>
//
// export reads any bundle (see internal/foundation/serialize) and emits
// plain, uncompressed NDJSON per shard — for NDJSON+gzip bundles (the only
// encoding implemented today) this is decompress+verify; for a future
// binary-encoded bundle this is the lossless escape hatch back to JSON
// (A3, R2). verify checks the header's format version and rehashes every
// shard, exiting non-zero with a clear message on any failure.
//
// Module key: tool.metctl (see code.json; GUID abb3d403-7e6b-41f4-adc9-97c34d000bc7)
// Spec ref:   M0-ENG §5; A8; int.serializer
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// exportStagingPrefix is the os.MkdirTemp prefix used for runExport's
// private staging directory (BUG-153). Named as a constant, rather than
// inlined at each of its two use sites (MkdirTemp's pattern arg and
// cleanupStaleExportStaging's matcher below), so the two can never drift
// apart -- a sweep that used a different prefix than MkdirTemp actually
// creates would silently stop finding anything to clean up.
const exportStagingPrefix = ".metctl-export-"

// staleExportStagingThreshold is how old a leftover exportStagingPrefix
// directory must be (by ModTime) before cleanupStaleExportStaging will
// treat it as orphaned rather than as a genuinely concurrent export still
// in progress. Generous on purpose: exports are architected for up to
// 100M-citizen saves (§5.3), so a real, slow, in-progress export can
// legitimately sit un-promoted for a long time -- an aggressive threshold
// here would reproduce BUG-129's "swept a live operation" class of bug
// (internal/engine/save/cleanup.go) in this package instead.
const staleExportStagingThreshold = 1 * time.Hour

// promoteRename performs the final stagingDir -> dest rename in runExport.
// It's a package-level seam (not a hardcoded os.Rename call) purely so
// tests can inject a deterministic failure at that exact point -- BUG-154's
// entire bug was about what happens when *that specific* rename fails after
// the backup step has already run, and the underlying syscall failure
// (locked file, AV scanner, concurrent reader, disk pressure) isn't
// reproducible on demand any other way. Production code always uses the
// real os.Rename; only tests in this package override it.
var promoteRename = os.Rename

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Println("metctl", buildinfo.String())
		os.Exit(0)
	}

	var err error
	switch args[0] {
	case "export":
		err = runExport(args[1:])
	case "verify":
		err = runVerify(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "metctl: unknown subcommand %q (want: export, verify)\n", args[0])
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "metctl:", err)
		os.Exit(1)
	}
}

// runVerify implements `metctl verify <save-dir>`: header format-version
// check + per-shard hash validation, via serialize.ValidateBundle.
func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: metctl verify <save-dir>")
	}
	dir := fs.Arg(0)

	h, err := serialize.ValidateBundle(dir)
	if err != nil {
		return err
	}

	// h.FormatVersion is header-derived (SEC-022 class); it's %q rather
	// than %s here even though this success path is only reachable once
	// ValidateBundle -> CheckFormatVersion -> ParseSemVer has already
	// accepted it (currently a strict digit-and-dot grammar, so no
	// control bytes can survive to here). That upstream-sanitisation
	// argument is real but lives only in this comment/reasoning, not in
	// the type system — and ParseSemVer's own doc comment already flags
	// "no pre-release or build-metadata suffixes" as a CURRENT
	// restriction, not a permanent one. %q removes the need to keep
	// re-verifying that argument every time the grammar changes.
	fmt.Printf("metctl verify: %q OK (formatVersion=%q, worldSeed=%d, createdAtTick=%d, gameMonth=%d, debugTouched=%t, shards=%d)\n",
		dir, h.FormatVersion, h.WorldSeed, h.CreatedAtTick, h.GameMonth, h.DebugTouched(), len(h.ShardIndex))
	return nil
}

// runExport implements `metctl export <save-dir> [-out dir]`: reads every
// shard of the bundle at <save-dir> via the StateSerializer matching each
// shard's recorded encoding, and re-writes it as plain (uncompressed)
// NDJSON — one JSON object per line, no wrapping envelope beyond what
// NDJSONSerializer already produces per record — into -out (default:
// <save-dir>.export).
func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	outDir := fs.String("out", "", "output directory for exported NDJSON shards (default: <save-dir>.export)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: metctl export <save-dir> [-out dir]")
	}
	dir := fs.Arg(0)
	dest := *outDir
	if dest == "" {
		dest = dir + ".export"
	}

	h, err := serialize.ReadHeader(dir)
	if err != nil {
		return err
	}

	// BUG-153: write every shard into a private staging directory first,
	// and promote (os.Rename) into dest only once every shard has
	// succeeded -- mirroring internal/engine/save's stage-then-promote
	// pattern (save.go's writeBundle / AC-9) so a shard failing partway
	// through a multi-shard bundle (e.g. a later hostile/traversal name,
	// MET-F301) never leaves earlier shards' output files sitting in
	// dest looking like a genuine complete-but-small export. Any failure
	// before promotion removes the staging directory and leaves dest
	// completely untouched (whatever it held before this call, or
	// nothing if it didn't exist).
	destParent := filepath.Dir(dest)
	if err := os.MkdirAll(destParent, 0o755); err != nil {
		return fmt.Errorf("metctl export: creating parent of output directory %q: %w", dest, err)
	}

	// BUG-156: before claiming our own staging directory, sweep destParent
	// for stale ones left behind by a prior runExport that was SIGKILL'd
	// (or otherwise forcefully terminated) between os.MkdirTemp creating
	// its staging directory and the deferred os.RemoveAll below running --
	// SIGKILL cannot be trapped, so that defer never executes, and the
	// directory is orphaned indefinitely. A NEXT-RUN sweep is the only
	// place this can be recovered from. See cleanupStaleExportStaging's
	// doc comment for why this is safe against a genuinely concurrent
	// export.
	cleanupStaleExportStaging(destParent, staleExportStagingThreshold, time.Now())

	stagingDir, err := os.MkdirTemp(destParent, exportStagingPrefix+"*")
	if err != nil {
		return fmt.Errorf("metctl export: creating staging directory: %w", err)
	}
	promoted := false
	defer func() {
		if !promoted {
			_ = os.RemoveAll(stagingDir)
		}
	}()

	for _, meta := range h.ShardIndex {
		if err := exportShard(dir, stagingDir, meta); err != nil {
			return fmt.Errorf("metctl export: shard %q: %w", meta.Name, err)
		}
	}

	// dest may already exist from a previous export; replace it wholesale
	// (rather than merging file-by-file) so a re-export never leaves
	// stale files from a different shard set mixed in with the new ones.
	//
	// BUG-154: this used to be os.RemoveAll(dest) followed by
	// os.Rename(stagingDir, dest) -- two separate, non-atomic syscalls. If
	// RemoveAll succeeded but the following Rename then failed (locked
	// file, AV scanner, concurrent reader, disk pressure, or a kill
	// landing in that exact gap), dest ended up deleted with the new
	// export never promoted: total loss of both old and new data, worse
	// than BUG-153's original symptom. The fix below renames dest out of
	// the way FIRST (recoverable), only promotes the staged export
	// second, and only deletes the old export AFTER promotion succeeds --
	// so a failure at any point leaves either the backup or the original
	// dest intact, never neither.
	backupPath := dest + ".metctl-export-backup"

	destExisted := true
	if _, statErr := os.Stat(dest); statErr != nil {
		if !os.IsNotExist(statErr) {
			return fmt.Errorf("metctl export: checking existing output directory %q: %w", dest, statErr)
		}
		destExisted = false
	}

	if !destExisted {
		// BUG-155: dest is missing. Before assuming this is a first-ever
		// export and clearing backupPath unconditionally, check whether a
		// stale backup survives from a prior run that completed step 1
		// (dest -> backupPath) but was killed before step 2 (promoteRename)
		// ran. If so, backupPath holds the ONLY surviving good export --
		// restore it to dest instead of deleting it, then fall through into
		// the ordinary "dest already exists" path below, which will
		// re-backup it before this run's promotion attempt, exactly as if
		// this were a normal replace-export.
		if _, statErr := os.Stat(backupPath); statErr == nil {
			if err := os.Rename(backupPath, dest); err != nil {
				return fmt.Errorf("metctl export: recovering surviving backup %q to %q: %w", backupPath, dest, err)
			}
			destExisted = true
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("metctl export: checking backup path %q: %w", backupPath, statErr)
		}
	} else {
		// dest exists cleanly, so any backupPath here is a genuine leftover
		// from a fully-completed-but-not-cleaned-up prior export -- dest is
		// already the good copy, so the backup is truly redundant and safe
		// to clear before we claim the name ourselves.
		if err := os.RemoveAll(backupPath); err != nil {
			return fmt.Errorf("metctl export: clearing stale backup path %q: %w", backupPath, err)
		}
	}

	if destExisted {
		if err := os.Rename(dest, backupPath); err != nil {
			return fmt.Errorf("metctl export: backing up existing output directory %q: %w", dest, err)
		}
		// If we fail anywhere below, put the prior export back exactly
		// where it was rather than leaving it stranded under the backup
		// name -- callers (and re-runs) expect dest to mean "the last
		// good export", not "the last good export, if you know to look
		// under a .metctl-export-backup suffix".
		defer func() {
			if !promoted {
				if _, statErr := os.Stat(backupPath); statErr == nil {
					_ = os.Rename(backupPath, dest)
				}
			}
		}()
	}

	if err := promoteRename(stagingDir, dest); err != nil {
		return fmt.Errorf("metctl export: promoting staged export to %q: %w", dest, err)
	}
	promoted = true

	// Promotion succeeded; the backup (the previous export) is no longer
	// needed. This is best-effort cleanup only -- promotion has already
	// happened, so a failure here is disk-space leakage, not data loss.
	if destExisted {
		if err := os.RemoveAll(backupPath); err != nil {
			return fmt.Errorf("metctl export: promoted to %q but failed to clean up backup %q: %w", dest, backupPath, err)
		}
	}

	fmt.Printf("metctl export: %s -> %s (%d shards)\n", dir, dest, len(h.ShardIndex))
	return nil
}

// cleanupStaleExportStaging sweeps destParent for leftover
// exportStagingPrefix directories orphaned by a prior runExport that was
// killed (SIGKILL, power loss, etc.) between os.MkdirTemp creating the
// staging directory and runExport's deferred os.RemoveAll cleaning it up
// (BUG-156). This is a disk-space leak only, never a correctness hazard:
// an orphaned staging directory is never promoted (only stagingDir, the
// one THIS call created, is ever passed to promoteRename) and nothing
// else in this package or serialize reads from a leftover one.
//
// A directory only qualifies for removal if its ModTime is older than
// now.Add(-olderThan) -- mirroring internal/engine/save/cleanup.go's
// CleanupStaleStaging "don't race a live operation" discipline (BUG-129
// there), this must never delete a staging directory a genuinely
// concurrent export invocation is still populating. os.MkdirTemp sets
// ModTime once at creation and nothing in runExport re-touches it
// afterward (shards are written as new files under it, which doesn't
// bump the parent directory's mtime on any platform this project
// targets), so an in-progress export's staging directory is
// indistinguishable from an idle one by mtime alone -- the threshold is
// the only guard, which is why staleExportStagingThreshold is
// deliberately generous.
//
// Best-effort: a per-entry Stat/RemoveAll failure, or being unable to
// list destParent at all (e.g. it doesn't exist yet on a first-ever
// export), is silently skipped rather than failing the export --
// exactly like CleanupStaleStaging's contract, an orphaned staging
// directory is forensic clutter, not something worth aborting a real
// export over.
func cleanupStaleExportStaging(destParent string, olderThan time.Duration, now time.Time) {
	entries, err := os.ReadDir(destParent)
	if err != nil {
		return
	}
	cutoff := now.Add(-olderThan)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), exportStagingPrefix) {
			continue
		}
		info, statErr := e.Info()
		if statErr != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(destParent, e.Name()))
	}
}

// exportLine is the plain-NDJSON line shape metctl export writes: the same
// {kind, data} shape NDJSONSerializer uses internally, so a plain-NDJSON
// export and a gzip'd NDJSON shard are the same document per line — only
// the outer gzip framing differs.
type exportLine struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

func exportShard(srcDir, destDir string, meta serialize.ShardMeta) error {
	// meta.Name is decoded from the source bundle's header.json, i.e. it
	// is untrusted (SEC-001) — validate before using it to build our own
	// output path, exactly as ShardPath does for the read side. Checked
	// separately from OpenShardReader below because this path targets
	// destDir (an arbitrary output directory), not ShardsDir(srcDir).
	if err := serialize.ValidateShardName(meta.Name); err != nil {
		return err
	}

	src, err := serialize.OpenShardReader(srcDir, meta)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	outPath := filepath.Join(destDir, meta.Name+".ndjson")
	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creating %q: %w", outPath, err)
	}
	defer func() { _ = out.Close() }()

	enc := json.NewEncoder(out)
	handle := func(rec serialize.Record) error {
		return enc.Encode(exportLine{Kind: rec.Kind, Data: json.RawMessage(rec.Data)})
	}

	// SEC-038: ReadShard's maxDecodedBytes is 0 (no limit) here
	// deliberately — this is metctl's SAVE-export path, a different,
	// legitimately much larger population than harness.replay's small
	// fixtures (which supply their own bound — see replay/limits.go's
	// maxFixtureDecodedBytes and its derivation comment). Saves are
	// architected for up to 100 M-citizen scale (§5.3); a single shared
	// byte ceiling picked to fit a fixture would wrongly reject a
	// legitimate save export.
	switch meta.Encoding {
	case "ndjson+gzip":
		return (serialize.NDJSONSerializer{}).ReadShard(src, 0, handle)
	default:
		return (serialize.BinarySerializer{}).ReadShard(src, 0, handle)
	}
}
