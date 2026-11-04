# 0008. Ecosystem Boundary: HRPAuth Is the Only Outbound Runtime Dependency

## Status
Accepted

## Date
2026-09-11 (HRPAuth integration introduced); boundary verified against HEAD on 2026-11-05

## Context
MCA lives in the HA ecosystem next to HRPAuth (account core),
HRPAuth-Yggdrasil-API (Minecraft auth protocol), HASkinLib / HASkinProxy
(skins), WinnerProxy, the BedwarsRel-MCA plugin, and operational tools
(MatchCoreArena-Client, MatchCoreArena-MaintenanceView). Which of these MCA
actually talks to — and which it deliberately does not — was audited against
the code on 2026-11-05.

## Decision
Verified dependency boundary (all checks are repository-wide searches on the
2026-11-05 HEAD unless noted otherwise):

- **HRPAuth — the only outbound runtime dependency.** MCA calls:
  `GET /status` (startup reachability check), `POST /oauth/login-ticket`,
  `POST /totp/verify`, `POST /oauth/token`, `POST /oauth/revoke`,
  `POST /user/lookup`, `POST /user/lookup-uuid`, and `POST /user` (token
  verification). Base URL comes from `auth.hrpauth.base_url`
  (default `http://localhost:2778`, HRPAuth's actual listen port).
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
  only changes to HRPAuth's `/user*` and `/oauth/*` endpoints can.
- Costs/limitations: the boundary is enforced by absence, not by an explicit
  architectural rule in the repo; nothing prevents future code from adding
  new outbound dependencies. Why MCA resolves identity through HRPAuth
  rather than ever querying the Yggdrasil layer directly is not documented —
  pending author confirmation.

## Evidence
- internal/auth/client.go:33 (`GET /status`), :93 (`/oauth/login-ticket`), :127 (`/totp/verify`), :168 (`/oauth/token`), :207 (`/oauth/revoke`), :303 (`/user/lookup`), :338 (`/user/lookup-uuid`)
- internal/auth/verifier.go:47-98 (token verification via `POST /user`)
- config/config.go:43-49, 354 (`auth.hrpauth.base_url`, default `http://localhost:2778`)
- internal/auth/client.go:3 (comment: design pattern aligned with WinnerProxy/internal/hrpauth — comment only, no code dependency)
- Repository-wide case-insensitive search on 2026-11-05 HEAD: `yggdrasil` → 0 hits in code/config; `skinlib`/`skinproxy` → 0 hits; `bs2ha`/`deployer` → 0 hits
- api/router.go:93 (`POST /api/maintenance/sql`), :84 (`POST /api/matches`)
- Cross-references: 0002 (reporter integration), 0003 (identity via HRPAuth), 0004 (no Minecraft runtime)
- HRPAuth default port 2778 confirmed in the HRPAuth repository (read-only): controllers/startup_controller.go:60
