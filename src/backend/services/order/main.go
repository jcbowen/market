package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
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

	// 启动订单定时任务调度器（超时取消 + 自动收货 + 卡单退款重试）
	schedulerCtx, schedulerCancel := context.WithCancel(context.Background())
	defer schedulerCancel()
	go service.NewScheduler(db, rds.GetClient(), svc.refundService).Start(
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
// 新表进 AutoMigrate；已有表（orders/order_items）加列走幂等 ensureColumns 显式 ALTER
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: 迁移错误信息
func autoMigrate(db *mysql.Instance) error {
	if err := db.GetDb().AutoMigrate(
		&model.Order{},
		&model.OrderItem{},
		&model.OrderStatusLog{},
		&model.Refund{},
		&model.RefundItem{},
		&model.RefundConfig{},
		&model.RefundLog{},
		&model.SchedulerFailure{},
		&model.PosHeldOrder{},
		&model.PosShift{},
		&model.AppPreference{},
	); err != nil {
		return err
	}
	return ensureOrderColumns(db)
}

// ensureOrderColumns 已有表幂等加列（缺列才 ALTER，避免 AutoMigrate 对已有表的列迁移误判）
// orders：manual_discount（收银台抹零/整单改价）、shift_id（收银班次归属）；
// order_items：original_price（改价痕迹单价快照）、price_modified（是否改价标记）
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: 迁移错误信息
func ensureOrderColumns(db *mysql.Instance) error {
	columns := []struct{ table, ddl string }{
		{"orders", "ALTER TABLE orders ADD COLUMN manual_discount DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '手动优惠总额（抹零/整单改价）'"},
		{"orders", "ALTER TABLE orders ADD COLUMN shift_id BIGINT NOT NULL DEFAULT 0 COMMENT '收银班次 ID 0=非收银单'"},
		{"order_items", "ALTER TABLE order_items ADD COLUMN original_price DECIMAL(10,2) NOT NULL DEFAULT 0.00 COMMENT '服务端实时单价快照（改价痕迹）'"},
		{"order_items", "ALTER TABLE order_items ADD COLUMN price_modified TINYINT NOT NULL DEFAULT 0 COMMENT '单价是否被手动修改 0否 1是'"},
	}
	d := db.GetDb()
	for _, col := range columns {
		var cnt int64
		if err := d.Raw("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?",
			col.table, columnName(col.ddl)).Scan(&cnt).Error; err != nil {
			return err
		}
		if cnt > 0 {
			continue
		}
		if err := d.Exec(col.ddl).Error; err != nil {
			return err
		}
	}

	// shift_id 索引幂等创建（列存在即建索引；MySQL 无 ADD INDEX IF NOT EXISTS）
	var idxCnt int64
	if err := d.Raw("SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'orders' AND index_name = 'idx_shift_id'").Scan(&idxCnt).Error; err != nil {
		return err
	}
	if idxCnt == 0 {
		if err := d.Exec("ALTER TABLE orders ADD INDEX idx_shift_id (shift_id)").Error; err != nil {
			return err
		}
	}
	return nil
}

// columnName 从 ALTER TABLE DDL 中提取列名（约定格式：... ADD COLUMN <name> <type> ...）
// 参数：
//   - ddl: ALTER TABLE 语句
//
// 返回值：
//   - string: 列名
func columnName(ddl string) string {
	const marker = "ADD COLUMN "
	idx := strings.Index(ddl, marker)
	if idx < 0 {
		return ""
	}
	rest := ddl[idx+len(marker):]
	if end := strings.IndexByte(rest, ' '); end > 0 {
		return rest[:end]
	}
	return rest
}
