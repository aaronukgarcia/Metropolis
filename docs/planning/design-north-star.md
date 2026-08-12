# Design North Star — where the entertainment comes from

**Author:** Aaron (dictated 2026-08-11, transcribed by Bob)
**Status:** Approved design intent — this is from the human developer directly; it outranks any derived document that conflicts with it.
**Altitude:** 10,000 feet. Deliberately. Do not decompose this into features or acceptance criteria inside this file — it is the *test* other documents are held to, not a spec.
**Relationship to the master plan:** extends §1 Vision & Pillars (especially Pillar 3, "squeezed never ambushed"). Pending integration into the SSOT (see BOW).

---

## The thesis

**The game is the juggle.** The entertainment comes from managing conflicting requirements — solving the cubic-quadratic equation *in your head*. The player is the optimiser; the sim's job is to present genuinely competing demands. The fun is not the maths, it's the *game* of holding the trade-offs in your mind and choosing.

Five load-bearing ideas:

### 1. The pie is only so big
Money is finite. Every allocation is a real choice, and the interesting half of every choice is what you *didn't* fund. If the player can afford everything that matters at once, the game has failed at that moment — the squeeze (Pillar 3's "competing demands on finite money, land, and throughput") is the core loop, not an obstacle to it.

### 2. Skin in the game
Our allocation of funds is *our choice*, and the consequences are ours to live with. That's what enfranchises the player. Consequences persist and compound — no take-backs, no scripted rescue. Ownership of outcomes, good and bad, is the emotional engine. (This is the player-experience face of Pillar 6: the same dials that grow the city can kill it, and the player turned them.)

### 3. The snowball
Building grows from a tiny hamlet. Early-game, small numbers matter enormously; late-game, compounding does the work. The player should *feel* the snowball — that the university they starved for at 2,000 residents is why the city hums at 200,000. Growth is earned momentum, not a slider.

### 4. Rich, fine-grained options
Breadth and granularity of levers is itself content. A rich range of options at fine grain is part of the entertainment — more meaningful dials means more ways the juggle can be personal, and more ways two players' cities diverge from identical starts.

### 5. Long-horizon investment
Planting a crop. Stretching — perhaps early, perhaps before you can comfortably afford it — to build a university, *knowing* the benefits of education pay long-term gains. Deferred payoffs must be real, legible, and worth the stretch. The player who invests long should be rewarded for the foresight, and should be able to see (via projections, §13) enough to make that bet a judgement call rather than a gamble.

---

## The test

Every feature, criteria set, and balance pass should be able to answer:

1. **Which conflicting demand does this sharpen?** If a feature relieves pressure without creating a new claim on the pie, it's making the game smaller.
2. **What does the player give up?** Every yes must have a felt no.
3. **Does the consequence stick?** If the outcome can be silently undone, there's no skin in the game.
4. **Does it snowball?** Does early investment here compound into late-game character?
5. **Can a long bet be made on it?** Is there a version of this the player stretches for early, and is the deferred payoff real and visible enough to justify the stretch?

A feature that answers none of these may still be necessary infrastructure — but it isn't *entertainment*, and it must not crowd out the ones that are.
