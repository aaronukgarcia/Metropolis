# Concept Document: Early-Game Bootstrapping, Player Archetypes, and Failure Analysis

This document outlines the conceptual design for the first 2-4 hours of Metropolis gameplay, focusing on the "Hamlet Bootstrap" phase, growth modeling, player archetypes, and post-mortem analysis tools to prevent complexity paralysis.

## 1. The Hamlet Bootstrap (Hours 0-2)

The game must prevent "North Africa Campaign" syndrome (where UI complexity and infinite choices paralyze the player). 

**The Goal:** Get the first 100 people to move in without requiring the player to understand global supply chains or taxation brackets.

**The "Cold Player" Experience (Progressive Un-hiding):**
- **Hour 0:** The player starts with a finite tile and 5,000,000 credits (Real Mode). The UI is strictly limited. They only see: Roads, Zoning (Low-Density Residential, Basic Shops, Light Industry), Power (Wind/Diesel), and Water (Pump/Tower).
- **The Initial Hook:** The player draws a road, drops a wind turbine, a water tower, and zones 10 tiles of Residential. 
- **The Helper System (Auto-Mode):** By default, supermarkets import food automatically (at a high cost), and garbage is handled by "Out-of-Tile Contractors". The player doesn't *need* to build farms or landfills yet.
- **The First Migrants:** Because housing is available and basic utilities exist, the `engine.attract` BDI score is positive. The first moving vans arrive.
- **The Transition:** Once the population hits 500 (Village Stage), the "Import Surcharges" start draining the OPEX budget. A notification pops up: *"Importing food is draining your treasury. Build local farms and a freight hub to produce food locally."* This unlocks the next layer of complexity organically.

## 2. Growth Speed Modeling

Growth in Metropolis is a **Sigmoid Curve** governed by the `engine.attract` (Attractiveness) and `engine.spiral` (Detroit Death Spiral) metrics.
- **Initial Phase (Hamlet):** High growth. The map is empty, land is cheap, and there is no pollution or crime. If basic jobs and housing exist, the influx is rapid.
- **Middle Phase (Town to City):** Growth slows. As density increases, traffic spillback (`engine.traffic`) causes commute failures. Crime (`engine.crime`) rises, demanding police OPEX. The player must optimize layouts.
- **Late Phase (Metropolis):** Hyper-optimized growth dependent on FDI (Mega-Facilities like TSMC or CERN) and high-education skill brackets. 

## 3. Player Archetype Performance Modeling

We pitch 5 distinct human player styles against the "Optimal" mathematical path to identify where they fail:

1. **The Aggressive Expander ("Build Wide")**
   - **Playstyle:** Spams residential zones and roads rapidly.
   - **Failure State:** Rapidly outstrips OPEX. They hit the "Death Spiral" because they cannot afford the police, fire, or refuse collection for the massive sprawl. Crime spikes, land value crashes, and they go insolvent.
   - **Learning Curve:** Needs to learn pacing and budget management.

2. **The Cautious Saver ("Build Tall & Slow")**
   - **Playstyle:** Builds a tiny block, saves millions in cash, waits hours before expanding.
   - **Failure State:** Stagnation. Their population is too small to attract high-tier businesses or afford advanced education. They survive, but never reach "City" status.
   - **Learning Curve:** Needs to learn safe debt leveraging and strategic CAPEX spending.

3. **The Specialist ("Deep Industry/Farming")**
   - **Playstyle:** Ignores commercial zones and focuses entirely on building massive industrial or agricultural sectors for export.
   - **Failure State:** Heavy pollution and commuting nightmares. The Death Spiral triggers because environmental blight tanks wellbeing, causing workers to flee, leaving the factories abandoned.
   - **Learning Curve:** Needs to learn zone separation, public transit, and pollution mitigation.

4. **The Diversity Planner ("One of Everything")**
   - **Playstyle:** Buys exactly one of every building unlocked (one farm, one mine, one factory, one school, etc.).
   - **Failure State:** Extreme OPEX inefficiency. Many buildings (like Mega-Factories) require economies of scale or massive specific workforce pools. They bleed cash maintaining fragmented, underutilized supply chains.
   - **Learning Curve:** Needs to learn specialization and scaling targeted industries.

5. **The Random/Cold Player ("Just clicking buttons")**
   - **Playstyle:** Drops zones arbitrarily. Mixes heavy industry next to housing. Ignores UI warnings.
   - **Failure State:** Immediate Detroit Spiral. Traffic deadlocks instantly, crime skyrockets, and health plummets.
   - **Learning Curve:** Relies heavily on the "Auto-Helper" toggles to survive the first few hours until they understand basic adjacency rules.

6. **The Optimal Path (Mathematical Baseline)**
   - Prioritizes tight, mixed-use low-pollution clusters. Relies on expensive auto-imports early to save CAPEX, then strategically pivots to local production for high-margin goods exactly when population demand hits the break-even threshold. 

## 4. Dissecting the Failure: The "Why Did My City Die?" Diagnostic

When a city hits insolvency or a Ghost City state, simply showing a "Game Over" screen is frustrating and teaches nothing. 

We need an **Autopsy Screen (The Post-Mortem Diagnostic)**.
- **The Snowball Trace:** The UI presents a chronological timeline of cascading failures, traced backward from the death state:
  1. *Month 48: Insolvency Declared.* (Treasury hit 0, loans maxed).
  2. *Month 42: Mass Emigration.* (Citizens fled due to S=0.85).
  3. *Month 38: Police Defunded.* (You cut police budget by 50% to save OPEX).
  4. *Month 30: The Catalyst - Heavy Industry CAPEX.* (You spent 2,000,000 credits on a Steel Mill, spiking your OPEX by 40,000/month before you had the population to staff it).
- **The Takeaway:** The player clearly sees that *building the Steel Mill too early* was the root cause that eventually forced them to defund the police, driving crime and causing the city to die. This transforms failure from frustration into a profound learning moment.
