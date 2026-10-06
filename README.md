# Metropolis

> A deterministic city-simulation game in Go — where the **simulation is the game**, not the graphics.

![License: AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-blue)
![Language: Go](https://img.shields.io/badge/engine-Go-00ADD8)
![Status: pre-release](https://img.shields.io/badge/status-pre--release-orange)

Most city-builders simulate a city as a set of statistics — a population number, an averaged happiness bar, a traffic heatmap. **Metropolis simulates the people.** Every citizen is a persistent individual with a household, a job (or not), income, consumption and a whole life arc — born, schooled, employed, retired, and eventually gone — and the engine is built to carry millions of them without ever quietly deleting anyone to save memory. The city you watch is the emergent result of all those lives, not a dial you set.

It is an unapologetically deep simulation with a deliberately modest presentation: a terminal UI and a browser console rather than a 3D cityscape. The guiding principle is pinned to the wall — *"it's a game, not NASA code"* — depth has to stay **watchable**.

---

## What makes it different

- **Persistent individuals, not aggregates.** Households form and dissolve, people take jobs and commute, wealth is earned and spent, and migration responds to how livable the city actually is.
- **Real-world geography.** The map is built from real Ordnance Survey terrain data, starting on an actual coastal tile in Kent, UK.
- **A UK-grounded economy.** Real tax instruments (PAYE, National Insurance, council tax) with elasticity and incidence; era-based government grants; budgets; policy levers.
- **Deterministic to the core.** The same seed produces the same city, every single run — which makes bugs reproducible, saves and replays compact, and "it worked on my machine" a non-excuse.

## What it models

| Area | Examples |
|------|----------|
| **People** | households, employment, wages, consumption, fertility, ageing, death, migration |
| **Economy** | tax (PAYE/NI/council), grants, budgets, insolvency, cost-of-living → real wages |
| **Land & growth** | zoning density, building activation gated on real connectivity, blight & reclamation |
| **Transport** | roads, rail, parking (parked cars consume land; on-street parking narrows the road), congestion, freight |
| **Services** | fire & emergency dispatch, waste & recycling, power generation and demand |
| **Draw** | tourism, destination leisure & retail, regional spend |

## Architecture

- **Contract-first modules** behind a strict **protocol-only split** between the engine and any UI — the simulation has no idea whether a terminal UI or a browser console is watching it.
- A single **source-of-truth specification** generates both the code registry and a database **Book of Work**; modules are built to written acceptance criteria, never freehand.
- Two front ends: a `tcell` **terminal UI** and a **React/TypeScript web console** that observes the live engine.

## Built by a team of AI agents

Metropolis is developed by a team of **Claude Code agents** working in parallel, with humans owning the design decisions. Every feature runs a disciplined pipeline:

**written acceptance criteria → a coder agent builds → an independent tester (pass/fail only, never fixes) → an adversarial "destructive round" where the attacker is never the author and must prove a test fails when the code is broken → green CI (format, build, lint, full suite) + a determinism gate + a performance gate (a tick at 1M+ citizens).**

A software shop staffed by AI, held to a real quality bar.

## Status

**Pre-release.** The foundational engine and most individual systems are built and independently verified; current work is wiring them into the first end-to-end **playable vertical slice** — citizens earning and spending, money moving, building, migration responding, all watchable live. It is not a release yet.

## Building & testing

- **Engine (Go):** `go test ./...`, `gofmt`, `golangci-lint run`
- **Web console (TypeScript/React):** in `webconsole/` — `npm install`, then `npm test` and `tsc --noEmit`

## Contributing

Contributions, bug reports and design ideas are all welcome — see **[CONTRIBUTING.md](CONTRIBUTING.md)**. Input from urban planners, economists, transport and housing specialists is especially valued; you don't need to write code to help shape the simulation.

## License

Copyright © 2026 Aaron Garcia. Metropolis is free software under the **GNU Affero General Public License v3.0** — see **[LICENSE](LICENSE)**. In short: use, study, modify and share it freely; if you run a modified version as a network service, make your source available to its users.
