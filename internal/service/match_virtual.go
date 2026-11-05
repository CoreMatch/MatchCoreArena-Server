package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"MatchCoreArena-Server/internal/apperr"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/rating"
)

// 对局来源常量（matches.source）。
const (
	MatchSourceGame    = "game"
	MatchSourceVirtual = "virtual"
)

// 虚拟对局队伍结果常量。
const (
	ResultWin  = "win"
	ResultLose = "lose"
	ResultDraw = "draw"
)

// VirtualTeamInput 虚拟对局中的一支队伍。
// Result 取值 win / lose / draw；一局中至多一支队伍为 win，
// 存在 win 时其余队伍必须为 lose（不允许胜负与平局混用）。
type VirtualTeamInput struct {
	TeamID       string              `json:"team_id" binding:"required"`
	Result       string              `json:"result" binding:"required"`
	InitialCount int                 `json:"initial_count"`
	AliveCount   int                 `json:"alive_count"`
	Members      []ParticipantReport `json:"members" binding:"required"`
}

// VirtualReportInput 虚拟对局提交参数。
// 不依赖游戏侧连接：无服务器进程上报，参与者身份仍经 HRPAuth 解析。
// StartedAt / FinishedAt 缺省取当前时间。
type VirtualReportInput struct {
	MatchType       string             `json:"match_type" binding:"required"`
	Teams           []VirtualTeamInput `json:"teams" binding:"required"`
	DurationSeconds int                `json:"duration_seconds"`
	MapName         string             `json:"map_name"`
	StartedAt       string             `json:"started_at"`
	FinishedAt      string             `json:"finished_at"`
}

// ParticipantSettlement 虚拟对局结算后单个参与者的评分变化。
type ParticipantSettlement struct {
	UID             int64  `json:"uid"`
	UUID            string `json:"uuid"`
	TeamID          string `json:"team_id"`
	Result          string `json:"result"`
	RankScoreBefore int    `json:"rank_score_before"`
	RankScoreDelta  int    `json:"rank_score_delta"`
	RankScoreAfter  int    `json:"rank_score_after"`
}

// VirtualMatchResult 虚拟对局结算结果：对局记录 + 每人评分变化。
type VirtualMatchResult struct {
	Match        Match                   `json:"match"`
	Participants []ParticipantSettlement `json:"participants"`
}

// validateVirtualInput 校验虚拟对局输入的结构语义。
func validateVirtualInput(in VirtualReportInput) error {
	if len(in.Teams) < 2 {
		return errors.New("teams 至少需要 2 支队伍")
	}
	seenUUID := make(map[string]bool)
	winners := 0
	for i := range in.Teams {
		team := &in.Teams[i]
		switch team.Result {
		case ResultWin:
			winners++
		case ResultLose, ResultDraw:
		default:
			return fmt.Errorf("teams[%d].result 非法: %q（允许 win/lose/draw）", i, team.Result)
		}
		if len(team.Members) == 0 {
			return fmt.Errorf("teams[%d].members 不能为空", i)
		}
		if team.InitialCount < 1 {
			return fmt.Errorf("teams[%d].initial_count 必须 >= 1", i)
		}
		if team.AliveCount < 0 || team.AliveCount > team.InitialCount {
			return fmt.Errorf("teams[%d].alive_count 必须在 [0, initial_count] 范围内", i)
		}
		for _, m := range team.Members {
			if seenUUID[m.UUID] {
				return fmt.Errorf("参与者 uuid 重复: %s", m.UUID)
			}
			seenUUID[m.UUID] = true
		}
	}
	if winners > 1 {
		return errors.New("至多一支队伍的 result 可为 win")
	}
	if winners == 1 {
		for i := range in.Teams {
			if in.Teams[i].Result == ResultDraw {
				return errors.New("存在获胜队伍时，其余队伍 result 必须为 lose，不能与平局混用")
			}
		}
	}
	return nil
}

// ReportVirtual 提交虚拟对局并完成定级结算。
//
// 评分规则（不发明新算法，复用 internal/rating 的加权 Elo）：
//   - 恰好一队获胜：公式与游戏上报路径 Report 完全一致
//     （胜/败双方平均分求期望胜率，胜队生存权重作用于全部参与者）。
//   - 全部平局：S=0.5，每队以本队平均分对对方全员平均分求期望胜率，
//     生存权重取本队自身，连胜/连败清零。
func (s *matchService) ReportVirtual(ctx context.Context, reporterUID int64, in VirtualReportInput) (*VirtualMatchResult, error) {
	if err := validateVirtualInput(in); err != nil {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest, err.Error())
	}

	now := time.Now().UTC()
	if in.StartedAt == "" {
		in.StartedAt = now.Format(time.RFC3339)
	}
	if in.FinishedAt == "" {
		in.FinishedAt = now.Format(time.RFC3339)
	}
	// 校验并以 time.Time 传递：MySQL TIMESTAMP 不接受 RFC3339 的 "Z"
	// 后缀，直接传原始字符串会以 1292 失败。
	startedAt, err := time.Parse(time.RFC3339, in.StartedAt)
	if err != nil {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest,
			fmt.Sprintf("started_at 必须为 RFC3339 格式: %v", err))
	}
	finishedAt, err := time.Parse(time.RFC3339, in.FinishedAt)
	if err != nil {
		return nil, apperr.New(apperr.CodeMCAInvalidRequest,
			fmt.Sprintf("finished_at 必须为 RFC3339 格式: %v", err))
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	// 1. 解析全部参与者身份并读取评分信息（与游戏上报同一信任边界：
	// uid 只来自 HRPAuth 解析结果，缺行时在事务内懒建行）。
	allParticipants := make(map[string]*userRatingInfo) // key: UUID
	for _, team := range in.Teams {
		for _, m := range team.Members {
			uid, err := s.resolveUID(ctx, m.UUID)
			if err != nil {
				switch {
				case errors.Is(err, auth.ErrUserNotFound):
					return nil, apperr.New(apperr.CodeMCAUserNotFound,
						fmt.Sprintf("参与者 (uuid: %s) 在 HRPAuth 中不存在", m.UUID))
				case errors.Is(err, auth.ErrServiceAuth):
					return nil, apperr.New(apperr.CodeOAuthHRPAuthDown,
						"HRPAuth 服务凭据不可用，无法解析参与者身份")
				default:
					return nil, apperr.New(apperr.CodeOAuthHRPAuthDown,
						fmt.Sprintf("解析参与者 (uuid: %s) 身份失败", m.UUID))
				}
			}
			info, err := s.getUserRatingInfo(ctx, tx, uid)
			if err != nil {
				return nil, err
			}
			info.UID = uid
			allParticipants[m.UUID] = info
		}
	}

	// 2. 定位获胜队（至多一支，已校验）。
	winnerIdx := -1
	for i := range in.Teams {
		if in.Teams[i].Result == ResultWin {
			winnerIdx = i
			break
		}
	}

	// 3. 记录对战主表（source=virtual；平局时 winner_team_id 为空、
	// survival_rate 记 0，表示无获胜队伍）。
	winnerTeamID := ""
	survivalRate := 0.0
	if winnerIdx >= 0 {
		w := &in.Teams[winnerIdx]
		winnerTeamID = w.TeamID
		survivalRate = float64(w.AliveCount) / float64(w.InitialCount)
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO matches (match_type, source, map_name, winner_team_id, survival_rate, duration_seconds, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		in.MatchType, MatchSourceVirtual, in.MapName, winnerTeamID, survivalRate,
		in.DurationSeconds, startedAt.UTC(), finishedAt.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("记录对战失败: %w", err)
	}
	matchID, _ := res.LastInsertId()

	// 4. 每队平均分。
	teamAvg := make([]float64, len(in.Teams))
	for i := range in.Teams {
		ratings := make([]float64, 0, len(in.Teams[i].Members))
		for _, m := range in.Teams[i].Members {
			ratings = append(ratings, float64(allParticipants[m.UUID].RankScore))
		}
		teamAvg[i] = rating.CalculateTeamAverageRating(ratings)
	}

	settlements := make([]ParticipantSettlement, 0, len(allParticipants))

	if winnerIdx >= 0 {
		// 5a. 有获胜队：与游戏上报路径 Report 完全一致的公式。
		loserRatings := make([]float64, 0)
		for i := range in.Teams {
			if i == winnerIdx {
				continue
			}
			for _, m := range in.Teams[i].Members {
				loserRatings = append(loserRatings, float64(allParticipants[m.UUID].RankScore))
			}
		}
		expectedW := rating.CalculateEloExpected(teamAvg[winnerIdx], rating.CalculateTeamAverageRating(loserRatings))
		expectedL := rating.CalculateEloExpected(rating.CalculateTeamAverageRating(loserRatings), teamAvg[winnerIdx])
		wRound := rating.GetSurvivalWeight(in.Teams[winnerIdx].AliveCount, in.Teams[winnerIdx].InitialCount)

		for i := range in.Teams {
			isWinner := i == winnerIdx
			for _, m := range in.Teams[i].Members {
				info := allParticipants[m.UUID]
				streak, err := s.getUserStreak(ctx, tx, info.UID)
				if err != nil {
					return nil, err
				}
				newStreak := streak
				if isWinner {
					if streak >= 0 {
						newStreak++
					} else {
						newStreak = 1
					}
				} else {
					if streak <= 0 {
						newStreak--
					} else {
						newStreak = -1
					}
				}

				k := rating.GetKFactor(info.WinsCount)
				sValue, eValue := 0.0, expectedL
				if isWinner {
					sValue, eValue = 1.0, expectedW
				}
				wStreak := rating.GetStreakWeight(newStreak)
				wPerf := rating.GetIndividualPerformanceTweak(m.PerfTweak)
				if wPerf == 0 {
					wPerf = 1.0
				}
				delta := rating.CalculateDelta(k, sValue, eValue, wRound, wStreak, wPerf)

				if err := s.applyVirtualDelta(ctx, tx, info.UID, isWinner, delta); err != nil {
					return nil, err
				}
				if err := s.insertVirtualParticipant(ctx, tx, matchID, info.UID, in.Teams[i].TeamID, isWinner, false, info.RankScore, delta, m); err != nil {
					return nil, err
				}
				if err := s.updateRanking(ctx, tx, info.UID, in.MatchType, 1); err != nil {
					return nil, err
				}
				settlements = append(settlements, ParticipantSettlement{
					UID: info.UID, UUID: m.UUID, TeamID: in.Teams[i].TeamID,
					Result: in.Teams[i].Result,
					RankScoreBefore: info.RankScore, RankScoreDelta: delta,
					RankScoreAfter: maxInt(0, info.RankScore+delta),
				})
			}
		}
	} else {
		// 5b. 全部平局：S=0.5，每队对对方全员平均分求期望，
		// 生存权重取本队自身，连胜/连败清零（wStreak=1.0），不计胜场。
		totalMembers := 0
		for i := range in.Teams {
			totalMembers += len(in.Teams[i].Members)
		}
		for i := range in.Teams {
			// 对方全员平均分 = (全体总分 - 本队总分) / 对方人数。
			oppCount := totalMembers - len(in.Teams[i].Members)
			oppSum := 0.0
			for j := range in.Teams {
				if j == i {
					continue
				}
				for _, m := range in.Teams[j].Members {
					oppSum += float64(allParticipants[m.UUID].RankScore)
				}
			}
			oppAvg := 0.0
			if oppCount > 0 {
				oppAvg = oppSum / float64(oppCount)
			}
			eValue := rating.CalculateEloExpected(teamAvg[i], oppAvg)
			wRound := rating.GetSurvivalWeight(in.Teams[i].AliveCount, in.Teams[i].InitialCount)

			for _, m := range in.Teams[i].Members {
				info := allParticipants[m.UUID]
				k := rating.GetKFactor(info.WinsCount)
				delta := rating.CalculateDelta(k, 0.5, eValue, wRound, rating.GetStreakWeight(0), 1.0)

				if err := s.applyVirtualDelta(ctx, tx, info.UID, false, delta); err != nil {
					return nil, err
				}
				if err := s.insertVirtualParticipant(ctx, tx, matchID, info.UID, in.Teams[i].TeamID, false, true, info.RankScore, delta, m); err != nil {
					return nil, err
				}
				if err := s.updateRanking(ctx, tx, info.UID, in.MatchType, 1); err != nil {
					return nil, err
				}
				settlements = append(settlements, ParticipantSettlement{
					UID: info.UID, UUID: m.UUID, TeamID: in.Teams[i].TeamID,
					Result: ResultDraw,
					RankScoreBefore: info.RankScore, RankScoreDelta: delta,
					RankScoreAfter: maxInt(0, info.RankScore+delta),
				})
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}

	return &VirtualMatchResult{
		Match: Match{
			ID:              matchID,
			MatchType:       in.MatchType,
			Source:          MatchSourceVirtual,
			MapName:         in.MapName,
			WinnerTeamID:    winnerTeamID,
			SurvivalRate:    survivalRate,
			DurationSeconds: in.DurationSeconds,
			StartedAt:       startedAt.UTC().Format(time.RFC3339),
			FinishedAt:      finishedAt.UTC().Format(time.RFC3339),
			CreatedAt:       time.Now().Format(time.RFC3339),
		},
		Participants: settlements,
	}, nil
}

// applyVirtualDelta 更新用户积分（与游戏上报路径相同的 GREATEST(0, ...) 下限），
// 获胜时同步累加 wins_count。
func (s *matchService) applyVirtualDelta(ctx context.Context, tx *sql.Tx, uid int64, isWinner bool, delta int) error {
	scoreUpdate := "rank_score = GREATEST(0, rank_score + ?)"
	winsUpdate := ""
	if isWinner {
		winsUpdate = ", wins_count = wins_count + 1"
	}
	_, err := tx.ExecContext(ctx,
		fmt.Sprintf("UPDATE users SET %s %s WHERE uid = ?", scoreUpdate, winsUpdate),
		delta, uid,
	)
	if err != nil {
		return fmt.Errorf("更新用户积分失败 (uid: %d): %w", uid, err)
	}
	return nil
}

// insertVirtualParticipant 写入虚拟对局参与者明细（显式写 is_draw）。
func (s *matchService) insertVirtualParticipant(ctx context.Context, tx *sql.Tx, matchID, uid int64, teamID string, isWinner, isDraw bool, scoreBefore, delta int, m ParticipantReport) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO match_participants (match_id, user_uid, team_id, is_winner, is_draw, rank_score_before, rank_score_delta, survival_time_seconds, kills, deaths)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		matchID, uid, teamID, isWinner, isDraw, scoreBefore, delta, m.SurvivalTime, m.Kills, m.Deaths,
	)
	if err != nil {
		return fmt.Errorf("记录参与者失败 (uid: %d): %w", uid, err)
	}
	return nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
