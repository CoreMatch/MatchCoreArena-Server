# 0009. Unified Match Reporting and Matchmaking Are First-Class Server Features

## Status
Accepted

## Context
Rating and matchmaking must be exercisable without starting a real game or a
Minecraft server: ops/debug tooling injects match data through the same
endpoint real game servers use, settlement and ranking must complete, and the
matchmaking system must queue players (solo or as parties) and form matches.

## Decision

### One reporting entry point

- `POST /api/matches` is the ONLY match-reporting endpoint; game servers and
  ops/debug tooling submit alike. There is no server-side "virtual match"
  concept: `matches.source` (`game` default, `virtual` when the reporter marks
  it) is a provenance marker only and never branches settlement.
- Reporting requires an OPS-level credential (maintenance token or a
  `tokens.yaml` service token) carrying the `match:report` scope; player
  tokens are rejected `403 oauth_insufficient_scope` even with the scope
  (see 0002).
- The payload accepts two equivalent shapes: the unified `teams` array (each
  team carries `result` win/lose/draw) and the legacy `winner_team` +
  `loser_teams` form (normalized to teams with win/lose results). At most one
  team may win; win/draw mixing is rejected; all-draw matches are allowed.
- Rating rules reuse `internal/rating` unchanged (parameters fixed by the
  author, see 0001):
  - exactly one winning team: expected score from winner-vs-loser team
    averages, the winner's survival weight applied to every participant,
    streak weights from history;
  - all-draw: S=0.5 for everyone, each team's expected score against the
    pooled average of all other teams, each team's own survival weight,
    streaks reset to zero (no streak query, no win increment).
- Draws store an empty `winner_team_id` and `survival_rate` 0;
  `match_participants.is_draw` records per-player draws and streak reading
  treats an `is_draw` row as a streak terminator.
- The settlement response returns the match plus per-participant
  `rank_score_before` / `rank_score_delta` / `rank_score_after`;
  `GET /api/matches/{id}` returns participant details for every match;
  `GET /api/users/{uid}/matches` and `GET /api/matches/me` list history with
  pagination.

### Matchmaking

- Any authenticated user can queue: `POST /api/matchmaking/join`
  (`match_type` must be a configured mode), `DELETE /api/matchmaking/leave`,
  `GET /api/matchmaking/status` (queue state plus the user's recent formed
  groups). One queue entry per user overall; duplicate join answers 409
  `mca_queue_already_joined`, leaving without an entry answers 404
  `mca_queue_not_joined`. Joining lazily provisions the `users` row with
  the same trust boundary as settlement and snapshots `rank_score`.
- Party join uses the `party_members` roster field (member UUID list — the
  same field name the ops panel sends); see 0012 for the party model.
- A background engine (`MatchmakingEngine`, started from `main` when
  `matchmaking.enabled`) ticks every `tick_seconds` and runs one immediate
  tick on start (restart recovery): it removes entries older than
  `queue_timeout_seconds`, then per mode greedily forms matches — pivot =
  longest-waiting unit (solo or whole party), rating window =
  `base_rating_window + waited_minutes * rating_window_growth_per_minute`
  capped at `max_rating_window`, units inside the window are packed into the
  mode's teams without splitting parties. All timing is computed
  database-side (`NOW()`/`TIMESTAMPDIFF`) so driver/session timezone
  differences cannot skew waits or timeouts.
- Formed matches are persisted (`matchmaking_groups` +
  `matchmaking_group_members`) in one transaction that also dequeues the
  members and cleans up committed parties; group commits are guarded by
  verifying the deleted queue-row count (concurrent-instance race →
  rollback). Queue state and formed matches live in MySQL (0005). Modes,
  sizes and all timing parameters are configuration
  (`config.MatchmakingConfig`).

## Consequences
- Ops-injected results move the same `users`/`rankings` tables as game
  matches by design; they are only distinguishable via `matches.source`
  (segmentation remains a pending author decision, 0007).
- Matchmaking forms match records only; it does not start games or report
  results. The formed-group record is the terminal artifact until a match is
  reported (optionally linked via `matchmaking_group_id` for party-shared
  settlement, 0012).
- Matchmaking parameters (window, growth, timeout, tick) are conservative
  placeholders, not tuned values.

## Evidence
- api/router.go::Register — `POST /api/matches`, `/api/matchmaking/*`, `/api/users/:uid/matches`, `/api/metrics` routes
- api/handler/match.go::Report — OPS gate, idempotent replay (200) vs created (201), rejection metrics
- internal/service/match.go::Report — unified settlement (win path and all-draw S=0.5 branch)
- internal/service/match.go::normalizeTeams — teams shape and legacy winner_team/loser_teams normalization
- internal/service/match.go::MatchSourceVirtual — source marker constants (game/virtual)
- internal/service/match.go::getUserStreak — is_draw terminates streaks
- internal/service/match.go::GetByID — participant details in match detail
- internal/service/matchmaking.go::Join — solo/party queue join, lazy provisioning, duplicate handling
- internal/service/matchmaking.go::Tick — DB-side timeout and per-mode matching, first-tick-on-start
- internal/service/matchmaking.go::ratingWindow — window growth and cap
- internal/service/matchmaking.go::packUnits — greedy party-preserving team packing
- internal/service/matchmaking.go::commitGroup — guarded group commit and party cleanup
- config/config.go::MatchmakingConfig — engine configuration block
- migrate/migrations/baseline.sql — matches.source/map_name/server_id/game_id, match_participants.is_draw, matchmaking_* tables
- internal/apperr/codes.go::CodeMCAQueueAlreadyJoined — mca_queue_already_joined / mca_queue_not_joined
- test/e2e/e2e_test.go::TestMatchCoreE2E — end-to-end flow (real database, stubbed HRPAuth)
