# Metropolis
### A deterministic city-simulation game in Go that models individual citizens — not statistics

> "A city is not its buildings but its people: thousands of individual lives, each one tracked, that together *become* a city."  
> — *Metropolis design principle*

## 📄 Overview
**Status:** Pre-release — building the first playable vertical slice ("Baseline One")  
**Engine:** Go, fully deterministic  
**Front ends:** `tcell` terminal UI + React/TypeScript web console  
**License:** AGPL-3.0  
**Author:** Aaron Garcia  

This repository hosts **Metropolis**, a deep city-simulation game where the simulation — not the graphics — is the point. It asks a central question:

**Can a city be simulated from the bottom up — every citizen a persistent individual, carried to millions without ever being culled — and still run as a watchable game?**

## 🎯 The Core Concept
Most mainstream city-builders model a city as aggregates:
* **A population number** that goes up or down.
* **Averaged bars** for happiness, traffic, pollution.
* **Zones** that fill with interchangeable, faceless demand.

**The approach here is the opposite — simulate the people:**
* Every citizen is a **persistent individual** with a household, a job (or not), income, consumption and a full life arc (born → schooled → employed → retired → gone).
* The population is never culled to save memory; the engine is built to carry **millions of individuals** at adaptive fidelity.
* The city you watch is the **emergent result** of all those lives — not a dial you set.

## 🧩 What It Models
| Area | Examples |
|------|----------|
| **People** | households, employment, wages, consumption, fertility, ageing, death, migration |
| **Economy** | UK tax (PAYE / NI / council) with elasticity & incidence, era-based grants, budgets, insolvency |
| **Land & growth** | zoning density, connectivity-gated building activation, blight & reclamation |
| **Transport** | roads, rail, parking (parked cars consume land; on-street parking narrows the road), congestion, freight |
| **Services** | fire & emergency dispatch, waste & recycling, power generation and demand |
| **Draw** | tourism, destination leisure & retail, regional spend |

## 🏗️ Architecture
* **Contract-first modules** behind a strict **protocol-only split** between engine and UI — the simulation has no idea whether a terminal or a browser is watching it.
* **Deterministic to the core:** the same seed produces the same city, every run — so bugs are reproducible and saves/replays are compact, data-only.
* A single **source-of-truth specification** generates both the code registry and a database **Book of Work**; modules are built to written acceptance criteria, never freehand.

## 🤖 Built by a Team of AI Agents
Metropolis is developed by a team of **Claude Code agents** working in parallel, with humans owning the design decisions. Every feature runs the same pipeline:

**acceptance criteria → a coder agent builds → an independent tester (pass/fail only, never fixes) → an adversarial "destructive round" where the attacker is never the author and must prove a test fails when the code is broken → green CI (format, build, lint, full suite) + a determinism gate + a performance gate (a tick at 1M+ citizens).**

## 📂 Repository Contents
* `docs/METROPOLIS-MASTER-v2.1.md` — the full design specification (single source of truth).
* `internal/engine/` — the deterministic simulation engine (66 modules).
* `webconsole/` — the React/TypeScript web console.
* `cmd/metropolis`, `cmd/metroserve`, `cmd/metctl` — terminal UI, server and control entry points.
* `docs/planning/` — sprint plans, acceptance criteria, design notes.
* `CONTRIBUTING.md` · `LICENSE` · `GGR.md` (the project's inviolable engineering rules).

## 🛠️ Building & Testing
* **Engine (Go):** `go test ./...`, `gofmt`, `golangci-lint run`
* **Web console (TypeScript/React):** in `webconsole/` — `npm install`, then `npm test` and `tsc --noEmit`

## 🤝 Invitation to Collaborate
This is not a funding request — it is an invitation to build and to critique. Input is especially valued from people who know how cities *actually* work:
* **Urban planning & affordable housing**
* **Transport & mobility**
* **Public finance & economics**
* **Go / large-scale simulation engineering**

**Where help is most wanted:**
1. **Realism vs. fun** — which systems deserve deep simulation, and which are better abstracted so the game stays enjoyable?
2. **Economic modelling** — tenure (rent vs. own), land value, political capital, and other mechanics that a planner's eye would sharpen.
3. **Scale** — proving and holding the "individual citizens to millions" promise under a real performance budget.

## 📬 Contact
Open an Issue in this repository, or reach out directly:

**Aaron Garcia** · aaron@garcia.ltd

---
*A city is the sum of the lives lived in it. Metropolis tries to simulate all of them.*
