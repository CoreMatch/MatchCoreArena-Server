<div align="center">
  <img src="https://mcnb.dev/images/hero-wide-1x.webp" alt="MatchCore artwork, not gameplay" width="800">
  <h1>MatchCoreArena Server</h1>
  <p><strong>Server-authoritative matches. Every result accounted for.</strong></p>
  <p><a href="./README.zh-CN.md">简体中文</a></p>
</div>

MatchCoreArena Server is the matchmaking and settlement core for the MatchCore Arena ecosystem, serving players, game servers, and operators.

## Why MatchCoreArena Server

- **Weighted competition.** Weighted Elo ratings reflect completed matches across seasons and rankings.
- **Flexible parties.** Solo queues and consent-based party play support different ways to compete.
- **Reliable reporting.** Idempotent reports prevent duplicate settlement, with typed rejection responses when a report cannot be accepted.
- **Clear authority.** The server owns matchmaking decisions and final results; game-side reporters submit outcomes.
- **Connected identity.** Armorial provides the identity boundary across the ecosystem.

## Learn more

- [Armorial Wiki](https://wiki.mcnb.dev/) — ecosystem documentation
- [Armorial Contract](https://github.com/CoreMatch/HA-Contract) — interface contracts
- [MatchCore Portal](https://mcnb.dev/) — Arena overview
- [Armorial](https://auth.mcnb.dev/) — accounts and identity services
- [Arena status](https://arena.mcnb.dev/api/status) — live service status

## Runtime boundary

Requires MySQL as the source of truth, Redis for caching, and a reachable Armorial service. It does not provide anti-cheat or guarantee fairness or capacity at any scale.

## License

Licensed under [AGPL-3.0](LICENSE).
