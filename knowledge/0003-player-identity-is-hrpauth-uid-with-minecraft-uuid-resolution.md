# 0003. Player Identity Is the HRPAuth UID; Minecraft UUIDs Are Resolved Through HRPAuth

## Status
Accepted

## Date
2026-09-18

## Context
Match reports arrive from the Minecraft server, where players are identified by
Minecraft (Mojang) UUIDs. MCA's own storage and all account data (credentials,
email, username) live in the separate HRPAuth account system. MCA needs one stable
internal identity key and a way to bridge the two identifier spaces.

## Decision
- The internal identity key is the HRPAuth user id: `users.uid BIGINT PRIMARY KEY`
  is the HRPAuth UID, and MCA keeps no user credentials of its own.
- API authentication is delegated to HRPAuth: the Bearer token is verified by
  calling HRPAuth `POST /user`, which yields the UID; results are cached in Redis
  for 5 minutes. MCA's own auth endpoints are a transparent proxy to HRPAuth
  (login-ticket, TOTP verify, refresh, revoke).
- Match reports carry Minecraft UUIDs per participant. For each UUID, MCA calls
  HRPAuth's contracted ops-level service endpoint `GET /services/users/{uuid}`
  to obtain the UID, caching the mapping in Redis for 24 hours. The endpoint
  is service-to-service, so MCA holds HRPAuth service credentials
  (`auth.hrpauth.service_client_id/secret`, deployment injects the built-in
  super-client pair, same source as Yggdrasil-API's `core_api` credentials),
  caches the issued `client_credentials` service token and refreshes it
  before expiry. This design exists because HRPAuth
  never implemented the user-facing `POST /user/lookup-uuid` originally
  assumed (HA-Contract knowledge/0006), so resolution uses the contracted
  service endpoint under a service identity instead. Credential or token
  failures surface as the typed `ErrServiceAuth`, an unknown UUID keeps the
  `ErrUserNotFound` semantics.
- MCA contains no Yggdrasil / Minecraft protocol code. The only Minecraft-related
  datum in the codebase is the `mojang_uuid` field passed through from the HRPAuth
  `/user` response.

## Consequences
- Costs and limitations:
  - Hard runtime dependency on HRPAuth: startup fails if HRPAuth `/status` is
    unreachable, token verification needs HRPAuth (or a warm cache), and a match
    report aborts entirely if any participant's UUID cannot be resolved.
  - The 24-hour UUID→UID cache has no invalidation hook; remapped identities would
    stay stale until expiry.
  - Local `users` rows are provisioned lazily: when a resolved UID has no
    local row, the settlement transaction creates it
    (`INSERT IGNORE INTO users (uid)`) and continues. The trust boundary is
    deliberate: the uid comes only from the current request's
    verified-token/HRPAuth resolution (`resolveUID`), never from the report
    payload, and initial rating fields are the table defaults, never
    reporter-supplied. If provisioning is impossible (e.g. the uid is held by
    a soft-deleted row), the report aborts with typed
    `409 mca_user_not_provisioned`.
  - Service-level identities use negative UIDs (-1 maintenance, -100
    match-reporter) to stay disjoint from real users.

## Evidence
- migrate/migrations/baseline.sql::users — users table keyed by uid BIGINT
- internal/auth/verifier.go::Verify — token verification via HRPAuth /user with 5-minute Redis cache
- internal/auth/client.go::GetUser — GetUser (POST /user), mojang_uuid pass-through (internal/auth/client.go::mojang_uuid)
- internal/auth/client.go::LookupUserByUUID — LookupUserByUUID (GET /services/users/{uuid}, service-token authenticated)
- internal/auth/client.go::serviceToken — client_credentials service token with caching and pre-expiry refresh (typed ErrServiceAuth on failure)
- config/config.go::HRPAuthConfig — service_client_id / service_client_secret (config 1.5.0)
- internal/service/match.go::ParticipantReport — participants reported by Minecraft UUID
- internal/service/match.go::resolveUID — report fails if any UUID resolution fails
- internal/service/match.go::resolveUID — resolveUID with 24-hour Redis cache (`mca:uuid_to_uid:`)
- internal/service/match.go::getUserRatingInfo — lazy provisioning: missing row → INSERT IGNORE (uid from resolveUID only, table-default rating) → continue; impossible provisioning → typed mca_user_not_provisioned (409)
- internal/auth/types.go::MojangUUID — MojangUUID field in UserResult
- internal/auth/tokens.go::createDefaultTokenStore — negative UIDs for service identities
- internal/auth/client.go::NewClient — startup reachability check against HRPAuth /status
- api/handler/auth.go::AuthHandler — AuthHandler documented as transparent proxy to HRPAuth; api/handler/auth.go::LoginTicket, api/handler/auth.go::TotpVerify, api/handler/auth.go::Refresh, api/handler/auth.go::Logout — login-ticket / totp-verify / refresh / logout handlers
- commit 5161b1ba5a1fc867ab175d6ec0604520cc0df0ec — feat: add UUID-to-UID resolution for match service (2026-09-18)
- commit 15321c7341326a771c6ea7a78e068987c953ad23 — feat(auth): rewrite auth system to use HRPAuth transparent proxy with Redis caching (2026-09-11)
- commit ab2efe1aee2b7648386d5b382d96c818067419c3 — feat: add HRPAuth OAuth2/OIDC auth support (2026-09-11)
- Whether MCA is expected to talk to HRPAuth-Yggdrasil-API directly in the future: intent not documented in repo; pending author confirmation
