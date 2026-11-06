# 0014. OPS Query Surface: Global Match List, game_id Lookup and Rejection Feed

## Status
Accepted

## Context
The maintenance/debug console (MaintenanceView) could only look up one
player's history; operating the reporting pipeline needs global views:
all matches with filters, a direct `game_id` lookup for idempotency/
conflict investigation (0011), and visibility into recent contradictory or
conflicting reports without scraping logs.

## Decision
- Three read-only endpoints, all restricted to OPS-level credentials
  (`403 oauth_insufficient_scope` for player tokens, same gate style as
  reporting and `/api/metrics`):
  - `GET /api/matches` — global match list with pagination and filters
    (`source`, `map_name`, `game_id` exact, `uid` participant,
    `finished_from`/`finished_to` on `finished_at`), ordered by
    `finished_at` DESC (then id DESC). Entries carry participant details —
    the same shape as `GET /api/matches/{id}`, no separate list shape.
  - `GET /api/matches/by-game-id/{game_id}` — single match by the
    idempotency key; unknown keys answer `404 mca_match_not_found`.
  - `GET /api/matches/rejections` — recent `mca_report_inconsistent` and
    `mca_game_id_conflict` rejections from a bounded in-memory ring
    (`internal/reportlog`, capacity 200, process-local): time, code,
    `game_id` and a sanitized reason summary. Minecraft UUIDs are masked to
    `<uuid>` before storage; no payload or player fields are kept.
- Rejections enter the ring inside the settlement service (single defer in
  `Report`), so every rejection path is captured regardless of caller.

## Consequences
- The rejection feed is debug-grade observability: bounded, process-local,
  reset on restart — complementary to the counters in 0013, not an audit
  trail.
- The global list loads participant details per row (page size bounded at
  100); acceptable for the ops console, not meant for bulk export.

## Evidence
- api/handler/match.go::List — OPS gate, filter/pagination binding
- api/handler/match.go::GetByGameID — game_id lookup endpoint
- api/handler/match.go::Rejections — rejection feed endpoint
- internal/service/match.go::MatchFilter — filter fields and finished_at range semantics
- internal/service/match.go::List — dynamic WHERE assembly, finished_at DESC ordering, per-row participants
- internal/service/match.go::GetByGameID — lookup via the idempotency index
- internal/reportlog/reportlog.go::Default — bounded ring, UUID masking, snapshot copy
- internal/service/match.go::Report — defer recording the two rejection codes
- api/router.go::Register — the three routes
- test/e2e/e2e_test.go::TestMatchCoreE2E — end-to-end filter/ordering/lookup/rejection assertions incl. UUID non-leakage
