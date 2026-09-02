# Assumptions triage 2026-09-02 — the 15 that need Aaron (bucket C)

From the full open-ASM sweep (83 read; 32 closed as stale/already-ruled; ~20 need code fixes tracked separately). These 15 are genuine design/balance calls only Aaron can make. Each has a drafted recommendation — Aaron can answer "rec" or edit. **Most are depth-feature design confirmations, NOT Baseline One blockers** — the two flagged (B1) matter sooner.

| ASM | Question | Options | Bev rec |
|---|---|---|---|
| 1481 **(B1-ish)** | Fire-spread AC-6 needs cell material/density/wind/hydrant data `world.Cell` lacks | extend `world.Cell` / coarse zone+density proxy / defer fire-spread | **coarse proxy** for Baseline One |
| 875 | Keep engine.tax at 6 UK-today instruments or grow toward the full §39 panel? | 6 now / full panel / 6 now + levies later | **ship 6, levies later** |
| 470 | Should FEAT-064's per-branch command log reuse `harness.replay.Recorder` unmodified? | give Recorder incremental-flush / rescope AC-11/12 to periodic Save() / dev-build only | **rescope to periodic Save()** (no contract change) |
| 486 | International commodity market: own price surface or extension of `MarketAPI`'s 9-commodity registry? | own surface / extend MarketAPI | **confirm own-surface** (lowest blast radius) |
| 530 | Should `ui.core` grow a Navigate/Pause API, or does chrome's local Effects seam stay permanent? | build shared API / bless chrome-local | **bless chrome-local** unless a 2nd screen needs it |
| 1012 | Harbour vs engine.coastal ownership of rescue/arrivals | fleet=rescue / harbour=arrival surface / both / neither | **both** (distinct concerns) |
| 306 | Who owns the "goods" conservation stock — market or logistics? | market / logistics / both | **both** (different concepts) |
| 909 | Bulk-extraction (chalk/sand/clay/ragstone/offshore): finite like deep coal, or geology-gated (pit depth+reclaim)? | finite-deposit / geology-gated | **geology-gated** per §32 |
| 579 | Where does a declared weather-emergency (suspends death-wave smoothing) come from? | local data-file threshold / new `SeasonAPI.IsWeatherEmergency` / route via feat.disasters | **new SeasonAPI method** |
| 580 | Monthly death budget: independent of funeral throughput, or derived from it? | independent (current) / derived / one knob | **keep independent**, revisit if confusing |
| 214 | Source real OS Terrain 50 licensed data, or stay synthetic permanently? | fetch real data / stay synth | **Aaron's call** — deferred, not a B1 blocker |
| 281 | Call-edge direction inferred from spec prose may not match intended GR#20 direction | per-candidate Architect ruling | **escalate to a /round Architect pass** |
| 861 | code.json only materializes `calls`, not `deps` — F-screen UI edges unregistered | fix `generate.js` to also emit deps-as-calls | **fix generate.js** (mechanical once approved) |
| 920 | `cloud.netpolicy` has a null inbound contract | register real inbound contract / cloud.azure keeps a local minimal policy | **register the contract** |
| 484 | Secret-guard camelCase entropy exemption has ~15% adversarial residual — worth a 2nd detection layer? | ship as-is (documented) / add dictionary+char-class layer | **ship as-is**, revisit if exploited |

Answer inline or in A2Bev001.md; I'll record each as a ruling on the ASM item and action it.
