# 0016. Display-Only Additions: Queue Stats, Self-View History Summary, Ranking Tiers

## Status
Accepted

## Context
The frontend (client / maintenance view) was blocked on three display
details: how busy the queue is, how "I" did in each listed match without
re-fetching every detail, and whether a rank-tier progress bar can be
shown. All three are read-only additions — no settlement, matchmaking or
permission semantics change.

## Decision
- `GET /api/matchmaking/status` now includes `queue_stats`: `players`
  (queue rows), `parties` (distinct queued parties — solos not counted) and
  `average_wait_seconds` (database-side `AVG(TIMESTAMPDIFF(...))`, rounded).
  Pure read; join/engine logic untouched.
- Match-history list items (`GET /api/matches/me`,
  `GET /api/users/{uid}/matches`) carry `self_summary` — the list subject's
  own team, win/draw flags, `rank_score_delta` and `rank_score_after`
  (floor 0) from their participant row, read in the same query. The list
  stays a plain paginated array (structure unchanged); the global OPS list
  (`GET /api/matches`) has no subject uid and omits the field.
- `ranking.tiers` (name + min_score each) is display-only configuration
  (config v1.9.0, optional; empty = no progress bar). It is served verbatim
  by `GET /api/rankings/tiers` (authenticated, like the other ranking
  endpoints). Settlement formulas and season handling are untouched.

## Consequences
- Tier membership is computed by the client from `rank_score` and the
  ladder; the server never stores or assigns tiers.
- `queue_stats` is global across modes (the status endpoint is not
  mode-scoped).

## Evidence
- internal/service/matchmaking.go::QueueStats — players/parties/average wait, read inside Status
- internal/service/match.go::SelfSummary — self-view summary shape; ListByUser fills it from the participant join
- config/config.go::RankingConfig — optional ranking.tiers block (config 1.9.0, no-op migration)
- api/handler/ranking.go::Tiers — ladder served verbatim, empty array when unconfigured
- api/router.go::Register — /api/rankings/tiers route
- test/e2e/e2e_test.go::TestMatchCoreE2E — queue stats (solo and party counts), self_summary from both perspectives, tiers ordering
