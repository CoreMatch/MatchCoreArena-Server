# 0006. Deployment Is a Multi-Stage Docker Image with Environment-Rendered Config

## Status
Accepted

## Context
MCA is deployed as a container. Deployment-scoped values (database host,
HRPAuth URL, credentials) must be injectable without baking them into the image,
while the image itself should stay small and free of build tooling.

## Decision
- A multi-stage Dockerfile builds in `golang:<version>-alpine`
  (`go mod download` first for layer caching, then a static `CGO_ENABLED=0`
  binary with `-trimpath -ldflags="-s -w"`), and copies only the binary into a
  minimal `alpine:3.22` runtime.
- The runtime creates a dedicated non-root user (`app`, UID 10001), pre-creates
  `/app/data`, `/app/storage`, `/app/cache`, `/app/config`, exposes port 9178
  (the service's default HTTP port) and runs the binary through an entrypoint
  script.
- The entrypoint renders the YAML config from a template with `envsubst` when
  `CONFIG_TEMPLATE` and `RENDERED_CONFIG` are set, so deployment-scoped values
  enter through environment variables; otherwise the application falls back to
  its normal config loading (`CONFIG_PATH` or `config/config.yaml`, auto-created
  with a random maintenance token when missing).

## Consequences
- Costs and limitations:
  - No compose file, Kubernetes manifests or CI pipeline exist in this
    repository; orchestration, MySQL and Redis provisioning live elsewhere
    (where: not verified).
  - The entrypoint only renders config when both env vars are set; forgetting
    them silently falls back to auto-generated local config instead of failing,
    which can mask misdeployment.
  - Secrets management is delegated entirely to the deployment environment; the
    repo ships placeholder credentials in `config/tokens.yaml` (see 0002).
  - The image assumes external MySQL/Redis endpoints reachable over the network;
    health-check and restart policies are not defined in this repository.

## Evidence
- Dockerfile::builder — multi-stage build, non-root user, EXPOSE 9178
- docker-entrypoint.sh::envsubst — envsubst rendering of CONFIG_TEMPLATE → RENDERED_CONFIG, then exec
- .dockerignore::build — build context exclusions (existence verified)
- config/config.go::Load — config path resolution and auto-creation when missing
- config/config.go::createDefaultConfig — default config values incl. port 9178 and random maintenance token
- config/config.example.yaml (field: port) — default server port 9178
- commit 2c6110936a1d55a8d0c09437fc5bf4aeb388b018 — feat(docker): add multi-stage container image build (2026-11-03)
- Where production orchestration lives: not verified (no manifests in repo)
