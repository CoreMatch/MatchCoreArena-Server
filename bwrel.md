**Investigating Game Statistics**

I'm currently digging into the `BedwarsRel-MCA` project to pinpoint where game statistics are generated upon match conclusion. My immediate focus is on identifying the relevant code sections that handle game end events and data recording.



**Investigating Statistic Storage**

I'm currently diving into the `BedwarsRel-MCA` codebase to pinpoint where game statistics are gathered and persisted once a match concludes. My focus is on pinpointing the specific classes and methods responsible for this crucial data handling.



**Analyzing Statistics Logic**

I've begun dissecting the statistical collection mechanisms. My focus has been on understanding how `PlayerStatistic` aggregates data and how the `Game` class actively updates these figures during gameplay.

**Examining Statistic Persistence**

I'm now concentrating on how statistics are saved. I've identified `PlayerStatisticManager.storeStatistic` as the key method, differentiating between YAML and database storage. It appears `Game.playerLeave` triggers individual player saves, and I'm investigating the game-end saving mechanism.



**Gathering Player Data**

I'm currently focused on the meticulous collection of player statistics. Specifically, I'm ensuring that metrics such as kills, deaths, wins, losses, score, and destroyed beds are accurately updated for each individual player throughout the match's duration within the `PlayerStatistic` objects.

**Updating Player Scores**

I'm refining the scorekeeping logic. Recent work involved ensuring wins and losses are correctly attributed, especially during team deaths and game endings. I've also been reviewing how match data is merged into overall player statistics before storage, particularly for YAML files.



在 BedwarsRel-MCA 中，游戏统计数据的收集和保存过程主要由 `PlayerStatisticManager` 类管理，并涉及游戏生命周期的多个阶段。以下是详细的收集与保存机制：

**1. 统计数据的收集 (Collection)**
统计数据在比赛进行期间实时记录在每个玩家对应的 `PlayerStatistic` 对象中。该对象维护了一组以 `current` 开头的字段（如 `currentKills`, `currentDeaths`），用于记录当前比赛的增量。

- **击杀与死亡**：在 [GameCycle.java](file:///home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/game/GameCycle.java#L86-L122) 的 `onPlayerDies` 方法中更新。
- **摧毁床铺**：在 [Game.java](file:///home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/game/Game.java#L808-L813) 的 `onBedDestroyed` 方法中更新。
- **胜场与败场**：
    - **胜场**：在 [GameCycle.java](file:///home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/game/GameCycle.java#L380-L386) 的 `runGameOver` 方法中，为获胜队伍的玩家增加胜场。
    - **败场**：在 [GameCycle.java](file:///home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/game/GameCycle.java#L235-L238) 的 `onPlayerRespawn` 方法中，如果玩家所属队伍已灭亡且无法复活，则记录败场。
- **分数 (Score)**：根据配置项（如 `statistics.scores.kill`），在上述行为发生时同步累加。

**2. 统计数据的保存触发 (Trigger)**
数据的持久化保存主要在以下两个时刻触发：
- **比赛结束时**：在 [SingleGameCycle.java](file:///home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/game/SingleGameCycle.java#L46-L51) 中，当 `onGameEnds` 被调用并依次“踢出”玩家回到大厅时，会触发 `storeStatistic`。
- **中途退出时**：在 [Game.java](file:///home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/game/Game.java#L1318) 的 `playerLeave` 方法中，如果玩家中途离开比赛，也会立即保存其当前统计数据。

**3. 保存位置与方式 (Persistence)**
保存逻辑位于 [PlayerStatisticManager.java](file:///home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/statistics/PlayerStatisticManager.java#L311-L325) 的 `storeStatistic` 方法中。系统会首先调用 `addCurrentValues()` 将本次比赛的增量合并到总计数值中，然后根据配置保存到指定位置：

- **YAML 文件（默认）**：
    - 路径：`plugins/BedwarsRel/database/bw_stats_players.yml`
    - 实现方法：`storeYamlStatistic`
- **数据库 (Database)**：
    - 如果在 `config.yml` 中配置了数据库存储，数据将通过 SQL 写入配置的外部数据库（如 MySQL）。
    - 实现方法：`storeDatabaseStatistic`

**总结**
统计数据在比赛中通过 `GameCycle` 和 `Game` 实时更新到内存，并在玩家离开比赛或比赛正式结束时，由 `PlayerStatisticManager` 持久化到本地 YAML 文件或远程数据库中。
**Synthesizing Information Accurately**

I've gathered all necessary data and am now focused on synthesizing it to meet your request. My analysis of in-match statistics collection within `PlayerStatistic` objects is complete, paving the way for the next steps.

**Processing Game Data Logic**

I'm currently detailing how player statistics are updated throughout a match, focusing on kills, deaths, beds destroyed, and wins. I'm also mapping out the exact points where these current match statistics are saved to the player's lifetime totals at the end of a game.



**Reviewing Player Data**

I'm currently examining the player statistic fields. My focus is on `deaths`, `destroyedBeds`, `kills`, `loses`, and `score` within the `PlayerStatistic.java` class, to understand their current implementations.



**Investigating Game Statistics**

I'm currently probing the codebase to determine if `BedwarsRel-MCA` outputs match-level statistics or reports upon game conclusion. My focus is on identifying any game-ending logic that might trigger the generation or logging of data such as match duration or winning team information.



**Reviewing Game Cycle Logic**

I've been looking into the game cycle, specifically how game endings are handled. My current focus is on the `runGameOver` method within `GameCycle.java`, noting its connections to record storage and the various game end events. The calculation aspect of the game end is also something I'm starting to examine.



**Reviewing Game End Logic**

I've been examining the `runGameOver` function. My focus is on how victory conditions are determined and communicated. I've noted the calculation of match duration and the subsequent sending of victory titles to players.

**Analyzing Game End Details**

My recent focus has been on how game outcomes are formally recorded. While chat messages and titles confirm winners and duration, there isn't a dedicated match report file. Instead, records like fastest win times are updated, and events are fired for external plugins to process.



是的，BedwarsRel-MCA 在游戏结束时会处理并生成相关的统计信息，但其重点在于**玩家累积统计**和**地图记录**，而不是为每场比赛生成独立的报告文件（如 PDF 或独立的日志文件）。

以下是 BedwarsRel-MCA 在游戏结束时的统计和报告功能详情：

### **1. 比赛结果通告 (Match Outcome)**
*   **胜者信息**：系统会识别获胜队伍，并通过聊天框和屏幕标题（Title）向所有玩家广播。
*   **比赛时长**：系统会计算比赛持续时间（`playTime`），并支持在配置文件中通过 `$time$` 占位符在结算标题中显示。
*   **平局处理**：如果达到最大比赛时间仍未分出胜负，系统会宣布平局（Draw）。

### **2. 地图纪录 (Map Records)**
*   **最快获胜记录**：插件会记录每张地图的“最快获胜时间”（Record）。
*   **记录保持者**：如果当前比赛打破了记录，系统会更新 `game.yml` 配置文件中的 `record` 和 `record-holders`（记录保持者名单），并在聊天框中发布通告。

### **3. 玩家个人统计 (Player Statistics)**
插件会实时追踪并在游戏结束时保存以下玩家数据：
*   **核心指标**：击杀数 (Kills)、死亡数 (Deaths)、摧毁床数 (Destroyed Beds)、胜场 (Wins)、败场 (Losses) 和积分 (Score)。
*   **数据存储**：这些数据可以存储在本地 YAML 文件（`database.yml`）或通过 MySQL 数据库进行持久化。
*   **自动展示**：如果在配置中启用了 `statistics.show-on-game-end: true`，玩家在比赛结束返回大厅时会自动收到 `/bw stats` 指令的输出，显示其当前的累积统计数据。

### **4. 报告形式**
*   **游戏内通告**：通过聊天信息和标题直接反馈给玩家。
*   **数据记录**：主要以数据库条目或配置文件字段的形式存在，没有内置的“每场比赛摘要文件”。
*   **扩展性**：插件在结束时会触发 `BedwarsGameOverEvent` 和 `BedwarsGameEndEvent` 事件。开发者可以编写简单的监听插件，利用这些事件将比赛详情导出为 JSON、Markdown 或其他自定义报告格式。

**总结**：如果你需要查看“谁赢了”或“打了多久”，这些信息会直接在游戏内显示并更新在地图记录中；如果你需要长期的玩家表现分析，可以通过插件提供的数据库支持来实现。

**Confirming Game Statistics**

My analysis of the `Grep` output has successfully identified key data points regarding map records. I've pinpointed the fastest win times, the players who hold those records, and the associated bonus score for record-breaking achievements. With this information in hand, my statistical compilation is complete.



**Tracking Player Stats**

I'm currently focused on how the `PlayerStatisticManager` handles player data. It meticulously logs key metrics like kills, deaths, wins, losses, and score. My attention is particularly drawn to how it calculates and displays in-match increments in parentheses.


在 BedwarsRel-MCA 项目中，每次对局结束后会生成并记录多维度的统计数据。这些数据主要分为**玩家个人统计**、**地图对局记录**以及**即时结算信息**三个部分。

以下是详细的统计数据列表：

### **1. 玩家个人统计 (Player Statistics)**
这些数据是持久化存储的（默认存放在 `plugins/BedwarsRel/database/bw_stats_players.yml` 或配置的外部数据库中），反映了玩家的长期表现。

- **核心指标**：
    - **击杀数 (Kills)**：玩家在比赛中击败对手的总次数。
    - **死亡数 (Deaths)**：玩家在比赛中被击杀或因环境因素死亡的总次数。
    - **摧毁床铺数 (Destroyed Beds)**：玩家成功摧毁敌方队伍床铺的次数。
    - **胜场 (Wins)**：玩家作为获胜队伍成员完成比赛的次数。
    - **败场 (Losses)**：玩家作为失败队伍成员（或床铺被毁后最终死亡）完成比赛的次数。
- **派生与综合指标**：
    - **积分 (Score)**：根据对局行为计算的总分。积分规则通常在配置文件中定义（如击杀得 X 分，破床得 Y 分）。
    - **KD 比 (KD Ratio)**：击杀数与死亡数的比例，由 [PlayerStatistic.java](file:///home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/statistics/PlayerStatistic.java#L138-L151) 实时计算。
    - **总场次 (Games Played)**：胜场与败场的总和。

### **2. 地图对局记录 (Map Records)**
这些数据记录在每张地图的配置文件中（如 `plugins/BedwarsRel/games/地图名.yml`），用于追踪该地图的历史最佳表现。

- **最快获胜记录 (Record)**：该地图历史上最短的获胜时间。
- **记录保持者 (Record Holders)**：创造最快获胜记录的玩家名单。如果当前对局打破了记录，系统会更新此名单并发布通告，见 [GameCycle.java](file:///home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/game/GameCycle.java#L301-L304)。

### **3. 即时结算信息 (Match Summary)**
在对局结束的瞬间，系统会计算并向玩家展示本次对局的即时表现。

- **对局时长 (Play Time)**：从比赛开始到决出胜负所经过的时间。
- **获胜队伍 (Winner)**：最终幸存并赢得比赛的队伍名称。
- **单场增量统计**：
    - 在结算界面，系统会对比显示玩家**本次对局**产生的增量数据（如“+2 Kills”），这些逻辑由 [PlayerStatisticManager.java](file:///home/lnb/Desktop/HA/BedwarsRel-MCA/common/src/main/java/io/github/bedwarsrel/statistics/PlayerStatisticManager.java#L42-L86) 处理。

### **数据处理流程**
1.  **实时记录**：在对局过程中，数据实时累加在内存中的 `PlayerStatistic` 对象里。
2.  **触发保存**：当对局正式结束（`onGameEnds`）或玩家中途离开时，系统触发保存。
3.  **持久化**：通过 `PlayerStatisticManager` 将内存中的增量数据合并到总数据中，并写入 YAML 文件或数据库。