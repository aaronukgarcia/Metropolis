package metricsdash

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// runBowCommand shells out to `node claude-bow.js <args...>` from
// repoRoot and returns its combined stdout — the SAME command a human
// runs by hand (weakness.go/lint.go/gatestatus.go's package docs name
// exactly which subcommand each caller passes here), never a
// re-implemented parallel query against the BOW (AC-1/GR#3). This
// package has no MariaDB driver of its own and does not add one (see
// the package doc comment's "Escalation B / ASM-453" section) — the
// Node tool is the only thing that ever opens a connection; this
// function only invokes it and reads its text output.
//
// Uses exec.CommandContext with an argv slice (never a shell string),
// matching the project-wide "shell:false" convention already
// documented in claude-devfeedback-import.js/claude-pre-push-check.js —
// an argument (e.g. a sprint number typed by a caller) can never be
// interpreted by a shell.
func runBowCommand(ctx context.Context, repoRoot string, args ...string) (string, error) {
	fullArgs := append([]string{"claude-bow.js"}, args...)
	cmd := exec.CommandContext(ctx, "node", fullArgs...)
	cmd.Dir = repoRoot

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("node claude-bow.js %s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
