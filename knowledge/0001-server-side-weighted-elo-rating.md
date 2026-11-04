# 0001. Server-Side Weighted Elo Rating Applied at Match Report Time

## Status
Accepted

## Date
2026-09-12 (initial implementation; team-match rework 2026-09-18)

## Context
MatchCoreArena-Server (MCA) is the rating and leaderboard service of the CoreMatch
ecosystem. It needs a score model for team-based bedwars-style matches where the
number of teams and team sizes vary per match, and where match outcomes arrive
as pushed reports rather than being observed live.

## Decision
Rating is computed exclusively on this server, synchronously inside the database
transaction that records a match report:

- Classic Elo expected score with a 400 divisor:
  `E = 1 / (1 + 10^((R_opponent - R_player) / 400))`.
- Delta formula: `delta = K * (S - E) * W_round * W_streak * W_perf`.
- K-factor is phase-based: a placement phase of the first 10 wins linearly decays
  K from 520 to 390; afterwards a constant stability value `(165 + 215) / 2 = 190`
  is used.
- W_round is a survival-rate weight of the winning team (1.00 / 1.05 / 1.15 / 1.25).
- W_streak is derived from the player's recent match history; W_perf is an
  individual performance coefficient clamped to [0.95, 1.05] and supplied by the
  reporter.
- Teams are compared by average rating (winner-team average vs. all losers pooled).
- `rank_score` is floored at 0 via `GREATEST(0, rank_score + ?)`.
- Leaderboards are served from the `rankings` table, upserted per participant on
  every report (`ON DUPLICATE KEY UPDATE`, season hard-coded to 1), and queried
  with a real-time `ORDER BY score DESC` — no precomputed positions, no Redis ZSET.

The K-factor constants carry the code comment "Constants based on CS2 Premier
Rating research report". A research PDF about deriving score-weight models for
three match situations was committed alongside the reporting flow; it is a binary
file and its content was not verified for this entry.

## Consequences
- All rating math is auditable per match: `match_participants` stores
  `rank_score_before` and `rank_score_delta` for every participant.
- Costs and limitations:
  - The stability-phase K is a fixed midpoint; the code comment notes activity-based
    adjustment was intentionally simplified away.
  - `perf_tweak` is trusted from the reporter; the only known reporter
    (BedwarsRel-MCA) hard-codes it to 1.0, so the coefficient is currently inert.
  - Leaderboard queries sort on every request (acknowledged in code comments as
    unsuitable for production scale without Redis ZSET or periodic `rank_position`
    updates).
  - Season is hard-coded to 1 in `updateRanking`; there is no season rollover logic.
  - Known inconsistency: the streak query reads `matches.winner_uid` /
    `matches.loser_uid`, but the team-match INSERT does not populate those columns,
    and they are declared `NOT NULL` without defaults in the baseline schema.
    Behavior therefore depends on MySQL `sql_mode` (insert failure in strict mode,
    meaningless streaks otherwise). Runtime outcome not verified.
  - The route comment calls rankings "public", but the endpoints are mounted inside
    the authenticated group, so a valid token is in fact required.

## Evidence
- internal/rating/rating.go:7-17 — constants incl. "CS2 Premier Rating research report" comment
- internal/rating/rating.go:21-23 — Elo expected score
- internal/rating/rating.go:27-37 — phase-based K-factor
- internal/rating/rating.go:42-57 — survival weight
- internal/rating/rating.go:86-107 — streak weight
- internal/rating/rating.go:111-114 — delta formula
- internal/rating/rating_test.go — unit tests for the rating package (existence verified)
- internal/service/match.go:100-263 — transactional report processing
- internal/service/match.go:206 — rank_score floor at 0
- internal/service/match.go:231, 353-369 — rankings upsert, season = 1
- internal/service/match.go:309-351 — streak query over winner_uid/loser_uid
- internal/service/match.go:152-157 — team-match INSERT omits winner_uid/loser_uid
- migrate/migrations/baseline.sql:61-62 — winner_uid/loser_uid NOT NULL without default
- internal/service/ranking.go:39-74 — real-time top-N query; scale caveat comment at lines 48-49
- api/router.go:84-90 — match and ranking routes; "public" comment at line 88
- research-推导三种对局情况的记分权重近似模型.pdf — binary research document, content not verified
- commit bb8b7c6136b1cf98bc8a40bdbf8294aef2bd9137 — feat: implement elo ranking and match reporting (2026-09-12)
- commit 6279658307d011791f3a15a166ae81caf1c7c5dd — feat(match): add team match support and participant tracking (2026-09-18)
- commit 7dc243d8ba789ae9926865f516bdfa105e1164d5 — added the research PDF (2026-09-11)
- Why these exact constants were chosen: intent not documented in repo beyond the code comment; pending author confirmation
