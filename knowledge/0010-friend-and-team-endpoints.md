# 0010. Friend and Team Endpoints Are Now Fully Implemented

## Status
Accepted (superseded former "explicitly unimplemented" decision)

## Context
The friend and team services were shipped as explicit 501 stubs (former
decision recorded here on initial writing). On 2026-11-12 the author
implemented them with real SQL against the existing baseline tables. The
implementation landed as commit `9faa44c` but was accidentally reverted by
the subsequent merge conflict resolution on `1010c52`. This batch
(`2026-11-12`) restores the real implementation from `9faa44c` and aligns
the contract.

## Decision
- `internal/service/friend.go` now performs real SQL: bidirectional
  lookup on `(user_uid, friend_uid)`, insert pending requests, duplicate
  rejection ("already friends" / "pending request exists"), and
  accept/reject/delete state transitions. Accept/reject/delete verify
  that the caller is one of the two sides of the relationship.
- `internal/service/team.go` implements team creation (auto-inserts the
  leader as member), member management (add/remove/role update), and a
  three-role model (0=member, 1=vice, 2=leader). Role enforcement: only
  leader/vice manage members, leader is protected from demotion/removal,
  and role-snipe (non-target caller) is blocked.
- The twelve HTTP endpoints are unchanged (routes, auth, envelope shape)
  and now return real data on success instead of 501. Business failures
  surface as the reserved typed error codes: `mca_friend_request_exists`
  (409, duplicate friend request/relationship), `mca_friend_not_found`
  (404, also used when the caller is not a party of the relationship, so
  existence is not leaked), `mca_team_not_found` (404, incl. disbanded),
  `mca_team_name_taken` (409), `mca_not_team_leader` (403), and
  `mca_invalid_request` (400, rule violations such as duplicate team
  member or leader-protected mutations — the team member endpoints
  document no 409, so member-level conflicts stay 400). The former plain
  `errors.New` returns (which collapsed to 500 internal) were replaced
  with `apperr.New` typed errors in this batch.
- Handler binding fix (contract compliance): `AddMember` previously bound
  `role` with `binding:"required"` on a plain int, which rejects the
  contract-valid default (role absent / 0 = plain member); it now binds a
  pointer and defaults to 0. `UpdateMemberRole` keeps `required` but on a
  pointer, so the contract-valid `role: 0` (demote vice-leader) is no
  longer misread as a missing field. Without this fix, plain-member adds
  and demotions were unreachable over HTTP.
- HA-Contract bumped to v1.14.0 for these endpoints: the "Currently
  unimplemented" description lines and extra `'501'` response entries were
  removed; the existing success-code and shape definitions were already
  correct and required no change.

## Consequences
- Clients (including MatchCoreArena-Client) can now consume real friend
  and team data. The desktop client previously rendered the 501 envelope
  into its friend-list empty state; it now receives real lists when the
  server has rows.
- The old `friend_team_notimpl_test.go` and `api/handler/friend_team_test.go`
  (both pinned to 501) were deleted. Real DB-backed coverage lives in
  `test/e2e/e2e_test.go` under the MariaDB test container — the "friend
  CRUD" and "team CRUD" subtests pin the full happy path plus the typed
  failure codes above.
- `baseline.sql` tables `friends`, `teams`, `team_members` now have
  real read/write traffic at runtime; no schema change was needed.

## Evidence
- internal/service/friend.go — full SQL implementation with typed apperr codes
- internal/service/team.go — full SQL implementation with role enforcement and typed apperr codes
- api/handler/friend.go::Request — still resolves username via HRPAuth, now calls real svc.Request
- api/handler/team.go — all seven team methods call real svc methods; role binding is pointer-based (0-value safe)
- migrate/migrations/baseline.sql — friends/teams/team_members tables unchanged
- test/e2e/e2e_test.go — "friend CRUD" and "team CRUD" subtests (MariaDB, run via test/e2e/run-e2e.sh)
- Contract HA-Contract/docs/api/openapi/matchcorearena-business.yaml — version 1.14.0, no unimplemented note
