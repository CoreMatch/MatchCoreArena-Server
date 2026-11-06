-- =============================================
-- MatchCoreArena 数据库 Baseline
-- 创建时间: 2026-09-11
-- =============================================

-- 用户表
CREATE TABLE IF NOT EXISTS users (
    uid BIGINT PRIMARY KEY,
    level INT NOT NULL DEFAULT 1,
    experience BIGINT NOT NULL DEFAULT 0,
    rank_score INT NOT NULL DEFAULT 0,
    wins_count INT NOT NULL DEFAULT 0 COMMENT '获胜场数，用于判定定级赛',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    INDEX idx_rank_score (rank_score),
    INDEX idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 好友关系表
CREATE TABLE IF NOT EXISTS friends (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_uid BIGINT NOT NULL,
    friend_uid BIGINT NOT NULL,
    status TINYINT NOT NULL DEFAULT 0 COMMENT '0:待确认 1:已接受 2:已拒绝',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_user_friend (user_uid, friend_uid),
    INDEX idx_user_uid (user_uid),
    INDEX idx_friend_uid (friend_uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 战队表
CREATE TABLE IF NOT EXISTS teams (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(50) NOT NULL,
    description TEXT,
    leader_uid BIGINT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    UNIQUE KEY uk_name (name),
    INDEX idx_leader_uid (leader_uid),
    INDEX idx_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 战队成员表
CREATE TABLE IF NOT EXISTS team_members (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    team_id BIGINT NOT NULL,
    user_uid BIGINT NOT NULL,
    role TINYINT NOT NULL DEFAULT 0 COMMENT '0:成员 1:副队长 2:队长',
    joined_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_team_user (team_id, user_uid),
    INDEX idx_team_id (team_id),
    INDEX idx_user_uid (user_uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 对战记录表
CREATE TABLE IF NOT EXISTS matches (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    match_type VARCHAR(20) NOT NULL COMMENT '对战类型',
    source VARCHAR(20) NOT NULL DEFAULT 'game' COMMENT '来源标记: game=游戏服务器, virtual=运维调试数据（仅标记，不分流结算）',
    map_name VARCHAR(50) NULL COMMENT '地图标识（可选）',
    server_id VARCHAR(50) NULL COMMENT '上报服务器标识（可选）',
    game_id VARCHAR(64) NULL COMMENT '上报方稳定幂等键（可选）',
    payload_hash CHAR(64) NULL COMMENT '上报载荷规范化哈希（game_id 幂等冲突检测）',
    winner_uid BIGINT NOT NULL DEFAULT 0,
    loser_uid BIGINT NOT NULL DEFAULT 0,
    winner_team_id VARCHAR(50) COMMENT '获胜队伍ID',
    survival_rate DOUBLE DEFAULT 1.0 COMMENT '获胜队生存比例',
    winner_score INT NOT NULL DEFAULT 0,
    loser_score INT NOT NULL DEFAULT 0,
    winner_perf DOUBLE NOT NULL DEFAULT 1.0 COMMENT '获胜者表现权重',
    loser_perf DOUBLE NOT NULL DEFAULT 1.0 COMMENT '失败者表现权重',
    duration_seconds INT NOT NULL DEFAULT 0 COMMENT '对战时长(秒)',
    started_at TIMESTAMP NOT NULL,
    finished_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_winner_uid (winner_uid),
    INDEX idx_loser_uid (loser_uid),
    INDEX idx_created_at (created_at),
    UNIQUE KEY uk_game_id (game_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 对战参与者表
CREATE TABLE IF NOT EXISTS match_participants (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    match_id BIGINT NOT NULL,
    user_uid BIGINT NOT NULL,
    team_id VARCHAR(50) NOT NULL COMMENT '队伍标识（如颜色或ID）',
    is_winner BOOLEAN NOT NULL DEFAULT FALSE,
    is_draw BOOLEAN NOT NULL DEFAULT FALSE COMMENT '平局（虚拟对局支持；游戏上报恒为 FALSE）',
    rank_score_before INT NOT NULL COMMENT '赛前积分',
    rank_score_delta INT NOT NULL COMMENT '本局积分变动',
    survival_time_seconds INT NOT NULL DEFAULT 0 COMMENT '生存时长',
    kills INT NOT NULL DEFAULT 0,
    deaths INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_match_id (match_id),
    INDEX idx_user_uid (user_uid),
    FOREIGN KEY (match_id) REFERENCES matches(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 匹配队列表：每个用户同时至多一条排队记录
CREATE TABLE IF NOT EXISTS matchmaking_queue (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_uid BIGINT NOT NULL,
    match_type VARCHAR(20) NOT NULL COMMENT '排队模式（与 matches.match_type 同源）',
    rank_score INT NOT NULL COMMENT '入队时评分快照',
    party_id BIGINT NULL COMMENT '组队入队时的队伍 ID（单人排队为 NULL）',
    enqueued_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_user (user_uid),
    INDEX idx_type_score (match_type, rank_score),
    INDEX idx_party (party_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 匹配队伍表：组队匹配的队伍（以队为单位入队与匹配）
CREATE TABLE IF NOT EXISTS matchmaking_parties (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    leader_uid BIGINT NOT NULL,
    match_type VARCHAR(20) NOT NULL COMMENT '队伍排队的目标模式',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_leader (leader_uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 匹配队伍成员表：每个用户同时至多属于一个队伍
CREATE TABLE IF NOT EXISTS matchmaking_party_members (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    party_id BIGINT NOT NULL,
    user_uid BIGINT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_user (user_uid),
    UNIQUE KEY uk_party_user (party_id, user_uid),
    FOREIGN KEY (party_id) REFERENCES matchmaking_parties(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 匹配成局表：引擎成局时写入一局
CREATE TABLE IF NOT EXISTS matchmaking_groups (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    match_type VARCHAR(20) NOT NULL,
    formed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_formed_at (formed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 匹配成局成员表：成局时记录每位参与者的队伍与评分快照
CREATE TABLE IF NOT EXISTS matchmaking_group_members (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    group_id BIGINT NOT NULL,
    user_uid BIGINT NOT NULL,
    team_id VARCHAR(50) NOT NULL COMMENT '成局分配的队伍标识',
    rank_score INT NOT NULL COMMENT '成局时评分',
    party_id BIGINT NULL COMMENT '组队匹配时的队伍 ID（单人为 NULL）',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_group_id (group_id),
    INDEX idx_user_uid (user_uid),
    FOREIGN KEY (group_id) REFERENCES matchmaking_groups(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 排行榜表
CREATE TABLE IF NOT EXISTS rankings (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_uid BIGINT NOT NULL,
    rank_type VARCHAR(20) NOT NULL COMMENT '排行类型',
    score BIGINT NOT NULL DEFAULT 0,
    rank_position INT NOT NULL DEFAULT 0,
    season INT NOT NULL DEFAULT 1,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_user_type_season (user_uid, rank_type, season),
    INDEX idx_rank_type_score (rank_type, score DESC),
    INDEX idx_season (season)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
