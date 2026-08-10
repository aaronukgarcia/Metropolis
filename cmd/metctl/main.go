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
// Module key: foundation.repo (see code.json)
// Spec ref:   M0-ENG §5; A8; int.serializer
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

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
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("metctl export: creating output directory %q: %w", dest, err)
	}

	for _, meta := range h.ShardIndex {
		if err := exportShard(dir, dest, meta); err != nil {
			return fmt.Errorf("metctl export: shard %q: %w", meta.Name, err)
		}
	}

	fmt.Printf("metctl export: %s -> %s (%d shards)\n", dir, dest, len(h.ShardIndex))
	return nil
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
