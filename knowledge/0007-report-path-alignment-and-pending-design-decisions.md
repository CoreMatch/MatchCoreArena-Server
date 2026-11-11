# 0007. Report-Path Schema Alignment and Pending Design Decisions

## Status
Accepted

## Context
The baseline schema, the streak query and the match report code path must
agree on which columns carry team-match results. Separately, several
design-level issues in this repository were waiting for an author decision.
This entry records the alignment, tracks the decisions the author has since
taken, and lists the items still open. It is the tracker: when an open item
is decided, the decision lands here.

## Decision

### Schema and report-path alignment

- `matches.winner_uid` / `loser_uid` are `BIGINT NOT NULL DEFAULT 0` and are
  kept although the report INSERT (`internal/service/match.go`) never writes
  them: under strict SQL mode an INSERT into a `NOT NULL` column without a
  default fails, and the defaults also keep fresh databases column-compatible
  with existing production databases.
- The only persisted per-player win/loss fact is
  `match_participants.is_winner` (team-match reports never populate
  `matches.winner_uid` / `loser_uid`), so `getUserStreak` reads
  `match_participants` joined to `matches`.

### Author decisions taken (2026-11-12)

1. **Dead score columns become real settlement writes** (former open item 4).
   `matches.winner_score` / `loser_score` / `winner_perf` / `loser_perf` are
   written at settlement instead of keeping their defaults:
   - `winner_score` / `loser_score` carry the winning/losing side's
     pre-settlement rating — the very input of the Elo computation (the team
     average rating; for 1v1 that degenerates to the single player's rating),
     so a settled match can be recomputed later;
   - `winner_perf` / `loser_perf` carry the performance factor that actually
     took effect in that match (the reported `perf_tweak` after server
     validation: clamped to [0.95, 1.05], absent/0 treated as 1.0), averaged
     over the winning/losing team's members;
   - all-draw matches have no winner/loser side and keep the column defaults
     (scores 0, perf 1.0).
   Existing rows are left untouched — no backfill. The columns carry meaning
   only for matches settled by the version that introduced the writes; older
   rows hold unknown/default values. Implemented: the settlement INSERT
   writes all four columns (`internal/service/match.go::perfAvgOf` computes
   the effective-perf average); the columns stay out of the API surface, so
   no contract schema changed.
2. **`virtual` reports settle as usual — deliberately** (former open item 8).
   Author decision: no separation, no filtering, and no retroactive rollback
   of already-settled `virtual` matches. Ops/debug-injected matches are
   supposed to move real ratings and leaderboards — a debug injection that
   affected nothing would be pointless. Settlement therefore keeps no
   `source` branch (see 0009); `source` remains a provenance marker and an
   ops-query filter only.
3. **Party rosters require consent** (former open item 10). The privilege
   hole — any authenticated player could queue arbitrary UUIDs and settle
   matches moving their ratings — is closed by author decision:
   - the caller must be part of the submitted `party_members` roster
     (otherwise the join is rejected with a readable error);
   - every other roster member must have ACCEPTED an invitation from the
     caller (default deny): a minimal invite/accept state machine
     (pending → accepted / rejected / expired; an acceptance is consumed by
     the party join it enabled) with dedicated endpoints; reject and expiry
     have explicit typed answers. Implemented (contract 1.11.0, migration
     v5): `/api/matchmaking/invites` (+ accept/reject) and the join-time
     consent checks, see 0012.

### Pending author decisions

The following 3 items still wait for an author decision; each needs a
semantic or design choice, not a mechanical fix.

1. **Season hardcoded to 1** in `updateRanking` calls
   (`internal/service/match.go`); season handling (windows, snapshots,
   per-season boards) is explicitly deferred by the author — no season code
   or tables exist by design until then.
2. **`perf_tweak` is hardcoded to 1.0 by the reporter** (BedwarsRel-MCA), so
   the individual performance weight currently has no effect end-to-end
   (the server-side mechanism is live and validated, see decision 1 above).
3. **Duration equality strictness**: a positive `duration_seconds` must
   exactly equal `finished_at - started_at` (0011, parity with the ops
   panel). If real game reporters ever send a measured duration that
   legitimately differs from the timestamp span, this rule would reject
   those reports; whether to relax it is open.

### Resolved items (no longer open)

- **K-factor derivation** (former open item 3, decided 2026-11-12):
  documented reviewably in 0020 — each constant (520 / 390 / 190) maps to
  an explicit passage of the research PDF; the values themselves are
  unchanged.
- **Schema drift on existing databases** (former open item 1): resolved by
  the versioned migration chain (0017) — v1 frozen baseline + additive v2,
  advisory-lock serialization across concurrently starting instances,
  `information_schema`-guarded idempotent DDL, and a dual-path
  byte-identical parity test. Already-initialized databases catch up
  automatically on startup; manual statements are no longer needed.
- **Matchmaking vs live game provisioning** (former open item 9, second
  half): the design is in place — formed matches carry `join_target` (0015),
  the proxy queries assignments for routing (0018) and the room layer
  reserves from a static inventory with TTL release (0019).
  `matchmaking.join_target` stays empty by convention until a real game
  server exists; the empty value is intentional, not a gap.

### Operational notes (not semantic decisions)

- **Matchmaking parameters** (former open item 9, first half): window
  size/growth, queue timeout and tick interval are conservative defaults in
  `config/config.go`, all yaml-configurable. Tuning them is an operational
  matter, not a semantic decision.
- **Service tokens** (former open item 2): the placeholder token is no
  longer committed — `config/tokens.yaml` is gitignored and only
  `config/tokens.yaml.example` is tracked. The remaining gap is an
  operational fact, not a semantic decision: the production bare-metal
  location of `tokens.yaml` is recorded nowhere (the deploy manifest notes
  the container template path `MCA_TOKENS_PATH=/app/data/tokens.yaml` and
  leaves `data_dirs` empty).

## Consequences
- Fresh databases stay column-compatible with existing production databases;
  `matches.winner_uid` / `loser_uid` carry no meaning for team matches and
  exist only for that compatibility.
- Streaks reflect real per-player results because they read
  `match_participants`.
- The 4 pending items above remain open. The 2026-11-12 decisions are
  settled: score/perf columns are written by settlement, `virtual` results
  settle deliberately, and party joins require caller membership plus member
  consent.

## Evidence
- migrate/migrations/baseline.sql::winner_uid — winner_uid/loser_uid NOT NULL DEFAULT 0; match_participants and matches.winner_team_id / survival_rate part of the baseline
- internal/service/match.go::winner_team_id — report INSERT columns (winner_uid/loser_uid not written)
- internal/service/match.go::getUserStreak — streak query over match_participants.is_winner
- internal/service/match.go::updateRanking — season hardcoded to 1
- internal/service/match.go::perfAvgOf — settlement writes winner/loser_score (pre-settlement side averages, the Elo inputs) and winner/loser_perf (effective perf_tweak averages); draws keep column defaults
- migrate/migrate.go::ApplyMigrations — versioned migration chain resolving former open item 1 (see 0017)
- .gitignore (field: config/tokens.yaml) — real service-token file untracked; only the example is committed
- deploy-manifest.yaml (field: data_dirs) — production tokens.yaml location recorded nowhere
