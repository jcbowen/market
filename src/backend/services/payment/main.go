package main

import (
	"log"

	"google.golang.org/grpc"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/internal/bootstrap"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/payment/model"
	"github.com/jcbowen/market/src/backend/services/payment/service"
)

/**
 * main payment-service 主入口
 * 支付服务：微信支付/支付宝支付对接、支付回调、退款
 */
func main() {
	cfg := bootstrap.LoadConfig()

	if err := grpcclient.InitOrderClient(cfg.Grpc.Order); err != nil {
		log.Fatalf("初始化 order-service 客户端失败: %v", err)
	}

	db, err := bootstrap.InitMySQL(cfg)
	if err != nil {
		log.Fatalf("初始化 MySQL 失败: %v", err)
	}
	if err := autoMigrate(db); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}
	_ = bootstrap.InitRedis(cfg)

	paymentService := service.NewPaymentService(db, cfg)

	srv := grpc.NewServer()
	payment.RegisterPaymentServiceServer(srv, newServer(paymentService))
	bootstrap.RegisterHealth(srv)

	if err := bootstrap.RunGRPCServer(srv, cfg.PaymentService.GrpcPort); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

// autoMigrate 自动迁移支付服务相关数据表
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: 迁移错误信息
func autoMigrate(db *mysql.Instance) error {
	return db.GetDb().AutoMigrate(
		&model.Payment{},
		&model.RefundPayment{},
		&model.PayConfig{},
	)
}
