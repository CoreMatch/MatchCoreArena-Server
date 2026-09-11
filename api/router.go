package api

import (
	"database/sql"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api/handler"
	"MatchCoreArena-Server/api/middleware"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/internal/response"
	"MatchCoreArena-Server/internal/service"
)

// Register 装配所有 API 路由。
func Register(r *gin.Engine, db *sql.DB, oauthClient *auth.Client, ver auth.Verifier, version string) {
	// 全局中间件
	r.Use(middleware.RequestID())
	r.Use(middleware.Recovery())

	// 初始化 service 层
	userSvc := service.NewUserService(db)
	friendSvc := service.NewFriendService(db)
	teamSvc := service.NewTeamService(db)
	matchSvc := service.NewMatchService(db)
	rankingSvc := service.NewRankingService(db)

	// 初始化 handler 层
	authH := handler.NewAuthHandler(oauthClient)
	userH := handler.NewUserHandler(userSvc)
	friendH := handler.NewFriendHandler(friendSvc)
	teamH := handler.NewTeamHandler(teamSvc)
	matchH := handler.NewMatchHandler(matchSvc)
	rankingH := handler.NewRankingHandler(rankingSvc)

	// 公开端点
	r.GET("/api/status", func(c *gin.Context) {
		response.OK(c, "MatchCoreArena Server 运行中", gin.H{
			"version": version,
		})
	})

	// 认证端点（公开，无需鉴权）
	authGroup := r.Group("/api/auth")
	{
		authGroup.POST("/login-ticket", authH.LoginTicket)
		authGroup.POST("/totp-verify", authH.TotpVerify)
		authGroup.POST("/refresh", authH.Refresh)
	}

	// 需要鉴权的端点
	api := r.Group("/api")
	api.Use(middleware.Auth(ver))
	{
		// 认证（需鉴权：logout 需要 token 用于吊销）
		api.POST("/auth/logout", authH.Logout)

		// users
		api.GET("/users/me", userH.GetMe)
		api.GET("/users/:uid", userH.GetByUID)
		api.POST("/users/me/experience", userH.AddExperience)

		// friends
		api.GET("/friends", friendH.List)
		api.POST("/friends", friendH.Request)
		api.PUT("/friends/:id/accept", friendH.Accept)
		api.PUT("/friends/:id/reject", friendH.Reject)
		api.DELETE("/friends/:id", friendH.Delete)

		// teams
		api.POST("/teams", teamH.Create)
		api.GET("/teams/:id", teamH.GetByID)
		api.DELETE("/teams/:id", teamH.Delete)
		api.GET("/teams/:id/members", teamH.ListMembers)
		api.POST("/teams/:id/members", teamH.AddMember)
		api.PUT("/teams/:id/members/:uid/role", teamH.UpdateMemberRole)
		api.DELETE("/teams/:id/members/:uid", teamH.RemoveMember)

		// matches
		api.POST("/matches", matchH.Report)
		api.GET("/matches/:id", matchH.GetByID)
		api.GET("/matches/me", matchH.ListByMe)

		// rankings（公开，但挂在鉴权组内不影响功能）
		api.GET("/rankings/:type", rankingH.GetTop)
		api.GET("/rankings/me", rankingH.GetMyRank)
	}
}
