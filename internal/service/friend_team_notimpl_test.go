package service_test

import (
	"errors"
	"net/http"
	"testing"

	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/service"
)

// assertNotImplemented 断言错误为类型化的 mca_not_implemented（501）。
func assertNotImplemented(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil (no fake success allowed)")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("expected a typed *apperr.Error, got %T: %v", err, err)
	}
	if ae.Code != apperr.CodeMCANotImplemented {
		t.Fatalf("code = %q, want mca_not_implemented", ae.Code)
	}
	if ae.HTTPStatus() != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", ae.HTTPStatus())
	}
}

// TestFriendServiceNotImplemented 钉住好友服务全部方法返回类型化
// "未实现"错误：调用方不得再收到空列表/硬编码假成功（作者决策）。
func TestFriendServiceNotImplemented(t *testing.T) {
	svc := service.NewFriendService(nil) // 未实现方法不触库
	ctx := t.Context()

	if _, err := svc.List(ctx, 7); err == nil {
		t.Fatal("List must not succeed")
	} else {
		assertNotImplemented(t, err)
	}
	if f, err := svc.Request(ctx, 7, 8); err == nil {
		t.Fatalf("Request must not return a fake friend, got %+v", f)
	} else {
		assertNotImplemented(t, err)
	}
	assertNotImplemented(t, svc.Accept(ctx, 7, 1))
	assertNotImplemented(t, svc.Reject(ctx, 7, 1))
	assertNotImplemented(t, svc.Delete(ctx, 7, 1))
}

// TestTeamServiceNotImplemented 钉住战队服务全部方法返回类型化
// "未实现"错误：调用方不得再收到硬编码假队伍/假成员。
func TestTeamServiceNotImplemented(t *testing.T) {
	svc := service.NewTeamService(nil) // 未实现方法不触库
	ctx := t.Context()

	if team, err := svc.Create(ctx, 7, "t", "d"); err == nil {
		t.Fatalf("Create must not return a fake team, got %+v", team)
	} else {
		assertNotImplemented(t, err)
	}
	if team, err := svc.GetByID(ctx, 1); err == nil {
		t.Fatalf("GetByID must not return a fake team, got %+v", team)
	} else {
		assertNotImplemented(t, err)
	}
	assertNotImplemented(t, svc.Delete(ctx, 7, 1))
	if members, err := svc.ListMembers(ctx, 1); err == nil {
		t.Fatalf("ListMembers must not succeed, got %+v", members)
	} else {
		assertNotImplemented(t, err)
	}
	if member, err := svc.AddMember(ctx, 7, 1, 8, 0); err == nil {
		t.Fatalf("AddMember must not return a fake member, got %+v", member)
	} else {
		assertNotImplemented(t, err)
	}
	assertNotImplemented(t, svc.UpdateMemberRole(ctx, 7, 1, 8, 1))
	assertNotImplemented(t, svc.RemoveMember(ctx, 7, 1, 8))
}
