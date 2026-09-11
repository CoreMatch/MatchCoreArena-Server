package main

import (
	"log"
	"strconv"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/config"
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

	// 4. 初始化 Gin 路由
	r := gin.Default()
	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "MatchCoreArena Server",
			"config":  appCfg.Version,
		})
	})

	// 5. 启动服务器
	addr := appCfg.Server.Host + ":" + strconv.Itoa(appCfg.Server.Port)
	if err := r.Run(addr); err != nil {
		log.Fatalf("HTTP 服务启动失败: %v", err)
	}
}
