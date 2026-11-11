# 0021. Seasons Are a Manual OPS Rollover (No Automatic Season Changes)

## Status
Accepted

## Context
Settlement used to hard-code season 1 (`updateRanking(..., 1)`), and the
ranking endpoints defaulted an omitted `season` to 1 — seasons were one of
the deferred design items (0007). On 2026-11-12 the author decided the
shape of season handling: a manual ops-side rollover, explicitly NO
time-based automatic rollover at this stage, and the reset-or-not choice
stays with the operator.

## Decision
- **Manual rollover only**: `POST /api/seasons/roll` (contract 1.12.0) is
  the ONLY way seasons change. It requires an OPS-level credential
  (maintenance or service token; player tokens answer 403). There is no
  scheduler, no date trigger, no automatic rollover anywhere — this is an
  explicit author requirement, not a missing feature.
- **One season at a time**: `target_season` must be exactly
  current + 1. Skipping (`400 mca_invalid_request`) and repeating/going
  backwards (`409 mca_season_exists`) are rejected; the unique key on
  `seasons.season` also absorbs concurrent duplicate rolls as the same 409.
  Retry semantics are therefore safe: re-sending a succeeded request
  reports the existing season instead of advancing again.
- **Reset is an explicit parameter, default false**: `reset_scores` is the
  caller's choice — the server never decides. When true, the same
  transaction resets every non-deleted `users.rank_score` to 0.
  `wins_count` and match history are deliberately untouched: the K-factor
  phase (0020) and streaks continue across rollovers.
- **Recorded and traceable**: every rollover writes a `seasons` row
  (migration v6) with the season number, the reset flag, `opened_at`,
  `opened_by` (`ops-uid:<uid>` of the credential that rolled) and an
  optional operator note; `GET /api/seasons` returns the full history,
  newest first. Migration v6 seeds season 1 so all pre-rollover history
  (rankings rows with season=1) stays attributed correctly.
- **Settlement and queries follow the current season**: `Report` resolves
  the current season inside its transaction (`SELECT MAX(season) FROM
  seasons`) and writes ranking entries for it; the ranking endpoints
  resolve an omitted/non-positive `season` parameter to the current
  season instead of the former hard-coded 1. Explicit `season` values
  still query archived seasons.

## Consequences
- Rollover is an audited ops action: who/when/reset/note are queryable;
  accidental double-rolls cannot advance twice.
- Old-season leaderboards remain queryable forever (rankings rows are
  never deleted by a rollover).
- With `reset_scores=false` (default), scores carry over and the new
  season's board starts as a copy of current scores at first settlement;
  with `true`, everyone starts from 0 but keeps their K phase and streak
  history — operators choosing a reset should know the K phase does not
  restart.
- Costs/limitations: season windows, snapshots and per-season archival
  boards are still out of scope (the author deferred them); a rollover
  concurrent with in-flight settlements orders by transaction isolation —
  a settlement already holding the old season writes the old season.

## Evidence
- internal/service/season.go::Roll — manual rollover transaction (validation, record, explicit reset)
- internal/service/season.go::CurrentSeason — current season resolution used by settlement and ranking defaults
- internal/service/match.go::CurrentSeason — settlement resolves the season inside its transaction
- internal/service/ranking.go::resolveSeason — omitted season follows the current season
- api/handler/season.go::Roll — OPS gate and request shape
- migrate/m006_seasons.go::applyV6 — seasons table + season-1 seed
- internal/apperr/codes.go::CodeMCASeasonExists — mca_season_exists (repeat/rollback target, concurrent duplicate)
- test/e2e/e2e_test.go::TestMatchCoreE2E — e2e subtest "manual season rollover" (403 gate, repeat 409, skip 400, carry-over vs reset, per-season boards, default-season resolution)
- Cross-reference: 0007 (former pending item, decided), 0020 (K phase continues across rollovers)
