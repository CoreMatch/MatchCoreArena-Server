# 0008. Ecosystem Boundary: HRPAuth Is the Only Outbound Runtime Dependency

## Status
Accepted

## Date
2026-09-11

## Context
MCA lives in the HA ecosystem next to HRPAuth (account core),
HRPAuth-Yggdrasil-API (Minecraft auth protocol), HASkinLib / HASkinProxy
(skins), WinnerProxy, the BedwarsRel-MCA plugin, and operational tools
(MatchCoreArena-Client, MatchCoreArena-MaintenanceView). This entry records
which of these MCA actually talks to and which it deliberately does not.

## Decision
Dependency boundary (all checks are repository-wide searches):

- **HRPAuth — the only outbound runtime dependency.** MCA calls:
  `GET /status` (startup reachability check), `POST /oauth/login-ticket`,
  `POST /totp/verify`, `POST /oauth/token`, `POST /oauth/revoke`,
  `POST /user/lookup`, `GET /services/users/{uuid}` (ops-level, service-token
  authenticated, see 0003), and `POST /user` (token verification). Base URL
  comes from `auth.hrpauth.base_url`
  (default `http://localhost:2778`, HRPAuth's actual listen port). [xref]
- **Inbound consumers** (registered routes, see 0002 for the reporter):
  BedwarsRel-MCA via `POST /api/matches`; MatchCoreArena-Client via the
  user-facing `/api/*` routes; MatchCoreArena-MaintenanceView via
  `POST /api/maintenance/sql` with the management token.
- **HRPAuth-Yggdrasil-API / Yggdrasil protocol — no dependency.** Zero
  occurrences of "yggdrasil" in Go code, config, or scripts. MCA never
  authenticates Minecraft clients itself (see 0003/0004).
- **WinnerProxy — no runtime dependency.** The only reference is a code
  comment noting that the auth client's design pattern mirrors
  WinnerProxy's.
- **HASkinLib / HASkinProxy — no dependency.** Zero references; MCA stores
  no skin data.
- **HA-Contract — documentation only.** The OpenAPI specs describe MCA's
  outward behavior; there is no runtime link.

## Consequences
- HRPAuth unavailability is MCA's single external failure mode: startup
  aborts if `/status` is unreachable, and token verification falls back to
  the 5-minute cache only (see 0003).
- Yggdrasil-protocol or skin-pipeline changes cannot affect MCA directly;
  only changes to HRPAuth's `/user*`, `/services/*` and `/oauth/*` endpoints
  can.
- Costs/limitations: the boundary is enforced by absence, not by an explicit
  architectural rule in the repo; nothing prevents future code from adding
  new outbound dependencies. Why MCA resolves identity through HRPAuth
  rather than ever querying the Yggdrasil layer directly is not documented —
  pending author confirmation.

## Evidence
- internal/auth/client.go::NewClient (`GET /status`), internal/auth/client.go::GetLoginTicket (`/oauth/login-ticket`), internal/auth/client.go::VerifyTotp (`/totp/verify`), internal/auth/client.go::RefreshToken (`/oauth/token`), internal/auth/client.go::RevokeToken (`/oauth/revoke`), internal/auth/client.go::LookupUserByUsername (`/user/lookup`), internal/auth/client.go::LookupUserByUUID (`GET /services/users/{uuid}`, service token)
- internal/auth/verifier.go::Verify (token verification via `POST /user`)
- config/config.go::AuthConfig, config/config.go::createDefaultConfig (`auth.hrpauth.base_url`, default `http://localhost:2778`) [xref]
- internal/auth/client.go::WinnerProxy (comment: design pattern aligned with WinnerProxy/internal/hrpauth — comment only, no code dependency)
- Repository-wide case-insensitive search: `yggdrasil` → 0 hits in code/config; `skinlib`/`skinproxy` → 0 hits; `bs2ha`/`deployer` → 0 hits
- api/router.go::Register (`POST /api/maintenance/sql`, `POST /api/matches`)
- Cross-references: 0002 (reporter integration), 0003 (identity via HRPAuth), 0004 (no Minecraft runtime)
- HRPAuth default port 2778 confirmed in the HRPAuth repository (read-only): /home/lnb/Desktop/HA/HRPAuth/controllers/startup_controller.go::buildDefaultConfig
