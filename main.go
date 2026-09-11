package main

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/api"
	"MatchCoreArena-Server/config"
	"MatchCoreArena-Server/internal/auth"
	"MatchCoreArena-Server/migrate"
)

func main() {
	// 1. 加载并校验应用配置（版本化、严格校验 + 自动迁移）
	appCfg, err := config.Load("")
	if err != nil {
		log.Fatalf("加载应用配置失败: %v", err)
	}
	log.Printf("应用配置加载完成，当前版本: %s", appCfg.Version)

	// 2. 使用应用配置构建数据库迁移配置
	migrateCfg := &migrate.Config{
		DBHost:         appCfg.Database.Host,
		DBPort:         strconv.Itoa(appCfg.Database.Port),
		DBUser:         appCfg.Database.User,
		DBPassword:     appCfg.Database.Password,
		DBName:         appCfg.Database.Name,
		MigrationsPath: appCfg.Migrate.Path,
	}

	// 3. 执行数据库迁移
	log.Println("初始化数据库迁移...")
	if err := migrate.RunMigrations(migrateCfg); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	// 4. 初始化数据库连接（供 service 层使用）
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=true",
		appCfg.Database.User, appCfg.Database.Password,
		appCfg.Database.Host, appCfg.Database.Port, appCfg.Database.Name)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(appCfg.Database.MaxOpenConns)
	db.SetMaxIdleConns(appCfg.Database.MaxIdleConns)
	if err := db.Ping(); err != nil {
		log.Fatalf("数据库 Ping 失败: %v", err)
	}
	log.Println("数据库连接成功")

	// 5. 初始化 token 校验器（骨架阶段使用 MockVerifier）
	var verifier auth.Verifier = &auth.MockVerifier{}
	log.Println("使用 MockVerifier（骨架阶段），请在实现期替换为 HRPAuthVerifier")

	// 6. 初始化 Gin 路由
	r := gin.Default()
	api.Register(r, db, verifier, appCfg.Version)

	// 7. 启动服务器
	addr := appCfg.Server.Host + ":" + strconv.Itoa(appCfg.Server.Port)
	log.Printf("HTTP 服务启动于 %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("HTTP 服务启动失败: %v", err)
	}
}
