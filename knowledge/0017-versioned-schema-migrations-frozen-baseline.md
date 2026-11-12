# 0017. Versioned Schema Migrations: Frozen Baseline v1 and an Additive-Only Chain

## Status
Accepted

## Context
Schema bootstrapping used to be baseline-only (see 0005): the embedded
baseline ran only on an empty database, so structure added to the baseline
after a database was initialized never reached that database. The unified
reporting and party-matchmaking batches shipped by editing the baseline
directly, leaving the production database (initialized 2026-11-05 by the
`71f45ea` binary, whose baseline is the `eff1784` snapshot) missing the new
`matches` columns/index, `match_participants.is_draw` and the five
`matchmaking_*` tables — a state the new binary cannot serve. This entry
documents the replacement mechanism; 0005 is updated accordingly.

## Decision
- Startup runs a numbered, embedded migration chain
  (`migrate/migrate.go::ApplyMigrations`), invoked before any HTTP route or
  the matchmaking engine (`main.go::main`), so a migration failure aborts
  startup and the service never provides writes on a half-migrated schema.
- Bookkeeping table `schema_migrations` (`service`, `version`, `name`,
  `applied_at`, primary key `(service, version)`) is created by
  `migrate/migrate.go::ensureMigrationsTable`; a version is inserted only
  after it applied in full.
- Concurrency: a MySQL advisory lock
  `GET_LOCK('MatchCoreArena-Server.schema_migrations', 60)` held on a
  dedicated connection (`migrate/migrate.go::acquireAdvisoryLock`)
  serializes concurrently starting instances; the applied set is re-read
  after the lock is acquired, so exactly one instance applies each version.
- Idempotency: MySQL DDL cannot roll back, so a failed version is replayed
  from its first statement on the next startup. Every DDL step therefore
  checks `information_schema` first and skips when the object exists
  (`migrate/ddl.go::ensureColumn`, `ensureIndex`, `ensureTable`).
- v1 is the frozen baseline (`migrate/m001_baseline.go::runBaseline`
  executing `migrate/migrations/baseline.sql`): the starting point of a
  fresh database. It consists solely of `CREATE TABLE IF NOT EXISTS`, so
  replaying it on an existing database is a per-statement no-op and
  completes a partially created one. The file is never edited again.
- v2 (`migrate/m002_additions.go::applyV2`) adds exactly the structures the
  2026-11-06 batches introduced into the baseline: `matches.source /
  map_name / server_id / game_id / payload_hash` plus the `uk_game_id`
  unique index, `match_participants.is_draw`, and the five `matchmaking_*`
  tables. Columns are added with `AFTER` placement matching the baseline, so
  column order matches too.
- Convention going forward: baseline frozen, additive-only. Every future
  schema change appends a new numbered migration (v3, v4, …); existing
  migrations are never modified.

## Consequences
- Fresh databases apply v1+v2; databases initialized from the old baseline
  replay v1 as no-ops and apply v2. Both paths are verified byte-identical
  by an `information_schema` fingerprint (tables, columns incl. order,
  types, nullability, defaults, comments, indexes, foreign keys) and the
  legacy path is verified to lose no rows
  (`test/e2e/migrations_test.go::TestMigrationDualPathSchemaParity`).
- v2 is purely additive (nullable or defaulted columns, new indexes, new
  tables); the pre-v2 binary's SQL references none of the new objects, so
  applying it while the old binary still runs is safe.
- `schema_migrations` keeps the `service` column even though HRPAuth and
  HASkinLib dropped theirs when moving to `(version, dirty)` bookkeeping for
  dedicated databases; this service's database is dedicated to it, so the
  column is harmless and preserves per-service attribution.
- A version present in the bookkeeping but unknown to a (downgraded) binary
  only logs a warning; known migrations still apply idempotently.

## Evidence
- migrate/migrate.go::ApplyMigrations — ordered application, record-after-success
- migrate/migrate.go::acquireAdvisoryLock — GET_LOCK serialization on a dedicated connection
- migrate/migrate.go::ensureMigrationsTable — schema_migrations shape
- migrate/ddl.go::ensureColumn — information_schema-guarded DDL (with ensureIndex, ensureTable)
- migrate/m001_baseline.go::runBaseline — frozen v1, re-runnable by construction
- migrate/m002_additions.go::applyV2 — v2 additions (column definitions verbatim from the baseline)
- migrate/migrations/baseline.sql::matches — frozen baseline content
- main.go::main — migrations run before HTTP/matchmaking; failure aborts startup
- test/e2e/migrations_test.go::TestMigrationDualPathSchemaParity — dual-path zero-diff and data preservation
- test/e2e/migrations_test.go::TestMigrationConcurrentInstances — five concurrent instances, one bookkeeping row per version
- migrate/migrate_db_test.go::TestApplyFailureNotRecorded — failed version not recorded; replay re-enters
- test/e2e/testdata/legacy_baseline_eff1784.sql — legacy fixture (git show eff1784:migrate/migrations/baseline.sql)
