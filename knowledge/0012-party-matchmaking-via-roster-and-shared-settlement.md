# 0012. Party Matchmaking via party_members Roster and Shared Settlement

## Status
Accepted

## Context
Groups of players must queue, match and settle as one unit. The ops panel
already sends a `party_members` field (member UUID list) in its join
requests; the author directed the server to adopt that exact field and model
rather than a parallel mechanism.

## Decision
- `POST /api/matchmaking/join` accepts `party_members` (member UUID list).
  Non-empty means "join as a party": the server resolves each UUID to a
  HRPAuth UID through the same ops-level endpoint and trust boundary as
  match reports (0003), creates an EPHEMERAL party
  (`matchmaking_parties` + `matchmaking_party_members`, leader_uid = first
  roster member) and enqueues every member with that `party_id` in one
  transaction. Roster rules: 2..(mode max team size) distinct members
  (oversized → `409 mca_party_full`), unknown UUID → `404 mca_user_not_found`,
  member already in a queued party → `409 mca_party_already_in_party`.
- Parties exist only while queued: committed groups and whole-party leaves
  delete the party rows. There are no standalone party-management endpoints.
- The engine treats a party as one matching unit (average member rating,
  longest member wait) and never splits it across teams; greedy packing
  fills the mode's team sizes and balances headcount. Group members record
  their `party_id`.
- Shared settlement: reporting a match with `matchmaking_group_id` pointing
  at the formed group makes same-party participants (>= 2 of them in the
  match) share the average of their individually computed deltas
  (`applyPartySharing`, rounding away from zero), so a party's members move
  together regardless of individual perf tweaks.
- Solo queue is unchanged, except that a user already in a queued party
  cannot solo-queue (`409 mca_party_already_in_party`).

## Consequences
- Consent is roster-based: whoever submits `party_members` queues the listed
  users. Whether production needs member-side consent checks is a pending
  author decision (0007).
- Party matching uses the members' rating snapshot at join time; rating
  changes between join and formation do not re-window the party.

## Evidence
- api/handler/matchmaking.go::joinInput — party_members request field
- internal/service/matchmaking.go::joinAsParty — roster resolution, ephemeral party creation, whole-party enqueue
- internal/service/matchmaking.go::Leave — whole-party dequeue and party cleanup
- internal/service/matchmaking.go::buildUnits — party aggregation (average rating, max wait)
- internal/service/matchmaking.go::commitGroup — party cleanup on group commit
- internal/service/match.go::applyPartySharing — same-party delta averaging via matchmaking_group_id
- internal/service/uuid_resolve.go::resolveUUID — shared UUID→uid resolution (Redis cache + HRPAuth)
- internal/apperr/codes.go::CodeMCAPartyAlreadyInParty — mca_party_already_in_party / mca_party_full
- migrate/migrations/baseline.sql::matchmaking_parties — party and party-member tables (uk_user: one party per user)
- test/e2e/e2e_test.go::TestMatchCoreE2E — party end-to-end (roster join, unsplit teams, shared settlement, cleanup)
