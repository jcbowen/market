package main

import (
	"context"
	"log"
	"strings"

	"google.golang.org/grpc"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/internal/bootstrap"
	"github.com/jcbowen/market/src/backend/services/product/model"
	"github.com/jcbowen/market/src/backend/services/product/service"
)

/**
 * main product-service 主入口
 * 商品服务：商品分类、商品管理、库存管理、保质期管理、营销
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
	backfillProductSummary(db)
	rds := bootstrap.InitRedis(cfg)

	srv := grpc.NewServer(
		// 图片经 gateway 转发（本地上传模式）时单条消息可达数 MB，默认 4MB 上限不够用
		grpc.MaxRecvMsgSize(10*1024*1024),
		grpc.MaxSendMsgSize(10*1024*1024),
	)
	product.RegisterProductServiceServer(srv, newServer(db, cfg))
	bootstrap.RegisterHealth(srv)

	// 优惠券过期流转调度器：user_coupons status 1未使用 → 3已过期（幂等条件更新，见 service/scheduler.go）
	schedulerCtx, schedulerCancel := context.WithCancel(context.Background())
	defer schedulerCancel()
	go service.NewCouponScheduler(db, rds.GetClient()).Start(schedulerCtx)

	// 定时上架调度器：products 预约商品（on_sale_at<=NOW()）到点自动上架（幂等条件更新，见 service/scheduler.go）
	go service.NewProductSaleScheduler(db, rds.GetClient()).Start(schedulerCtx)

	if err := bootstrap.RunGRPCServer(srv, cfg.ProductService.GrpcPort); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

// autoMigrate 自动迁移商品服务相关数据表
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: 迁移错误信息
func autoMigrate(db *mysql.Instance) error {
	if err := db.GetDb().AutoMigrate(
		&model.Category{},
		&model.Product{},
		&model.ProductImage{},
		&model.ProductSpec{},
		&model.ProductSpecValue{},
		&model.Sku{},
		&model.CommunityPricing{},
		&model.StockRecord{},
		&model.StockOpLog{},
		&model.StockBatch{},
		&model.Supplier{},
		&model.PurchaseOrder{},
		&model.PurchaseOrderItem{},
		&model.StockInOrder{},
		&model.StockInOrderItem{},
		&model.StockOutOrder{},
		&model.StockOutOrderItem{},
		&model.StockCheck{},
		&model.StockCheckItem{},
		&model.Coupon{},
		&model.UserCoupon{},
		&model.CouponProductScope{},
		&model.FlashSale{},
		&model.FlashSaleItem{},
		&model.FullReduction{},
		&model.FreeShippingActivity{},
		&model.FreeShippingScope{},
		&model.Banner{},
		&model.AttachmentConfig{},
		&model.Attachment{},
		&model.AttachmentMigrateItem{},
	); err != nil {
		return err
	}
	return ensureSkuBarcodeUniqueIndex(db)
}

// ensureSkuBarcodeUniqueIndex 创建 SKU 条码「同社区唯一、空串不参与」的功能唯一索引
// AutoMigrate 无法用 tag 表达函数式索引（把空条码归一化为 NULL 以放行多行未设置条码），
// 故在 AutoMigrate 后幂等补建；应用层同社区查重校验（spec.go）仍是第一道防线，
// 此索引把「查后写窗口内并发落同一条码」兜成库级拒绝（GetByBarcode 不再可能随机命中多行）
//
// 依赖 MySQL 8.0.13+ 功能索引（部署镜像为 MySQL 8.0.x，满足）
//
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: DDL 执行错误
func ensureSkuBarcodeUniqueIndex(db *mysql.Instance) error {
	// MySQL 8.0 不支持 CREATE INDEX IF NOT EXISTS（MariaDB 语法），先查 information_schema 再建；
	// 并发双实例同时建索引撞 1061（duplicate key name）也按幂等成功处理
	const indexName = "uk_skus_community_barcode"
	var count int64
	if err := db.GetDb().Raw(
		"SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'product_skus' AND index_name = ?",
		indexName).Scan(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	ddl := "CREATE UNIQUE INDEX " + indexName +
		" ON product_skus ((IF(barcode = '', NULL, CONCAT(community_id, ':', barcode))))"
	if err := db.GetDb().Exec(ddl).Error; err != nil {
		if strings.Contains(err.Error(), "Duplicate key name") || strings.Contains(err.Error(), "1061") {
			return nil
		}
		// 1062 = 存量数据已存在同社区重复条码（查后写窗口的历史产物）。
		// 不阻断启动：条码唯一第一道防线在应用层（spec.go 社区查重），
		// 库级索引待数据清洗或重建数据库后重启即自动补上
		if strings.Contains(err.Error(), "Duplicate entry") || strings.Contains(err.Error(), "1062") {
			log.Printf("警告: SKU 条码唯一索引创建失败（存量数据存在同社区重复条码），应用层查重继续兜底，数据清洗后重启自动补建: %v", err)
			return nil
		}
		return err
	}
	return nil
}

// backfillProductSummary 回填商品汇总字段的存量数据
// min_price/max_price/stock 列由规格保存流程回写维护；历史上线前存量的商品
// 这三列均为默认值 0，此处按启用 SKU 一次性回填（价格必须大于 0，故以
// min_price = 0 作为"未回写过"的判断条件，回填后不会重复执行）。
// 仅记录日志不中断启动：回填失败不影响服务运行，可重新启动重试。
//
// 参数：
//   - db: MySQL 实例
func backfillProductSummary(db *mysql.Instance) {
	const sql = `
UPDATE products p
JOIN (
    SELECT product_id, MIN(price) AS min_p, MAX(price) AS max_p, SUM(stock) AS total
    FROM product_skus
    WHERE status = 1
    GROUP BY product_id
) s ON s.product_id = p.id
SET p.min_price = s.min_p, p.max_price = s.max_p, p.stock = s.total
WHERE p.min_price = 0`

	result := db.GetDb().Exec(sql)
	if result.Error != nil {
		log.Printf("回填商品汇总字段失败: %v", result.Error)
		return
	}
	if result.RowsAffected > 0 {
		log.Printf("已回填 %d 个商品的汇总价格与库存", result.RowsAffected)
	}
}
