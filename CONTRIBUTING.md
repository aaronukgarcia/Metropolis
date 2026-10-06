# Contributing to Metropolis

Thanks for your interest — contributions, bug reports and design ideas are all welcome. Metropolis is an ambitious deep-simulation project, and input from people with real-world domain knowledge (urban planning, housing, transport, public finance, economics) is as valuable as code.

## Ways to contribute

- **Ideas & design feedback** — open a GitHub issue describing the mechanic, the real-world behaviour it models, and why it matters. Design discussion is genuinely wanted; you don't need to write code.
- **Bug reports** — open an issue with steps to reproduce. Because the engine is deterministic, please include the seed and any save/replay data where relevant.
- **Code** — for anything non-trivial, please open an issue to discuss the approach before sending a pull request, so effort isn't wasted on something that cuts across a design decision.

## Licensing of contributions

By submitting a contribution you agree that it is licensed under the project's license, the **GNU Affero General Public License v3.0** (see [LICENSE](LICENSE)). Don't submit code you don't have the right to license this way.

## Development setup

- **Engine** — Go. Run the suite with `go test ./...`, format with `gofmt`, and lint with `golangci-lint run`.
- **Web console** — TypeScript/React under `webconsole/` (`npm install`, then `npm test` and `tsc --noEmit`).

## Quality bar

Pull requests are expected to meet the same bar the project holds itself to:

- **Determinism.** The simulation must be reproducible: the same seed produces the same result. Avoid wall-clock time, map-iteration-order dependence, or other sources of nondeterminism in engine logic.
- **Tests that can fail.** A test must actually exercise the behaviour it claims to — a test that still passes when you break the production code it covers is not a test. Where practical, show that the test fails before your fix and passes after.
- **Green checks.** Formatting, build, lint and the full test suite should pass before you open or update a PR.
- **Clear, small changes.** Prefer focused commits. Use `[type]: short description` commit messages (`feat`, `fix`, `refactor`, `docs`, `chore`, `test`).

## Review

All changes are reviewed before merging. Maintainers may ask for adjustments to keep the codebase consistent and the simulation deterministic. Please be patient and constructive — and expect the same in return.

## Conduct

Be respectful and assume good faith. Harassment or abuse of any kind isn't welcome here.
