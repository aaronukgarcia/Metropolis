# Metropolis

A deterministic city-simulation game in Go, where the simulation — not the graphics — is the point.

Metropolis models a city as **persistent individual citizens** (not statistical aggregates): each person has a household, employment, income, consumption and a life arc, and the engine is designed to carry that to very large populations without ever culling anyone. It runs on real-world geography (Ordnance Survey terrain) and a UK-grounded economy (real tax instruments with elasticity and incidence, era-based government grants, budgets, policies).

> **Status:** pre-release. The foundational engine and most individual systems are built and independently verified; current work is assembling them into the first end-to-end playable vertical slice ("Baseline One") — citizens earning and spending, money moving, building, migration responding, watchable live.

## Architecture

- **Deterministic core.** Same seed → same city, every run. This makes bugs reproducible and saves/replays data-only.
- **Contract-first modules** behind a strict protocol-only split between the engine and any UI — the simulation has no idea whether a terminal UI or a browser console is watching it.
- **Front ends:** a `tcell` terminal UI and a React/TypeScript web console that observes the live engine.

## Building & testing

- Engine (Go): `go test ./...`, `gofmt`, `golangci-lint run`
- Web console (TypeScript/React): see `webconsole/` (`npm test`, `tsc`)

Every change is expected to pass formatting, build, lint and the full test suite, plus a determinism check and a performance gate, before it is considered done. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Copyright (C) 2026 Aaron Garcia.

Metropolis is free software licensed under the **GNU Affero General Public License v3.0** (AGPL-3.0). You may use, study, modify and redistribute it under the terms of that license; if you run a modified version to interact with users over a network, you must make the corresponding source available to them. See [LICENSE](LICENSE) for the full text.

## Contributing

Contributions and ideas are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). Input from urban planners, economists and other domain experts is especially valued.
