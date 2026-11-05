# 0009. Virtual Matches and Matchmaking Are First-Class Server Features

## Status
Accepted

## Context
Rating and matchmaking must be exercisable without starting a real game or a
Minecraft server: match data can be injected directly, settlement and ranking
must complete, and the matchmaking system must be able to queue players and
form matches. MCA already had the game-report settlement path (0002) but no
virtual entry point and no matchmaking at all.

## Decision

### Virtual matches

- `POST /api/matches/virtual` accepts match data without any game-side
  connection. It sits behind the same Bearer middleware as the game report
  path and additionally requires the `match:report` scope (maintenance
  bypasses, as everywhere). It can be disabled with
  `virtual_matches.enabled: false`, in which case it answers 400
  `mca_invalid_request`.
- The input is a team list (`VirtualReportInput`): `match_type`, `teams`
  (each with `team_id`, `result` win/lose/draw, `initial_count`,
  `alive_count`, members as in the game path), optional
  `duration_seconds`, `map_name`, `started_at`, `finished_at` (RFC3339,
  defaulting to now). At most one team may be `win`; when one exists the
  other teams must be `lose` (no win/draw mixing); all-draw matches are
  allowed.
- Identity resolution, lazy user provisioning, streaks, rankings and the
  transactional settlement are the same plumbing as the game path (0003):
  participant UUIDs resolve through HRPAuth, and the report aborts with the
  same typed errors (`mca_user_not_found`, `oauth_hrpauth_down`,
  `mca_user_not_provisioned`).
- Rating rules reuse `internal/rating` unchanged:
  - exactly one winning team: the formulas are identical to the game path
    (`Report`): expected score from winner-vs-loser team averages, the
    winner's survival weight applied to every participant, streak weights
    from history;
  - all-draw: S=0.5 for everyone, each team's expected score against the
    pooled average of all other teams, each team's own survival weight,
    streaks reset to zero (no streak query, no win increment).
- Matches are marked at rest: `matches.source` is `virtual` (game reports
  keep the default `game`), `matches.map_name` stores the optional map
  identifier, draws store an empty `winner_team_id` and `survival_rate` 0,
  and `match_participants.is_draw` records per-player draws. Streak reading
  treats an `is_draw` row as a streak terminator.
- The settlement response returns the match plus per-participant
  `rank_score_before` / `rank_score_delta` / `rank_score_after`;
  `GET /api/matches/{id}` returns participant details for every match.

### Matchmaking

- Any authenticated user can queue: `POST /api/matchmaking/join`
  (`match_type` must be a configured mode), `DELETE /api/matchmaking/leave`,
  `GET /api/matchmaking/status` (queue state plus the user's recent formed
  groups). One queue entry per user overall; duplicate join answers 409
  `mca_queue_already_joined`, leaving without an entry answers 404
  `mca_queue_not_joined`. Joining lazily provisions the `users` row with
  the same trust boundary as settlement and snapshots `rank_score`.
- A background engine (`MatchmakingEngine`, started from `main` when
  `matchmaking.enabled`) ticks every `tick_seconds`: it removes entries
  older than `queue_timeout_seconds`, then per mode greedily forms matches —
  pivot = longest-waiting entry, rating window =
  `base_rating_window + waited_minutes * rating_window_growth_per_minute`
  capped at `max_rating_window`, first `sum(team_sizes)` entries inside the
  window form a match. All timing is computed database-side
  (`NOW()`/`TIMESTAMPDIFF`) so driver/session timezone differences cannot
  skew waits or timeouts.
- Formed matches are persisted (`matchmaking_groups` +
  `matchmaking_group_members`) in one transaction that also dequeues the
  members; teams are assigned by snake draft over rating-descending players
  (team labels A/B/C...). Queue state and formed matches live in MySQL
  (0005). Modes, sizes and all timing parameters are configuration
  (`config.MatchmakingConfig`).

## Consequences
- Virtual matches write to the same `users`/`rankings` tables as game
  matches: injected results move real ratings and leaderboard entries.
  Whether virtual results should count toward real rankings (or be
  filtered/segmented) is a pending author decision (0007).
- Matchmaking forms match records only; it does not start games or report
  results. The formed-group record is the terminal artifact until a game
  (real or virtual) reports the outcome.
- Matchmaking parameters (window, growth, timeout, tick) are conservative
  placeholders, not tuned values.

## Evidence
- api/router.go::Register — `POST /api/matches/virtual` and `/api/matchmaking/*` routes
- api/handler/match.go::Virtual — scope check, config gate, binding
- internal/service/match_virtual.go::ReportVirtual — virtual settlement (win path identical to Report; draw branch S=0.5)
- internal/service/match_virtual.go::validateVirtualInput — team/result validation rules
- internal/service/match.go::getUserStreak — is_draw terminates streaks
- internal/service/match.go::GetByID — participant details in match detail
- internal/service/matchmaking.go::Join — queue join, lazy provisioning, duplicate handling
- internal/service/matchmaking.go::Tick — DB-side timeout and per-mode matching
- internal/service/matchmaking.go::ratingWindow — window growth and cap
- internal/service/matchmaking.go::assignTeams — snake-draft team assignment
- config/config.go::MatchmakingConfig — engine configuration block (v1.6.0)
- migrate/migrations/baseline.sql — matches.source/map_name, match_participants.is_draw, matchmaking_* tables
- internal/apperr/codes.go::CodeMCAQueueAlreadyJoined — mca_queue_already_joined / mca_queue_not_joined
- test/e2e/e2e_test.go::TestVirtualMatchE2E — end-to-end flow (real database, stubbed HRPAuth)
