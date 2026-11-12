# 0020. K-Factor Derivation and Phase Design (520 / 390 / 190)

## Status
Accepted

## Context
The rating engine (`internal/rating`) uses a phase-based K-factor — 520
decaying to 390 over the first 10 wins, then a constant 190 — but the only
in-repo explanation was a one-line code comment citing a "CS2 Premier
Rating research report" and a binary PDF nobody could review from text.
0007 tracked this as an open item ("whether to document the derivation is
open"); the author decided 2026-11-12 to document it reviewably. This entry
is that document: where each constant comes from, why it has this value,
and what the choices imply. It documents the existing values; it changes
nothing.

## Decision

### Source of the model

The constants implement the approximate model derived in the repo-root
research document
`research-推导三种对局情况的记分权重近似模型.pdf` ("Deriving approximate
score-weight models for three match situations", a study of CS2 Premier's
CS Rating). The PDF's per-phase formulas are the same shape the server
computes:

    ΔR = K · (S − E) · W_round · W_streak · W_perf        (code: CalculateDelta)

with `E = 1 / (1 + 10^((R_opponent − R_player)/400))` (classic Elo, 400
divisor). The three "match situations" of the title are: all players
unrated (placement), mixed rated/unrated, and all rated (stability); this
server collapses them to a per-player phase keyed on that player's own
`wins_count` (see "Phase key" below).

### Placement phase: K decays 520 → 390 over the first 10 wins

- CS2 Premier assigns an initial visible rating only after **10 placement
  wins**; during placement each match must move the score a lot so a
  limited number of games can calibrate true skill ("high weight, high
  volatility"; the PDF reports placement wins of +300…500 points and
  single-match swings of 500+).
- Community rating models put the placement base K-factor at **400–520**.
  The code takes the top of that band, `KPlacementStart = 520`: new
  accounts here start at `rank_score = 0` with no prior signal at all, so
  the fastest reasonable convergence is wanted.
- **Calibration uncertainty decays with each played game**: the early
  matches carry the most information, later ones refine. The PDF's decay
  approximation is linear,

      K(n) = K_start − (n − 1) · (K_start − K_end) / (N − 1),  n = 1…N

  with `K_start = 520` (explicit example in the PDF), `K_end = 390`
  (explicit example), `N = 10` — exactly `GetKFactor`'s formula
  (`n = wins + 1`, i.e. n is the win currently being settled). 390 stays
  far above the stability K, so the 10th win still moves the score like a
  placement match; convergence is finished by the phase switch, not by the
  decay alone.
- Worked example (also the e2e fixture): first placement win against an
  equal-rated opponent, full survival, no streak, no perf:
  `520 · (1 − 0.5) · 1.25 · 1.0 · 1.0 = 325` — inside the PDF's reported
  +300…500 placement-win band.

### Stability phase: K = 190 = (165 + 215) / 2

- For rated players the PDF states a base K of **≈165–215**, fine-tuned by
  account activity and recent form; volatility drops sharply by design —
  an established rating should resist single-match noise, opponent-quality
  swings and streaks of one-sided games.
- The code uses the **midpoint 190** and deliberately drops the
  activity-based modulation (`GetKFactor` comment: "实际可能随活跃度微调，
  这里简化取中间值"). That simplification is retained: there is no
  activity tracking to feed a modulated K, and a fixed K keeps every delta
  recomputable from stored inputs (0007 decision 1 writes those inputs
  down).
- Worked example: stable-phase win against an equal-rated opponent, full
  survival, no streak: `190 · 0.5 · 1.25 ≈ 119`. With the weight stack at
  its maximum (`W_round 1.25 · W_streak 1.28 · W_perf 1.05 ≈ 1.68`) a
  stable-phase delta is capped around `190 · 1.68 ≈ 319` per match, i.e.
  even an extreme stable-phase result stays below one placement win — the
  phase gap (390→190) is what keeps established ratings quiet.

### Phase key: wins, not total matches

`GetKFactor(wins)` keys the phase on the player's **win count**
(`users.wins_count`), mirroring the CS2 rule that placement completes on
10 *wins*. Consequences, accepted as-is:

- A player who loses a lot stays in the placement band (high K) until they
  have 10 wins — their rating keeps moving fast, which is the intended
  "still calibrating" semantics.
- Draws do not increment `wins_count` (all-draw settlement settles with
  S = 0.5 and no win), so draws neither advance placement nor reset it.
- K is per-player: in one team match, a placement-phase player and a
  stable-phase player settle with different K values (each against the
  same team-average expectation).

### Interaction with the rest of the formula

- K is the amplitude knob; the weights only shape it. `W_round`
  (1.00/1.05/1.15/1.25 by winning-team survival rate) maps the PDF's
  "round margin" factor onto bedwars-style team survival; `W_streak`
  (1.00…1.28 / 0.73…1.00) is the PDF's streak bonus/penalty, taken at the
  middle of the reported ranges; `W_perf` (±5%) is the individual
  performance term (currently inert end-to-end — reporter sends 1.0; see
  0007 pending item on `perf_tweak`).
- `rank_score` is floored at 0 (`GREATEST(0, …)`), so the large
  placement-phase losses cannot drive scores negative; the floor is a
  display/storage bound, not part of the K design.
- The tier ladder (config `ranking.tiers`) is cosmetic; K values were not
  tuned against it.

### What changing the constants would do

Documenting, not proposing: the constants apply to **future** deltas only
(settlement never recomputes history), so any change re-scales the speed
of all future rating movement without touching stored rows. Raising
stability K re-opens settled ratings to volatility; lowering placement K
slows calibration of new accounts; changing the 10-win threshold shifts
when players quiet down. Any such change needs this entry and 0001 updated
in the same commit.

## Consequences
- The 520/390/190 values are reviewable: each maps to an explicit passage
  of the research PDF (placement band 400–520 with 520/390 decay examples;
  stability band 165–215 with midpoint simplification).
- 0007's open item on the K-factor derivation is closed by this entry;
  the constants themselves stay unchanged.
- Costs/limitations: the PDF is a community-model approximation of CS2
  Premier (itself opaque), not a measurement of this ecosystem's player
  base; the activity-based K modulation is intentionally absent; the
  wins-keyed phase means loss-heavy accounts stay volatile by design.

## Evidence
- internal/rating/rating.go::KPlacementStart — 520/390 placement constants with the "CS2 Premier Rating research report" comment
- internal/rating/rating.go::KStabilityMin — 165/215 stability band
- internal/rating/rating.go::GetKFactor — linear placement decay and stability midpoint (190)
- internal/rating/rating.go::CalculateDelta — ΔR = K·(S−E)·W_round·W_streak·W_perf
- internal/service/match.go::updateRanking — per-player K from users.wins_count at settlement
- research-推导三种对局情况的记分权重近似模型.pdf — source study; placement K band 400–520 and decay formula K(n) = K_start − (n−1)·(K_start−K_end)/(N−1) with 520/390 examples (§3), stability K 165–215 with activity fine-tuning (§4), 10 placement wins rule (§2.1)
- Cross-reference: 0001 (rating model), 0007 (derivation open item, closed here)
