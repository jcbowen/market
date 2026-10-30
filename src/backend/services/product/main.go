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

	// 时钟口径体检：定时上架按 MySQL NOW() 判定，外部库时区不一致会静默整体偏移（只告警不阻断）
	if err := service.WarnIfDBClockZoneMismatch(db.GetDb()); err != nil {
		log.Printf("[WARN] 数据库时钟体检失败: %v", err)
	}

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
// 表清单来自 model.Models()（与 market-admin init 的等待面同源，新增模型只改那一处）
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: 迁移错误信息
func autoMigrate(db *mysql.Instance) error {
	if err := db.GetDb().AutoMigrate(model.Models()...); err != nil {
		return err
	}
	// 存量数据对齐须在建索引之前：历史「商品软删但 SKU 仍启用」的行会占住条码，
	// 让新口径索引因 1062 建不起来
	disableSkusOfDeletedProducts(db)
	if err := ensureSkuBarcodeUniqueIndex(db); err != nil {
		return err
	}
	// AutoMigrate 只建不删，改名后的旧规格社区索引须在此清理
	return dropLegacySpecCommunityIndex(db)
}

// disableSkusOfDeletedProducts 一次性对齐存量数据：把已软删商品（products.status=2）下
// 仍启用的 SKU 置为禁用，与 DeleteProduct 的连带禁用保持同一口径
// 条码「存活」的唯一判定是 SKU status=1（应用层 spec.go ensureSkuBarcodeAvailable 排除已删商品的 SKU、
// 库级函数索引同样只看 status=1），历史行不对齐会让删除过的商品条码既被索引占住又可被应用层复用，
// 表现为商家复用旧条码时插入撞 1062。幂等条件更新，重复执行无副作用；失败只记日志不中断启动
//
// 参数：
//   - db: MySQL 实例
func disableSkusOfDeletedProducts(db *mysql.Instance) {
	const sql = `UPDATE product_skus s
JOIN products p ON p.id = s.product_id
SET s.status = 0
WHERE p.status = 2 AND s.status = 1`

	result := db.GetDb().Exec(sql)
	if result.Error != nil {
		log.Printf("对齐已删商品的 SKU 状态失败: %v", result.Error)
		return
	}
	if result.RowsAffected > 0 {
		log.Printf("已禁用 %d 条归属已删除商品的 SKU（释放其条码占用）", result.RowsAffected)
	}
}

// SKU 条码库级唯一索引名：
//   - skuBarcodeIndexName 现口径，仅「启用 SKU + 非空条码」参与唯一，
//     与应用层查重（spec.go ensureSkuBarcodeAvailable）同构；
//   - skuBarcodeIndexLegacy 旧口径（不含 status 条件），存在时按新表达式重建后删除。
//
// 换名而非原地改表达式的原因：MySQL 不支持 CREATE/DROP INDEX IF NOT EXISTS，
// 表达式差异也无法从 information_schema 稳定识别，靠索引名区分新旧最直白、可幂等重跑
const (
	skuBarcodeIndexName   = "uk_skus_community_barcode_alive"
	skuBarcodeIndexLegacy = "uk_skus_community_barcode"
)

// ensureSkuBarcodeUniqueIndex 创建 SKU 条码「同社区唯一、仅启用行参与」的功能唯一索引
// AutoMigrate 无法用 tag 表达函数式索引（把未设置条码与禁用 SKU 归一化为 NULL 以放行），
// 故在 AutoMigrate 后幂等补建；应用层同社区查重（spec.go）仍是第一道防线，
// 此索引把「查后写窗口内并发落同一条码」兜成库级拒绝（GetByBarcode 不再可能随机命中多行）。
//
// 表达式必须与应用层存活口径一致：旧表达式不含 status 条件，导致「商品软删后条码自动释放可复用」
// 的承诺在库级不成立（复用旧条码插入即 1062，商家编辑被卡死）。
// 新表达式以「status = 1 且 barcode 非空」为存活条件，让禁用/软删行归一化为 NULL，
// 多个 NULL 不参与唯一比较，因此被禁用的 SKU 不再占住条码。
//
// 存量库生效方式：启动时先按新表达式建 skuBarcodeIndexName，建成后再 DROP 旧的
// skuBarcodeIndexLegacy（顺序保证任一时刻至少有一个索引在保护，中途失败也不会裸奔）；
// 全程幂等，无需人工先删旧索引。
//
// 依赖 MySQL 8.0.13+ 功能索引（部署镜像为 MySQL 8.0.x，满足；函数索引键部分须双层括号）
//
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: DDL 执行错误
func ensureSkuBarcodeUniqueIndex(db *mysql.Instance) error {
	exists, err := tableIndexExists(db, "product_skus", skuBarcodeIndexName)
	if err != nil {
		return err
	}
	if !exists {
		ddl := "CREATE UNIQUE INDEX " + skuBarcodeIndexName +
			" ON product_skus ((IF(status = 1 AND barcode <> '', CONCAT(community_id, ':', barcode), NULL)))"
		if err := db.GetDb().Exec(ddl).Error; err != nil {
			// 1061 = 并发双实例同时建（MariaDB 的 IF NOT EXISTS 在 MySQL 8 不可用，只能按错误码认幂等）
			if strings.Contains(err.Error(), "Duplicate key name") || strings.Contains(err.Error(), "1061") {
				return dropLegacySkuBarcodeIndex(db)
			}
			// 1064 = 当前 MySQL 不支持函数式索引（需 8.0.13+，如 5.7 测试环境）。
			// 不阻断启动：条码唯一第一道防线在应用层（spec.go 社区查重），
			// 库级兜底索引仅在 8.0.13+ 实例上生效（同下方 1062 的非阻断哲学）
			if strings.Contains(err.Error(), "1064") {
				log.Printf("警告: SKU 条码唯一索引 %s 创建失败（当前 MySQL 版本不支持函数式索引，需 8.0.13+），"+
					"跳过库级兜底，应用层同社区查重继续生效: %v", skuBarcodeIndexName, err)
				return nil
			}
			// 1062 = 存量「启用行」里已有同社区重复条码（查后写窗口的历史产物）。
			// 不阻断启动：条码唯一第一道防线在应用层（spec.go 社区查重），
			// 库级索引待按下方指引清洗后重启自动补建
			if strings.Contains(err.Error(), "Duplicate entry") || strings.Contains(err.Error(), "1062") {
				log.Printf("警告: SKU 条码唯一索引 %s 创建失败（存量启用行存在同社区重复条码），应用层查重继续兜底。"+
					"排查重复条码（保留 id 最小的一条，其余置 status=0 或清 barcode 后重启即自动补建）: "+
					"SELECT community_id, barcode, COUNT(*) AS dup_cnt, GROUP_CONCAT(id ORDER BY id) AS sku_ids "+
					"FROM product_skus WHERE status = 1 AND barcode <> '' "+
					"GROUP BY community_id, barcode HAVING dup_cnt > 1; 原始错误: %v", skuBarcodeIndexName, err)
				return nil
			}
			return err
		}
	}
	return dropLegacySkuBarcodeIndex(db)
}

// tableIndexExists 判断指定表的某个索引是否已存在
// MySQL 8.0 不支持 CREATE/DROP INDEX IF NOT EXISTS，DDL 前一律先查 information_schema
//
// 参数：
//   - db: MySQL 实例
//   - tableName: 表名（本包内部的固定字面量，非外部输入）
//   - indexName: 索引名
//
// 返回值：
//   - bool: 索引是否存在
//   - error: 查询错误
func tableIndexExists(db *mysql.Instance, tableName, indexName string) (bool, error) {
	var count int64
	err := db.GetDb().Raw(
		"SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?",
		tableName, indexName).Scan(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// dropLegacySkuBarcodeIndex 删除旧口径（不含 status 条件）的条码唯一索引
// 只在新索引已建成后调用；不存在时直接返回，重复启动幂等
//
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: DDL 执行错误
func dropLegacySkuBarcodeIndex(db *mysql.Instance) error {
	exists, err := tableIndexExists(db, "product_skus", skuBarcodeIndexLegacy)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := db.GetDb().Exec("DROP INDEX " + skuBarcodeIndexLegacy + " ON product_skus").Error; err != nil {
		log.Printf("警告: 旧 SKU 条码唯一索引 %s 删除失败（新索引 %s 已生效，可手工清理）: %v",
			skuBarcodeIndexLegacy, skuBarcodeIndexName, err)
		return nil
	}
	log.Printf("已删除旧 SKU 条码唯一索引 %s（口径改为仅启用行参与，由 %s 接管）",
		skuBarcodeIndexLegacy, skuBarcodeIndexName)
	return nil
}

// product_specs 社区索引名：
//   - specCommunityIndexName 现口径，单列索引，命名与兄弟索引 idx_product_specs_product_id 对齐；
//   - specCommunityIndexLegacy 旧名，模型 tag 只写了 priority:1（从未有第二列），
//     实际一直是单列索引却顶着复合索引的名字。
//
// 复合口径本就不必存在：product_specs 的读路径只有 product_id 过滤，无 community_id+product_id 联合查询，
// 而 InnoDB 二级索引叶子自带主键，(community_id, product_id) 相对 (community_id) 无增量收益；
// 保留单列索引是为社区维度兜底查询与数据隔离规范（见 05-数据库设计/01-设计规范）。
// AutoMigrate 不删除已不再声明的索引，故改名后须在此显式清理旧名。
const (
	specCommunityIndexName   = "idx_product_specs_community_id"
	specCommunityIndexLegacy = "idx_community_product"
)

// dropLegacySpecCommunityIndex 删除 product_specs 上顶着复合名、实为单列的旧索引
// AutoMigrate 已按新 tag 建好 specCommunityIndexName，本函数只负责清理孤儿索引；
// 不存在时直接返回，重复启动幂等
//
// 参数：
//   - db: MySQL 实例
//
// 返回值：
//   - error: 查询 information_schema 的错误（DDL 失败只告警，不阻塞启动）
func dropLegacySpecCommunityIndex(db *mysql.Instance) error {
	exists, err := tableIndexExists(db, "product_specs", specCommunityIndexLegacy)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := db.GetDb().Exec("DROP INDEX " + specCommunityIndexLegacy + " ON product_specs").Error; err != nil {
		log.Printf("警告: 旧规格社区索引 %s 删除失败（新索引 %s 已生效，可手工清理）: %v",
			specCommunityIndexLegacy, specCommunityIndexName, err)
		return nil
	}
	log.Printf("已删除旧规格社区索引 %s（名为复合实为单列，由 %s 接管）",
		specCommunityIndexLegacy, specCommunityIndexName)
	return nil
}

// backfillProductSummary 校正商品汇总列（min_price/max_price/stock）
// 汇总列由 syncProductAggregates 维护：规格保存与扣减/回补/入库/出库/盘点都在
// 同事务内回写。本函数是启动期兜底对账，把改动前遗留的存量漂移（旧版本只在保存
// 规格时回写，交易链路不更新）一次校正掉，只写实际不一致的行，可重复执行。
// 划线价 min_original_price 随价格配置变更、仅由规格保存链路回写，不参与本对账。
// 仅记录日志不中断启动：校正失败不影响服务运行，可重新启动重试。
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
WHERE p.min_price <> s.min_p OR p.max_price <> s.max_p OR p.stock <> s.total`

	result := db.GetDb().Exec(sql)
	if result.Error != nil {
		log.Printf("校正商品汇总字段失败: %v", result.Error)
		return
	}
	if result.RowsAffected > 0 {
		log.Printf("已校正 %d 个商品的汇总价格与库存", result.RowsAffected)
	}
}
