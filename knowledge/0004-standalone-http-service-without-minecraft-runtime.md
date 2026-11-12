# 0004. MCA Is a Standalone Go HTTP Service with No Minecraft Server Runtime Dependency

## Status
Accepted

## Context
The surrounding ecosystem (BedwarsRel-MCA) is a JVM/Bukkit Minecraft plugin, so a
rating service could in principle have been implemented as part of the Minecraft
server or as a plugin. MCA instead exists as its own repository and service.

## Decision
MCA is a plain, self-contained HTTP service written in Go:

- Framework is Gin; the only runtime dependencies are a MySQL driver, a Redis
  client, YAML parsing and UUID generation. There is no Minecraft server, Bukkit,
  Yggdrasil or Mojang dependency anywhere in the module graph.
- Startup order is: load versioned YAML config → run embedded DB migrations →
  open MySQL → connect Redis → initialize the HRPAuth HTTP client → register Gin
  routes → listen (default port 9178).
- Every interaction with the Minecraft ecosystem crosses an HTTP boundary: match
  results are pushed in by the BedwarsRel-MCA plugin (see 0002), and account /
  identity data is pulled from HRPAuth (see 0003). MCA itself never speaks any
  Minecraft protocol.
- The container image builds a static, CGO-disabled Go binary and runs it as a
  non-root user on Alpine.

## Consequences
- Costs and limitations:
  - MCA can be deployed, scaled and restarted independently of any game server,
    and game servers can restart without losing rating state (state lives in
    MySQL).
  - Conversely, MCA has no live view of ongoing matches: it only knows what is
    reported after the fact, so ratings lag gameplay and unreported matches are
    invisible to it.
  - Correctness depends on reporters being honest and correctly configured
    (token, endpoint); MCA cannot observe the game to cross-check.
  - Why this separation was chosen over an in-plugin implementation: intent not
    documented in repo (the repository has no README); pending author confirmation.

## Evidence
- go.mod (field: require) — module dependencies: gin, go-sql-driver/mysql, go-yaml, google/uuid, go-redis; nothing Minecraft-related
- main.go::main — startup sequence (config, migrations, MySQL, Redis, HRPAuth client, Gin)
- config/config.example.yaml (field: port) — default listen port 9178
- Dockerfile::CGO_ENABLED — `CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mca .`
- Dockerfile::alpine — alpine runtime, non-root `app` user, EXPOSE 9178
- Absence of Minecraft/Yggdrasil code: repository-wide search matches only the `mojang_uuid` pass-through field (internal/auth/client.go::mojang_uuid, internal/auth/types.go::MojangUUID)
- commit 2f16bac2e319220d915ac2da565e94ce7ef3ad1e — feat: add initial MatchCoreArena server setup (2026-09-11)
- commit 787deb65d1525e9c0d2c39778d67e1de05796cc4 — feat: add initial MatchCoreArena server project skeleton (2026-09-11)
- commit 2c6110936a1d55a8d0c09437fc5bf4aeb388b018 — feat(docker): add multi-stage container image build (2026-11-03)
- Rationale for the separation: intent not documented in repo; pending author confirmation
