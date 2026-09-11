package service

import (
	"context"
	"database/sql"
	"fmt"
)

// User 用户模型。
type User struct {
	UID        int64  `json:"uid"`
	Level      int    `json:"level"`
	Experience int64  `json:"experience"`
	RankScore  int    `json:"rank_score"`
	WinsCount  int    `json:"wins_count"`
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
	// EnsureUser 确保用户存在（首次登录时自动落库）。uid 为 HRPAuth User.UID。
	EnsureUser(ctx context.Context, uid int64)
}

// userService 数据库实现。
type userService struct {
	db *sql.DB
}

// NewUserService 创建 UserService 实例。
func NewUserService(db *sql.DB) UserService {
	return &userService{db: db}
}

// GetByUID 获取用户公开信息。
func (s *userService) GetByUID(ctx context.Context, uid int64) (*User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		"SELECT uid, level, experience, rank_score, wins_count, created_at, updated_at FROM users WHERE uid = ? AND deleted_at IS NULL",
		uid,
	).Scan(&u.UID, &u.Level, &u.Experience, &u.RankScore, &u.WinsCount, &u.CreatedAt, &u.UpdatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("用户不存在")
		}
		return nil, fmt.Errorf("查询用户信息失败: %w", err)
	}
	return &u, nil
}

// GetMe 获取当前用户完整信息（骨架 stub）。
func (s *userService) GetMe(ctx context.Context, uid int64) (*User, error) {
	return s.GetByUID(ctx, uid)
}

// AddExperience 增加经验。
func (s *userService) AddExperience(ctx context.Context, uid int64, amount int64) (*User, error) {
	if _, err := s.db.ExecContext(ctx, "UPDATE users SET experience = experience + ? WHERE uid = ? AND deleted_at IS NULL", amount, uid); err != nil {
		return nil, fmt.Errorf("增加经验失败: %w", err)
	}
	return s.GetByUID(ctx, uid)
}

// EnsureUser 确保用户存在（首次登录时自动落库）。
func (s *userService) EnsureUser(ctx context.Context, uid int64) {
	// INSERT IGNORE INTO users (uid) VALUES (?)
	_, _ = s.db.ExecContext(ctx, "INSERT IGNORE INTO users (uid) VALUES (?)", uid)
}
