package service_test

// 赛季手动更迭（契约 1.12.0）：逐季递增、重复目标 409、跳季 400、
// 重置评分为显式参数（事务内清 users.rank_score）、并发唯一键兜底。

import (
	"errors"
	"net/http"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	gomysql "github.com/go-sql-driver/mysql"

	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/service"
)

func TestSeasonRoll(t *testing.T) {
	t.Run("roll advances by one and records", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewSeasonService(db)

		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT MAX\(season\) FROM seasons`).
			WillReturnRows(sqlmock.NewRows([]string{"season"}).AddRow(1))
		mock.ExpectExec(`INSERT INTO seasons \(season, reset_scores, opened_by, note\) VALUES`).
			WithArgs(2, false, "ops-uid:-100", nil).
			WillReturnResult(sqlmock.NewResult(2, 1))
		mock.ExpectCommit()
		mock.ExpectQuery(`SELECT opened_at FROM seasons WHERE season = \?`).
			WithArgs(2).
			WillReturnRows(sqlmock.NewRows([]string{"opened_at"}).AddRow("2026-11-12 12:00:00"))

		rec, err := svc.Roll(t.Context(), 2, false, "ops-uid:-100", "")
		if err != nil {
			t.Fatalf("Roll: %v", err)
		}
		if rec.Season != 2 || rec.ResetScores || rec.OpenedBy != "ops-uid:-100" || rec.OpenedAt == "" {
			t.Errorf("record = %+v, want season 2 without reset", rec)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("reset_scores is an explicit transactional reset", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewSeasonService(db)

		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT MAX\(season\) FROM seasons`).
			WillReturnRows(sqlmock.NewRows([]string{"season"}).AddRow(2))
		mock.ExpectExec(`INSERT INTO seasons \(season, reset_scores, opened_by, note\) VALUES`).
			WithArgs(3, true, "ops-uid:-100", "S3 重置开荒").
			WillReturnResult(sqlmock.NewResult(3, 1))
		mock.ExpectExec(`UPDATE users SET rank_score = 0 WHERE deleted_at IS NULL`).
			WillReturnResult(sqlmock.NewResult(0, 5))
		mock.ExpectCommit()
		mock.ExpectQuery(`SELECT opened_at FROM seasons WHERE season = \?`).
			WithArgs(3).
			WillReturnRows(sqlmock.NewRows([]string{"opened_at"}).AddRow("2026-11-12 12:00:00"))

		rec, err := svc.Roll(t.Context(), 3, true, "ops-uid:-100", "S3 重置开荒")
		if err != nil {
			t.Fatalf("Roll: %v", err)
		}
		if !rec.ResetScores || rec.Note != "S3 重置开荒" {
			t.Errorf("record = %+v, want reset with note", rec)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("repeat or backwards target answers 409", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewSeasonService(db)

		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT MAX\(season\) FROM seasons`).
			WillReturnRows(sqlmock.NewRows([]string{"season"}).AddRow(2))
		mock.ExpectRollback()

		_, err = svc.Roll(t.Context(), 2, false, "ops-uid:-100", "")
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCASeasonExists {
			t.Fatalf("err = %v, want mca_season_exists", err)
		}
		if ae.HTTPStatus() != http.StatusConflict {
			t.Fatalf("status = %d, want 409", ae.HTTPStatus())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations (no INSERT must happen): %v", err)
		}
	})

	t.Run("skipping seasons rejected", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewSeasonService(db)

		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT MAX\(season\) FROM seasons`).
			WillReturnRows(sqlmock.NewRows([]string{"season"}).AddRow(1))
		mock.ExpectRollback()

		_, err = svc.Roll(t.Context(), 3, false, "ops-uid:-100", "")
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCAInvalidRequest {
			t.Fatalf("err = %v, want mca_invalid_request", err)
		}
	})

	t.Run("concurrent duplicate via unique key answers 409", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		svc := service.NewSeasonService(db)

		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT MAX\(season\) FROM seasons`).
			WillReturnRows(sqlmock.NewRows([]string{"season"}).AddRow(1))
		mock.ExpectExec(`INSERT INTO seasons \(season, reset_scores, opened_by, note\) VALUES`).
			WithArgs(2, false, "ops-uid:-100", nil).
			WillReturnError(&gomysql.MySQLError{Number: 1062, Message: "Duplicate entry"})
		mock.ExpectRollback()

		_, err = svc.Roll(t.Context(), 2, false, "ops-uid:-100", "")
		var ae *apperr.Error
		if !errors.As(err, &ae) || ae.Code != apperr.CodeMCASeasonExists {
			t.Fatalf("err = %v, want mca_season_exists", err)
		}
	})
}

func TestSeasonCurrentAndList(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := service.NewSeasonService(db)

	mock.ExpectQuery(`SELECT MAX\(season\) FROM seasons`).
		WillReturnRows(sqlmock.NewRows([]string{"season"}).AddRow(3))
	current, err := svc.Current(t.Context())
	if err != nil || current != 3 {
		t.Fatalf("Current = %d/%v, want 3", current, err)
	}

	mock.ExpectQuery(`SELECT season, reset_scores, opened_at, opened_by, note FROM seasons ORDER BY season DESC`).
		WillReturnRows(sqlmock.NewRows([]string{"season", "reset_scores", "opened_at", "opened_by", "note"}).
			AddRow(3, true, "2026-11-12 12:00:00", "ops-uid:-100", "S3").
			AddRow(2, false, "2026-11-10 09:00:00", "ops-uid:-100", nil).
			AddRow(1, false, "2026-11-01 00:00:00", "migration-v6", nil))
	list, err := svc.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 || list[0].Season != 3 || !list[0].ResetScores || list[2].Season != 1 {
		t.Fatalf("list = %+v, want 3 seasons newest first", list)
	}
	if list[1].Note != "" {
		t.Errorf("null note = %q, want empty", list[1].Note)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
