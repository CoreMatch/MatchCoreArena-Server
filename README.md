# MatchCoreArena-Server

Matchmaking and settlement core service of the MatchCoreArena ecosystem:
a standalone Go/Gin HTTP service (no Minecraft server runtime dependency).
Design decisions live in [`knowledge/`](knowledge/README.md); the outward
contract lives in HA-Contract.

## Deployment facts

Authoritative deployment facts live in [`deploy-manifest.yaml`](deploy-manifest.yaml)
at the repo root (consumed by HA-Services-Deployer's orchestrator;
`manager: aapanel` means the process is panel-managed and the orchestrator
treats it as read-only):

- Listens on `:9178` (public); health probe `GET /api/status`.
- Requires MySQL (single source of truth) and Redis (cache-only), plus a
  reachable HRPAuth core — the only outbound runtime dependency
  (see `knowledge/0008`).
- Config file `config/config.yaml` (`CONFIG_PATH` overrides the path;
  template version 1.9.0); versioned schema migrations run automatically
  on startup (see `knowledge/0017`).
