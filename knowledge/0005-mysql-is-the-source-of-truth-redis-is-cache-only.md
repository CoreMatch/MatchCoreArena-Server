# 0005. MySQL Is the Single Source of Truth; Versioned Embedded Migrations; Redis Is Cache-Only

## Status
Accepted

## Context
MCA needs durable rating state (users, matches, participants, rankings) plus fast
lookups for token verification and identity resolution.

## Decision
- MySQL (InnoDB, utf8mb4) holds all persistent state: `users`, `friends`,
  `teams`, `team_members`, `matches`, `match_participants`,
  `matchmaking_queue`, `matchmaking_parties`, `matchmaking_party_members`,
  `matchmaking_groups`, `matchmaking_group_members`, `rankings`, plus the
  migration bookkeeping table `schema_migrations`.
- Schema bootstrapping is a versioned embedded migration chain (see 0017):
  numbered migrations run in order at startup under an advisory lock and are
  recorded in `schema_migrations`; v1 is the frozen baseline (the starting
  point of a fresh database, re-runnable because it is all
  `CREATE TABLE IF NOT EXISTS`), later versions are additive and guarded by
  `information_schema` checks.
- Redis is used strictly as a cache with TTLs, never as a source of truth:
  token verification results (5 min), UUID→UID mappings (24 h), and username
  lookups for friend requests (10 min). Leaderboards are read from MySQL, not
  Redis.

## Consequences
- Costs and limitations:
  - Schema evolution is additive-only: migrations never alter or drop
    existing columns, so obsolete columns stay in place (see 0017).
  - All durable-state risk concentrates on a single MySQL instance; no replication
    or backup tooling is defined in this repository.
  - Cache staleness windows are bounded only by TTL (see 0003 for the UUID→UID
    case); there is no cache invalidation on identity changes.
  - Ranking reads scale linearly with table size (see 0001).

## Evidence
- main.go::main — MySQL DSN (utf8mb4, parseTime), pool setup, migrations at startup
- migrate/migrate.go::ApplyMigrations — versioned chain, advisory lock, record-after-success
- migrate/m001_baseline.go::runBaseline — frozen baseline v1
- migrate/embed.go::MigrationsFS — `//go:embed migrations/*.sql`
- migrate/migrations/baseline.sql::match_participants — baseline schema
- internal/auth/verifier.go::Verify — token verification cache, 5-minute TTL
- internal/service/match.go::resolveUID — UUID→UID cache, 24-hour TTL
- api/handler/friend.go::Request — username lookup cache, 10-minute TTL
- internal/service/ranking.go::GetTop — rankings served from MySQL; Redis ZSET noted as future work
- commit afc83a654be30b28d420c13aba3bf04fb96d6ffd — feat: add database migrations and initial schema (2026-09-11)
- commit 88dda2a47aed6fb400f6549caf9ba2a7ddbc2d29 — refactor: switch database migration to use baseline SQL (2026-09-11)
- commit e9fb667eb7f535f6b55bd326b20e1b6102c0d70d — refactor: switch to embedded migrations and remove migrate config (2026-09-11)
