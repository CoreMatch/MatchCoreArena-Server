<div align="center">
  <img src="https://mcnb.dev/images/hero-wide-1x.webp" alt="MatchCore 视觉作品，非游戏画面" width="800">
  <h1>MatchCoreArena Server</h1>
  <p><strong>服务端权威对局，每一份结算都有据可查。</strong></p>
  <p><a href="./README.md">English</a></p>
</div>

MatchCoreArena Server 是 MatchCore Arena 生态的匹配与结算核心，为玩家、游戏服务器和运维人员提供支持。

## 为什么选择 MatchCoreArena Server

- **加权竞技。** 加权 Elo 评分反映已完成比赛的结果，并贯穿赛季与排行榜。
- **灵活组队。** 单人排队与基于同意的组队，支持不同的参赛方式。
- **可靠上报。** 幂等上报避免重复结算；无法接受的上报会返回类型明确的拒绝响应。
- **权威清晰。** 服务端掌握匹配决策与最终结果，游戏侧仅负责提交赛果。
- **身份互联。** Armorial 为整个生态提供统一的身份边界。

## 进一步了解

- [Armorial Wiki](https://wiki.mcnb.dev/) — 生态文档
- [Armorial Contract](https://github.com/CoreMatch/HA-Contract) — 接口契约
- [MatchCore 官网](https://mcnb.dev/) — Arena 概览
- [Armorial](https://auth.mcnb.dev/) — 账号与身份服务
- [Arena 状态](https://arena.mcnb.dev/api/status) — 实时服务状态

## 运行边界

需要 MySQL 作为事实来源、Redis 提供缓存，并依赖可访问的 Armorial 服务。本项目不提供反作弊，也不保证公平性或任何规模下的承载能力。

## 许可证

依据 [AGPL-3.0](LICENSE) 授权。
