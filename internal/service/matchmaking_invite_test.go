package service_test

// 组队邀请生命周期（契约 1.11.0 同意机制）：发出（含重复/邀请自己
// 拒绝）、接受/拒绝的状态机（非受邀人 403、已终态 409、过期 409、
// 不存在 404）、列表的过期惰性判定。

import (
	"errors"
	"net/http"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/service"
)

// inviteRowColumns scanInvite 的列序。
var inviteRowColumns = []string{"id", "inviter_uid", "invitee_uid", "match_type",
	"status", "created_at", "expires_at", "resolved_at", "consumed_at"}

func TestInviteCreate(t *testing.T) {
	t.Run("invite creates pending rows", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), newReportFakeHRPAuth(t), nil)

		// 成员 8：无既有 pending 邀请 → 写入并回读。
		mock.ExpectQuery(`SELECT id FROM matchmaking_party_invites`).
			WithArgs(int64(7), int64(8), "2v2", service.InviteStatusPending).
			WillReturnRows(sqlmock.NewRows([]string{"id"}))
		mock.ExpectExec(`INSERT INTO matchmaking_party_invites`).
			WithArgs(int64(7), int64(8), "2v2", service.InviteStatusPending, 0).
			WillReturnResult(sqlmock.NewResult(42, 1))
		mock.ExpectQuery(`SELECT id, inviter_uid, invitee_uid, match_type, status`).
			WithArgs(int64(42)).
			WillReturnRows(sqlmock.NewRows(inviteRowColumns).
				AddRow(42, 7, 8, "2v2", "pending", "2026-11-12 10:00:00", "2099-01-01 00:00:00", nil, nil))

		// PartyInviteTTLSeconds 未配置 → 校验缺省 300；此处直接以 0 传入，
		// 服务层按配置原值透传（缺省由 config 校验层补齐）。
		invites, err := svc.Invite(t.Context(), 7, "2v2", []string{"uuid-2"})
		if err != nil {
			t.Fatalf("Invite: %v", err)
		}
		if len(invites) != 1 || invites[0].ID != 42 || invites[0].Status != service.InviteStatusPending {
			t.Fatalf("invites = %+v, want one pending invite id 42", invites)
		}
		if invites[0].InviterUID != 7 || invites[0].InviteeUID != 8 {
			t.Errorf("inviter/invitee = %d/%d, want 7/8", invites[0].InviterUID, invites[0].InviteeUID)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("inviting self rejected", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), newReportFakeHRPAuth(t), nil)

		// uuid-1 解析为 uid 7，与调用者相同。
		_, err = svc.Invite(t.Context(), 7, "2v2", []string{"uuid-1"})
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInvalidRequest {
			t.Fatalf("err = %v, want mca_invalid_request", err)
		}
	})

	t.Run("duplicate pending invite rejected", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), newReportFakeHRPAuth(t), nil)

		mock.ExpectQuery(`SELECT id FROM matchmaking_party_invites`).
			WithArgs(int64(7), int64(8), "2v2", service.InviteStatusPending).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(41))

		_, err = svc.Invite(t.Context(), 7, "2v2", []string{"uuid-2"})
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInviteExists {
			t.Fatalf("err = %v, want mca_invite_exists", err)
		}
		if ae.HTTPStatus() != http.StatusConflict {
			t.Fatalf("status = %d, want 409", ae.HTTPStatus())
		}
	})

	t.Run("unknown mode rejected", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), newReportFakeHRPAuth(t), nil)

		_, err = svc.Invite(t.Context(), 7, "4v4", []string{"uuid-2"})
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInvalidRequest {
			t.Fatalf("err = %v, want mca_invalid_request", err)
		}
	})
}

func TestInviteAcceptReject(t *testing.T) {
	t.Run("accept transitions pending to accepted", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

		mock.ExpectQuery(`SELECT inviter_uid, invitee_uid, match_type, status, expires_at FROM matchmaking_party_invites`).
			WithArgs(int64(42)).
			WillReturnRows(sqlmock.NewRows([]string{"inviter_uid", "invitee_uid", "match_type", "status", "expires_at"}).
				AddRow(7, 8, "2v2", "pending", "2099-01-01 00:00:00"))
		mock.ExpectExec(`UPDATE matchmaking_party_invites SET status`).
			WithArgs(service.InviteStatusAccepted, int64(42)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(`SELECT id, inviter_uid, invitee_uid, match_type, status`).
			WithArgs(int64(42)).
			WillReturnRows(sqlmock.NewRows(inviteRowColumns).
				AddRow(42, 7, 8, "2v2", "accepted", "2026-11-12 10:00:00", "2099-01-01 00:00:00", "2026-11-12 10:01:00", nil))

		inv, err := svc.AcceptInvite(t.Context(), 8, 42)
		if err != nil {
			t.Fatalf("AcceptInvite: %v", err)
		}
		if inv.Status != service.InviteStatusAccepted || inv.ResolvedAt == "" {
			t.Errorf("invite = %+v, want accepted with resolved_at", inv)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("reject transitions pending to rejected", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

		mock.ExpectQuery(`SELECT inviter_uid, invitee_uid, match_type, status, expires_at FROM matchmaking_party_invites`).
			WithArgs(int64(42)).
			WillReturnRows(sqlmock.NewRows([]string{"inviter_uid", "invitee_uid", "match_type", "status", "expires_at"}).
				AddRow(7, 8, "2v2", "pending", "2099-01-01 00:00:00"))
		mock.ExpectExec(`UPDATE matchmaking_party_invites SET status`).
			WithArgs(service.InviteStatusRejected, int64(42)).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(`SELECT id, inviter_uid, invitee_uid, match_type, status`).
			WithArgs(int64(42)).
			WillReturnRows(sqlmock.NewRows(inviteRowColumns).
				AddRow(42, 7, 8, "2v2", "rejected", "2026-11-12 10:00:00", "2099-01-01 00:00:00", "2026-11-12 10:01:00", nil))

		inv, err := svc.RejectInvite(t.Context(), 8, 42)
		if err != nil {
			t.Fatalf("RejectInvite: %v", err)
		}
		if inv.Status != service.InviteStatusRejected {
			t.Errorf("invite = %+v, want rejected", inv)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("only the invitee may resolve", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

		mock.ExpectQuery(`SELECT inviter_uid, invitee_uid, match_type, status, expires_at FROM matchmaking_party_invites`).
			WithArgs(int64(42)).
			WillReturnRows(sqlmock.NewRows([]string{"inviter_uid", "invitee_uid", "match_type", "status", "expires_at"}).
				AddRow(7, 8, "2v2", "pending", "2099-01-01 00:00:00"))

		// uid 9 既非邀请人 7 也非受邀人 8。
		_, err = svc.AcceptInvite(t.Context(), 9, 42)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCANotInvitee {
			t.Fatalf("err = %v, want mca_not_invitee", err)
		}
		if ae.HTTPStatus() != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", ae.HTTPStatus())
		}
	})

	t.Run("already resolved answers 409", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

		mock.ExpectQuery(`SELECT inviter_uid, invitee_uid, match_type, status, expires_at FROM matchmaking_party_invites`).
			WithArgs(int64(42)).
			WillReturnRows(sqlmock.NewRows([]string{"inviter_uid", "invitee_uid", "match_type", "status", "expires_at"}).
				AddRow(7, 8, "2v2", "accepted", "2099-01-01 00:00:00"))

		_, err = svc.AcceptInvite(t.Context(), 8, 42)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInviteResolved {
			t.Fatalf("err = %v, want mca_invite_resolved", err)
		}
	})

	t.Run("expired invite answers 409", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

		// expires_at 早于当前时刻 → 惰性判定过期。
		mock.ExpectQuery(`SELECT inviter_uid, invitee_uid, match_type, status, expires_at FROM matchmaking_party_invites`).
			WithArgs(int64(42)).
			WillReturnRows(sqlmock.NewRows([]string{"inviter_uid", "invitee_uid", "match_type", "status", "expires_at"}).
				AddRow(7, 8, "2v2", "pending", "2020-01-01 00:00:00"))

		_, err = svc.AcceptInvite(t.Context(), 8, 42)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInviteExpired {
			t.Fatalf("err = %v, want mca_invite_expired", err)
		}
		if ae.HTTPStatus() != http.StatusConflict {
			t.Fatalf("status = %d, want 409", ae.HTTPStatus())
		}
	})

	t.Run("unknown invite answers 404", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

		mock.ExpectQuery(`SELECT inviter_uid, invitee_uid, match_type, status, expires_at FROM matchmaking_party_invites`).
			WithArgs(int64(999)).
			WillReturnRows(sqlmock.NewRows([]string{"inviter_uid", "invitee_uid", "match_type", "status", "expires_at"}))

		_, err = svc.RejectInvite(t.Context(), 8, 999)
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInviteNotFound {
			t.Fatalf("err = %v, want mca_invite_not_found", err)
		}
		if ae.HTTPStatus() != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", ae.HTTPStatus())
		}
	})
}

func TestMyInvitesLazyExpiry(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewMatchmakingService(db, testMatchmakingConfig(), nil, nil)

	// 一行未过期 pending、一行已过期的 pending（惰性判定为 expired）、
	// 一行 accepted。
	mock.ExpectQuery(`SELECT id, inviter_uid, invitee_uid, match_type, status`).
		WithArgs(int64(8), int64(8)).
		WillReturnRows(sqlmock.NewRows(inviteRowColumns).
			AddRow(43, 7, 8, "2v2", "pending", "2026-11-12 10:00:00", "2099-01-01 00:00:00", nil, nil).
			AddRow(42, 7, 8, "2v2", "pending", "2026-11-12 09:00:00", "2020-01-01 00:00:00", nil, nil).
			AddRow(41, 8, 9, "1v1", "accepted", "2026-11-12 08:00:00", "2099-01-01 00:00:00", "2026-11-12 08:01:00", nil))

	invites, err := svc.MyInvites(t.Context(), 8)
	if err != nil {
		t.Fatalf("MyInvites: %v", err)
	}
	if len(invites) != 3 {
		t.Fatalf("invites = %d, want 3", len(invites))
	}
	if invites[0].Status != service.InviteStatusPending {
		t.Errorf("invite 43 status = %s, want pending", invites[0].Status)
	}
	if invites[1].Status != service.InviteStatusExpired {
		t.Errorf("invite 42 status = %s, want expired (lazy)", invites[1].Status)
	}
	if invites[2].Status != service.InviteStatusAccepted || invites[2].ResolvedAt == "" {
		t.Errorf("invite 41 = %+v, want accepted with resolved_at", invites[2])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
