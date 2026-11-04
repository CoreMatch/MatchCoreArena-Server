# Knowledge Base

This knowledge base is the project's memo to itself. It is self-hosted and
maintained by this repository (MatchCoreArena-Server) and records internal design
decisions and their history. It does **not** define or override outward behavior.
Outward behavior is specified by HA-Contract
(https://github.com/SamuelCheston/HA-Contract); if an entry here ever conflicts
with HA-Contract, fix the entry, not the contract.

## Entries

| ID | Title | Status | Summary |
|----|-------|--------|---------|
| [0001](0001-server-side-weighted-elo-rating.md) | Server-Side Weighted Elo Rating Applied at Match Report Time | Accepted | Ratings are computed on this server inside the match-report transaction using Elo expected score with phase-based K-factor and survival/streak/performance weights. |
| [0002](0002-match-results-are-pushed-by-bedwarsrel-mca.md) | Match Results Are Pushed by BedwarsRel-MCA via POST /api/matches with a Scoped Bearer Token | Accepted | Match results enter only through `POST /api/matches`, which requires a Bearer token carrying the `match:report` scope (typically the static `match-reporter` service token). |
| [0003](0003-player-identity-is-hrpauth-uid-with-minecraft-uuid-resolution.md) | Player Identity Is the HRPAuth UID; Minecraft UUIDs Are Resolved Through HRPAuth | Accepted | The internal identity key is the HRPAuth UID; Minecraft UUIDs from match reports are mapped to UIDs via HRPAuth `lookup-uuid`, and MCA holds no credentials or Minecraft protocol code of its own. |
| [0004](0004-standalone-http-service-without-minecraft-runtime.md) | MCA Is a Standalone Go HTTP Service with No Minecraft Server Runtime Dependency | Accepted | MCA is a self-contained Go/Gin HTTP service; every Minecraft-ecosystem interaction crosses an HTTP boundary (plugin push in, HRPAuth lookups out). |
| [0005](0005-mysql-is-the-source-of-truth-redis-is-cache-only.md) | MySQL Is the Single Source of Truth; Baseline-Only Embedded Migrations; Redis Is Cache-Only | Accepted | All durable state lives in MySQL, bootstrapped by an embedded baseline that runs only on empty databases; Redis serves only TTL caches. |
| [0006](0006-multi-stage-docker-build-with-env-rendered-config.md) | Deployment Is a Multi-Stage Docker Image with Environment-Rendered Config | Accepted | A multi-stage Dockerfile produces a minimal non-root runtime image; deployment-scoped config is rendered from a template via `envsubst` at container start. |
| [0007](0007-cleanup-pass-checklist.md) | Cleanup Pass Checklist: Fixed Items and Pending Decisions | Accepted | 2026-11-04 low-risk cleanup: baseline schema aligned with the report code path (verified against MariaDB strict mode), streaks read from match_participants, stale comments/tests fixed; design-level issues listed as pending author decisions. |

## Maintenance

- New entries take the next sequential number (`NNNN`, zero-padded, starting from
  0001) and a kebab-case slug: `knowledge/NNNN-<slug>.md`.
- History is not rewritten: accepted entries are kept as-is even when they age.
- An entry that is no longer true is marked `Superseded` (or `Historical`) in its
  Status section and linked to its replacement — it is never deleted.
- Every factual claim needs evidence that was actually checked (`file:line` and/or
  full commit hash). Anything that cannot be verified is written as "not verified"
  or "intent not documented in repo" — never invented.
- Entries are written in English.
