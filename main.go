package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"MatchCoreArena-Server/migrate"
)

func main() {
	// 加载迁移配置
	migrateCfg := migrate.LoadConfig()

	// 执行数据库迁移
	log.Println("初始化数据库迁移...")
	if err := migrate.RunMigrations(migrateCfg); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	// 初始化 Gin 路由
	r := gin.Default()

	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "MatchCoreArena Server",
		})
	})

	// 启动服务器
	r.Run(":9178")
}
