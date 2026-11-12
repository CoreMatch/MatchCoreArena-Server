# 0010. Friend and Team Endpoints Are Explicitly Unimplemented

## Status
Accepted

## Context
The friend and team services shipped as skeleton stubs: some methods
returned hardcoded fake successes (e.g. a friend request answering a
fabricated ID=1 record) and the rest answered empty results, so callers
could not tell the features do nothing. Whether to implement them, take
the endpoints down, or replace the fake data with explicit errors was a
pending author decision; the author chose explicit errors without
implementing the features.

## Decision
- Every `FriendService` and `TeamService` method answers a typed error
  `mca_not_implemented` (HTTP 501, unified error envelope); none returns
  data or a success envelope.
- The twelve HTTP endpoints stay registered and authenticated exactly as
  before (`/api/friends`, `/api/friends/{id}/accept|reject`,
  `/api/friends/{id}`, `/api/teams`, `/api/teams/{id}`,
  `/api/teams/{id}/members`, `/api/teams/{id}/members/{uid}/role`,
  `/api/teams/{id}/members/{uid}`); only their business outcome changes
  to 501. `POST /api/friends` still resolves the target username through
  HRPAuth before reaching the unimplemented service.
- The friends/team tables remain in the baseline schema for a future
  implementation; the reserved error codes (`mca_friend_not_found`,
  `mca_friend_request_exists`, `mca_team_not_found`,
  `mca_team_name_taken`, `mca_not_team_leader`) stay registered but no
  code path returns them yet.

## Consequences
- Clients calling these endpoints receive an actionable 501 instead of
  plausible-looking fake data; the desktop client's friend list renders
  the error rather than an empty/fake list.
- Implementing the features later only touches the service bodies; the
  routes, auth, envelope and reserved error codes are already in place.

## Evidence
- internal/apperr/codes.go::CodeMCANotImplemented — mca_not_implemented registered with HTTP 501
- internal/service/friend.go::errFriendNotImplemented — all five friend methods answer the typed error
- internal/service/team.go::errTeamNotImplemented — all seven team methods answer the typed error
- api/handler/friend.go::Request — username resolution kept ahead of the unimplemented service
- api/handler/friend_team_test.go::TestFriendTeamEndpointsAnswer501 — all twelve endpoints pinned to 501 + envelope code
- internal/service/friend_team_notimpl_test.go::TestFriendServiceNotImplemented — service-level pin (no fake success)
- migrate/migrations/baseline.sql — friends/teams/team_members tables retained
