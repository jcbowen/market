package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"github.com/jcbowen/market/src/backend/common/debuglog"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/internal/bootstrap"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/order/model"
	"github.com/jcbowen/market/src/backend/services/order/service"
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

	if err := grpcclient.InitPaymentClient(cfg.Grpc.Payment); err != nil {
		log.Fatalf("初始化 payment-service 客户端失败: %v", err)
	}

	db, err := bootstrap.InitMySQL(cfg)
	if err != nil {
		log.Fatalf("初始化 MySQL 失败: %v", err)
	}
	if err := autoMigrate(db); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}
	rds := bootstrap.InitRedis(cfg)

	srv := grpc.NewServer()
	svc := newServer(db, grpcclient.PaymentClient, cfg)
	svc.orderService.WithRedis(rds.GetClient())
	svc.refundService.WithRedis(rds.GetClient())
	order.RegisterOrderServiceServer(srv, svc)
	bootstrap.RegisterHealth(srv)

	// 初始化退款配置默认行（固定单行，始终退运费）
	if err := svc.refundService.EnsureDefaultRefundConfig(); err != nil {
		log.Fatalf("初始化退款配置失败: %v", err)
	}

	// 启动订单定时任务调度器（超时取消 + 自动收货 + 卡单退款重试）
	schedulerCtx, schedulerCancel := context.WithCancel(context.Background())
	defer schedulerCancel()

	// debugger 进程级日志：与 gateway 共享 debug_logs 表（ServiceName 由 entrypoint
	// 按进程名覆写配置副本为 order-service），供调度器等异步进程记录摘要；
	// 连接池复用主链路的 db，不另开一份
	dbg, stopDebugger := bootstrap.RegisterProcessDebugger(cfg, db)
	defer stopDebugger()
	debuglog.SetInstance(dbg)

	go service.NewScheduler(db, rds.GetClient(), svc.refundService, svc.exportTaskService, cfg).Start(
		schedulerCtx,
		cfg.Business.OrderTimeout,
		cfg.Business.AutoCompleteDays,
	)

	// 监听退出信号，优雅关闭定时任务与 gRPC 服务
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("正在优雅关闭 order-service...")
		schedulerCancel()
		srv.GracefulStop()
	}()

	if err := runGRPCServer(srv, cfg.OrderService.GrpcPort); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

// runGRPCServer 启动 gRPC 服务并阻塞，直到服务停止
// 参数：
//   - server: gRPC 服务端实例
//   - port: 监听端口
//
// 返回值：
//   - error: 启动或关闭过程中的错误信息
func runGRPCServer(server *grpc.Server, port int) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("监听端口 %d 失败: %w", port, err)
	}

	log.Printf("gRPC 服务启动于 :%d", port)
	if err := server.Serve(lis); err != nil {
		return fmt.Errorf("gRPC 服务异常: %w", err)
	}
	return nil
}

// autoMigrate 自动迁移订单服务全部数据表
// tag 需与 DB 列定义严格对齐：单列唯一用 unique（uniqueIndex 会触发 gorm
// MigrateColumnUnique 误发 DROP FOREIGN KEY 导致 1091 启动失败，见需求文档）
// 全部表结构演进统一走 AutoMigrate，禁止手写 DDL；
// 存量库需手动清理历史遗留索引：DROP INDEX idx_shift_id ON orders
// （模型已用同名 index tag 对齐，清理后由 AutoMigrate 重建）
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
