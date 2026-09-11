# MatchCoreArena API Contract

This directory contains the OpenAPI 3.1.0 specification for the MatchCoreArena Business API.

## Files

- `openapi/matchcorearena-business.yaml` — Main contract. Covers all endpoints exposed by this server.
- The same file is mirrored to the central HA-Contract repository at `HA-Contract/docs/api/openapi/matchcorearena-business.yaml`.

## Versioning

The contract version is aligned with the application's configuration version (currently `1.1.0`):
- Changes to endpoints, request/response shapes, or error codes must bump the contract `info.version`.
- New error codes must be registered in the central `HA-Contract/docs/api/error-codes.md` registry first.

## Endpoint Inventory

| Group | Endpoints | Auth |
|---|---|---|
| System | `GET /status` | Public |
| Auth | `GET /api/auth/login`, `POST /api/auth/callback`, `POST /api/auth/refresh`, `POST /api/auth/logout` | Public + Bearer |
| Users | `GET /api/users/me`, `GET /api/users/{uid}`, `POST /api/users/me/experience` | Bearer |
| Friends | `GET/POST /api/friends`, `PUT /api/friends/{id}/accept`, `PUT /api/friends/{id}/reject`, `DELETE /api/friends/{id}` | Bearer |
| Teams | `POST /api/teams`, `GET/DELETE /api/teams/{id}`, `GET/POST /api/teams/{id}/members`, `PUT /api/teams/{id}/members/{uid}/role`, `DELETE /api/teams/{id}/members/{uid}` | Bearer |
| Matches | `POST /api/matches`, `GET /api/matches/{id}`, `GET /api/matches/me` | Bearer |
| Rankings | `GET /api/rankings/{type}`, `GET /api/rankings/me` | Bearer |

## Error codes

All error codes follow the HA-Contract registry. Service-specific codes carry the `mca_` prefix; OAuth-related codes are shared with HRPAuth.

See `HA-Contract/docs/api/error-codes.md` for the canonical list.

## Response envelope

Success:
```json
{
  "success": true,
  "message": "...",
  "data": { ... },
  "meta": { "request_id": "..." }
}
```

Error:
```json
{
  "success": false,
  "message": "...",
  "code": "...",
  "error": "...",
  "meta": { "request_id": "..." }
}
```