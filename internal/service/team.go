package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"MatchCoreArena-Server/internal/apperr"
)

// Team 战队模型。
type Team struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	LeaderUID   int64  `json:"leader_uid"`
	CreatedAt   string `json:"created_at"`
}

// TeamMember 战队成员模型。
type TeamMember struct {
	ID       int64  `json:"id"`
	TeamID   int64  `json:"team_id"`
	UserUID  int64  `json:"user_uid"`
	Role     int    `json:"role"` // 0:成员 1:副队长 2:队长
	JoinedAt string `json:"joined_at"`
}

// TeamService 战队业务接口。
type TeamService interface {
	// Create 创建战队。
	Create(ctx context.Context, leaderUID int64, name, description string) (*Team, error)
	// GetByID 获取战队详情。
	GetByID(ctx context.Context, id int64) (*Team, error)
	// Delete 解散战队（仅队长）。
	Delete(ctx context.Context, uid, teamID int64) error
	// ListMembers 列出战队成员。
	ListMembers(ctx context.Context, teamID int64) ([]*TeamMember, error)
	// AddMember 添加成员（队长/副队长）。
	AddMember(ctx context.Context, uid, teamID, targetUID int64, role int) (*TeamMember, error)
	// UpdateMemberRole 调整成员角色（队长/副队长，不能设为队长）。
	UpdateMemberRole(ctx context.Context, uid, teamID, targetUID int64, role int) error
	// RemoveMember 移除成员（队长/副队长，不能移除队长）。
	RemoveMember(ctx context.Context, uid, teamID, targetUID int64) error
}

// teamService 数据库实现。
type teamService struct {
	db *sql.DB
}

// NewTeamService 创建 TeamService 实例。
func NewTeamService(db *sql.DB) TeamService {
	return &teamService{db: db}
}

// Create 创建战队，创建者自动成为队长（role=2）。
func (s *teamService) Create(ctx context.Context, leaderUID int64, name, description string) (*Team, error) {
	if name == "" {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest, "战队名称不能为空")
	}

	// 名称唯一性由 uk_name 唯一键兜底；这里先查一次，把唯一键冲突
	// 显式化为 409 mca_team_name_taken（含已解散战队占用的名称）。
	var nameCount int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM teams WHERE name = ?`, name,
	).Scan(&nameCount); err != nil {
		return nil, fmt.Errorf("检查战队名称失败: %w", err)
	}
	if nameCount > 0 {
		return nil, apperr.New(apperr.CodeMCATeamNameTaken, "战队名称已存在")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	// 1. 插入战队
	res, err := tx.ExecContext(ctx,
		`INSERT INTO teams (name, description, leader_uid) VALUES (?, ?, ?)`,
		name, description, leaderUID,
	)
	if err != nil {
		return nil, fmt.Errorf("创建战队失败: %w", err)
	}
	teamID, _ := res.LastInsertId()

	// 2. 插入创建者为队长
	_, err = tx.ExecContext(ctx,
		`INSERT INTO team_members (team_id, user_uid, role) VALUES (?, ?, 2)`,
		teamID, leaderUID,
	)
	if err != nil {
		return nil, fmt.Errorf("添加队长成员记录失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}

	return &Team{
		ID:          teamID,
		Name:        name,
		Description: description,
		LeaderUID:   leaderUID,
	}, nil
}

// GetByID 获取战队详情。
func (s *teamService) GetByID(ctx context.Context, id int64) (*Team, error) {
	var t Team
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, description, leader_uid, created_at 
		 FROM teams WHERE id = ? AND deleted_at IS NULL`,
		id,
	).Scan(&t.ID, &t.Name, &t.Description, &t.LeaderUID, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperr.New(apperr.CodeMCATeamNotFound, "战队不存在或已解散")
		}
		return nil, fmt.Errorf("查询战队失败: %w", err)
	}
	return &t, nil
}

// Delete 解散战队（仅队长）。
func (s *teamService) Delete(ctx context.Context, uid, teamID int64) error {
	var leaderUID int64
	err := s.db.QueryRowContext(ctx,
		`SELECT leader_uid FROM teams WHERE id = ? AND deleted_at IS NULL`,
		teamID,
	).Scan(&leaderUID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperr.New(apperr.CodeMCATeamNotFound, "战队不存在或已解散")
		}
		return fmt.Errorf("查询战队失败: %w", err)
	}
	if leaderUID != uid {
		return apperr.New(apperr.CodeMCANotTeamLeader, "只有队长可以解散战队")
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE teams SET deleted_at = NOW() WHERE id = ?`,
		teamID,
	)
	if err != nil {
		return fmt.Errorf("解散战队失败: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return apperr.New(apperr.CodeMCATeamNotFound, "战队不存在或已解散")
	}
	return nil
}

// ListMembers 列出战队成员。
func (s *teamService) ListMembers(ctx context.Context, teamID int64) ([]*TeamMember, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, team_id, user_uid, role, joined_at 
		 FROM team_members WHERE team_id = ? 
		 ORDER BY role DESC, joined_at ASC`,
		teamID,
	)
	if err != nil {
		return nil, fmt.Errorf("查询战队成员失败: %w", err)
	}
	defer rows.Close()

	members := make([]*TeamMember, 0)
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(&m.ID, &m.TeamID, &m.UserUID, &m.Role, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("解析战队成员失败: %w", err)
		}
		members = append(members, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历战队成员失败: %w", err)
	}
	return members, nil
}

// checkMemberRole 获取指定用户在指定战队的角色。
func (s *teamService) checkMemberRole(ctx context.Context, teamID, uid int64) (int, error) {
	var role int
	err := s.db.QueryRowContext(ctx,
		`SELECT role FROM team_members WHERE team_id = ? AND user_uid = ?`,
		teamID, uid,
	).Scan(&role)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, apperr.New(apperr.CodeMCANotTeamLeader, "你不是该战队成员")
		}
		return 0, fmt.Errorf("查询成员角色失败: %w", err)
	}
	return role, nil
}

// checkTeamAlive 检查战队是否存在且未解散。
func (s *teamService) checkTeamAlive(ctx context.Context, teamID int64) error {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM teams WHERE id = ? AND deleted_at IS NULL`,
		teamID,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("查询战队状态失败: %w", err)
	}
	if count == 0 {
		return apperr.New(apperr.CodeMCATeamNotFound, "战队不存在或已解散")
	}
	return nil
}

// AddMember 添加成员（队长/副队长权限）。
// role 只能是 0（普通成员）或 1（副队长），不能设为队长。
func (s *teamService) AddMember(ctx context.Context, uid, teamID, targetUID int64, role int) (*TeamMember, error) {
	if role != 0 && role != 1 {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest, "角色值无效，只能是 0（成员）或 1（副队长）")
	}

	// 1. 战队存活校验
	if err := s.checkTeamAlive(ctx, teamID); err != nil {
		return nil, err
	}

	// 2. 调用者权限校验
	callerRole, err := s.checkMemberRole(ctx, teamID, uid)
	if err != nil {
		return nil, err
	}
	if callerRole < 1 { // 普通成员没有管理权
		return nil, apperr.New(apperr.CodeMCANotTeamLeader, "只有队长或副队长可以添加成员")
	}

	// 3. 不能添加自己
	if targetUID == uid {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest, "不能添加自己为成员")
	}

	// 4. 不能重复添加
	var existingCount int
	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM team_members WHERE team_id = ? AND user_uid = ?`,
		teamID, targetUID,
	).Scan(&existingCount)
	if err != nil {
		return nil, fmt.Errorf("检查成员是否存在失败: %w", err)
	}
	if existingCount > 0 {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest, "该用户已经是战队成员")
	}

	// 5. 插入
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO team_members (team_id, user_uid, role) VALUES (?, ?, ?)`,
		teamID, targetUID, role,
	)
	if err != nil {
		return nil, fmt.Errorf("添加成员失败: %w", err)
	}
	id, _ := res.LastInsertId()

	return &TeamMember{
		ID:      id,
		TeamID:  teamID,
		UserUID: targetUID,
		Role:    role,
	}, nil
}

// UpdateMemberRole 调整成员角色（队长/副队长权限）。
// 不能把任何人设为队长（role=2），也不能降职队长。
func (s *teamService) UpdateMemberRole(ctx context.Context, uid, teamID, targetUID int64, role int) error {
	if role < 0 || role > 2 {
		return apperr.New(apperr.CodeMCAInvalidRequest, "角色值无效")
	}
	if role == 2 {
		return apperr.New(apperr.CodeMCAInvalidRequest, "不能通过此接口设置队长，队长由战队创建者自动担任或通过转让接口变更")
	}

	// 1. 战队存活校验
	if err := s.checkTeamAlive(ctx, teamID); err != nil {
		return err
	}

	// 2. 调用者权限校验
	callerRole, err := s.checkMemberRole(ctx, teamID, uid)
	if err != nil {
		return err
	}
	if callerRole < 1 {
		return apperr.New(apperr.CodeMCANotTeamLeader, "只有队长或副队长可以调整角色")
	}

	// 3. 目标角色校验
	var currentTargetRole int
	err = s.db.QueryRowContext(ctx,
		`SELECT role FROM team_members WHERE team_id = ? AND user_uid = ?`,
		teamID, targetUID,
	).Scan(&currentTargetRole)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperr.New(apperr.CodeMCAInvalidRequest, "目标用户不是该战队成员")
		}
		return fmt.Errorf("查询目标角色失败: %w", err)
	}

	// 4. 队长保护：队长不能被降职
	if currentTargetRole == 2 {
		return apperr.New(apperr.CodeMCAInvalidRequest, "队长不能被降职，请先转让队长职位")
	}

	// 5. 副队长降级只能由队长操作
	if currentTargetRole == 1 && role == 0 && callerRole < 2 {
		return apperr.New(apperr.CodeMCANotTeamLeader, "只有队长可以将副队长降为普通成员")
	}

	// 6. 自己不能升级自己（副队长要当队长需转让）
	if targetUID == uid && role > currentTargetRole && callerRole < 2 {
		return apperr.New(apperr.CodeMCANotTeamLeader, "只有队长可以晋升成员")
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE team_members SET role = ? WHERE team_id = ? AND user_uid = ?`,
		role, teamID, targetUID,
	)
	if err != nil {
		return fmt.Errorf("更新角色失败: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return apperr.New(apperr.CodeMCAInvalidRequest, "目标用户不是该战队成员")
	}
	return nil
}

// RemoveMember 移除成员（队长/副队长权限）。
// 不能移除队长。
func (s *teamService) RemoveMember(ctx context.Context, uid, teamID, targetUID int64) error {
	// 1. 战队存活校验
	if err := s.checkTeamAlive(ctx, teamID); err != nil {
		return err
	}

	// 2. 调用者权限校验
	callerRole, err := s.checkMemberRole(ctx, teamID, uid)
	if err != nil {
		return err
	}
	if callerRole < 1 {
		return apperr.New(apperr.CodeMCANotTeamLeader, "只有队长或副队长可以移除成员")
	}

	// 3. 队长保护：不能移除队长
	var targetRole int
	err = s.db.QueryRowContext(ctx,
		`SELECT role FROM team_members WHERE team_id = ? AND user_uid = ?`,
		teamID, targetUID,
	).Scan(&targetRole)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperr.New(apperr.CodeMCAInvalidRequest, "目标用户不是该战队成员")
		}
		return fmt.Errorf("查询目标角色失败: %w", err)
	}
	if targetRole == 2 {
		return apperr.New(apperr.CodeMCAInvalidRequest, "不能移除队长，请先转让队长职位")
	}

	// 4. 副队长移除只能由队长操作
	if targetRole == 1 && callerRole < 2 {
		return apperr.New(apperr.CodeMCANotTeamLeader, "只有队长可以移除副队长")
	}

	// 5. 自己不能移除自己——应使用 LeaveTeam 接口（不在当前 scope）
	if targetUID == uid {
		return apperr.New(apperr.CodeMCAInvalidRequest, "不能通过此接口移除自己")
	}

	result, err := s.db.ExecContext(ctx,
		`DELETE FROM team_members WHERE team_id = ? AND user_uid = ?`,
		teamID, targetUID,
	)
	if err != nil {
		return fmt.Errorf("移除成员失败: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return apperr.New(apperr.CodeMCAInvalidRequest, "目标用户不是该战队成员")
	}
	return nil
}
