package migrate

// v5：组队同意机制（契约 1.11.0，knowledge/0007 2026-11-12 决策）——
// 组队入队从"提交名单即拉人"改为"邀请-接受"制：
//
//   - matchmaking_party_invites：邀请状态机持久层。
//     pending → accepted / rejected（终态）；过期不写库，由读取方按
//     expires_at 惰性判定（无后台清扫）。accepted 且未被消费
//     （consumed_at IS NULL）、未过期的邀请是组队入队的同意凭证，
//     入队成功即消费。
//
// 纯增量：仅新增表，不改动任何既有结构与数据。

import (
	"context"
	"database/sql"
)

func applyV5(ctx context.Context, db *sql.DB) error {
	return ensureTable(ctx, db, "matchmaking_party_invites", ddlMatchmakingPartyInvites)
}

const ddlMatchmakingPartyInvites = `CREATE TABLE IF NOT EXISTS matchmaking_party_invites (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    inviter_uid BIGINT NOT NULL COMMENT '邀请人 HRPAuth UID',
    invitee_uid BIGINT NOT NULL COMMENT '被邀请人 HRPAuth UID',
    match_type VARCHAR(20) NOT NULL COMMENT '目标排队模式（与 matchmaking_queue.match_type 同源）',
    status VARCHAR(10) NOT NULL DEFAULT 'pending' COMMENT 'pending/accepted/rejected；过期按 expires_at 惰性判定，不写库',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL COMMENT 'created_at + party_invite_ttl_seconds',
    resolved_at TIMESTAMP NULL COMMENT '接受/拒绝时间',
    consumed_at TIMESTAMP NULL COMMENT '组队入队消费该同意的时间',
    INDEX idx_inviter (inviter_uid, match_type),
    INDEX idx_invitee (invitee_uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`
