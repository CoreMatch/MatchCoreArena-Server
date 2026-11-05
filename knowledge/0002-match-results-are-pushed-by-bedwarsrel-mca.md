# 0002. Match Results Are Pushed by BedwarsRel-MCA via POST /api/matches with a Scoped Bearer Token

## Status
Accepted

## Context
MCA does not observe gameplay. It must receive authoritative match results from the
Minecraft-side plugin that runs the matches (BedwarsRel-MCA), and the report
endpoint must be protected so that only trusted system callers can write ratings.

## Decision
- Match results enter MCA exclusively through `POST /api/matches`, mounted behind
  the Bearer-token auth middleware.
- The handler additionally requires the `match:report` OAuth scope; without it the
  request is rejected with an insufficient-scope error.
- Token verification resolves in this order: (1) the configured maintenance token
  (UID -1, scope `maintenance`, which bypasses every scope check), (2) static
  service tokens from `config/tokens.yaml`, (3) Redis-cached HRPAuth verification,
  (4) a live HRPAuth `/user` call. The intended reporter identity is the
  `match-reporter` service token (UID -100, scopes `match:report`, `user:read`),
  which is independent of the HRPAuth user system.
- The payload is the team-oriented `TeamReportInput`: `match_type`, `winner_team`,
  `loser_teams` (each team carrying `team_id`, `initial_count`, `alive_count` and
  per-member `uuid`, `survival_time`, `kills`, `deaths`, `perf_tweak`), plus
  `duration_seconds`, `started_at`, `finished_at`.
- Cross-verified against the reporter side (read-only reference): BedwarsRel-MCA
  builds exactly this JSON and POSTs it with `Authorization: Bearer <token>` read
  from its `match-reporting` config block, triggered from `runGameOver` after the
  `BedwarsGameOverEvent`. The outward request/response shapes are specified in
  HA-Contract (`matchcorearena-business.yaml`); MCA's own copy of the OpenAPI doc
  was deleted when documentation was consolidated into HA-Contract.

## Consequences
- Costs and limitations:
  - Reporting is fire-and-forget: the plugin sends on a new thread, has no retry,
    and only logs a warning on failure — a lost report means the match is never
    rated.
  - The plugin silently skips reporting while the token is the placeholder value.
  - This repository commits `config/tokens.yaml` containing the placeholder token
    `default-match-report-token-change-me`; auto-generation of random tokens only
    happens when the file is absent, so a deployment that keeps the committed file
    runs with a known placeholder unless operators replace it.
  - The `maintenance` scope implicitly passes every scope check, so the maintenance
    credential can also report matches.
  - A report fails as a whole if any participant cannot be resolved (see 0003).

## Evidence
- api/router.go::Register — `POST /api/matches` route
- api/handler/match.go::Report — scope check and report handling
- api/middleware/auth.go::Auth — Bearer token middleware
- api/middleware/auth.go::HasScope — HasScope with `maintenance` bypass
- internal/auth/verifier.go::Verify — verification order (maintenance token, service tokens, Redis cache, HRPAuth)
- internal/auth/tokens.go::createDefaultTokenStore — default token store incl. `match-reporter` (UID -100, scopes match:report/user:read)
- config/tokens.yaml (field: match-reporter) — committed service-token file with placeholder token
- internal/service/match.go::TeamReportInput — TeamReportInput / TeamInfo / ParticipantReport shapes
- config/tokens.yaml:2 — comment stating service tokens serve cross-system calls such as game-server reporting, independent of HRPAuth users
- BedwarsRel-MCA (read-only reference): /home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/utils/MatchReporter.java::sendReport — POST with Bearer token; /home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/utils/MatchReporter.java::buildJson — JSON body; /home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/utils/MatchReporter.java::appendParticipantInfo — participant fields incl. hard-coded perf_tweak 1.0; /home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/utils/MatchReporter.java::report — fire-and-forget thread
- BedwarsRel-MCA (read-only reference): /home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/game/GameCycle.java::runGameOver — report triggered in runGameOver
- BedwarsRel-MCA (read-only reference): /home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/resources/config.yml (field: match-reporting) — `match-reporting.api-url` (default https://api.matchcorearena.com/api/matches) and `access-token` placeholder
- HA-Contract (read-only reference): /home/lnb/Desktop/HA/HA-Contract/docs/api/openapi/matchcorearena-business.yaml (field: /api/matches) — POST /api/matches contract (BearerAuth, 201); /home/lnb/Desktop/HA/HA-Contract/docs/api/openapi/matchcorearena-business.yaml::TeamReportInput — TeamReportInput/TeamReportInfo/ParticipantReport schemas; HA-Contract commit 309a888b00accea1e230476292301a70aa4c7a5f (2026-10-04)
- commit 7dc243d8ba789ae9926865f516bdfa105e1164d5 — feat: implement match reporting flow and add OAuth scope checks (2026-09-11)
- commit 6279658307d011791f3a15a166ae81caf1c7c5dd — feat(match): add team match support and participant tracking (2026-09-18)
- commit 12dbd67a7e611e3d85e41844c398cd5d626b6ddd — feat(auth,config): add service token support and config improvements (2026-09-19)
- commit 8c9dddab75f57233e0017ecdef344fc0e00d1a8e — docs(api): remove deprecated API documentation files (2026-09-18)
