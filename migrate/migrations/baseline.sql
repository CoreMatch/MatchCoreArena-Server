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
    winner_uid BIGINT NOT NULL,
    loser_uid BIGINT NOT NULL,
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
    INDEX idx_created_at (created_at)
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
