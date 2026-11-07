# 0002. Match Results Are Pushed by BedwarsRel-MCA via POST /api/matches with an OPS Credential

## Status
Accepted

## Context
MCA does not observe gameplay. It must receive authoritative match results from the
Minecraft-side plugin that runs the matches (BedwarsRel-MCA), and the report
endpoint must be protected so that only trusted system callers can write ratings.

## Decision
- Game match results enter MCA through `POST /api/matches`, the ONLY
  match-reporting endpoint (0009): game servers and ops/debug tooling submit
  here alike (`matches.source` is a provenance marker, not a routing key).
- Reporting requires an OPS-level credential — the maintenance token or a
  `tokens.yaml` service token — carrying the `match:report` scope. Player
  tokens are rejected `403 oauth_insufficient_scope` even when the scope is
  present; the `maintenance` scope passes every scope check as everywhere.
- Token verification resolves in this order: (1) the configured maintenance token
  (UID -1, scope `maintenance`), (2) static service tokens from
  `config/tokens.yaml`, (3) Redis-cached HRPAuth verification, (4) a live
  HRPAuth `/user` call; maintenance and service tokens are flagged OPS. The
  intended reporter identity is the `match-reporter` service token (UID -100,
  scopes `match:report`, `user:read`), which is independent of the HRPAuth
  user system.
- The payload is the team-oriented `TeamReportInput`: unified `teams` array or
  the equivalent `winner_team` + `loser_teams` form (each team carrying
  `team_id`, `result` in the teams shape, `initial_count`, `alive_count` and
  per-member `uuid`, `survival_time`, `kills`, `deaths`, `perf_tweak`), plus
  `duration_seconds`, `map_name`, `server_id`, `game_id` (idempotency key,
  0011), `source`, `matchmaking_group_id`, `started_at`, `finished_at`.
- Cross-verified against the reporter side (read-only reference): BedwarsRel-MCA
  builds exactly this JSON (`MatchReportPayload`) and POSTs it with
  `Authorization: Bearer <token>` from the `match-reporting` config block
  (legacy `access-token` or `credentials.service-token`/OAuth2
  client_credentials), triggered from `runGameOver` after the game ends.
  Delivery is retried with classification-driven backoff (`sendWithRetry`),
  and the plugin keeps its own idempotency ledger with a stable `game_id`
  (`GameIdGenerator`), which the server honors (0011). The outward
  request/response shapes are specified in HA-Contract
  (`matchcorearena-business.yaml`); MCA's own copy of the OpenAPI doc was
  deleted when documentation was consolidated into HA-Contract.

## Consequences
- Costs and limitations:
  - The plugin retries with classification-driven backoff on a background
    loop, but delivery is still best-effort: when credentials are missing
    the report is skipped with a warning, and an undeliverable report is
    eventually dropped — server-side idempotency (0011) makes the retries
    safe, not guaranteed.
  - The plugin skips reporting while no credential is configured (the
    committed `access-token` placeholder keeps it unused).
  - This repository commits `config/tokens.yaml` containing the placeholder token
    `default-match-report-token-change-me`; auto-generation of random tokens only
    happens when the file is absent, so a deployment that keeps the committed file
    runs with a known placeholder unless operators replace it.
  - The `maintenance` scope implicitly passes every scope check, so the maintenance
    credential can also report matches.
  - A report fails as a whole if any participant cannot be resolved (see 0003).

## Evidence
- api/router.go::Register — `POST /api/matches` route
- api/handler/match.go::Report — OPS + scope gate and report handling
- api/middleware/auth.go::Auth — Bearer token middleware writing the OPS flag
- api/middleware/auth.go::GetOps — OPS flag accessor used by the report gate
- api/middleware/auth.go::HasScope — HasScope with `maintenance` bypass
- internal/auth/verifier.go::TokenResult — Ops flag on maintenance/service-token results
- internal/auth/tokens.go::createDefaultTokenStore — default token store incl. `match-reporter` (UID -100, scopes match:report/user:read)
- config/tokens.yaml (field: match-reporter) — committed service-token file with placeholder token
- internal/service/match.go::TeamReportInput — TeamReportInput / TeamInfo / ParticipantReport shapes
- config/tokens.yaml:L2-L2 fp=40835f02 ~ 用于系统间调用、游戏服务器上报等场景，独立于 HRPAuth 用户体系。 — comment stating service tokens serve cross-system calls such as game-server reporting, independent of HRPAuth users
- BedwarsRel-MCA (read-only reference): /home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/mca/MatchReportService.java::reportGameEnd — entry point with Bearer token; /home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/mca/MatchReportService.java::sendWithRetry — classification-driven retries; /home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/mca/MatchReportService.java::onGameOver — summary build and idempotency-ledger skip; /home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/mca/MatchReportPayload.java::MatchReportPayload — JSON body per HA-Contract TeamReportInput; /home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/mca/GameIdGenerator.java::GameIdGenerator — stable game_id generation
- BedwarsRel-MCA (read-only reference): /home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/game/GameCycle.java::runGameOver — report triggered in runGameOver
- BedwarsRel-MCA (read-only reference): /home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/resources/config.yml (field: match-reporting) — `match-reporting.api-url` (default https://api.matchcorearena.com/api/matches) and `access-token` placeholder
- HA-Contract (read-only reference): /home/lnb/Desktop/HA/HA-Contract/docs/api/openapi/matchcorearena-business.yaml (field: /api/matches) — POST /api/matches contract (BearerAuth, OPS gate, 200 replay / 201); /home/lnb/Desktop/HA/HA-Contract/docs/api/openapi/matchcorearena-business.yaml::TeamReportInput — TeamReportInput/TeamReportInfo/ParticipantReport schemas
- commit 7dc243d8ba789ae9926865f516bdfa105e1164d5 — feat: implement match reporting flow and add OAuth scope checks (2026-09-11)
- commit 6279658307d011791f3a15a166ae81caf1c7c5dd — feat(match): add team match support and participant tracking (2026-09-18)
- commit 12dbd67a7e611e3d85e41844c398cd5d626b6ddd — feat(auth,config): add service token support and config improvements (2026-09-19)
- commit 8c9dddab75f57233e0017ecdef344fc0e00d1a8e — docs(api): remove deprecated API documentation files (2026-09-18)
