package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"github.com/jcbowen/market/src/backend/common/debuglog"
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
	rds := bootstrap.InitRedis(cfg)

	paymentService := service.NewPaymentService(db, cfg).WithRedis(rds.GetClient())

	// debugger 进程级日志：与 gateway 共享 debug_logs 表（ServiceName 由 entrypoint
	// 按进程名覆写配置副本为 payment-service），供对账调度器等异步进程记录摘要；
	// 连接池复用主链路的 db，不另开一份
	dbg, stopDebugger := bootstrap.RegisterProcessDebugger(cfg, db)
	defer stopDebugger()
	debuglog.SetInstance(dbg)

	// 支付对账调度器：回查待支付单（回调丢失捞回）+ 挂账检出 + 流水清理
	schedulerCtx, schedulerCancel := context.WithCancel(context.Background())
	defer schedulerCancel()
	go service.NewScheduler(paymentService, rds.GetClient()).Start(schedulerCtx)

	srv := grpc.NewServer()
	payment.RegisterPaymentServiceServer(srv, newServer(paymentService))
	bootstrap.RegisterHealth(srv)

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("正在优雅关闭 payment-service...")
		schedulerCancel()
		srv.GracefulStop()
	}()

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
//
// 表清单来自 model.Models()（与 market-admin init 的等待面同源，新增模型只改那一处）
func autoMigrate(db *mysql.Instance) error {
	return db.GetDb().AutoMigrate(model.Models()...)
}
