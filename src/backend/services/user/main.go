package main

import (
	"log"

	"google.golang.org/grpc"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/proto/user"
	"github.com/jcbowen/market/src/backend/internal/bootstrap"
	"github.com/jcbowen/market/src/backend/services/user/model"
)

/**
 * main user-service 主入口
 * 用户服务：注册/登录、授权、地址管理、会员管理、社区管理
 */
func main() {
	cfg := bootstrap.LoadConfig()

	db, err := bootstrap.InitMySQL(cfg)
	if err != nil {
		log.Fatalf("初始化 MySQL 失败: %v", err)
	}
	if err := autoMigrate(db); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}
	_ = bootstrap.InitRedis(cfg)

	srv := grpc.NewServer()
	user.RegisterUserServiceServer(srv, newServer(db))
	bootstrap.RegisterHealth(srv)

	if err := bootstrap.RunGRPCServer(srv, cfg.UserService.GrpcPort); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

// autoMigrate 自动迁移用户服务相关数据表
// 参数：
//   - db: MySQL 实例
//
// 返回：
//   - error: 迁移错误信息
func autoMigrate(db *mysql.Instance) error {
	return db.GetDb().AutoMigrate(
		&model.User{},
		&model.Address{},
		&model.Community{},
		&model.Admin{},
		&model.AdminCommunity{},
		&model.Role{},
		&model.Region{},
		&model.RegionUpdateTask{},
	)
}
