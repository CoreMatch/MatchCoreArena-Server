# 0003. Player Identity Is the HRPAuth UID; Minecraft UUIDs Are Resolved Through HRPAuth

## Status
Accepted

## Date
2026-09-18 (UUID-to-UID resolution; HRPAuth-backed auth introduced 2026-09-11)

## Context
Match reports arrive from the Minecraft server, where players are identified by
Minecraft (Mojang) UUIDs. MCA's own storage and all account data (credentials,
email, username) live in the separate HRPAuth account system. MCA needs one stable
internal identity key and a way to bridge the two identifier spaces.

## Decision
- The internal identity key is the HRPAuth user id: `users.uid BIGINT PRIMARY KEY`
  is the HRPAuth UID, and MCA keeps no credentials of its own.
- API authentication is delegated to HRPAuth: the Bearer token is verified by
  calling HRPAuth `POST /user`, which yields the UID; results are cached in Redis
  for 5 minutes. MCA's own auth endpoints are a transparent proxy to HRPAuth
  (login-ticket, TOTP verify, refresh, revoke).
- Match reports carry Minecraft UUIDs per participant. For each UUID, MCA calls
  HRPAuth `POST /user/lookup-uuid` to obtain the UID, caching the mapping in Redis
  for 24 hours.
- MCA contains no Yggdrasil / Minecraft protocol code. The only Minecraft-related
  datum in the codebase is the `mojang_uuid` field passed through from the HRPAuth
  `/user` response.

## Consequences
- Costs and limitations:
  - Hard runtime dependency on HRPAuth: startup fails if HRPAuth `/status` is
    unreachable, token verification needs HRPAuth (or a warm cache), and a match
    report aborts entirely if any participant's UUID cannot be resolved or the
    resolved UID has no local `users` row.
  - The 24-hour UUID→UID cache has no invalidation hook; remapped identities would
    stay stale until expiry.
  - Local `users` rows must exist before a participant can be rated. An
    `EnsureUser` auto-provisioning method exists but has no call sites in the
    current code, so how production rows are created is not documented in the
    repo (the raw-SQL maintenance endpoint is the only visible mechanism) —
    pending author confirmation.
  - Service-level identities use negative UIDs (-1 maintenance, -100
    match-reporter) to stay disjoint from real users.

## Evidence
- migrate/migrations/baseline.sql:7-18 — users table keyed by uid BIGINT
- internal/service/user.go:28 — comment: uid is HRPAuth User.UID
- internal/auth/verifier.go:66-95 — token verification via HRPAuth /user with 5-minute Redis cache
- internal/auth/client.go:55-85 — GetUser (POST /user), mojang_uuid pass-through at :68
- internal/auth/client.go:331-363 — LookupUserByUUID (POST /user/lookup-uuid)
- internal/service/match.go:59-65 — participants reported by Minecraft UUID
- internal/service/match.go:114-126 — report fails if any UUID resolution fails
- internal/service/match.go:271-295 — resolveUID with 24-hour Redis cache (`mca:uuid_to_uid:`)
- internal/service/match.go:297-307 — report fails when the UID has no local users row
- internal/service/user.go:72-76 — EnsureUser defined; no call sites found in repo (gap)
- internal/auth/types.go:10 — MojangUUID field in UserResult
- internal/auth/tokens.go:56-69 — negative UIDs for service identities
- internal/auth/client.go:28-45 — startup reachability check against HRPAuth /status
- api/handler/auth.go:13-17 — AuthHandler documented as transparent proxy to HRPAuth; :34-132 — login-ticket / totp-verify / refresh / logout handlers
- commit 5161b1ba5a1fc867ab175d6ec0604520cc0df0ec — feat: add UUID-to-UID resolution for match service (2026-09-18)
- commit 15321c7341326a771c6ea7a78e068987c953ad23 — feat(auth): rewrite auth system to use HRPAuth transparent proxy with Redis caching (2026-09-11)
- commit ab2efe1aee2b7648386d5b382d96c818067419c3 — feat: add HRPAuth OAuth2/OIDC auth support (2026-09-11)
- Whether MCA is expected to talk to HRPAuth-Yggdrasil-API directly in the future: intent not documented in repo; pending author confirmation
