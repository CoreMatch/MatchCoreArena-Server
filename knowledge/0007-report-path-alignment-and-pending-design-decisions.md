# 0007. Report-Path Schema Alignment and Pending Design Decisions

## Status
Accepted

## Date
2026-11-04

## Context
The baseline schema, the streak query and the match report code path must
agree on which columns carry team-match results. Separately, several
design-level issues in this repository are still waiting for an author
decision. This entry records the current alignment and the open items.

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

### Pending author decisions

1. **Schema drift on existing databases**: the baseline only runs against
   empty databases; already-initialized databases that miss
   `match_participants` or the team columns need the statements applied
   manually (the full schema lives in `baseline.sql`). Whether an
   incremental migration mechanism is wanted is a deployment decision.
2. **Placeholder token committed in `config/tokens.yaml`** — deployment
   convention; explicitly deferred by the author.
3. **K-factor constants**: the code comment cites a "CS2 Premier Rating
   research report"; the derivation is a binary PDF in the repo root and is
   not verifiable from text. Whether to document the derivation is open.
4. **Dead score columns**: `matches.winner_score` / `loser_score` /
   `winner_perf` / `loser_perf` are never written by the report path (they
   keep their defaults). Kept for now because they have defaults and exist
   in production databases; their intended semantics need a decision.
5. **Season hardcoded to 1** in `updateRanking` calls
   (`internal/service/match.go`); season handling is not designed yet.
6. **`perf_tweak` is hardcoded to 1.0 by the reporter** (BedwarsRel-MCA), so
   the individual performance weight currently has no effect end-to-end.
7. **No README** in this repository; documentation lives in `knowledge/` and
   `bwrel.md` investigation notes only.

## Consequences
- Fresh databases stay column-compatible with existing production databases;
  `matches.winner_uid` / `loser_uid` carry no meaning for team matches and
  exist only for that compatibility.
- Streaks reflect real per-player results because they read
  `match_participants`.
- The pending items above remain open; each needs a semantic or design
  choice, not a mechanical fix.

## Evidence
- migrate/migrations/baseline.sql::winner_uid — winner_uid/loser_uid NOT NULL DEFAULT 0; match_participants and matches.winner_team_id / survival_rate part of the baseline
- internal/service/match.go::winner_team_id — report INSERT columns (winner_uid/loser_uid not written)
- internal/service/match.go::getUserStreak — streak query over match_participants.is_winner
- internal/service/match.go::updateRanking — season hardcoded to 1
