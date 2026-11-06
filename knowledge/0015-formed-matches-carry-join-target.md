# 0015. Formed Matches Carry the Game Server join_target When Configured

## Status
Accepted

## Context
After matchmaking forms a group, the player must reach the game server. The
client handoff chain (MatchCoreArena-Client INTEROP-SPEC v1 sender →
SamuelClient receiver) launches the game at a `join_target` (host/port)
that, until now, came only from the client's local configuration — the
arena server provided no address, so deployments could only guess via
local settings.

## Decision
- `matchmaking.join_target` (host, port) is an optional server
  configuration block (config v1.8.0). Empty host means "not configured".
- When configured, every formed-group record returned by
  `GET /api/matchmaking/status` carries `join_target: {host, port}` — the
  same field name and shape as the client's interop payload
  (`internal/interop` JoinTarget). When not configured the field is omitted
  entirely (omitempty) and client behavior is unchanged (local-config
  fallback).
- The address is attached at read time in the matchmaking service
  (`attachJoinTarget`), not stored with the group: it follows current
  server configuration, and formed-group rows stay deployment-agnostic.

## Consequences
- One global join_target for all modes; per-mode or per-region targets are
  not supported (none are needed today).
- The server asserts the address is reachable nowhere — delivery and
  reachability probing remain the client's job (3-second dial + manual-join
  fallback on the sender side).

## Evidence
- config/config.go::JoinTargetConfig — optional block, Configured() semantics, port validation
- config/config.go::CurrentVersion — config 1.8.0 migration (no-op: absent block = unconfigured)
- internal/service/matchmaking.go::JoinTarget — wire shape aligned with the client interop payload
- internal/service/matchmaking.go::attachJoinTarget — read-time attachment to formed groups
- internal/service/matchmaking.go::Status — status response enrichment
- test/e2e/e2e_test.go::TestMatchCoreE2E — formed group carries configured join_target end-to-end
- Cross-repo (read-only): /home/lnb/Desktop/HA/MatchCoreArena-Client/internal/interop/interop.go::JoinTarget — client-side shape; /home/lnb/Desktop/HA/MatchCoreArena-Client/knowledge/0012-match-handoff-sender-interop-spec-v1.md — local-config fallback behavior
