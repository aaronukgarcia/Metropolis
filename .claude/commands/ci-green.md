---
description: Run the complete, exact CI verification suite locally to check format, build, lint, and tests (Golang + Node) before commit/push
allowed-tools: Bash, Read
---

# /ci-green — locally verify that the codebase is completely green before push

Use this command to verify that your local workspace passes every single validation check that the central CI gate executes. Running this locally prevents pushing code that breaks the build or fails lint checks, saving CI resources and avoiding automatic P0 "Red Determinism Gate" blocks.

## Execution Sequence

Perform these checks in order on the working tree:

1. **Format Check:**
   Verify that all Go files are perfectly formatted:
   ```bash
   gofmt -l .
   ```
   (If any files are listed, run `gofmt -w <files>` immediately to format them.)

2. **Standard Go Build:**
   Compile all packages and binaries to ensure zero compilation or dependency errors:
   ```bash
   go build ./...
   ```

3. **Go Vet:**
   Run the static analysis compiler checks:
   ```bash
   go vet ./...
   ```

4. **Linting (Strict static analysis):**
   Ensure there are zero lint issues in the Go engine:
   ```bash
   golangci-lint run ./...
   ```

5. **Automated Testing Suite (Engine + UI):**
   Run all Go tests with race-condition detection active:
   ```bash
   go test ./... -count=1 -race
   ```

6. **Tooling & Hooks Testing:**
   Run all Node.js and script unit tests to ensure no local guard regressions:
   ```bash
   node --test
   ```

Confirm to the user that all 6 validation gates are perfectly green. If any gate fails, stop, report the exact error, and focus on fixing that specific failure first.
