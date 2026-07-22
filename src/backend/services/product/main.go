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
	_ = bootstrap.InitRedis(cfg)

	srv := grpc.NewServer()
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
		&model.Sku{},
		&model.CommunityPricing{},
		&model.StockRecord{},
		&model.StockCheck{},
		&model.StockCheckItem{},
		&model.Coupon{},
		&model.UserCoupon{},
		&model.FlashSale{},
		&model.FlashSaleItem{},
		&model.FullReduction{},
		&model.Banner{},
	)
}
