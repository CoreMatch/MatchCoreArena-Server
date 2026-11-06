# 0011. Match Reports Are Idempotent by game_id and Self-Contradictions Are Rejected Typed

## Status
Accepted

## Context
Real game servers (BedwarsRel-MCA keeps its own plugin-side idempotency
ledger) and ops tooling may submit the same match more than once; a duplicate
must never settle twice. Separately, corrupted or contradictory reports must
not reach settlement, and the reporter must be able to tell WHY a report was
rejected.

## Decision
- `game_id` (reporter-supplied stable key, `matches.game_id`, unique key
  `uk_game_id`) is the server-side idempotency key. When present, the server
  hashes the normalized payload (`payloadHash`: teams/members sorted, times
  canonicalized, source defaulted) into `matches.payload_hash`:
  - same `game_id` + identical hash → the existing record is returned with
    HTTP 200 and nothing is settled again (idempotent replay);
  - same `game_id` + different hash → `409 mca_game_id_conflict`;
  - concurrent first writes race on the unique key; the loser re-reads and
    answers replay or conflict the same way.
  Reports without `game_id` are always settled (no key to deduplicate on).
- Self-contradictory payloads are rejected before any database write with
  `400 mca_report_inconsistent`: multiple winning teams, win/draw mixing,
  duplicate participant UUIDs, `initial_count` < 1, `alive_count` outside
  [0, initial_count], negative kills/deaths/survival_time/duration,
  `perf_tweak` outside [0.95, 1.05] (parity with the ops panel's client-side
  validation; no silent clamping), `finished_at` before `started_at`, and a
  positive `duration_seconds` that disagrees with the timestamp difference.
  Structural gaps (missing teams/members, bad formats) remain
  `mca_invalid_request`.
- Handler-level outcomes feed the metrics registry (0013): success,
  replay, and per-code rejection counters.

## Consequences
- Reporters may retry blindly: a retried report either settles once or
  replays the existing record; only genuinely conflicting reuse of a
  `game_id` errors.
- `payload_hash` covers the settlement-relevant fields; metadata outside the
  hash (e.g. `matchmaking_group_id`) does not participate in conflict
  detection.

## Evidence
- migrate/migrations/baseline.sql::uk_game_id — matches.game_id unique key and payload_hash column
- internal/service/match.go::payloadHash — normalized payload hashing
- internal/service/match.go::findByGameID — replay/conflict pre-check read
- internal/service/match.go::validateTeams — contradiction rules incl. perf_tweak range
- internal/apperr/codes.go::CodeMCAGameIDConflict — mca_game_id_conflict (409)
- internal/apperr/codes.go::CodeMCAReportInconsistent — mca_report_inconsistent (400)
- api/handler/match.go::Report — 200 replay vs 201 created, rejection counters
- test/e2e/e2e_test.go::TestMatchCoreE2E — replay/conflict/inconsistent end-to-end cases
