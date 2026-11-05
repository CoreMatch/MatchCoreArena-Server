# 0007. Cleanup Pass Checklist: Fixed Items and Pending Decisions

## Status
Accepted

## Date
2026-11-04 (cleanup commits listed in Evidence)

## Context
Per the author's 2026-11 instruction, the three MatchCoreArena repositories had
drifted and needed a low-risk cleanup pass: fix only items with a single correct
answer (compile breaks, schema-vs-code mismatches, stale comments/tests), and
defer everything that requires a design decision. This entry is the checklist for
this repository. It updates (does not rewrite) entry 0005's note about the orphan
migration file.

## Decision

### Fixed in this pass

1. Baseline schema aligned with the match report code path (commit `eff1784`):
   - `matches.winner_uid` / `loser_uid` were `BIGINT NOT NULL` without
     defaults, but the report INSERT (`internal/service/match.go`) never
     writes them. Under strict SQL mode every report failed with
     `ERROR 1364 (Field 'winner_uid' doesn't have a default value)` on a
     fresh database — reproduced on MariaDB 11.8.8. Both columns now have
     `DEFAULT 0` and are kept, so fresh databases remain column-compatible
     with existing production databases.
   - The orphan `20260918_add_match_participants.sql` was embedded but never
     executed (the runner only applies `baseline.sql` to empty databases),
     while the code requires the `match_participants` table and the
     `matches.winner_team_id` / `survival_rate` columns. Its content was
     folded into `baseline.sql` and the file deleted. Fresh databases now get
     the full schema; already-initialized databases are untouched (the
     runner skips them). Deployed databases that miss the table still need
     the statements applied manually (SQL preserved in git history at the
     parent of `eff1784`).
2. Streaks computed from `match_participants` (commit `218bb99`):
   `getUserStreak` read `matches.winner_uid` / `loser_uid`, which team match
   reports never populate, so streaks could never reflect real results. The
   query now reads `match_participants.is_winner` (the only persisted
   per-player win/loss fact); the streak accumulation logic is unchanged.
3. Rankings comment corrected (commit `61b960c`): the comment claimed the
   rankings routes were public, but they are registered inside the
   authenticated group (`api/router.go`). Comment-only change; routing
   behavior is unchanged.
4. Obsolete `TestGetRoundWeight` removed (commit `3363e1d`): it called
   `GetRoundWeight`, which does not exist (replaced by `GetSurvivalWeight`
   with different semantics), and made `go vet` / `go test` fail to compile.
5. Stale config test expectations aligned (commit `2eb0ba1`): default HRPAuth
   base URL is `http://localhost:2778` [xref] (`config/config.go::createDefaultConfig`;
   2778 is HRPAuth's actual listen port), and `Load()` migrates older
   config files to `CurrentVersion` (documented behavior in `config/config.go`).

### Not fixed — requires author decision

1. **`EnsureUser` is defined but never called — RESOLVED 2026-11-05.**
   The author decided on lazy initialization: the settlement transaction
   creates a missing `users` row itself
   (`internal/service/match.go::getUserRatingInfo`, uid only from the
   verified resolution result, table-default rating) and the dead
   `EnsureUser` method was deleted. See knowledge 0003 (Consequences).
2. **Schema drift on existing databases**: the baseline only runs against
   empty databases; production databases created before 2026-11-04 may lack
   `match_participants` and the two team columns. Whether/when to apply them
   manually (and whether an incremental migration mechanism is wanted) is a
   deployment decision.
3. **Placeholder token committed in `config/tokens.yaml`** — deployment
   convention; explicitly deferred by the author.
4. **K-factor constants**: the code comment cites a "CS2 Premier Rating
   research report"; the derivation is a binary PDF in the repo root and is
   not verifiable from text. Whether to document the derivation is open.
5. **Dead score columns**: `matches.winner_score` / `loser_score` /
   `winner_perf` / `loser_perf` are never written by the report path (they
   keep their defaults). Kept for now because they have defaults and exist
   in production databases; their intended semantics need a decision.
6. **Season hardcoded to 1** in `updateRanking` calls
   (`internal/service/match.go`); season handling is not designed yet.
7. **`perf_tweak` is hardcoded to 1.0 by the reporter** (BedwarsRel-MCA), so
   the individual performance weight currently has no effect end-to-end.
8. **No README** in this repository; documentation lives in `knowledge/` and
   `bwrel.md` investigation notes only.

## Consequences
- Verified 2026-11-04 after the fixes: `go build ./...`, `go vet ./...`,
  `go test ./...` all pass (before the pass: `go vet`/`go test` failed to
  compile because of `TestGetRoundWeight`; both config tests were red).
- SQL verification level: full execution against a local MariaDB 11.8.8 in
  strict mode (`STRICT_TRANS_TABLES`). Every statement of the report
  transaction (matches INSERT, participants INSERT, users UPDATE, rankings
  upsert, streak SELECT, detail/history SELECTs) succeeds on the new
  baseline; the pre-fix baseline rejects the matches INSERT with ERROR 1364.
- Pending items above remain open; each needs a semantic or design choice
  that this pass was explicitly not allowed to make.

## Evidence
- `eff1784` fix(db): align baseline schema with the match report code path
- `218bb99` fix(match): compute streaks from match_participants
- `61b960c` docs(api): correct rankings comment to match actual auth behavior
- `3363e1d` test(rating): remove obsolete TestGetRoundWeight
- `2eb0ba1` test(config): align stale expectations with current behavior
- `migrate/migrations/baseline.sql::match_participants` (matches + match_participants after the fix)
- `internal/service/match.go::winner_team_id` (report INSERT columns), `internal/service/match.go::getUserStreak` (rewritten streak query)
- `api/router.go::Register` (rankings inside the authenticated group)
- MariaDB 11.8.8 session 2026-11-04: new baseline accepted all report
  statements; old baseline returned `ERROR 1364 (HY000): Field 'winner_uid'
  doesn't have a default value` for the same INSERT.
