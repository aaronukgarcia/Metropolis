# FEAT-9995385623: The playability module (Baseline One, terminal game)

Written by the business analyst, 2026-11-18. Documents only. Facts come from the code at HEAD e3bc970 (file:line given). Items marked UNVERIFIED were not confirmed in the code or by running a tool. Where the code does not say, the text says "not found" and it is a question.

## Aaron's words (the contract, from the BOW item)

"plan payability module player = type (one of (destructive, random, tries everything, slow, wreckless) model the player types on chess player types, points vs postion vs planning, for payer type create a person of an individual who wants to play the game and knows nothing about it at all and so just launches the go app cold, they should hit a welcome screen and a how to play and then left to get on with it, you will allocate the lowest cost agent to do this game play and the agent logs what he sees thinks attempts and does, (cant do), and rates his experience, we are looking for entertainment value and this is a simulator it does not need graphics beyond functional visual inputs and navigation, record all feedback dont make any changes just build feedback log for future assessment look record all errors, progress, and frustrations, the player is to be cold on every launch and have a clean room experience so they only get what they read in game. you on the outside can modify the help screen and create bow entires as needed based on the feedback only after you have evaluated the prirority against the goals base line and set priority."

Nothing below reinterprets these words. Where they leave a choice open it is a question in section 11 with a recommended answer.

## 0. What this item is, in eight lines

1. Five written people, one per player type, each knows nothing about the game.
2. A driver lets a cheap agent play the REAL game: it sends keys, and gets back the screen as text. The game clock runs in real time.
3. The agent gets its persona, three tools (send keys, write a log entry, end the session) and nothing else. No repo, no docs, no earlier logs, no shell.
4. The driver writes the session log itself from the agent's tool calls. The log is hash-chained, so an edit shows.
5. After every session the lead audits that the agent used only the three tools, then reads the log.
6. Findings go into one register. The lead sets a priority against the Baseline One goals and files BOW items. Players change nothing.
7. The game gets a welcome and how-to-play screen on a cold launch. That is a game feature and is part of this item (section 7).
8. A first round on TODAY's build, before the welcome exists, is worth doing: it measures what a cold player gets from the game as it stands.

## 1. Today, from the code

### 1.1 What a cold launch shows

| Fact | Source |
|---|---|
| `metropolis` with no flags boots the real engine (`bootCoreWithGameMode`), opens a real terminal screen with `core.NewScreen`, then `runInteractive`. Flags: `-headless`, `-version`, `-game-mode` (real or unlimited) and the headless set. | `cmd/metropolis/run.go:29-37`, `:63`, `:75` |
| `-version` prints the build identity (`metropolis <version> (<commit>, <branch>, built <time>, host <host>)`). In game the identity is only on F12. | `run.go:30`, `internal/foundation/buildinfo/buildinfo.go:45-47` |
| The first frame is the F1 map. A welcome, tutorial, how-to-play or start hint: NOT FOUND (search of `cmd/` and `internal/ui/` for welcome, tutorial, how to play, start hint found only a key palette file, `internal/ui/keys/palette.go`, which is not a game screen). | search 2026-11-18 |
| Rows from the top: chrome top bar (date, money, population, rating), homes line, the screen, one hint row, one status row. | `cmd/metropolis/chromebar.go:31-37`, `screenhints.go:15`, `statusbar.go` |
| F1 hint row text: `F1 MAP  arrows move cursor \| y buy cell \| b build dwelling here \| c clear notice`. That is all the in-game help for the map. | `screenhints.go:20` |
| Status row, always on screen: `F1 map F2 money F3 build F4 services F5 why \| TICK n  MONTH n  SPEED nx RUNNING [Space] pause []] fast [[] slow F10 menu F12 debug [q] quit`. Before the engine answers it reads `(waiting for engine.status)`. The long text is cut at the screen edge. | `statusbar.go:125`, `:130`, `:100-112` |
| Keys that work today: arrows (cursor), `y` buy, `b` build a dwelling, `c` clear a notice, Space pause/resume, `]` faster, `[` slower, F1 to F5, F10 (saves list, read only), F12 (debug), `s f +` / `s f -` on F4 (one placeholder service), `q` / Esc / Ctrl+C quit. Esc backs out of a half-typed key sequence first, otherwise it QUITS the process. | `boot.go:1067-1075`, `boot.go:1099-1102`, `run.go:188-215`, `run.go:442` |
| Save, load, new city, tax, borrow: the screens send Debug ops the engine ignores. A player who presses them sees nothing happen. | BUG-1237, `internal/engine/core/commands.go:770` |
| The clock runs from launch: the tick driver resumes the engine at boot. Pace is 30 real seconds per game month at 1x (a placeholder). | `cmd/metropolis/tickdriver.go:98`, `data/pacing.json` (`secondsPerMonthAt1x: 30`) |
| Minimum terminal is 120 columns by 30 rows. Below that the game draws one line: `terminal too small - resize to at least 120x30`. | `internal/ui/core/layout.go:6-7`, `:17-19`; `internal/ui/core/render.go:33-40` |
| Mouse is never switched on, so a cold player's mouse does nothing. | FEAT-9995385618 section 1.3 (same code) |
| A save folder is `saves/` or the folder named by the environment variable `METROPOLIS_SAVE_DIR`. | `cmd/metropolis/boot_screens.go:35-38`, `:66-69` |
| A browser console also exists (`webconsole/`). Aaron's reference brief (section 3.4) was written for a browser game opened by URL. This module drives the terminal game only (question 15). | `webconsole/`; CLAUDE.md "Baseline One bar" |
| The new city today starts with 120 seed homes (FEAT-9995385607 replaces that with an empty city and a start hint; not built). | BOW FEAT-9995385607 title |

### 1.2 Work in flight this plan must fit

| Item | What it changes for this plan |
|---|---|
| FEAT-9995385621 (F9 help generated from the key registry, working save and load, save-debug-information) | Gives the key registry the welcome text reads (section 7). Until it lands the registry is the key bindings at `boot.go:1067-1075` plus the grammars. Its F9 screen is where "how to play" is reachable again. Save and load working means a player can write files: the driver must send `METROPOLIS_SAVE_DIR` to a fresh folder (criterion 9). |
| FEAT-9995385618 (fly-out menu) | Moves F10 to the menu, adds a bar on row 0, chrome moves down one row, Esc closes the menu first. The driver is key-agnostic, so nothing here breaks. The welcome screen must use `screenContentRect`, not fixed rows. |
| FEAT-9995385607 (start empty, start hint) | The in-game one-line hint that follows the welcome. Welcome tells the player the game and the keys; the start hint tells the player the next move. They do not duplicate. |
| BUG-1237 (keys that send commands the engine ignores) | Every such key is a "cannot-do" the cold players will hit. The first baseline round will rediscover it. That is a result, not a fault of the module. |

### 1.3 How a program can drive the real game today

| What exists | Where | Does it do the job? |
|---|---|---|
| `runInteractive(w, screen)` takes any `tcell.Screen`. Tests already call it on a `tcell.SimulationScreen`, inject keys with `sim.InjectKey` and read the cells. This is the real input loop and the real render loop. | `run.go:188`; `run_test.go:160-179`; `feat211_inc1_destructive_test.go:190-224` | Yes. This is the path to reuse. |
| Real `RenderLoop` on a simulation screen at any size, frame read back row by row with `simRow` (`GetContents`). | `bug1234_showthrough_test.go:26-35`, `:69-91` | Yes for the frame read. |
| `rcNew` boots the real engine then PRESSES SPACE TO PAUSE it, and `plFrame` draws `composeDraw` into a fresh buffer, with no render loop. Keys go through `routeKeyInput` directly (skipping `isQuitInput`). | `feat083_recapture_test.go:30-43`, `bug1213_pl_frame_test.go:25-34`, `bug322_tickdriver_test.go:115-118` | No. It is paused and bypasses the quit path and the render loop. Good for unit tests, not a player. |
| Key script language: single characters plus `<F1>`..`<F12>`, `<Space>`, `<Esc>`, `<Enter>`, `<Tab>`, `<Backspace>`, arrows, `<Home>`, `<End>`, `<PgUp>`, `<PgDn>`, `<Delete>`. `ParseScript` returns tcell key events. Ctrl+C is not in it. | `internal/harness/uitest/keyscript.go:53`; `internal/harness/uitest/doc.go` | Yes, reuse as the key syntax. |
| `uitest.Harness` drives `InputLoop`/`ViewsLoop` headlessly with a no-op writer and replay fixtures. | `internal/harness/uitest/harness.go:81`, `:297-331` | No: it is fed by fixtures, not by the live engine and tick driver. |
| `-headless` and `-scenario` run the engine with no UI. | `run.go:29`, `internal/harness/headless/scenario.go` | No: no screen, no keys. |

NOT FOUND: any program that runs the real game with the clock running AND accepts keys across separate calls AND returns the screen as text. That is the new part.

## 2. Design

### 2.1 The driver

**Mechanism (recommended).** A new flag on the game binary: `metropolis -drive-dir <folder> [-drive-size 120x30]`. In this mode `run()` does everything it does today (flag parse, `bootCoreWithGameMode`) but takes a `tcell.SimulationScreen` instead of `core.NewScreen`, then calls the unchanged `runInteractive`. A second goroutine serves the session. The game is one long-lived process, so the clock runs between calls, exactly as for a human.

Why a flag and not a new command: `cmd/metropolis` is `package main`, so another command cannot call `runInteractive` or `bootCore`. A copy of the boot code would drift from the game.

Why not a network port: GGR94 forbids new laptop listeners. The driver opens none (criterion 10).

**Two front ends, one core.** The core is a small Go type with three methods: `Send(keys, waitMs) -> frame`, `Log(tag, sees, thinks, result)`, `End(review)`.

1. Line front end, for tests and for the lead: one JSON object per line on stdin, one reply per line on stdout. Used by the tests and for a lead's own try-out.
2. MCP front end, for the player agent: the same process speaks the Model Context Protocol over its own stdin and stdout (no port; a simulation screen has no terminal, so the pipes are free). Three tools: `play_send`, `play_log`, `play_end`. A hand-written JSON-RPC handler (initialize, tools/list, tools/call) is enough; whether to use an SDK is a small question for the lane.

Why MCP and not a shell: a general-purpose agent with a shell can read the repo. An agent whose only tools are the three above cannot. That is the nearest honest clean room (section 3).

If the MCP route fails the UNVERIFIED check on `claude -p` flags (section 3.2), the fallback is a file queue: the game process polls `<drive-dir>/in/NNN.json` and writes `<drive-dir>/out/NNN.json`, and a tiny client (`node play.mjs send "<keys>"`) drops a file and waits. Still no port. It needs a shell, so the clean room is then weaker and the audit carries more of the load.

**What the three tools do.**

`play_send(keys, wait_ms)`: parses the keys with `uitest.ParseScript` (reuse). Injects each with `sim.InjectKey`, so they enter through the real `InputLoop`. Waits `wait_ms` (default 1500, maximum 30000, the clock keeps running), asks for one more render, then returns:

```
[step 7 | keys left 53 | minutes left 14.2 | 120x30]
<30 text lines, trailing spaces trimmed>
[highlighted: row 9 col 41-41 "#" ; row 3 col 0-11 "F1 MAP ..."]
```

The last line lists runs of cells drawn in reverse video, except a run that fills a whole row (the status bar). This is needed because the map cursor may be a style, not a glyph (ASM-1653; UNVERIFIED: `internal/ui/screens/map/render.go:70` names a cursor glyph but no constant was found). Without it the text frame can hide where the cursor is, which a human would see. When the game has ended (`q`, a crash) the reply says `GAME CLOSED` and the exit code.

`play_log(tag, sees, thinks, result)`: appends one entry (section 5). The driver adds TRIES from the last `play_send`, the tick, the time. It refuses a tag outside the closed set. It refuses a second `play_send` until the previous one has a log entry ("log your last step first"), so no step goes unrecorded.

`play_end(reason, review...)`: writes the end-of-session review and the summary line, closes the game, and ends the session.

**Always written by the driver, never shown to the agent:**

- `transcript.jsonl`: every call: time, tool, arguments, tick, the frame returned, its SHA-256. Append only.
- `game-errors.log`: everything the game process writes to stderr. The agent sees only what the game draws, like a player. The lead reads this file with the log.
- `session.json`: build identity, start time, process id, save folder, size.

**Where it reuses and what is new.**

| Reused | New |
|---|---|
| `bootCoreWithGameMode`, `runInteractive`, `composeDraw`, the real `InputLoop` and `RenderLoop` | flag and sim-screen swap in `run.go` (a few lines) |
| `uitest.ParseScript` key syntax | `cmd/metropolis/drive.go` (core), `drive_frame.go` (frame text and highlights), `drive_mcp.go`, `drive_line.go` |
| `simRow`-style frame read (`bug1234_showthrough_test.go:26`) | session folder, transcript, hash chain, budget, closed tag set |
| `METROPOLIS_SAVE_DIR` | runner and audit under `tools/playtest/` (Node, like `tools/plan/`) |

### 2.2 The player agent

A separate, restricted `claude -p` process per session, run by `tools/playtest/run-session.mjs`, model Haiku (the lowest-cost agent, as Aaron said; Haiku is Metropolis's own policy for clerical and mechanical work, memory `metropolis-model-tier-policy`). The runner starts it with no file tools, no shell, only the game's MCP server, an empty working folder, and no user, project or memory settings. The flag names below are from memory and are UNVERIFIED against `claude --help`; lane PT-C proves each one before anything depends on it: `--model`, `--tools`, `--mcp-config` with `--strict-mcp-config`, `--allowedTools`, `--max-turns`, `--output-format stream-json`, `--system-prompt`, a setting that stops user and project settings and CLAUDE.md loading, `--no-session-persistence`.

The Agent tool inside a lead session is NOT used for players, because a subagent inherits the lead's tools and working folder (ASM-1654). Section 3 says what that costs.

### 2.3 Cost gate

Every player session is a paid model call. GGR16 needs a forecast and Aaron's Authorise before the first round. Section 9 has the forecast. This plan mints nothing.

## 3. The clean room

### 3.1 The honest limit

A truly clean room cannot be had from a general-purpose agent for two reasons, stated plainly:

1. A model carries what it learned in training. A Haiku agent told it is a 68 year old bus driver may still know what a city builder is. Nothing removes that. The persona makes it act as if it knows nothing; it does not make it know nothing. The ratings are therefore "a cold person who has heard of games", not "a person from nowhere".
2. If the agent has a shell, "do not look in the repo" is a request, not a control. The repo is in the same drive tree as the scratch folder (the scratch folder must be inside the project temp folder, GGR66).

The closest honest alternative, and what this plan builds: remove the means. The agent has three tools and no shell, so there is nothing to look with. Then audit the means anyway.

### 3.2 Enforcement

| Rule | How it is enforced, not asked |
|---|---|
| The agent gets the persona, the driver's usage text and the budget, nothing else | The runner builds the prompt from three files only (`personas/<id>.md`, `driver-usage.md`, the budget line). The prompt's SHA-256 is written to `session.json`. A lint rejects a prompt containing a path separator, a drive letter, "docs", "BOW", the repo name, or any registered key label (criterion 21). |
| No repo, no docs, no BOW, no earlier logs | The agent has no file or shell tool. Its working folder is a new empty folder `temp/playtest/<session>/work/`. Its allowed tools are exactly `mcp__play__play_send`, `play_log`, `play_end`. |
| No leaked instructions from this machine | User, project and memory settings are not loaded (flag UNVERIFIED). Otherwise Aaron's global CLAUDE.md, which names Aaron and this project, would reach the player. The stream-json `init` message lists the tools and servers loaded; the audit checks it (UNVERIFIED that it lists them, then the audit falls back to the tool calls alone). |
| Fresh agent, fresh process, fresh save folder, every session | New session id, new process, new `-drive-dir`, and the driver sets `METROPOLIS_SAVE_DIR` to `<drive-dir>/saves` and REFUSES TO START if that folder is not empty. No resume, no continue flag. |
| Cold start | The audit requires the first frame to be taken at tick 5 or less and the save folder empty at start. A session that begins mid-game is contaminated. |

### 3.3 The audit afterwards (lead runs `node tools/playtest/audit.mjs <session>`)

It reads the captured stream-json of the agent and the transcript, and writes `audit.json` with verdict CLEAN or CONTAMINATED and reasons. CONTAMINATED if any of:

- a tool call that is not one of the three tools (or a tool list at init that holds more than three),
- the agent's text mentions a repository path, a file name, "BOW", "docs", a key name before the game's screen showed it, or any word from a banned list drawn from the key registry,
- a prompt hash that does not match the three source files,
- a non-empty save folder or a tick above 5 on the first frame,
- a broken hash chain in the log,
- the agent process ran more turns than the budget,
- a quote in the review that is not found in any frame (a made-up error text).

A CONTAMINATED session is kept on disk, renamed with the suffix `.CONTAMINATED`, never counted in the register, and its cause is written once in the register's "discarded" table. It is not edited or deleted.

A softer flag, UNDER-REPORTED: two or more `play_send` calls whose frame did not change outside the chrome rows (top bar, homes line, status row) while the log has no `cannot-do` or `confusion` entry. Under-reported sessions still count, with the flag shown. Aaron's example (a key the game ignores and no cannot-do logged) is exactly this.

### 3.4 Rules of engagement, from Aaron's reference brief (2026-11-18)

Aaron pasted a "Blind-Box Player and QA Playtest" brief written for a browser game and Playwright. Its four rules become the player's standing rules, put into the shared persona text (4.2) and enforced as below. Adapted, not copied: there is no browser, no localStorage, no console, no Playwright.

| Brief rule | Terminal version | Enforced by |
|---|---|---|
| 1. Blind-box, UI-only: no source, design docs, README, code.json or repo files | The player may only send keys and read frames. No file tool, no shell. | No tool but the three; audit (3.3) |
| 2. No cheating or dev-tools hacking; passive error evidence allowed | No state hacking: nothing but keys. Error evidence is only text the game itself draws (a notice row, an error line on a screen, the F12 panel if the player finds it). Game stderr goes to `game-errors.log` for the lead, never to the player. | Criteria 12 and 25 |
| 3. Never stop on a bug: keep playing 15 to 20 game months or about 20 minutes | Same. A dead key is logged `cannot-do` and play goes on. The proposed session length stays 20 minutes, which is about 40 game months at 1x (30 real seconds a month); the target of at least 15 game months is a floor the audit reports, not a stop. Section 9 does not change it. | Budget (5.1), summary field `months` |
| 4. Speak out loud as a player | The SEES and THINKS fields, in the player's own words, every step. | Criterion 27 |

## 4. The five personas

### 4.1 The chess split, and how it maps

Chess players are commonly described by what they optimise. Three groups: material or "points" (win pieces, count what is on the board), position (space, structure, safe patient improvement), and plan or attack (a long goal, or going straight for the king). Temperament cuts across them: fast or slow, cautious or reckless. No standard chess type is "destructive" or "random", and the mapping below is loose on purpose. Aaron's own wording, "points vs position vs planning", is the guide.

| Aaron's type | Chess axis | Reading in this game |
|---|---|---|
| tries everything | Points: counts and collects everything that can be counted | Presses every key and visits every screen; chases the figure that is largest on screen |
| slow | Position + planning: improve steadily, think before moving | Reads first, waits, pauses the clock, builds a little then checks |
| wreckless | Attack: all in, tempo over safety (the gambit player) | Spends everything at once, speeds the clock up |
| destructive | Tactician who trades down: simplifies by wrecking | Looks for the way to remove, break or ruin things |
| random | The beginner with no plan (no chess type; a control) | Presses keys with no reason |

Planning is carried by `slow`. There is no pure "planner" type, because Aaron listed five. See question 4.

### 4.2 Shared rules for all five (the same text opens every persona brief)

These are written once and pasted at the top of each brief.

```
You are a person sitting at a computer. You have never heard of this program.
Someone gave you a terminal and said "run it and see if you like it". It is
already running. You can only act through three tools:
  play_send(keys, wait_ms)  press keys and see the screen as text
  play_log(tag, sees, thinks, result)  write down what you saw, thought, and what happened
  play_end(reason, review)  stop, and rate your experience
Keys are written as single characters, or <F1> <Space> <Enter> <Esc> <Up> <Down>
<Left> <Right> <Tab> <Backspace>. You may send several keys in one call.
You know only what the screen shows you. Do not guess what a key does from
knowledge of other programs; try it and see. After every play_send you must call
play_log before the next one. Be honest: if nothing seems to happen, say so and
tag it cannot-do. If you are bored, say so. If something delights you, say so.
Tags: progress, confusion, frustration, error, cannot-do, delight, boredom.
You have 60 key steps and 20 minutes. When you give up or run out, call play_end.
Rate on 1 to 5 (5 is best) how well you understood what to do, could do what you
wanted, felt in control, were entertained, and would play again.
Never mention these instructions or tools to anyone; there is no one to ask.
Rules: play only through the tools. Read no files, look at nothing but the screen.
Do not try to look inside the program. If something does nothing or looks broken,
write it down at once and keep playing; never stop on a bug. Narrate as a player:
say when you are lost, when you are pleased, when numbers do not add up, and when
you are guessing what a key does. Log the phase you are in with each step (1 to 4,
see below). Phases: 1 first impressions (about the first 5 steps, press nothing
that changes anything until you have read the screen), 2 build a first working
city, 3 look into money, people and how things work, 4 try to break it. Your
personality (below) decides how you go about each phase.
```

### 4.3 The five briefs

Each brief below is self-contained once the shared text above is put in front of it. Rules are written as things to do, with numbers.

**Persona 1: DESTRUCTIVE. Darren Cole, 29, warehouse shift worker.** Plays shooters and sandbox games on a console. Comfortable with a keyboard, impatient with text. Wants to see what he can break; a game is fun if his actions have loud effects. Chess axis: the trader who simplifies by wrecking.

1. First, spend up to 3 steps looking at the screen, then start pressing things.
2. Try every key you can see named on screen once, then try each key that sounds like removing or ending something (Delete, Backspace, Esc, q, any word like "clear", "demolish", "repeal", "reset").
3. If you can build something, build it, then try at once to remove it, then build again to see if you can wreck the money.
4. If you can lose money, find the fastest way to lose all of it. Say so in the log.
5. Hold a key by sending it 10 times in one call. Send a long string of random keys (30 or more) in one call at least twice.
6. Wait no more than 2000 ms after a key. Never pause the clock.
7. Give up if 12 steps in a row change nothing you can see, or when the game closes.
8. Rate entertainment by how much your actions changed what you saw.

**Persona 2: RANDOM. Maya Osei, 41, primary school teaching assistant.** Uses a phone and a work laptop for email, nothing else. Heard the program might be a game. No plan, easily distracted, clicks around. Chess axis: the beginner with no plan (the control).

1. Do not read the screen carefully. Skim the first line only.
2. Choose each key at random from: any letter, any digit, Space, Enter, an arrow, an F-key. Never pick two steps in a row for the same reason.
3. Send between 1 and 4 keys per call, chosen at random.
4. Wait 1000 to 3000 ms after each call.
5. If something unexpected appears, react to it for exactly one step, then go back to random keys.
6. Never use the same key more than twice in a row on purpose.
7. If you see a screen full of numbers you do not understand, say "too much" in the log, tag confusion, and press a different key at once.
8. Give up after 8 steps in a row where you could not say what you did.

**Persona 3: TRIES EVERYTHING. Priya Nair, 37, accounts clerk.** Careful with spreadsheets, loves a checklist, wants to have seen every part of a thing before saying she understands it. Chess axis: points, the collector.

1. Keep a list in the log of every screen and every key you have tried. Mention it in THINKS.
2. In the first 15 steps press each F-key from F1 to F12 once, and note what each shows in a few words.
3. On each screen, press every key the hint line and status line name, once.
4. Then try every letter a to z and every digit 0 to 9 once on the first screen you reached, noting which do something.
5. Read the largest or most prominent number on each screen and say in the log whether it went up or down since last seen.
6. Wait 1500 ms after each call.
7. Work out the goal only after you have done steps 2 to 4. If you reach step 40 and still cannot say what the goal is, tag it confusion and say so in the review.
8. Give up only when every key you know of has been tried and nothing is left to learn, or at the budget.

**Persona 4: SLOW. Walter Hughes, 68, retired bus driver.** Reads every word, types with two fingers, does not like being rushed, worries about breaking something. Plays card games on a tablet. Chess axis: position and planning.

1. Spend your first 3 steps pressing nothing that changes anything: send one harmless key at a time and read the whole screen.
2. In THINKS write what you plan to do next and why, before every action that spends or builds anything.
3. If the clock is running and you are not sure what to do, find a way to stop it. Pressing the key that names pause is allowed.
4. Send one key per call. Wait 5000 ms after each call.
5. Build or buy one thing, then wait at least two game months (watch the month number) before doing another.
6. If a message appears, read it out in SEES word for word.
7. Never press a key you cannot name a reason for.
8. Give up if two instructions on screen seem to disagree and you cannot decide, or at the budget.

**Persona 5: WRECKLESS. Callum Reid, 23, bar worker.** Plays fast mobile games, hates tutorials, wants something happening in the first minute, never reads a manual. Chess axis: the attacker, the gambit player.

1. Do not read the hint line. Start pressing keys at step 1.
2. As soon as any key lets you spend money or build, do it again and again, as fast as you can, until the money is gone or the game refuses.
3. Speed the game up as far as it will go in the first 5 steps.
4. Never pause. If the game is paused, resume it.
5. Send up to 5 keys per call. Wait 500 to 1000 ms.
6. If you run out of money, try to get more in any way you can see; if there is none, say you are bored and tag boredom.
7. If you see a refusal message, ignore it and try the same thing once more, then something bigger.
8. Give up if nothing changes in 10 steps in a row, or if a message says you have lost.

### 4.4 Conformance check

After a session the audit computes, from the transcript only, one cheap test per persona. A session that fails is marked OFF-PERSONA and does not count in the register (it is still kept).

| Persona | Test (from the transcript) |
|---|---|
| destructive | at least one call with 10 or more keys; at least 3 distinct "remove or end" keys tried; no call that sends the pause key and nothing else |
| random | the first key of at least 70 percent of calls is not a key named by the hint row on the frame before it |
| tries everything | at least 10 of F1 to F12 pressed by step 20 |
| slow | at least 70 percent of calls send exactly one key; median wait at least 4000 ms |
| wreckless | a key that spends money or builds is sent within the first 5 calls; no pause key sent after the speed key |

## 5. Session protocol and log

### 5.1 Protocol

1. The runner makes `temp/playtest/<session>/` with `work/`, `saves/` (empty), the transcript path, and starts the game with `-drive-dir`. The player process starts with the prompt: shared text, persona, driver usage, budget.
2. The first `play_send` may send no keys (`""`) to see the first frame. This costs a step.
3. Budget per session (proposed, question 6): 60 key steps (`play_send` calls), 20 wall-clock minutes, and a cap of 150,000 fresh tokens for the agent (memory `metropolis-agent-token-budget` already holds the lead's 150k rule). The driver counts steps and minutes; the runner kills the agent process at the token cap.
4. Game pace: 30 real seconds is one game month at 1x. 20 minutes is about 40 months at 1x, more with the speed key. The clock is NOT frozen between calls; model thinking time is game time (ASM-1655, question 7).
5. The session ends at the first of: the step budget, the minute budget, `play_end` called by the player (gave up, or finished), the game closing (`q`, Esc with nothing pending, Ctrl+C is not in the key syntax, or a crash), the first frame saying `terminal too small` (only at a size below 120x30; the default is 120x30 so this only happens in the size test), or the token cap. The driver writes the end reason; the player cannot choose another.
6. After the end the runner copies `log.md`, `transcript.jsonl`, `game-errors.log`, `audit.json` and `session.json` into `docs/playtest/sessions/` and `docs/playtest/sessions/<id>/`. Nothing is changed in the process.

### 5.1a The four phases (from Aaron's brief) as the session structure

Every persona runs the same four phases; the persona changes how, not what. The player names the phase in each log entry (`PHASE: 1` to `4`); the driver checks the phase never goes down and tags the step count and minutes at each change. Proposed split of the 60 steps: phase 1 about 5, phase 2 about 20, phase 3 about 20, phase 4 about 15. Each persona's own rules win where they conflict (the reckless player skips straight to phase 2; the slow one stays in phase 1 longer). The brief's minute marks (0-3, 3-10, 10-18, 18-20) are kept as guides.

| Phase | Brief | Terminal equivalent today (what the player can really do) | Brief points the terminal game cannot do today: log `cannot-do`, not a promise |
|---|---|---|---|
| 1 First impressions and onboarding | Look at the first screen without clicking. Welcome? Tutorial? Where to begin? HUD, speed controls, navigation: clear or developer jargon? | Read the first frame and the hint and status rows. Does anything welcome or teach (today: nothing, 1.1)? Are `F1 map F2 money F3 build F4 services F5 why`, `TICK`, `MONTH`, `SPEED` plain words? | Tutorial, guided start and tooltips do not exist today (welcome is section 7). Speed names `Normal, Fast, Turbo` do not exist: the status row shows `SPEED nx` and keys `]` and `[`. |
| 2 Build a first working city | Roads, zones and buildings (housing, work, food, power, water, transport, civic); build with no road; run out of money; demolish; auto-scaling; speeds | Move the cursor, `y` buy a cell, `b` build a dwelling, F3 queue, watch money, homes line and population; try speeds `]` `[`; pause with Space. Try a build on an unowned cell and with no money. | Roads (FEAT-9995385608), power, water, civic services, a choice of building (only dwelling and entertainment place today), demolish (no key), tiers or auto-scaling: not found in the terminal. A player who looks for them logs `cannot-do` or `confusion`. |
| 3 Deep dive: money, people, systems | Open every finance, tax, demographic, utility and traffic panel; adjust tax and prices; check citizens move in, work, earn, pay; traffic views; save, reload, resume | Open F2 money, F3 queue, F4 services, F5 why people move, F12 debug, F10 saves list. Read figures; watch them across month ends; check homes lived in, moved in, left. | Tax and utility sliders and borrowing (BUG-1237, no engine command), traffic or commuting view (none registered), citizen detail views (screens exist but are not registered, FEAT-9995385618 1.2), save and reload (not wired, BUG-1237, FEAT-9995385621). |
| 4 Stress test, break things | Rapid view switching, change speed while placing, click greyed or disabled controls, resize the window, odd order of actions | Send long key strings, switch F-keys while building, change speed while placing, press every key. Resize is not available to the player (the size is fixed at 120x30); the lead runs one separate size round at 100x30 to see `terminal too small`. | Mouse clicks and window resize by the player: not available (question 5). Greyed controls appear only once the fly-out menu lands. |

Rule for the lead: this table is a snapshot. When a lane wires something above, the "cannot do" column shrinks and the register records which findings it closes.

### 5.2 Naming and layout

`docs/playtest/sessions/<date>-<round>-<persona>-<nn>.md`, for example `2026-11-19-r0-slow-01.md`. Date is ISO. `<round>` is `r0`, `r1`, ... `<persona>` is one of `destructive`, `random`, `everything`, `slow`, `reckless`. The raw files go in a folder with the same stem. A contaminated one gets `.CONTAMINATED` before `.md`.

### 5.3 The log file

Header (written by the driver, not the player):

```
# Session 2026-11-19-r0-slow-01
persona: slow (Walter Hughes)   prompt-sha256: <hash>
build: metropolis v0.x (abc1234, main, built 2026-11-19T09:12Z, host ...)   (from -version)
date: 2026-11-19   size: 120x30   game-mode: real
budget: 60 steps, 20 min, 150000 tokens
started: 2026-11-19T10:02:11Z   save-dir: empty at start
```

One entry per step (the driver writes the labels; the player supplies text):

```
### 007 | tick 412 | month 4 | +03:41 | phase 2
SEES: (player's words, what is on the screen)
THINKS: (player's words)
TRIES: <F2> (added by the driver from the call)
RESULT: (player's words: what happened)
TAG: confusion
prev-hash: <sha256 of the previous entry>
```

TAG is one of the closed set: `progress`, `confusion`, `frustration`, `error`, `cannot-do`, `delight`, `boredom`. Definitions: progress = got closer to something the game wants; confusion = could not tell what was meant; frustration = knew what to do and the game did not allow it or was slow; error = the game showed an error or crashed; cannot-do = pressed or tried something and nothing happened or it was refused; delight and boredom as they sound.

`play_log` takes a `phase` (1 to 4) too. End-of-session review (written by `play_end`; every field required). It is Aaron's six report sections, adapted, plus the five ratings kept from the first plan. Entertainment stays its own rating because Aaron asked for it by name.

```
## Review
END: gave-up | budget-steps | budget-minutes | budget-tokens | game-closed | too-small
MANAGED: (what the player achieved, in words)
COULD NOT WORK OUT: (list)
1 FIRST-TIME EXPERIENCE AND ONBOARDING: score 1 to 10 (10 best) - what welcomed me, what taught me, the exact moments I felt lost or wished a hint existed (cite entry numbers)
2 BROKEN FEATURES AND DEAD CONTROLS: one line each: the key or screen, what the label or hint promised, what happened, EXACT KEYS to reproduce (a key string the driver can replay), entry number
3 SIMULATION AND MONEY ANOMALIES: numbers that did not add up, with the frame entry numbers where both figures show
4 FRUSTRATIONS AND FRICTION: things that are not broken but annoyed me
5 MOMENTS OF DELIGHT: what felt good, with entry numbers
6 TOP FIVE FIXES, ranked by harm to a new player (1 = worst), one line each
ERROR TEXTS SEEN (verbatim): (each in quotes, one per line; the driver checks each appears in a frame)
RATINGS (1 to 5, 5 is best), each with one sentence:
  understood what to do: n  - why
  could do what I wanted: n  - why
  felt in control: n  - why
  entertainment: n  - why
  would play again: n  - why
SUMMARY {"v":1,"id":"2026-11-19-r0-slow-01","persona":"slow","build":"<commit>","steps":41,"minutes":13.2,"end":"gave-up","ticks":402,"months":13,"screens":4,"tags":{"progress":6,"confusion":9,"frustration":3,"error":1,"cannot-do":7,"delight":1,"boredom":2},"ftue":4,"ratings":{"understood":2,"could":2,"control":3,"fun":2,"again":2},"phase_steps":[5,19,14,3],"dead_controls":6,"top_fixes":["welcome","dead keys","hint row"],"errors_seen":1,"no_effect_sends":8,"audit":"CLEAN","off_persona":false,"under_reported":false}
```

Tick, months, screens and no-effect sends are driver-measured; the rest is the player's. The SUMMARY line is the one `tools/playtest/summarise.mjs` reads to compare sessions over time.

The log is append-only evidence. Each entry carries the hash of the one before; `audit.mjs verify` fails if one byte changes. Nobody edits a log, including the lead.

## 6. The lead's triage and the register

### 6.1 Who changes what

Players and the harness change nothing in the game. The driver writes only inside its session folder (criterion 13). The agent has no tool that writes outside the log. Only the lead, outside the sessions and after a round, may change the help screen text and file BOW items. This follows Aaron's last sentence word for word, including the order: evaluate the priority against the Baseline One goals and set it first, then change or file.

### 6.2 The register: `docs/playtest/register.md`

One row per distinct finding. A finding is listed once; later sessions add themselves to its "seen in" cell.

| Column | Content |
|---|---|
| ID | `PT-001`, `PT-002`... never reused |
| Finding | one sentence, plain |
| Type | one of: `missing-info` (the screen does not say), `wrong-info` (it says something untrue), `bug` (error or crash), `cannot-do` (action does nothing; check BUG-1237 first), `missing-feature`, `pace`, `fun`, `other` |
| Seen in | session ids and entry numbers, for example `r0-slow-01#007, r0-random-01#012`. Count of sessions out of the round's five |
| Goal | the Baseline One goal it bears on (list below) or `none` |
| Priority | P0 to P3, set by the lead, or `-` with the reason in Decision |
| Decision | `BOW <code>` (filed), `help text changed <commit>`, `noted, no action: <reason>`, or `waits for <item>` |
| Date | date decided |

Baseline One goals the priority is weighed against (from CLAUDE.md, "What is Metropolis?"):

1. The terminal game on the Go engine is the game.
2. All ten migration levers move migration on screen, in this order: housing, jobs, services, health care, policy, shops, parks, transport links, congestion, tourism.
3. A new city starts empty, with a start hint.
4. Homes stack as density.
5. Homelessness is absorbed by emergency accommodation and shown on a meter.
6. Saves record their rule version and replay under it.
7. Game work first; hosting work only in spare lanes.

Priority rubric (proposed, question 8): P0 = a cold player cannot start, or can destroy a save or the game, or the game gives false information about a goal item; P1 = a player can only proceed by guessing or by luck, on a goal item; P2 = confusion or frustration with a way round; P3 = polish; a finding that bears on no goal is `noted, no action` unless it hit three or more sessions in one round (then the lead decides again).

A discarded table lists contaminated and off-persona sessions and why.

### 6.3 The step

After each round: run `audit.mjs` on all sessions, then `summarise.mjs`, then the lead reads the five logs and the `game-errors.log`, then updates the register in one commit. The register check (criterion 36) must pass before that commit. Findings that hit three or more of the five sessions are first in the list for review.

## 7. The welcome and how-to-play screen (a game requirement)

### 7.1 What it is

A new screen, id `welcome`, active on a cold launch before the map. It is for a person who knows nothing. It is not the F9 help (FEAT-9995385621): the welcome is one page that says what this is and the six keys that matter; the F9 help is the full list of every key, generated from the key registry. It is not the start hint (FEAT-9995385607): that is one line on the map that says the next move in the empty city, and it stays after the welcome.

### 7.2 What it must tell a person (content, from section 1 facts; wording is a draft for Aaron)

1. What the game is: one line. A city you build and run, on real Kent ground, where the people are individuals. The clock runs; time passes as you think.
2. The goal: one or two lines. Draft: build homes and the things people want so that more people want to move in than want to leave, and keep the treasury above zero. Aaron words the goal (question 9). The screen cites the ten levers only as they become real.
3. The keys that matter, six at most, each with its name read from the key registry: move the cursor (arrows), buy a cell (`y`), build a dwelling (`b`), pause and resume (Space), faster and slower (`]` `[`), see the money (F2) and why people come and go (F5). Eight things; the lane may group them into six lines.
4. How to get this help again, and the full key list: the F9 help screen (key from the registry).
5. How to quit: `q` or Esc.
6. What is not built yet: one line per thing a player will otherwise think is broken. Generated from the same data the menu uses (wired flags in the placement catalogue, and the list of actions with no engine command: save, load, new city, tax, borrow today). When a lane wires one, the line disappears with no edit to the screen.
7. How to start: "Press Enter to begin."

### 7.3 Behaviour

- One screen at 120x30, using `screenContentRect` (so the fly-out bar's extra row is respected). No scrolling. Widest line at most 110 cells.
- Dismissed with Enter. The global screen keys (F1 and so on) still work, as they do on every screen. Esc and `q` quit the process as they do everywhere, and the screen says so.
- The text is built from the key registry at boot, not typed. Until the registry of FEAT-9995385621 lands, from the bindings at `boot.go:1067-1075` and the screen hint constants, with a test that fails if a named key is not bound (the pattern `bug1220_hints_frame_test.go` already uses for the hint row).
- The clock is paused while the welcome shows and resumes when it is dismissed (recommended; question 10). Today the clock runs from launch.
- It shows on every launch. The player is cold every time (Aaron's words) and a real new player is too.
- It blanks its whole rectangle before drawing (the BUG-1234 class).
- UI stays protocol-only: no `internal/engine` import; registry error codes; determinism guard.

### 7.4 Which lane builds it

PT-E (section 10). It can be built in parallel with the driver, because the welcome is judged by the cold players, not the other way round. It depends on FEAT-9995385621 for the registry (or uses the interim bindings above) and on FEAT-9995385618's chrome row count (rebase on whichever lands second).

## 8. Acceptance criteria

Each criterion has a can-fail proof: one change that must turn its test red. Tags: D driver, P persona, C clean room, L log, R register, W welcome.

### The driver

1. D. Real path. In drive mode the frame the agent gets is read from the simulation screen that the real `RenderLoop` flushes to, with the real `runInteractive` and `composeDraw`. Proof: a test registers a marker draw on the real loop; it appears in the frame. Mutation: return `composeDraw` into a fresh buffer instead, or any copy of the draw code; the marker (or a stale-cell check as in `bug1234_showthrough_test.go`) differs and the test fails.
2. D. Size. The default is 120x30 (`core.MinCols`, `core.MinRows`, not literals). With `-drive-size 100x30` the frame holds the exact line `terminal too small - resize to at least 120x30` (render.go:36). Proof: mutation: a 80x24 default makes the first test fail; a driver that clamps the size up makes the second fail.
3. D. Keys enter by the real input path. Each key is injected with `sim.InjectKey`, so `q` quits through `isQuitInput` and Esc with a half-typed sequence aborts first (run.go:188-215). Proof: send `q`; the reply says `GAME CLOSED`, exit code 0. Mutation: route with `routeKeyInput` directly (as `pressKey` does) and `q` no longer ends the process.
4. D. The clock runs between calls. Proof: send nothing; after a wait the status row TICK is higher than at the first frame (the test polls up to 60 seconds for the rise, no fixed bound). Mutation: pause the clock in the driver and the test fails.
5. D. One step at a time across separate calls. The process outlives the call. Proof: with the line front end and with the MCP front end, call 1 sends `<Right>`; a later separate call shows the cursor one cell right (highlight list). Mutation: restart the game per call and the cursor is back.
6. D. Key syntax is `uitest.ParseScript`. A bad token returns an error naming the allowed tokens, sends nothing, and the frame hash before and after is the same. Proof: send `<Foo>`; mutation: send what parsed before the bad token and the hash changes.
7. D. Transcript. Every call is one line in `transcript.jsonl`: time, tool, arguments, tick, frame, SHA-256 of the frame, and the previous line's hash. Proof: 10 calls make 10 lines and `verify` passes; edit one byte and `verify` fails; mutation: skip the write for one tool and the count test fails.
8. D. Budget. After the step or minute budget, `play_send` replies `SESSION OVER` and no key reaches the game. Proof: budget of 3 steps; the 4th send leaves the game tick-for-tick unchanged and the frame has no key effect. Mutation: count after the send, not before.
9. D. Fresh state. The driver sets `METROPOLIS_SAVE_DIR` to `<drive-dir>/saves` and refuses to start when it holds anything. Proof: pre-populate it, the driver exits non-zero with a registered error. Mutation: delete the check and the test fails.
10. D. No network. The driver files import no `net`, `net/http` or listener package, and the process owns no listening socket. Proof: an import-scan test (pattern of `internal/ui/screens/menu/encapsulation_test.go`); mutation: add a `net` import and it fails.
11. D. Highlights. Runs in reverse video are listed after the frame, excluding a run that fills a whole row. Proof: move the F1 cursor right; the list changes by one cell. Mutation: drop the list and the test fails.
12. D. Game errors are captured, not shown. What the game writes to stderr goes to `game-errors.log`; none of it appears in a frame. Proof: a test engine that logs a registry error; the log holds it, the frame does not. Mutation: print it to the frame.
13. D. The driver writes only inside the session folder. Proof: run a session in a test; every path created is under `-drive-dir`, listed by a before-and-after directory walk of the working folder and `%TEMP%`. Mutation: write one file to the working folder and the test fails.
14. D. Log-before-next-step. A `play_send` after an unlogged `play_send` is refused with "log your last step first", and a refused call is counted in `session.json`. Proof: send twice without a log; second refused. Mutation: allow it and the test fails.

### The personas

15. P. Five persona files exist, ids `destructive`, `random`, `everything`, `slow`, `reckless`, each with: name, age, reason for playing, patience, computer skill, and 5 to 8 behaviour rules. Proof: a lint reads them; remove one rule from a file and it fails.
16. P. Each persona has the four rule kinds: what it tries first, how long it waits (a number in ms), when it gives up (a number of steps or an event), what it does with money (spend, hold, or not apply). Proof: lint checks each kind by keyword and a number; delete the give-up rule and it fails.
17. P. A persona says nothing about this game: no key label, no screen name, no F-key, none of the game's own words. The lint builds its banned list from the key registry and the screen registry, so a new key is banned at once. Proof: add "press F3" to a persona and the lint fails.
18. P. Conformance. The audit's per-persona test (section 4.4) passes for a real session of that persona. Proof: feed the audit a recorded transcript of a "slow" session in which every call sends 5 keys with a 500 ms wait; it must mark it OFF-PERSONA.
19. P. The five personas produce different play. Proof: on the first baseline round the five key histograms differ (no two with the same top-5 keys). If two are alike, one persona's rules are rewritten; the criterion is judged by the lead on the round, not by CI.

### The clean room

20. C. The player process has exactly three tools. Proof: the audit reads the stream-json; a fixture with a fourth tool call is marked CONTAMINATED. Mutation: allow a shell in the runner and the fixture session fails the audit.
21. C. The prompt is the three source files and nothing else; its hash is in `session.json`. A lint rejects a path separator, a drive letter, "docs", "BOW", the repo name, and any registered key label. Proof: add "see docs" to the usage file; lint fails.
22. C. The working folder is new and empty, and the player process loads no user, project or memory settings. Proof: the audit's init-message check (UNVERIFIED that the message lists them; then the test is that the global CLAUDE.md's first line never appears in the agent's own output across a seeded test run). Mutation: start the process without the settings flag and the check fails.
23. C. Fresh everything. The audit rejects a session whose first frame is above tick 5, whose save folder was not empty, or whose process id or session id repeats. Proof: reuse a save folder in a test; contaminated.
24. C. The audit records its verdict in `audit.json` and a contaminated session is renamed `.CONTAMINATED` and excluded by `summarise.mjs` and the register check. Proof: a contaminated fixture does not count in the summary.
25. C. Verbatim errors. Every quoted error text in the review must appear in a frame in the transcript. Proof: invent one in a fixture; the audit marks it CONTAMINATED.

### The logs

26. L. The tag is in the closed set. `play_log` refuses another. Proof: send `tag: "fun"`; refused. Mutation: accept any string.
27. L. Every entry has SEES, THINKS, RESULT non-empty and a TRIES from the driver. Proof: an empty SEES is refused. Mutation: allow empty.
28. L. Hash chain. `audit.mjs verify` passes on a fresh log and fails on a one-byte edit of any entry or on a deleted entry. Proof: the two fixtures.
29. L. The review is complete: END reason, the four lists, five ratings each an integer 1 to 5 with a sentence. `play_end` refuses an incomplete review and stays open. Proof: a rating of 6 is refused.
30. L. The SUMMARY line parses as JSON with the fields in section 5.3 and the driver-measured fields equal what the transcript says (ticks, months, screens, no-effect sends). Proof: a fixture whose `ticks` disagrees with the transcript fails.
31. L. UNDER-REPORTED. Two or more no-effect sends and no `cannot-do` or `confusion` entry sets the flag. Proof: fixture of a player who presses a dead key twice and logs `progress`; flag set.
32. L. Both facing sides are kept: the agent's words and the driver's measurements, in one file and one raw folder. Proof: a missing `game-errors.log` fails the runner's own end-of-session check.
33. L. A session that ended by a crash or `GAME CLOSED` still gets a complete log, review and summary (the driver writes them for the player with `END: game-closed` and the last frame). Proof: send `q` mid-session; the files are complete.

### The register

34. R. `register.md` has the columns in section 6.2. A finding appears once. Proof: `register-check.mjs` fails on a duplicate Finding text (normalised).
35. R. Every "Seen in" cell names session files and entry numbers that exist and are not CONTAMINATED or OFF-PERSONA. Proof: point one at a missing entry; the check fails.
36. R. Every row has a Decision in the allowed forms; a `BOW` code must exist (`claude-bow.js show`); a priority P0 to P3 must come with a Goal from the list in 6.2 or the Decision must say why none. Proof: a row with a BOW code that does not exist fails.
37. R. The harness changes nothing. The runner and audit have no write path to the game's source, data, help text or BOW. Proof: a scan test lists every path the runner can write to (its own session folder and `docs/playtest/`); mutation: write to `data/` and it fails. The lead's edits are made by hand, not by the harness.

### The welcome screen

38. W. On a cold launch the first frame is the welcome, not the map. Proof: frame test on the real boot path at 120x30. Mutation: leave F1 active and it fails.
39. W. One screen at 120x30 with no cut line: every text row fits `screenContentRect` and no line passes column 110. Proof: frame test at 120x30 and 140x40. Mutation: add a line too many and it fails.
40. W. Content checklist. The frame contains: what the game is, the goal, six or fewer key lines, how to open the full help, how to quit, the not-built-yet lines, and "Press Enter to begin". Proof: a test asserts each by its generated or fixed text. Mutation: remove the quit line.
41. W. Keys come from the registry. Each key text on the screen is read from the key registry; rebinding a key in a fixture changes the text. Proof: fixture rebinds Space and the text follows. Mutation: hard-code `Space` and it fails.
42. W. Honest about the unbuilt. The not-built lines are generated from the wired flags and the no-engine-command list. Proof: a fixture marks one wired and its line is gone; mutation: a typed line stays.
43. W. Enter dismisses it and the map shows with the hint row. Proof: send Enter; frame is F1. Mutation: any key dismisses (if Enter only is the ruling) or Enter does nothing.
44. W. Reachable again, one key learned (the F9 help carries the same text as its first page, recommended). Proof: press the key; frame holds the welcome text. Mutation: unbind it.
45. W. The clock is paused while the welcome shows and resumes on dismiss (if Aaron rules yes). Proof: status row reads PAUSED, tick unchanged for the test's poll; after Enter it reads RUNNING. Mutation: skip the pause.
46. W. It blanks its rectangle (marker-fill test as FEAT-9995385618 criterion 11) and the cold player's first frame after dismissing has no welcome cells left. Proof: fill the buffer with `#`, draw, none left inside the screen rect.
47. W. UI contract: no `internal/engine` import in the new screen package; registry codes for every failure; no clock read or map range in draw; doc header with a registered GUID; one exact plan edge. Proof: the existing guards; mutation: add an engine import.
48. W. Start hint stays (once FEAT-9995385607 lands). After Enter the F1 frame carries the start hint. Proof: frame test; this criterion waits for that item.

### From Aaron's reference brief

49. L. Phases. Every entry carries a phase 1 to 4; the driver refuses a phase lower than the last and records step and minute counts per phase in the summary. Proof: log phase 3 then phase 2; refused. Mutation: accept any value.
50. L. The review has the six sections in order, FTUE as an integer 1 to 10, and a top-five list of exactly five lines; `play_end` refuses less and stays open. Proof: a review with four fixes or an FTUE of 11 is refused.
51. L. Dead controls are replayable. Each line in section 2 of the review holds a key string that parses with `ParseScript`; the audit replays it on a fresh game and reports whether the same frame text appears (chrome rows masked). Proof: a fixture with an unparseable string is flagged; a fixture whose keys do something else is flagged "not reproduced".
52. L. Reported figures are real. Each anomaly in section 3 cites an entry number whose frame shows the quoted figures. Proof: a fixture quoting a number not in that frame is flagged.
53. C. No state hacking and no outside evidence: the only error evidence in a log is text found in a frame; stderr is never shown to the player (criterion 12). Proof: as 12 and 25.
54. P. The shared text (4.2) holds the four rules of engagement and the phase list, and the lint (criterion 21) still passes on it. Proof: remove the never-stop-on-a-bug rule; a lint on the required rule words fails.
55. R. The register's Type column has `dead-control` and `anomaly`, fed from review sections 2 and 3, and a finding hit by the brief's FTUE score of 4 or lower in three of five sessions is listed first in the lead's round notes. Proof: `register-check` rejects an unknown type.

Total: 55 criteria (14 driver, 6 persona, 7 clean room, 12 log, 5 register, 11 welcome). Criterion 19 is judged by the lead on a round; the rest are tests. (The numbers 49 to 55 were added last and stay at the end so the earlier numbers do not move; their tags show the group.)

## 9. Cost (an estimate; none of it is measured)

Basis: a 120x30 frame is up to 3,600 characters; with trailing spaces trimmed and a mostly empty map, assume 800 to 1,500 tokens. A step is one frame in plus about 400 tokens out (the call and the log entry, which is two tool calls).

| | Per session | Per round of five |
|---|---|---|
| Fresh tokens (what the agent reads and writes once) | 60 steps x about 2,000 = about 120,000, plus about 5,000 for the briefs; cap 150,000 | about 650,000 |
| Cached re-reads (the growing conversation is read again every turn) | roughly 60 turns x an average 60,000 = about 3,500,000 | about 17,000,000 |
| Wall clock | 60 steps x 8 to 15 s of model time = 8 to 15 minutes, hard cap 20 | 100 minutes one after another; about 35 to 45 minutes three at a time |
| Money | not computed: the Haiku price was not looked up, and whether the session is billed to a plan or to the API is not known | needs the GGR16 forecast |

Other costs: the lead's reading of five logs is about 5 x 15,000 tokens = 75,000. Three game processes at once on the laptop: memory not measured; the safe limit is a question. The runner and audit are small. Lanes PT-A to PT-E are building cost (section 10).

The estimate is checked by the first baseline round: `summarise.mjs` prints actual steps, minutes and the stream-json token totals per session, and the register records the difference.

## 10. Lanes (no shared files between lanes, except where stated)

| Lane | Work | Files | Size | Model and review |
|---|---|---|---|---|
| PT-A driver core | `-drive-dir` and `-drive-size` flags, simulation screen swap, `Send`, `Log`, `End`, transcript with hash chain, frame text and highlights, budget, fresh-state refusal, line front end, error codes | new `cmd/metropolis/drive.go`, `drive_frame.go`, `drive_line.go`, tests; a few lines in `cmd/metropolis/run.go` (shared with FEAT-9995385618 lane L2 and the welcome lane: whichever lands second rebases) | M, 1.5 to 2 days | Sonnet build; round r1 (the change touches the boot and quit path) |
| PT-B MCP front end | stdio MCP server in the same process: initialize, tools/list, tools/call for the three tools; test client | new `cmd/metropolis/drive_mcp.go`, tests | S-M | Sonnet build; round r1 |
| PT-C runner and audit | `run-session.mjs` (builds the prompt, launches the restricted `claude -p`, captures stream-json), `audit.mjs`, `summarise.mjs`, `register-check.mjs`, the UNVERIFIED flag check against `claude --help` | new `tools/playtest/` | M | Sonnet build; round r1 (it is the clean-room enforcement) |
| PT-D personas, templates, README | five persona files from section 4, `driver-usage.md`, the register template, the log header template, `docs/playtest/README.md` (what players may and may not do) | new `docs/playtest/` | S | Haiku (clerical, from this document); lead review |
| PT-E welcome screen | new screen, boot registration, text from the key registry, not-built lines, clock pause on show, Enter to dismiss | new `internal/ui/screens/welcome/`, new `cmd/metropolis/welcome.go`; one registration line in `boot_screens.go` (shared with fly-out L4) | M | Sonnet build; round r1; Opus review recommended (it changes the boot first frame and the clock start) |
| PT-F rounds | run the five sessions, audit, read, update the register, file BOW items | `docs/playtest/sessions/`, `register.md` | per round: the lead, about 1 hour | players are Haiku; the lead's reading and decisions are not delegated |

Plan registration: a new module key for the driver and the welcome screen, with plan edges from `cmd/metropolis` to `internal/harness/uitest` (for `ParseScript`) and from the new screen to the terminal skeleton (`/register-guid`, `/edge-lint`).

Order and what can run when:

1. PT-D and PT-E start at once (no shared files). PT-A starts at once.
2. PT-B after PT-A's `Send/Log/End` core is in.
3. PT-C can start with PT-A (it needs only the usage text and the tool names) and finishes after PT-B.
4. Round R0 (baseline) runs after PT-A, PT-B, PT-C, PT-D: on today's build with NO welcome. This is worth doing before PT-E lands, and it measures: how far a cold person gets with only the hint row and status row (first action taken, time to first build, steps until the first thing the player calls progress); how many sessions give up and when; which keys the players cannot work out; how many cannot-do hits come from BUG-1237 keys; the first honest 1 to 5 ratings. The register built from R0 is the "before".
5. PT-E lands. Round R1 (same five personas, same budget) runs on the build with the welcome. The ratings and the time to first build are compared with R0. This comparison is the proof that the welcome screen helps.
6. Rounds after that run on request or after a batch of landings (question 3).

Standing project rules apply to every dispatch: BUDGET line, model by role, lane cut from current main, the lead runs the full gates, round-first (GR#23), the agent verifies only the files it touches, a 30-minute cap on any test run.

## 11. Out of scope

- Any change to the game by a player or by the harness. The lead alone, afterwards (section 6.1).
- Graphics, colour, screenshots: this is a simulator and needs function only (Aaron's words).
- The mouse. Mouse is never switched on today (section 1.1). If FEAT-9995385618 turns it on, that is a new persona rule, not part of this item (question 5).
- Making the game balanced, fixing any finding, wiring any gap (BUG-1237), the fly-out menu, the F9 help, start empty. Those are their own items; this module finds what a cold player hits and records it.
- A web or browser player.
- Voice, sound.
- Training the agent, or comparing models. One model (Haiku) for all five types.
- Real human playtests. This module does not replace them.

## 12. Assumptions (logged on the Book of Work)

- ASM-1652: the driver is a `-drive-dir` flag on `cmd/metropolis`, not a new binary (reason: `package main` cannot be imported).
- ASM-1653: the text frame loses style, so reverse-video runs are listed beside it (the map cursor may be a style, UNVERIFIED).
- ASM-1654: the Agent tool alone cannot give a clean room; players run as a restricted `claude -p` process plus an audit (flags UNVERIFIED).
- ASM-1655: the game clock is not frozen while the agent thinks.
- ASM-1656: the fresh save folder is `METROPOLIS_SAVE_DIR` set per session (nothing writes saves from the terminal yet, BUG-1237).
- Not logged: a Haiku player's judgement of "entertainment" is a cheap proxy, not a human's. Treat the ratings as relative between rounds, not as absolute scores.

## 13. Questions only Aaron can answer

1. Clean room. A true clean room is not possible with a general-purpose agent (section 3.1): the model knows what games are. Is "no tools but the game, no files, no shell, plus an audit" enough?
   - Option A: yes, and say so in the register header (recommended).
   - Option B: also run a second pass with a human friend now and then to calibrate.
   - Option C: require a shell-free guarantee only after the `claude -p` flags are proved (this is what the plan does anyway).
2. How many sessions per type per round?
   - Option A: one each, five per round (recommended to start; about 650,000 fresh tokens).
   - Option B: two each, ten per round, once R0 shows how noisy one session is.
3. When do rounds run?
   - Option A: on demand, plus R0 now and R1 after the welcome lands (recommended).
   - Option B: after every landing that changes a screen or a key (needs the GGR16 budget per month).
4. Chess types. Aaron listed five, the chess split is three (points, position, planning), and planning has no pure persona. Keep five, with `slow` carrying position and planning?
   - Option A: yes (recommended).
   - Option B: add a sixth, a PLANNER who writes a plan first and executes it.
5. May a session use the mouse? Today the game ignores it.
   - Option A: no, keys only; revisit when the fly-out menu turns mouse on (recommended).
   - Option B: add click events to the driver now.
6. Budget per session: 60 key steps, 20 minutes, 150,000 fresh tokens.
   - Option A: as proposed (recommended; the baseline round tests it).
   - Option B: 100 steps and 30 minutes (a month of game time costs 30 real seconds, so 60 steps shows about 20 months).
7. Between calls the game clock keeps running, so model thinking time is game time.
   - Option A: yes, as a human's would (recommended; honest to a real-time sim).
   - Option B: freeze the clock while the agent thinks (needs a hook; results stop being what a human would see).
8. The priority rubric in section 6.2 (P0 cold player cannot start or the game lies about a goal item; P1 only guessing; P2 confusion with a way round; P3 polish). OK?
   - Option A: yes (recommended).
   - Option B: Aaron sets priorities row by row at first.
9. The goal text on the welcome screen is Aaron's to word. Draft in 7.2 item 2. Accept the draft, or give words?
   - Option A: accept the draft and change after R1 (recommended).
   - Option B: Aaron writes it.
10. Should the clock be paused while the welcome screen shows?
    - Option A: yes, resume on Enter (recommended; with the empty city nothing is lost, and the real-time pressure should not start on a screen the player is reading).
    - Option B: leave it running.
11. Should the same five named people be kept forever so rounds compare?
    - Option A: yes, names and rules fixed, versioned in git; change one only with a note in the register (recommended).
    - Option B: new people each round to avoid learned behaviour (the agent is fresh anyway, so little is gained).
12. How is the welcome dismissed and found again?
    - Option A: Enter dismisses; the F9 help opens on the same text as page one (recommended; one key to learn).
    - Option B: any key dismisses; a separate key reopens it.
13. Money. Every session spends money (section 9). Does Aaron Authorise the first round, and at what ceiling? The plan mints no token.
15. Aaron's reference brief was written for a browser game opened by URL. Is this module for the terminal game only?
    - Option A: yes, terminal only, per the 2026-11-18 ruling that the terminal game on the Go engine is the game; keep the browser brief (and a Playwright-based module) for later (recommended).
    - Option B: also build a browser variant now (a different driver, Playwright, and the console as the clean room; a second lane set).
14. Parallel sessions on the laptop: three at once is the proposal; memory per game process is unmeasured.
    - Option A: three at once after the first round measures one (recommended).
    - Option B: one at a time.
