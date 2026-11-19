// Command journalscan is a read-only report on a persist directory
// (FEAT-9995385612 lane 5, "the home-server journal: read-only checks before
// deploy"). For each city under a tenant it reports the rule version stamped
// on each snapshot, the number of journal frames per rule version, the first
// and last tick of the journal, and every problem the restore would refuse
// (the lane 2 checks: a version newer than this build, older than the oldest
// supported, going backwards, or a malformed stamp).
//
// # A static check, not a restore
//
// The rule-version verdict here is compose.DecideRuleVersions: a STATIC check
// of the journal and the snapshot headers that cannot run an engine. The
// server's own pre-check is different: it is a dry-run restore
// (compose.PrecheckRuleVersions). The one case this tool cannot see is a
// snapshot whose journal tail is rejected on replay (for example after a lost
// journal append): the restore then walks back to an older snapshot and may
// refuse that one for its rule version, while this tool reports clean.
// tools/journalscan/agree_test.go asserts that disagreement; every report
// says so on its status line.
//
// Usage: go run ./tools/journalscan [-tenant local] [-city ID] <persist-dir>
//
// Exit status: 0 clean; 2 at least one finding (each printed with its
// registered MET code); 1 the tool could not run (bad usage, directory
// missing).
//
// # Read-only, and safe against a live server
//
// The tool never creates, locks, truncates or renames anything under the
// directory it scans. It opens the root only after proving it exists (the
// persist store's constructor would create a missing root), and it reads
// through the persist store's own read methods (ReadJournal, ListSnapshots,
// GetSnapshot, ListCities), which open files read-only. Snapshot payloads
// are unpacked into the OS temp folder (and removed again), never into the
// scanned directory.
//
// It does NOT refuse a directory a live server holds. Reading is safe there:
// the journal is append-only with whole-frame writes and the reader drops a
// torn final frame without touching the file; snapshots appear by atomic
// rename; the citizen-paging claim lives in a separate paging directory the
// tool never opens. The one effect of a live server is that a frame appended
// while the scan runs may or may not be seen; the report is a snapshot in
// time. For a stable report copy the directory first (the deploy checklist
// does).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

const defaultTenant = "local"

const (
	exitClean    = 0
	exitCannot   = 1
	exitFindings = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// finding is one problem the restore would refuse.
type finding struct {
	Code string
	Text string
}

// snapshotReport is one snapshot's recorded version and tick.
type snapshotReport struct {
	ID      string
	Version int
	Tick    int64
	HasTick bool
}

// cityReport is everything journalscan says about one city.
type cityReport struct {
	City      persist.CityKey
	Snapshots []snapshotReport
	Frames    int
	FirstTick int64
	LastTick  int64
	// Advanced is the sum of every AdvanceTicks frame: the tick a genesis
	// replay of this journal reaches. FirstTick and LastTick are the
	// IssuedAtTick stamps of the first and last frame (0 when the issuer
	// did not stamp one, as metroserve's tick loop does not).
	Advanced int64
	PerRule  map[rules.Version]int
	Findings []finding
	// Stopped is true when the journal decode stopped at its first malformed
	// frame, so later frames were not checked.
	Stopped bool
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("journalscan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	tenant := fs.String("tenant", defaultTenant, "tenant whose cities are scanned (directory names are hashed, so the tenant cannot be discovered)")
	only := fs.String("city", "", "scan only this city id (default: every city of the tenant)")
	if err := fs.Parse(args); err != nil {
		return exitCannot
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: journalscan [-tenant local] [-city ID] <persist-dir>")
		return exitCannot
	}
	root := fs.Arg(0)

	// Never let the store's constructor create a missing root.
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		_, _ = fmt.Fprintf(stderr, "journalscan: %q is not an existing directory\n", root)
		return exitCannot
	}
	store, err := persist.NewDiskStore(root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "journalscan: open %q: %v\n", root, err)
		return exitCannot
	}

	ctx := context.Background()
	cities, err := store.ListCities(ctx, *tenant)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "journalscan: list cities of tenant %q: %v\n", *tenant, err)
		return exitCannot
	}
	if *only != "" {
		var keep []persist.CityKey
		for _, c := range cities {
			if c.CityID == *only {
				keep = append(keep, c)
			}
		}
		cities = keep
	}

	_, _ = fmt.Fprintf(stdout, "journalscan: tenant=%s cities=%d (read-only)\n", *tenant, len(cities))
	bad := 0
	for _, c := range cities {
		rep := scanCity(ctx, store, c)
		printCity(stdout, rep)
		if len(rep.Findings) > 0 {
			bad++
		}
	}
	if bad > 0 {
		_, _ = fmt.Fprintf(stdout, "result: %d of %d cities have findings\n", bad, len(cities))
		return exitFindings
	}
	_, _ = fmt.Fprintln(stdout, "result: clean")
	return exitClean
}

// codeOf pulls the registry code and message out of a registered error,
// without the random correlation id Display adds (so output is byte-stable).
func codeOf(err error) finding {
	var e *errs.E
	if errors.As(err, &e) {
		text := e.Msg
		if e.Wrapped != nil {
			text += ": " + e.Wrapped.Error()
		}
		return finding{Code: e.Code, Text: text}
	}
	return finding{Code: "-", Text: err.Error()}
}

func scanCity(ctx context.Context, store *persist.DiskStore, city persist.CityKey) cityReport {
	rep := cityReport{City: city, PerRule: map[rules.Version]int{}}

	// Snapshots: the recorded rule version and tick of each, read by the
	// restore's own reader (compose.ReadSnapshotRuleInfo, which picks the
	// composition bundle exactly as the restore does).
	ids, listErr := store.ListSnapshots(ctx, city)
	if listErr != nil {
		rep.Findings = append(rep.Findings, finding{Code: "-", Text: "list snapshots: " + listErr.Error()})
	}
	// infos[i] and readErrs[i] are snapshot i (oldest first) as the decision
	// function will ask for it.
	infos := make([]compose.SnapshotRuleInfo, len(ids))
	readErrs := make([]error, len(ids))
	for i, id := range ids {
		sr := snapshotReport{ID: string(id)}
		data, err := store.GetSnapshot(ctx, city, id)
		if err != nil {
			rep.Findings = append(rep.Findings, finding{Code: "-", Text: "snapshot " + string(id) + ": " + err.Error()})
			readErrs[i] = err
			rep.Snapshots = append(rep.Snapshots, sr)
			continue
		}
		info, err := compose.ReadSnapshotRuleInfo(data)
		if err != nil {
			rep.Findings = append(rep.Findings, finding{Code: "-", Text: "snapshot " + string(id) + ": " + err.Error()})
			readErrs[i] = err
		} else {
			sr.Version, sr.Tick, sr.HasTick = info.Version, info.Tick, true
			infos[i] = info
		}
		rep.Snapshots = append(rep.Snapshots, sr)
	}

	// Journal: decode every frame with the restore's own reader. A frame it
	// refuses (malformed stamp, undecodable command) stops the decode, so
	// later frames of that city cannot be reported.
	frames, err := compose.RestoreCommands(ctx, store, city)
	if err != nil {
		rep.Findings = append(rep.Findings, codeOf(err))
		rep.Stopped = true
		return rep
	}
	rep.Frames = len(frames)
	for i, f := range frames {
		rep.PerRule[f.Version]++
		tick := int64(f.IssuedAtTick)
		if i == 0 {
			rep.FirstTick = tick
		}
		rep.LastTick = tick
		if p, ok := f.Payload.(protocol.AdvanceTicksPayload); ok {
			rep.Advanced += p.N
		}
	}

	// The verdict is the static decision procedure compose.DecideRuleVersions
	// (the server's own pre-check is a dry-run restore instead). It follows
	// the restore's order, so a journal frame from a newer
	// build is refused even when a snapshot is unreadable; an unreadable
	// snapshot is reported above and ends the decision only where the
	// restore itself would stop on it.
	if listErr == nil { // the restore stops on a failed listing before any rule check
		verr := compose.DecideRuleVersions("", frames, len(infos), func(i int) (compose.SnapshotRuleInfo, error) {
			return infos[i], readErrs[i]
		})
		if verr != nil {
			rep.Findings = append(rep.Findings, codeOf(verr))
		}
	}
	return rep
}

func printCity(w io.Writer, r cityReport) {
	_, _ = fmt.Fprintf(w, "city %s\n", r.City.CityID)
	_, _ = fmt.Fprintf(w, "  snapshots: %d\n", len(r.Snapshots))
	for _, s := range r.Snapshots {
		tick := "?"
		if s.HasTick {
			tick = strconv.FormatInt(s.Tick, 10)
		}
		_, _ = fmt.Fprintf(w, "    snapshot %s: rule version %d, tick %s\n", s.ID, s.Version, tick)
	}
	if r.Stopped {
		_, _ = fmt.Fprintln(w, "  journal frames: not counted (decoding stopped at the first malformed frame)")
	} else if r.Frames == 0 {
		_, _ = fmt.Fprintln(w, "  journal frames: 0")
	} else {
		_, _ = fmt.Fprintf(w, "  journal frames: %d, first tick %d, last tick %d, advanced ticks %d\n", r.Frames, r.FirstTick, r.LastTick, r.Advanced)
		vs := make([]int, 0, len(r.PerRule))
		for v := range r.PerRule {
			vs = append(vs, int(v))
		}
		sort.Ints(vs)
		parts := make([]string, 0, len(vs))
		for _, v := range vs {
			parts = append(parts, fmt.Sprintf("v%d=%d", v, r.PerRule[rules.Version(v)]))
		}
		_, _ = fmt.Fprintf(w, "  frames per rule version: %s\n", strings.Join(parts, " "))
	}
	_, _ = fmt.Fprintln(w, "  check: static (no engine is run); the server's own pre-check is a dry-run restore; this cannot see a snapshot whose tail is rejected on replay, where the restore walks back to an older snapshot")
	if len(r.Findings) == 0 {
		_, _ = fmt.Fprintln(w, "  status: ok")
		return
	}
	for _, f := range r.Findings {
		_, _ = fmt.Fprintf(w, "  finding: %s %s\n", f.Code, f.Text)
	}
	if r.Stopped {
		_, _ = fmt.Fprintln(w, "  note: checking stopped at the first malformed journal frame; frames after this point were not checked")
	}
	_, _ = fmt.Fprintln(w, "  status: FINDINGS")
}
