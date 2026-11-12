# 0018. Assignment Query for Proxy Routing: Validity = Unsettled and Within TTL

## Status
Accepted

## Context
The Velocity proxy plugin must decide, on player connect and with zero
client-side changes, whether to route the player to the game server of a
formed match or keep them in the lobby. All player-scoped endpoints
require the player's own OAuth token, which the proxy does not have — it
only knows the Minecraft UUID. A trusted-infrastructure query keyed by
UUID was missing.

## Decision
- `GET /api/matchmaking/assignment/{uuid}` (contract 1.9.0) resolves the
  UUID via HRPAuth (same trust boundary as reporting) and returns the
  player's current assignment: minimal routing surface only (`assigned`,
  `group_id`, `match_type`, `formed_at`, `team_id`, `join_target`), no
  member lists, scores or payloads. OPS-level credentials only, like the
  other operational reads; player tokens answer 403 even with scopes.
  A UUID unknown to HRPAuth is routing-equivalent to "no assignment" and
  answers `200 {assigned: false}` (1.9.1) — callers never branch on 404;
  only a failed resolution (HRPAuth unreachable, unusable service
  credential) answers `502 oauth_hrpauth_down`.
- Validity: the player's MOST RECENT formed group is the assignment while
  (a) no `matches` row references it via `matchmaking_group_id` (a settled
  match consumes the assignment) and (b) `formed_at` is within
  `matchmaking.assignment_ttl_seconds` (config 1.10.0, default 1800s,
  database-side `TIMESTAMPDIFF` like the engine). Settled or expired
  groups answer `assigned: false`; the endpoint never falls back to older
  groups.
- `matches.matchmaking_group_id` is persisted when the report carries it
  (migration v3, nullable column + index; NULL when absent, e.g. game-side
  reporters that cannot know the group id — TTL remains the backstop).
- `join_target` follows the formed-group rule from 0015: attached when
  configured, omitted otherwise.

## Consequences
- The proxy maps `join_target` host:port to a Velocity-registered server
  name in its own configuration; the arena server stays unaware of proxy
  topology.
- Game-side reporters do not know the group id, so consumption via the
  matches link only fires for reports that carry it (ops/debug and future
  group-aware reporters); TTL expiry covers the rest.
- The endpoint is read-only and adds no push channel; the proxy polls it
  once per connection.

## Evidence
- api/router.go::Register — `GET /api/matchmaking/assignment/:uuid` route
- api/handler/matchmaking.go::Assignment — OPS gate, uuid path param
- internal/service/matchmaking.go::Assignment — most-recent group, consumed check, TTL check, join_target attachment
- internal/service/match.go::Report — matchmaking_group_id persisted when provided
- config/config.go::MatchmakingConfig — AssignmentTTLSeconds (config 1.10.0, migration injects 1800)
- migrate/m003_assignment_link.go::applyV3 — matches.matchmaking_group_id column + idx_mm_group
- HA-Contract docs/api/openapi/matchcorearena-business.yaml (version 1.9.1, path /api/matchmaking/assignment/{uuid}, schema MatchAssignment)
- test/e2e/e2e_test.go::TestMatchCoreE2E — "assignment query for proxy routing" subtest (valid / consumed / 403 / unknown-uuid answers assigned:false)
