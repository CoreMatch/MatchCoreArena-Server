# Knowledge Base

This knowledge base is the project's memo to itself. It is self-hosted and
maintained by this repository (MatchCoreArena-Server) and records internal design
decisions. It does **not** define or override outward behavior.
Outward behavior is specified by HA-Contract
(https://github.com/SamuelCheston/HA-Contract); if an entry here ever conflicts
with HA-Contract, fix the entry, not the contract.

## Entries

| ID | Title | Status | Summary |
|----|-------|--------|---------|
| [0001](0001-server-side-weighted-elo-rating.md) | Server-Side Weighted Elo Rating Applied at Match Report Time | Accepted | Ratings are computed on this server inside the match-report transaction using Elo expected score with phase-based K-factor and survival/streak/performance weights. |
| [0002](0002-match-results-are-pushed-by-bedwarsrel-mca.md) | Match Results Are Pushed by BedwarsRel-MCA via POST /api/matches with an OPS Credential | Accepted | Match results enter only through `POST /api/matches`, which requires an OPS-level credential (maintenance or service token) carrying the `match:report` scope; player tokens are rejected. |
| [0003](0003-player-identity-is-hrpauth-uid-with-minecraft-uuid-resolution.md) | Player Identity Is the HRPAuth UID; Minecraft UUIDs Are Resolved Through HRPAuth | Accepted | The internal identity key is the HRPAuth UID; Minecraft UUIDs from match reports are mapped to UIDs via the contracted HRPAuth service endpoint `GET /services/users/{uuid}` with a service token, local `users` rows are provisioned lazily in the settlement transaction, and MCA holds no user credentials or Minecraft protocol code of its own. |
| [0004](0004-standalone-http-service-without-minecraft-runtime.md) | MCA Is a Standalone Go HTTP Service with No Minecraft Server Runtime Dependency | Accepted | MCA is a self-contained Go/Gin HTTP service; every Minecraft-ecosystem interaction crosses an HTTP boundary (plugin push in, HRPAuth lookups out). |
| [0005](0005-mysql-is-the-source-of-truth-redis-is-cache-only.md) | MySQL Is the Single Source of Truth; Versioned Embedded Migrations; Redis Is Cache-Only | Accepted | All durable state lives in MySQL, bootstrapped by a versioned embedded migration chain (frozen baseline v1 plus additive, guarded versions); Redis serves only TTL caches. |
| [0006](0006-multi-stage-docker-build-with-env-rendered-config.md) | Deployment Is a Multi-Stage Docker Image with Environment-Rendered Config | Accepted | A multi-stage Dockerfile produces a minimal non-root runtime image; deployment-scoped config is rendered from a template via `envsubst` at container start. |
| [0007](0007-report-path-alignment-and-pending-design-decisions.md) | Report-Path Schema Alignment and Pending Design Decisions | Accepted | `matches.winner_uid`/`loser_uid` kept with `DEFAULT 0` for production column compatibility, streaks read from `match_participants.is_winner`; decision tracker — 2026-11-12 author decisions recorded (score/perf column writes, virtual settles deliberately, party consent), 4 items still pending. |
| [0008](0008-ecosystem-boundary-hrpauth-only-outbound-dependency.md) | Ecosystem Boundary: HRPAuth Is the Only Outbound Runtime Dependency | Accepted | MCA calls only HRPAuth (/status, /oauth/*, /user*, /services/users/{uuid}); no Yggdrasil, WinnerProxy, or skin-service references in code; inbound consumers are BedwarsRel-MCA, the client, and the maintenance view. |
| [0009](0009-unified-match-reporting-and-matchmaking.md) | Unified Match Reporting and Matchmaking Are First-Class Server Features | Accepted | `POST /api/matches` is the only reporting entry point (ops-injected data is just `source=virtual`, never a separate path), and a background matchmaking engine queues solo players and parties and forms rating-windowed matches persisted to MySQL. |
| [0010](0010-friend-and-team-endpoints-are-explicitly-unimplemented.md) | Friend and Team Endpoints Are Explicitly Unimplemented | Accepted | All friend/team service methods answer typed `mca_not_implemented` (HTTP 501) instead of fake successes or empty fake data; routes, auth and reserved error codes stay in place for a future implementation. |
| [0011](0011-match-report-idempotency-and-typed-rejection.md) | Match Reports Are Idempotent by game_id and Self-Contradictions Are Rejected Typed | Accepted | `game_id` is the reporter-supplied idempotency key (identical re-reports replay with 200, conflicting reuse answers 409 `mca_game_id_conflict`), and self-contradictory payloads are rejected before settlement with 400 `mca_report_inconsistent`. |
| [0012](0012-party-matchmaking-via-roster-and-shared-settlement.md) | Party Matchmaking via party_members Roster and Shared Settlement | Accepted | Join accepts a `party_members` UUID roster (ops-panel field name) that enqueues as one ephemeral party unit, never split across teams, with same-party rating deltas averaged when the match is reported against the formed group. |
| [0013](0013-observability-metrics-registry.md) | Observability Is an In-Process Counter Registry Exposed at /api/metrics | Accepted | A dependency-free counter registry tracks settlements, rejection reasons and queue activity; `GET /api/metrics` (OPS-only) exposes the snapshot plus live queue depth per mode. |
| [0014](0014-ops-query-surface-matches-rejections.md) | OPS Query Surface: Global Match List, game_id Lookup and Rejection Feed | Accepted | OPS-only reads for the debug console: `GET /api/matches` (filters + pagination, detail-shaped entries), `GET /api/matches/by-game-id/{game_id}`, and a bounded sanitized feed of recent inconsistent/conflicting reports. |
| [0015](0015-formed-matches-carry-join-target.md) | Formed Matches Carry the Game Server join_target When Configured | Accepted | Optional `matchmaking.join_target` (host/port) config; configured, every formed-group record in the matchmaking status carries `join_target` in the client interop shape; unconfigured, the field is omitted and clients fall back to local config. |
| [0016](0016-display-additions-queue-stats-self-summary-tiers.md) | Display-Only Additions: Queue Stats, Self-View History Summary, Ranking Tiers | Accepted | Matchmaking status carries read-only `queue_stats`; history list items carry a `self_summary` for the subject player; optional `ranking.tiers` ladder served by `GET /api/rankings/tiers` — all display-only, no settlement/matchmaking/permission changes. |
| [0017](0017-versioned-schema-migrations-frozen-baseline.md) | Versioned Schema Migrations: Frozen Baseline v1 and an Additive-Only Chain | Accepted | Startup applies numbered embedded migrations under an advisory lock with `schema_migrations(service, version, name, applied_at)` bookkeeping; v1 is the frozen baseline, v2 adds the reporting/matchmaking structures, every DDL step is `information_schema`-guarded and re-entrant, failure aborts startup. |
| [0018](0018-assignment-query-for-proxy-routing.md) | Assignment Query for Proxy Routing: Validity = Unsettled and Within TTL | Accepted | OPS-only `GET /api/matchmaking/assignment/{uuid}` returns the player's most recent formed group as a minimal routing payload while it is unsettled (no match references it via `matchmaking_group_id`) and within `matchmaking.assignment_ttl_seconds`; the Velocity proxy uses it to route connecting players with zero client changes. |
| [0019](0019-room-reservations-static-inventory.md) | Room Reservations: Static Inventory, Reserve at Formation, Release on TTL or Settlement | Accepted | With `matchmaking.servers` configured, `commitGroup` reserves one inventory room per formed group via `matchmaking_room_bindings` (PK-guarded against concurrent double-booking); release happens on TTL expiry (engine cleanup) or on a settlement report echoing `matchmaking_group_id`; assignments carry `room` + the bound sub-server address, or honest `no_room_available`. |
| [0020](0020-k-factor-derivation-and-phase-design.md) | K-Factor Derivation and Phase Design (520 / 390 / 190) | Accepted | Reviewable derivation of the K-factor constants from the CS2 Premier research PDF: placement K decays linearly 520→390 over the first 10 wins (calibration uncertainty), stability K is the midpoint 190 of the 165–215 band (activity modulation intentionally simplified away); phase keys on wins_count; values unchanged, documented for review. |

## Maintenance

- New entries take the next sequential number (`NNNN`, zero-padded, starting from
  0001) and a kebab-case slug: `knowledge/NNNN-<slug>.md`.
- Entries record the current state only: descriptions of removed code, deprecated
  fields or former behavior are deleted, not kept as history. The one exception is
  a change of design intent — why the current design differs from an earlier one —
  which stays only when removing it would make the current state incomprehensible.
- Evidence anchors point at the current tree (`path::Symbol`, line ranges);
  time-point anchors (`path:Lx-Ly @ commit`) are not used.
- Every factual claim needs evidence that was actually checked (`file:line` and/or
  full commit hash). Anything that cannot be verified is written as "not verified"
  or "intent not documented in repo" — never invented.
- Entries are written in English.
