# METROPOLIS PROJECT INDEPENDENT AUDIT REPORT
**Date:** August 16, 2026  
**Auditor:** Independent Reviewer (Ben Slot)  
**Target Workspace:** `E:\git\Metropolis`  
**Current Branch:** `feature/services-astgate`  

---

## 1. PROJECT PROGRESS & VELOCITY SNAPSHOT

* **Project Progress to Date (%):** **45.38%**
  * **Database Proof of Truth (SSOT):** There are exactly **1331** total items tracked in the Book of Work (BOW) database (`metro` MariaDB). **604** are marked as `done`, leaving **710** items open, blocked, or in-progress, and a small subset in transient pipeline states.
* **Milestone Distribution:**
  * Milestone **M0** (Walking Skeleton, core protocols, error registries, and tooling scaffolding) is largely complete or in final verification sweeps.
  * Sprints **S1 through S11** have their acceptance criteria fully drafted by Business Analysts (BAs), which is running far ahead of the actual build.
  * The actual Go implementation is currently focused on Milestone **M1 / Sprint 3** features (e.g., `engine.services`, `engine.season`, `engine.world`).
* **Repository Health / Sync State:**
  * **NOT SYNCED:** The working directory currently holds **26 uncommitted changes** across multiple packages.
  * **Lanes Operating:** There are up to **6/12 parallel lanes running** across several sessions, representing substantial coordination pressure on the team.

---

## 2. THE META-TOOLING CRISIS: BEING "LOST ADRIFT"

The most critical finding of this audit is that **the Metropolis project is trapped in an extreme Meta-Tooling and Compliance Overhead Loop.**

* **Over-engineered Environment:**
  The workspace contains **over 35 custom Node.js guard, hook, and sync files** (e.g., `claude-destructive-guard.js`, `claude-plan-checker.js`, `claude-pre-commit-check.js`, `claude-worktree-guard.js`, `claude-secret-guard.js`, etc.) in the root directory alone.
  Instead of building the game, the team is spending massive amounts of engineering capacity and developer cognitive load on *writing, debugging, bypassing, and hardening their own compliance tooling*.
* **The "Banned Git Commands" Absurdity (An Architectural Anti-Pattern):**
  Due to an incident where an uncommitted work-reversion occurred, the project implemented a strict, fail-closed `claude-worktree-guard.js` that **bans standard Git commands** such as `git restore`, `git stash`, and `git reset --hard` for all developers and subagents.
  This is a massive operational bottleneck. Banning core version control commands and replacing them with custom node scripts to "prevent human error" is an over-engineered, brittle anti-pattern that creates artificial complexity and fails to address proper education or clean branching strategies.
* **Iterative Loop Hell (Compliance-Centered Development):**
  * Look at **BUG-123 (P0, quote-mask bypass)**: The team ran **6 rounds** of a destructive-agent loop on a single git commit bypass because developers kept trying to hand-roll custom regexes and quote scanners rather than using robust, standard libraries.
  * Look at **BUG-119 (P1, astgate false positives)**: The team went through **10 rounds of re-attacks** for generics parsing in astgate.
  The developers are not focused on building gameplay or resolving engine bottlenecks; they are playing an adversarial game against their own "Destructive" scanning agents.

---

## 3. THE "VALUE" AUDIT: ARE YOU GETTING GOOD VALUE?

To assess the ROI on your staff, we examine each group:

### A. ARCHITECTS: Rating: 5/10 (High Technical Skill, Negative Operational ROI)
* **What’s Working:** The underlying technical blueprint is sound. Deterministic simulations using Philox-style RNGs, 256 fixed shards, integer-only currency (`int64` micro-pounds), and absolute decoupling of the UI from the engine via view subscriptions are excellent. Perfect determinism is written into the foundation.
* **What’s Not Working:** The Architects have introduced an extremely rigid, punitive, and high-friction set of **24 Golden Rules** and a coordination system backed by a custom MariaDB schema. They have created a "compliance trap" where complying with the rules takes precedence over shipping code. Writing custom synchronization engines and filesystem guards is a waste of capital for a game prototype.

### B. BUSINESS ANALYSTS (BAs): Rating: 7/10 (Thorough but Detached)
* **What’s Working:** The BAs have produced exceptionally detailed, high-quality markdown specifications for every engine module and feature under `docs/planning/acceptance/`.
* **What’s Not Working:**
  * **Spec Contradictions:** They are writing criteria that contain logical impossibilities. For example, in **ASM-468** (`feat.devmode`), the `RequireConsole` gate physically prevents the "enable debug on first open" code path from being reached, rendering the test un-passable in a real production wire.
  * **Phantom Citations (BUG-075):** BAs have cited items in documents that were never created in the database.
  * **Disconnect from Velocity:** The criteria estate is running so far ahead of the build queue that they are writing criteria for Sprint 11 while the developers are struggling to get Sprint 3 out of the gate due to tool blocks.

### C. TESTERS & DESTRUCTIVE AGENTS: Rating: 6/10 (Rigid Enforcement, Limited Synthesis)
* **What’s Working:** They are brutally honest. They find real boundary overflows, concurrency issues, and race conditions. The "Destructive Agent" process (Pattern-finding) has successfully surfaced 95 security/safety findings.
* **What’s Not Working:** They are disjoint, siloed, and act as a massive bottleneck. Because they are not allowed to communicate with each other or propose structural fixes, they bounce issues back to developers (Juniors) with trivial rejection remarks, leading to endless loops (e.g. 10 rounds of attacks on BUG-119). Furthermore, they can miss critical structural issues if a unit test passes (like the racing `Close()` call).

### D. DEVELOPERS (JUNIORS): Rating: 6.5/10 (Hard-Working but Defensive)
* **What’s Working:** They are writing well-structured Go code and comprehensive unit tests. They have successfully implemented the core structure of Milestone 0 and are beginning on Milestone 1 modules (season, world, services).
* **What’s Not Working:** They are writing defensive, brittle, and repetitive code to appease the automated guards and Destructive agents. Instead of using shared, clean abstractions, they copy-paste blocks (e.g., multiple custom implementations of `buildQuoteMask` which led to BUG-123) or hand-roll insecure string parsers to bypass specific test payloads.

---

## 4. TECHNICAL ANALYSIS: CODE QUALITY, SECURITY & PERFORMANCE

### A. CODING STANDARDS & CODE QUALITY
* **The Error Registry Bloat:**
  * In compliance with **Golden Rule #7** (Registry-Sourced Errors), every single error must be pre-declared in `data/errors.json` before compile time.
  * This file has bloated to **193KB**! This represents a massive operational friction point. If a developer wants to add error handling to a minor function, they must interrupt their flow, edit a shared JSON file (increasing merge conflicts across 12 lanes), run a generator, and declare a strict code.
  * While error traceability via correlation IDs is excellent, this rigid system discourages granular error reporting and leads to developers using generic, wide errors.

### B. SECURITY CONCERNS (The Recurring 95 Findings)
* There are **95 security weakness findings** logged, of which **42 are still open**.
* The recurring classes are alarming and indicate a foundational lack of security-by-default primitives:
  * **Input Validation (26 findings):** Developers keep writing unvalidated inputs that crash or bypass boundaries.
  * **Concurrency Safety (17 findings):** Unlocked map reads, lock-order inversions, and TOCTOU.
  * **Encapsulation Leaks (13 findings):** Public APIs leaking live mutable internal slices (e.g. keymaps, layout slices), violating the copyguard rules.
* **The Diagnostic:** The developers are patching individual bugs *post-hoc* when the Destructive agent catches them, rather than the Architect providing standard, thread-safe, safe-coercion helper primitives.

### C. PERFORMANCE CONCERNS
* **The Performance Gate Failure:**
  * For a long time, the performance gate was "not a gate" because it measured execution times below its own noise floor, letting over 30 commits drift by **13x** in execution time without triggering warnings.
  * Worse, the `AcceptedRegression` system allowed developers to declare false "accepted regressions" by manually modifying a JSON file.
  * **The Architectural Lesson:** Enforcing a control where the metric is *recorded* (a forgeable JSON file) rather than where it is *created* (CI runner environment) is a severe security and quality failure.

---

## 5. STRATEGIC ROADMAP FOR HEALTH & IMPROVEMENT

To get the project out of this "adrift" loop and maximize the value of your team, we recommend a rapid **3-Step De-Escalation & Focus Shift**:

### 1. De-Escalate and Simplify the Tooling (Stop Compliance Theater)
* **Consolidate Guards:** Delete or consolidate the 35+ custom Node.js guards. Stand up standard, battle-tested linters (like `golangci-lint` or ESLint) in CI rather than running invasive pre-commit scripts on local workstations.
* **Unban Git Commands:** Instantly decommission `claude-worktree-guard.js`. Lift the ban on `git restore`, `git reset`, and `git stash`. Educate the developers and subagents on proper Git branch hygiene and clean commit separation rather than breaking their environment.
* **Stop Hand-Rolling Parsers:** Standardize on robust, existing parsers for command parsing, quote masking, and AST gating.

### 2. Standardize Secure-by-Default Foundational Primitives
* Instead of treating 26 input-validation findings and 17 concurrency bugs as separate tickets, the Architect must provide **centralized Go helper libraries**:
  * Centralized, type-safe validation helpers (e.g. `safeInt64()`, slug domains).
  * Centralized lock-order guidelines and immutable, defensive-copy view models.
  * Standardized copyguard wrappers.

### 3. Align BAs and Re-align the Pipeline
* **De-bottleneck Testing:** Merge the disjoint Testers into a collaborative code-review flow, or simplify the Destructive-testing loops. Bouncing items 10 times is an expensive waste of AI tokens.
* **Clean up the Criteria:** Direct BAs to stop writing future S11 criteria and instead perform a complete audit of the existing spec contradictions (such as the unreachable debug console path in `ASM-468` and the 4 prose-vs-graph drift lint errors).
* **Shift Focus to Gameplay:** Unblock S3 (MOD-018) by establishing a solid, noise-calibrated performance baseline in CI and then focus the entire team's capacity on building core gameplay systems.
