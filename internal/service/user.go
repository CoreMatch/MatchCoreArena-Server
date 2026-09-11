package service

import (
	"context"
	"database/sql"
)

// User 用户模型。
type User struct {
	UID        int64  `json:"uid"`
	Level      int    `json:"level"`
	Experience int64  `json:"experience"`
	RankScore  int    `json:"rank_score"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// UserService 用户业务接口。
type UserService interface {
	// GetByUID 获取用户公开信息。
	GetByUID(ctx context.Context, uid int64) (*User, error)
	// GetMe 获取当前用户完整信息。
	GetMe(ctx context.Context, uid int64) (*User, error)
	// AddExperience 增加经验（占位）。
	AddExperience(ctx context.Context, uid int64, amount int64) (*User, error)
	// EnsureUser 确保用户存在（首次登录时自动落库）。uid 为 HRPAuth sub 字符串。
	EnsureUser(ctx context.Context, uid string)
}

// userService 数据库实现。
type userService struct {
	db *sql.DB
}

// NewUserService 创建 UserService 实例。
func NewUserService(db *sql.DB) UserService {
	return &userService{db: db}
}

// GetByUID 获取用户公开信息（骨架 stub）。
func (s *userService) GetByUID(ctx context.Context, uid int64) (*User, error) {
	// TODO: SELECT uid, level, experience, rank_score, created_at, updated_at FROM users WHERE uid = ?
	return &User{
		UID:        uid,
		Level:      1,
		Experience: 0,
		RankScore:  0,
		CreatedAt:  "2026-09-11T00:00:00Z",
		UpdatedAt:  "2026-09-11T00:00:00Z",
	}, nil
}

// GetMe 获取当前用户完整信息（骨架 stub）。
func (s *userService) GetMe(ctx context.Context, uid int64) (*User, error) {
	return s.GetByUID(ctx, uid)
}

// AddExperience 增加经验（骨架 stub）。
func (s *userService) AddExperience(ctx context.Context, uid int64, amount int64) (*User, error) {
	// TODO: UPDATE users SET experience = experience + ? WHERE uid = ?
	return s.GetByUID(ctx, uid)
}

// EnsureUser 确保用户存在（首次登录时自动落库）。
func (s *userService) EnsureUser(ctx context.Context, uid string) {
	// INSERT IGNORE INTO users (uid) VALUES (?)
	// uid 为 HRPAuth sub，直接作为本地 uid
	_, _ = s.db.ExecContext(ctx, "INSERT IGNORE INTO users (uid) VALUES (?)", uid)
}
