package main

import (
	"fmt"
	"log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/proto/cart"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/internal/bootstrap"
	"github.com/jcbowen/market/src/backend/services/cart/model"
)

/**
 * main cart-service 主入口
 * 购物车服务：购物车增删改查，基于 MySQL 实现
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

	// 连接商品服务，用于加购时补齐商品名称、价格、库存等展示字段
	productConn, err := grpc.NewClient(cfg.Grpc.Product,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("连接商品服务失败: %v", err)
	}
	defer productConn.Close()
	productClient := product.NewProductServiceClient(productConn)

	srv := grpc.NewServer()
	cart.RegisterCartServiceServer(srv, newServer(db, productClient))
	bootstrap.RegisterHealth(srv)

	if err := bootstrap.RunGRPCServer(srv, cfg.CartService.GrpcPort); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}

// autoMigrate 迁移购物车数据表
// carts 为已有表且带 uniqueIndex，禁用 AutoMigrate（gorm 驱动对含唯一索引的已有表
// 执行列变更时会误发 DROP FOREIGN KEY / DROP INDEX），统一走幂等 SQL 建表/补列
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: 迁移错误信息
func autoMigrate(db *mysql.Instance) error {
	return ensureCartTable(db)
}

// ensureCartTable 幂等创建购物车表并补充缺失列
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: 迁移错误信息
func ensureCartTable(db *mysql.Instance) error {
	if err := db.GetDb().Exec(`CREATE TABLE IF NOT EXISTS ` + "`carts`" + ` (
		` + "`id`" + ` bigint NOT NULL AUTO_INCREMENT,
		` + "`user_id`" + ` bigint NOT NULL,
		` + "`community_id`" + ` bigint NOT NULL,
		` + "`product_id`" + ` bigint NOT NULL,
		` + "`sku_id`" + ` bigint NOT NULL,
		` + "`quantity`" + ` int DEFAULT 1,
		` + "`selected`" + ` tinyint(1) DEFAULT 1,
		` + "`product_name`" + ` varchar(255) DEFAULT '',
		` + "`spec_name`" + ` varchar(255) DEFAULT '',
		` + "`product_image`" + ` varchar(500) DEFAULT '',
		` + "`created_at`" + ` datetime DEFAULT NULL,
		` + "`updated_at`" + ` datetime DEFAULT NULL,
		PRIMARY KEY (` + "`id`" + `),
		UNIQUE KEY ` + "`uk_user_sku`" + ` (` + "`user_id`" + `,` + "`sku_id`" + `),
		KEY ` + "`idx_user_community`" + ` (` + "`user_id`" + `,` + "`community_id`" + `)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;`).Error; err != nil {
		return err
	}

	updates := []struct {
		column string
		ddl    string
	}{
		{"product_name", "ALTER TABLE `carts` ADD COLUMN `product_name` varchar(255) DEFAULT '' COMMENT '商品名称快照'"},
		{"spec_name", "ALTER TABLE `carts` ADD COLUMN `spec_name` varchar(255) DEFAULT '' COMMENT '规格名快照'"},
		{"product_image", "ALTER TABLE `carts` ADD COLUMN `product_image` varchar(500) DEFAULT '' COMMENT '商品图片快照'"},
	}
	for _, u := range updates {
		if db.GetDb().Migrator().HasColumn(&model.Cart{}, u.column) {
			continue
		}
		if err := db.GetDb().Exec(u.ddl).Error; err != nil {
			return fmt.Errorf("补充 carts.%s 列失败: %w", u.column, err)
		}
	}
	return nil
}
