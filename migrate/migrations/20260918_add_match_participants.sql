-- 对战参与者表
CREATE TABLE IF NOT EXISTS match_participants (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    match_id BIGINT NOT NULL,
    user_uid BIGINT NOT NULL,
    team_id VARCHAR(50) NOT NULL COMMENT '队伍标识（如颜色或ID）',
    is_winner BOOLEAN NOT NULL DEFAULT FALSE,
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

-- 扩展 matches 表以支持团队统计
ALTER TABLE matches ADD COLUMN winner_team_id VARCHAR(50) COMMENT '获胜队伍ID';
ALTER TABLE matches ADD COLUMN survival_rate DOUBLE DEFAULT 1.0 COMMENT '获胜队生存比例';
