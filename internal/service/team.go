package service

import (
	"context"
	"database/sql"

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
//
// 全部方法当前未实现：返回类型化错误 mca_not_implemented（HTTP 501），
// 而不是假数据或假成功（作者决策，见 knowledge 0010）。
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

// errTeamNotImplemented 战队功能未实现的统一类型化错误。
func errTeamNotImplemented(op string) error {
	return apperr.New(apperr.CodeMCANotImplemented, "战队功能未实现: "+op)
}

// Create 创建战队（未实现）。
func (s *teamService) Create(ctx context.Context, leaderUID int64, name, description string) (*Team, error) {
	return nil, errTeamNotImplemented("Create")
}

// GetByID 获取战队详情（未实现）。
func (s *teamService) GetByID(ctx context.Context, id int64) (*Team, error) {
	return nil, errTeamNotImplemented("GetByID")
}

// Delete 解散战队（未实现）。
func (s *teamService) Delete(ctx context.Context, uid, teamID int64) error {
	return errTeamNotImplemented("Delete")
}

// ListMembers 列出成员（未实现）。
func (s *teamService) ListMembers(ctx context.Context, teamID int64) ([]*TeamMember, error) {
	return nil, errTeamNotImplemented("ListMembers")
}

// AddMember 添加成员（未实现）。
func (s *teamService) AddMember(ctx context.Context, uid, teamID, targetUID int64, role int) (*TeamMember, error) {
	return nil, errTeamNotImplemented("AddMember")
}

// UpdateMemberRole 调整角色（未实现）。
func (s *teamService) UpdateMemberRole(ctx context.Context, uid, teamID, targetUID int64, role int) error {
	return errTeamNotImplemented("UpdateMemberRole")
}

// RemoveMember 移除成员（未实现）。
func (s *teamService) RemoveMember(ctx context.Context, uid, teamID, targetUID int64) error {
	return errTeamNotImplemented("RemoveMember")
}
