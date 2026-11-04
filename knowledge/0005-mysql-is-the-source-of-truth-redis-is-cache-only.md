# 0005. MySQL Is the Single Source of Truth; Baseline-Only Embedded Migrations; Redis Is Cache-Only

## Status
Accepted

## Date
2026-09-11 (baseline + embedded migrations; migrations config removed same day)

## Context
MCA needs durable rating state (users, matches, participants, rankings) plus fast
lookups for token verification and identity resolution. The migration mechanism
was reworked several times on the first development day (numbered migration files
→ single baseline SQL → embedded files).

## Decision
- MySQL (InnoDB, utf8mb4) holds all persistent state: `users`, `friends`, `teams`,
  `team_members`, `matches`, `match_participants`, `rankings`.
- Schema bootstrapping is baseline-only: at startup the embedded `baseline.sql`
  runs if and only if the database contains zero tables. There is no migration
  version tracking table and no incremental migration runner.
- Incremental schema changes used to ship as additional embedded SQL files, but
  nothing in the code ever applied them to an already-initialized database. The
  only such file (the 2026-09-18 match-participants extension) was folded into
  `baseline.sql` and deleted on 2026-11-04 because the report code path
  requires those tables (see 0007). Schema changes therefore still take effect
  only on a fresh database or when executed manually (e.g. through the raw-SQL
  maintenance endpoint).
- Redis is used strictly as a cache with TTLs, never as a source of truth:
  token verification results (5 min), UUID→UID mappings (24 h), and username
  lookups for friend requests (10 min). Leaderboards are read from MySQL, not
  Redis.

## Consequences
- Costs and limitations:
  - Existing databases receive no automatic schema evolution; operators must run
    incremental SQL by hand, and there is no rollback mechanism.
  - Because the baseline is skipped as soon as any table exists, a partially
    created database (e.g. a failed first run) will never be completed
    automatically.
  - All durable-state risk concentrates on a single MySQL instance; no replication
    or backup tooling is defined in this repository.
  - Cache staleness windows are bounded only by TTL (see 0003 for the UUID→UID
    case); there is no cache invalidation on identity changes.
  - Ranking reads scale linearly with table size (see 0001).

## Evidence
- main.go::parseTime — MySQL DSN (utf8mb4, parseTime) and pool setup
- migrate/migrate.go::RunMigrations — baseline runs only; no version tracking
- migrate/migrate.go::isInitialized — isInitialized: skip when any table exists
- migrate/embed.go::MigrationsFS — `//go:embed migrations/*.sql`
- migrate/migrations/baseline.sql::match_participants — full schema (users, friends, teams, team_members, matches, match_participants, rankings); since 2026-11-04 this includes the match_participants table and the matches.winner_team_id / survival_rate columns
- migrate/migrations/20260918_add_match_participants.sql:L1 @ 0950310aafaa90acddc5a45b4f477dd73f8e61d4 — formerly embedded but referenced by no runner; folded into baseline.sql and deleted on 2026-11-04 (commit eff1784, see 0007)
- internal/auth/verifier.go::Verify — token verification cache, 5-minute TTL
- internal/service/match.go::resolveUID — UUID→UID cache, 24-hour TTL
- api/handler/friend.go::Request — username lookup cache, 10-minute TTL
- internal/service/ranking.go::GetTop — rankings served from MySQL; Redis ZSET noted as future work
- commit afc83a654be30b28d420c13aba3bf04fb96d6ffd — feat: add database migrations and initial schema (2026-09-11)
- commit 88dda2a47aed6fb400f6549caf9ba2a7ddbc2d29 — refactor: switch database migration to use baseline SQL (2026-09-11)
- commit e9fb667eb7f535f6b55bd326b20e1b6102c0d70d — refactor: switch to embedded migrations and remove migrate config (2026-09-11)
- commit 6279658307d011791f3a15a166ae81caf1c7c5dd — added the match_participants migration file (2026-09-18)
