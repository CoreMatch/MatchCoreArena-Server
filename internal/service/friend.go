package service

import (
	"context"
	"database/sql"

	"MatchCoreArena-Server/internal/apperr"
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
//
// 全部方法当前未实现：返回类型化错误 mca_not_implemented（HTTP 501），
// 而不是假数据或假成功（作者决策，见 knowledge 0010）。
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

// errFriendNotImplemented 好友功能未实现的统一类型化错误。
func errFriendNotImplemented(op string) error {
	return apperr.New(apperr.CodeMCANotImplemented, "好友功能未实现: "+op)
}

// List 列出好友（未实现）。
func (s *friendService) List(ctx context.Context, uid int64) ([]*Friend, error) {
	return nil, errFriendNotImplemented("List")
}

// Request 发起好友请求（未实现）。
func (s *friendService) Request(ctx context.Context, requesterUID, targetUID int64) (*Friend, error) {
	return nil, errFriendNotImplemented("Request")
}

// Accept 接受好友请求（未实现）。
func (s *friendService) Accept(ctx context.Context, uid, friendID int64) error {
	return errFriendNotImplemented("Accept")
}

// Reject 拒绝好友请求（未实现）。
func (s *friendService) Reject(ctx context.Context, uid, friendID int64) error {
	return errFriendNotImplemented("Reject")
}

// Delete 删除好友（未实现）。
func (s *friendService) Delete(ctx context.Context, uid, friendID int64) error {
	return errFriendNotImplemented("Delete")
}
