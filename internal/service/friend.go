package service

import (
	"context"
	"database/sql"
)

// Friend 好友关系模型。
type Friend struct {
	ID         int64  `json:"id"`
	UserUID    int64  `json:"user_uid"`
	FriendUID  int64  `json:"friend_uid"`
	Status     int    `json:"status"` // 0:待确认 1:已接受 2:已拒绝
	CreatedAt  string `json:"created_at"`
}

// FriendService 好友业务接口。
type FriendService interface {
	// List 列出当前用户的好友（已接受）。
	List(ctx context.Context, uid int64) ([]*Friend, error)
	// Request 发起好友请求。
	Request(ctx context.Context, requesterUID, targetUID int64) (*Friend, error)
	// Accept 接受好友请求。
	Accept(ctx context.Context, uid, friendID int64) error
	// Reject 拒绝好友请求。
	Reject(ctx context.Context, uid, friendID int64) error
	// Delete 删除好友关系。
	Delete(ctx context.Context, uid, friendID int64) error
}

// friendService 数据库实现。
type friendService struct {
	db *sql.DB
}

// NewFriendService 创建 FriendService 实例。
func NewFriendService(db *sql.DB) FriendService {
	return &friendService{db: db}
}

// List 列出好友（骨架 stub）。
func (s *friendService) List(ctx context.Context, uid int64) ([]*Friend, error) {
	// TODO: SELECT * FROM friends WHERE (user_uid = ? OR friend_uid = ?) AND status = 1
	return []*Friend{}, nil
}

// Request 发起好友请求（骨架 stub）。
func (s *friendService) Request(ctx context.Context, requesterUID, targetUID int64) (*Friend, error) {
	// TODO: INSERT INTO friends (user_uid, friend_uid, status) VALUES (?, ?, 0)
	return &Friend{
		ID:        1,
		UserUID:   requesterUID,
		FriendUID: targetUID,
		Status:    0,
		CreatedAt: "2026-09-11T00:00:00Z",
	}, nil
}

// Accept 接受好友请求（骨架 stub）。
func (s *friendService) Accept(ctx context.Context, uid, friendID int64) error {
	// TODO: UPDATE friends SET status = 1 WHERE id = ? AND (user_uid = ? OR friend_uid = ?)
	return nil
}

// Reject 拒绝好友请求（骨架 stub）。
func (s *friendService) Reject(ctx context.Context, uid, friendID int64) error {
	// TODO: UPDATE friends SET status = 2 WHERE id = ? AND (user_uid = ? OR friend_uid = ?)
	return nil
}

// Delete 删除好友（骨架 stub）。
func (s *friendService) Delete(ctx context.Context, uid, friendID int64) error {
	// TODO: DELETE FROM friends WHERE id = ? AND (user_uid = ? OR friend_uid = ?)
	return nil
}
