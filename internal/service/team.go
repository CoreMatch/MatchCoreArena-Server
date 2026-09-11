package service

import (
	"context"
	"database/sql"
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
	// AddMember 添加成员。
	AddMember(ctx context.Context, uid, teamID, targetUID int64, role int) (*TeamMember, error)
	// UpdateMemberRole 调整成员角色。
	UpdateMemberRole(ctx context.Context, uid, teamID, targetUID int64, role int) error
	// RemoveMember 移除成员。
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

// Create 创建战队（骨架 stub）。
func (s *teamService) Create(ctx context.Context, leaderUID int64, name, description string) (*Team, error) {
	// TODO: INSERT INTO teams (name, description, leader_uid) VALUES (?, ?, ?)
	// TODO: INSERT INTO team_members (team_id, user_uid, role) VALUES (?, ?, 2)
	return &Team{
		ID:          1,
		Name:        name,
		Description: description,
		LeaderUID:   leaderUID,
		CreatedAt:   "2026-09-11T00:00:00Z",
	}, nil
}

// GetByID 获取战队详情（骨架 stub）。
func (s *teamService) GetByID(ctx context.Context, id int64) (*Team, error) {
	// TODO: SELECT * FROM teams WHERE id = ? AND deleted_at IS NULL
	return &Team{
		ID:          id,
		Name:        "stub-team",
		Description: "stub description",
		LeaderUID:   1,
		CreatedAt:   "2026-09-11T00:00:00Z",
	}, nil
}

// Delete 解散战队（骨架 stub）。
func (s *teamService) Delete(ctx context.Context, uid, teamID int64) error {
	// TODO: 校验 uid == leader_uid，然后 UPDATE teams SET deleted_at = NOW() WHERE id = ?
	return nil
}

// ListMembers 列出成员（骨架 stub）。
func (s *teamService) ListMembers(ctx context.Context, teamID int64) ([]*TeamMember, error) {
	// TODO: SELECT * FROM team_members WHERE team_id = ?
	return []*TeamMember{}, nil
}

// AddMember 添加成员（骨架 stub）。
func (s *teamService) AddMember(ctx context.Context, uid, teamID, targetUID int64, role int) (*TeamMember, error) {
	// TODO: 校验 uid 有权限（队长/副队长），然后 INSERT INTO team_members
	return &TeamMember{
		ID:       1,
		TeamID:   teamID,
		UserUID:  targetUID,
		Role:     role,
		JoinedAt: "2026-09-11T00:00:00Z",
	}, nil
}

// UpdateMemberRole 调整角色（骨架 stub）。
func (s *teamService) UpdateMemberRole(ctx context.Context, uid, teamID, targetUID int64, role int) error {
	// TODO: 校验权限，UPDATE team_members SET role = ? WHERE team_id = ? AND user_uid = ?
	return nil
}

// RemoveMember 移除成员（骨架 stub）。
func (s *teamService) RemoveMember(ctx context.Context, uid, teamID, targetUID int64) error {
	// TODO: 校验权限，DELETE FROM team_members WHERE team_id = ? AND user_uid = ?
	return nil
}
