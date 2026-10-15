package main

import (
	"log"

	"google.golang.org/grpc"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/internal/bootstrap"
	"github.com/jcbowen/market/src/backend/services/product/model"
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
	_ = bootstrap.InitRedis(cfg)

	srv := grpc.NewServer(
		// 图片经 gateway 转发（本地上传模式）时单条消息可达数 MB，默认 4MB 上限不够用
		grpc.MaxRecvMsgSize(10*1024*1024),
		grpc.MaxSendMsgSize(10*1024*1024),
	)
	product.RegisterProductServiceServer(srv, newServer(db, cfg))
	bootstrap.RegisterHealth(srv)

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
	return db.GetDb().AutoMigrate(
		&model.Category{},
		&model.Product{},
		&model.ProductImage{},
		&model.ProductSpec{},
		&model.ProductSpecValue{},
		&model.Sku{},
		&model.CommunityPricing{},
		&model.StockRecord{},
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
	)
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
