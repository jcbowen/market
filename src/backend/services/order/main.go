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
	svc := newServer(db, grpcclient.PaymentClient)
	order.RegisterOrderServiceServer(srv, svc)
	bootstrap.RegisterHealth(srv)

	// 初始化退款配置默认行（固定单行，始终退运费）
	if err := svc.refundService.EnsureDefaultRefundConfig(); err != nil {
		log.Fatalf("初始化退款配置失败: %v", err)
	}

	// 启动订单定时任务调度器（超时取消 + 自动收货）
	schedulerCtx, schedulerCancel := context.WithCancel(context.Background())
	defer schedulerCancel()
	go service.NewScheduler(db, rds.GetClient()).Start(
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

// autoMigrate 自动迁移订单服务相关数据表
// 仅迁移新增表，已有表（orders/refunds 等）的加列由各 ensureXxxColumns 函数显式处理，
// 避免 GORM AutoMigrate 对已有表的索引/外键变更产生误判（如对 refunds 的 uniqueIndex
// 误发 DROP FOREIGN KEY 导致启动失败）
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: 迁移错误信息
func autoMigrate(db *mysql.Instance) error {
	if err := db.GetDb().AutoMigrate(
		&model.SchedulerFailure{},
		&model.RefundItem{},
		&model.RefundConfig{},
	); err != nil {
		return err
	}
	return ensureRefundColumns(db)
}

// ensureRefundColumns 幂等补充 refunds 表新增列（freight_amount/type）
// 不走 AutoMigrate 的原因见 autoMigrate 注释
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: 执行错误信息
//
// 使用示例：
//   err := ensureRefundColumns(db)
func ensureRefundColumns(db *mysql.Instance) error {
	updates := []struct {
		column string
		ddl    string
	}{
		{"freight_amount", "ALTER TABLE `refunds` ADD COLUMN `freight_amount` decimal(10,2) DEFAULT 0 NOT NULL COMMENT '本单退还的运费金额'"},
		{"type", "ALTER TABLE `refunds` ADD COLUMN `type` tinyint DEFAULT 1 NOT NULL COMMENT '1整单退 2部分退'"},
	}
	for _, u := range updates {
		if db.GetDb().Migrator().HasColumn(&model.Refund{}, u.column) {
			continue
		}
		if err := db.GetDb().Exec(u.ddl).Error; err != nil {
			return fmt.Errorf("补充 refunds.%s 列失败: %w", u.column, err)
		}
	}
	return nil
}
