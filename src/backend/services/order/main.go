package main

import (
	"log"

	"google.golang.org/grpc"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/internal/bootstrap"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * main order-service 主入口
 * 订单服务：订单创建、状态流转、退款
 */
func main() {
	cfg := bootstrap.LoadConfig()

	if err := grpcclient.InitProductClient(cfg.Grpc.Product); err != nil {
		log.Fatalf("初始化 product-service 客户端失败: %v", err)
	}

	if err := grpcclient.InitUserClient(cfg.Grpc.User); err != nil {
		log.Fatalf("初始化 user-service 客户端失败: %v", err)
	}

	db, err := bootstrap.InitMySQL(cfg)
	if err != nil {
		log.Fatalf("初始化 MySQL 失败: %v", err)
	}
	if err := autoMigrate(db); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}
	_ = bootstrap.InitRedis(cfg)

	srv := grpc.NewServer()
	order.RegisterOrderServiceServer(srv, newServer(db))
	bootstrap.RegisterHealth(srv)

	if err := bootstrap.RunGRPCServer(srv, cfg.OrderService.GrpcPort); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

// autoMigrate 自动迁移订单服务相关数据表
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: 迁移错误信息
func autoMigrate(db *mysql.Instance) error {
	return db.GetDb().AutoMigrate(
		&model.Order{},
		&model.OrderItem{},
		&model.OrderStatusLog{},
		&model.Refund{},
	)
}
