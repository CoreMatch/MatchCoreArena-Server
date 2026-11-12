package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Friend 好友关系模型。
type Friend struct {
	ID        int64  `json:"id"`
	UserUID   int64  `json:"user_uid"`
	FriendUID int64  `json:"friend_uid"`
	Status    int    `json:"status"` // 0:待确认 1:已接受 2:已拒绝
	CreatedAt string `json:"created_at"`
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

// List 列出当前用户的已接受好友（双向查询）。
func (s *friendService) List(ctx context.Context, uid int64) ([]*Friend, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_uid, friend_uid, status, created_at 
		 FROM friends 
		 WHERE (user_uid = ? OR friend_uid = ?) AND status = 1`,
		uid, uid,
	)
	if err != nil {
		return nil, fmt.Errorf("查询好友列表失败: %w", err)
	}
	defer rows.Close()

	friends := make([]*Friend, 0)
	for rows.Next() {
		var f Friend
		if err := rows.Scan(&f.ID, &f.UserUID, &f.FriendUID, &f.Status, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("解析好友记录失败: %w", err)
		}
		friends = append(friends, &f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历好友列表失败: %w", err)
	}
	return friends, nil
}

// Request 发起好友请求。
// 会先检查是否已存在 pending 或 accepted 的关系，防止重复请求。
func (s *friendService) Request(ctx context.Context, requesterUID, targetUID int64) (*Friend, error) {
	// 1. 检查是否已存在任何状态的关系（双向）
	var existingID int64
	var existingStatus int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, status FROM friends 
		 WHERE (user_uid = ? AND friend_uid = ?) OR (user_uid = ? AND friend_uid = ?)`,
		requesterUID, targetUID, targetUID, requesterUID,
	).Scan(&existingID, &existingStatus)
	if err == nil {
		// 已存在
		if existingStatus == 1 {
			return nil, errors.New("你们已经是好友了")
		}
		if existingStatus == 0 {
			return nil, errors.New("好友请求已发送，等待对方确认")
		}
		// status == 2（已拒绝），允许重新发起——删除旧记录再插入
		if _, delErr := s.db.ExecContext(ctx, "DELETE FROM friends WHERE id = ?", existingID); delErr != nil {
			return nil, fmt.Errorf("清理旧好友关系失败: %w", delErr)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("检查好友关系失败: %w", err)
	}

	// 2. 插入新请求
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO friends (user_uid, friend_uid, status) VALUES (?, ?, 0)`,
		requesterUID, targetUID,
	)
	if err != nil {
		return nil, fmt.Errorf("发送好友请求失败: %w", err)
	}
	id, _ := res.LastInsertId()

	return &Friend{
		ID:        id,
		UserUID:   requesterUID,
		FriendUID: targetUID,
		Status:    0,
	}, nil
}

// Accept 接受好友请求。
// 只有被请求方（friend_uid）能执行，且只能接受 status=0 的请求。
func (s *friendService) Accept(ctx context.Context, uid, friendID int64) error {
	// 1. 取出这条关系，校验操作者是被请求方
	var (
		id       int64
		userUID  int64
		friendUID int64
		status   int
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_uid, friend_uid, status FROM friends WHERE id = ?`,
		friendID,
	).Scan(&id, &userUID, &friendUID, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("好友请求不存在")
		}
		return fmt.Errorf("查询好友请求失败: %w", err)
	}

	if userUID != uid && friendUID != uid {
		return errors.New("无权操作此好友请求")
	}
	if status != 0 {
		if status == 1 {
			return errors.New("好友关系已建立")
		}
		return errors.New("请求已结束，无法重复操作")
	}

	// 2. 更新状态为已接受
	result, err := s.db.ExecContext(ctx,
		`UPDATE friends SET status = 1 WHERE id = ?`,
		friendID,
	)
	if err != nil {
		return fmt.Errorf("接受好友请求失败: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return errors.New("好友请求不存在或已被处理")
	}
	return nil
}

// Reject 拒绝好友请求。
func (s *friendService) Reject(ctx context.Context, uid, friendID int64) error {
	var (
		id        int64
		userUID   int64
		friendUID int64
		status    int
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_uid, friend_uid, status FROM friends WHERE id = ?`,
		friendID,
	).Scan(&id, &userUID, &friendUID, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("好友请求不存在")
		}
		return fmt.Errorf("查询好友请求失败: %w", err)
	}

	if userUID != uid && friendUID != uid {
		return errors.New("无权操作此好友请求")
	}
	if status != 0 {
		return errors.New("请求已结束，无法重复操作")
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE friends SET status = 2 WHERE id = ?`,
		friendID,
	)
	if err != nil {
		return fmt.Errorf("拒绝好友请求失败: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return errors.New("好友请求不存在或已被处理")
	}
	return nil
}

// Delete 删除好友关系（双向）。
func (s *friendService) Delete(ctx context.Context, uid, friendID int64) error {
	var (
		id        int64
		userUID   int64
		friendUID int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT id, user_uid, friend_uid FROM friends WHERE id = ?`,
		friendID,
	).Scan(&id, &userUID, &friendUID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("好友关系不存在")
		}
		return fmt.Errorf("查询好友关系失败: %w", err)
	}

	if userUID != uid && friendUID != uid {
		return errors.New("无权删除此好友关系")
	}

	result, err := s.db.ExecContext(ctx, "DELETE FROM friends WHERE id = ?", friendID)
	if err != nil {
		return fmt.Errorf("删除好友关系失败: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return errors.New("好友关系不存在")
	}
	return nil
}
