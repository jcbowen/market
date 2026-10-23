package service

// 本文件集中放支付服务的「启动迁移补偿」。
// GORM AutoMigrate 只做加法：既不会重命名也不会删除既有索引，所以索引命名方式变更
// （payment.model.Payment.PendingFlag 由 uniqueIndex:名字 改为列级 unique，本仓库约定）
// 之后，旧索引会长期滞留在同一列上：两套等价唯一索引既拖慢写入，
// 又让「按索引名定位冲突」的注释与线上排查全部失真，必须显式做一次性幂等清理。
// 调用点在 NewPaymentService（main.go 的 autoMigrate 之后紧接着构造服务），
// 与 product 服务 ensureSkuBarcodeUniqueIndex 的「AutoMigrate 后补 DDL」写法同构。

import (
	"log"
	"strings"

	"gorm.io/gorm"
)

const (
	// pendingFlagIndexName PendingFlag 列级 unique tag 生成的现行唯一索引名
	// （GORM 命名规则：uni_ + 表名 + _ + 列名）
	pendingFlagIndexName = "uni_payments_pending_flag"
	// legacyPendingFlagIndexName 历史写法 uniqueIndex:uk_payments_order_pending 留下的旧索引名
	legacyPendingFlagIndexName = "uk_payments_order_pending"
)

// EnsurePaymentPendingFlagIndex 清理 payments.pending_flag 上的历史唯一索引 uk_payments_order_pending
// 幂等且容错，可每次启动都执行：
//  1. 先确认现行索引 uni_payments_pending_flag 已存在，才动旧索引——
//     若 AutoMigrate 还没建出新索引就删旧的，列上会出现「没有任何唯一约束」的窗口，
//     同订单多笔待支付单的库级防线短暂失效；新索引不存在时直接跳过，留到下次启动再清；
//  2. 旧索引不存在（新库或已清理过）时零 DDL；
//  3. 删除时撞 1091（Can't DROP；索引已不在）按幂等成功处理，与并发双实例启动等价。
//
// 参数：
//   - db: 支付服务的 GORM 连接
//
// 返回值：
//   - error: information_schema 查询或 DDL 执行失败（调用方只记日志，不阻断启动）
//
// 使用示例：
//
//	if err := EnsurePaymentPendingFlagIndex(db.GetDb()); err != nil {
//		log.Printf("启动迁移补偿失败: %v", err)
//	}
func EnsurePaymentPendingFlagIndex(db *gorm.DB) error {
	exists, err := paymentIndexExists(db, pendingFlagIndexName)
	if err != nil {
		return err
	}
	if !exists {
		log.Printf("[payment-migrate] 现行唯一索引 %s 尚未建立，本轮跳过旧索引清理", pendingFlagIndexName)
		return nil
	}
	exists, err = paymentIndexExists(db, legacyPendingFlagIndexName)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := db.Exec("ALTER TABLE payments DROP INDEX " + legacyPendingFlagIndexName).Error; err != nil {
		// 1091 = 索引已不存在（并发启动的另一实例先删掉了），幂等成功
		if strings.Contains(err.Error(), "Can't DROP") || strings.Contains(err.Error(), "1091") {
			return nil
		}
		return err
	}
	log.Printf("[payment-migrate] 已清理历史唯一索引 %s（列级 unique tag 生成的 %s 接管同订单待支付唯一约束）",
		legacyPendingFlagIndexName, pendingFlagIndexName)
	return nil
}

// paymentIndexExists 判断 payments 表上是否已有指定名字的索引
// 走 information_schema.statistics 而非 SHOW INDEX：后者返回结果集列数随版本变化，
// 前者一条 COUNT 即可判定，且天然限定在当前库（table_schema = DATABASE()），
// 多环境共用实例时不会误判同名表
//
// 参数：
//   - db: GORM 连接
//   - indexName: 索引名
//
// 返回值：
//   - bool: 索引存在
//   - error: 查询失败
func paymentIndexExists(db *gorm.DB, indexName string) (bool, error) {
	var count int64
	err := db.Raw(`SELECT COUNT(*) FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND table_name = 'payments' AND index_name = ?`,
		indexName).Scan(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
