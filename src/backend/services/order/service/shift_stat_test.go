package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/services/order/model"
	paymentmodel "github.com/jcbowen/market/src/backend/services/payment/model"
)

/**
 * 交班退款统计口径测试（直连本地开发库，env 守卫跳过）
 *
 * 运行方式（需本地 docker 栈已启动，MySQL 端口映射到宿主机）：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/order/service/ -run TestCalcStatisticsRefund -v
 *
 * 覆盖两个资金口径防线：
 *   - 退款按 refunds.payment_no 精确关联支付单：一单多笔成功支付单不得把退款金额按笛卡尔积翻倍
 *   - payment_no 为空的存量退款单不得被静默漏统计：按订单最近一笔成功支付单兜底归组
 *
 * 测试数据统一使用 SHIFTTEST- 前缀单据号，结束即清理，不与业务数据混淆。
 */

// shiftTestPrefix 交班统计测试单据号前缀（清理依据）
const shiftTestPrefix = "SHIFTTEST-"

// shiftTestOperatorID 测试操作员 ID（远超真实管理员 ID 区间，清理依据）
const shiftTestOperatorID = int64(99200001)

// newShiftStatEnv 构建交班统计测试环境：真实 DB（含支付表）+ 独立班次
// 返回值：
//   - *ShiftService: 被测交班服务
//   - int64: 新建进行中班次 ID
//   - func(): 清理函数（删除本组测试数据）
//
// 使用示例：
//
//	svc, shiftID, cleanup := newShiftStatEnv(t)
//	defer cleanup()
func newShiftStatEnv(t *testing.T) (*ShiftService, int64, func()) {
	t.Helper()
	if os.Getenv("TEST_MYSQL_PASSWORD") == "" {
		t.Skip("TEST_MYSQL_PASSWORD 未设置，跳过交班统计集成测试（需本地 docker 栈）")
	}
	dbCfg := jcbaseGo.DbStruct{
		Host:      envOr("TEST_MYSQL_HOST", "127.0.0.1"),
		Port:      envOr("TEST_MYSQL_PORT", "3306"),
		Dbname:    envOr("TEST_MYSQL_DBNAME", "market"),
		Username:  envOr("TEST_MYSQL_USER", "root"),
		Password:  os.Getenv("TEST_MYSQL_PASSWORD"),
		Charset:   "utf8mb4",
		ParseTime: "True",
		Loc:       "Local",
	}
	dbInst, err := mysql.New(dbCfg)
	if err != nil {
		t.Fatalf("连接测试数据库失败: %v", err)
	}
	// 退款统计跨服务读 payments 表：测试库需与 order 侧模型同时具备该表
	if err := dbInst.GetDb().AutoMigrate(&model.Order{}, &model.Refund{}, &model.PosShift{}, &paymentmodel.Payment{}); err != nil {
		t.Fatalf("迁移交班统计测试表失败: %v", err)
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	shift := &model.PosShift{
		CommunityID:  1,
		OperatorID:   shiftTestOperatorID,
		OperatorName: "交班统计测试",
		StartTime:    now,
		Status:       1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := dbInst.GetDb().Create(shift).Error; err != nil {
		t.Fatalf("创建测试班次失败: %v", err)
	}

	cleanup := func() {
		db := dbInst.GetDb()
		db.Where("refund_no LIKE ?", shiftTestPrefix+"%").Delete(&model.Refund{})
		db.Where("payment_no LIKE ?", shiftTestPrefix+"%").Delete(&paymentmodel.Payment{})
		db.Where("order_no LIKE ?", shiftTestPrefix+"%").Delete(&model.Order{})
		db.Where("operator_id = ?", shiftTestOperatorID).Delete(&model.PosShift{})
	}
	return NewShiftService(dbInst), shift.ID, cleanup
}

// seedShiftOrder 在指定班次下直插一条已完成订单，返回订单 ID
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - shiftID: 归属班次 ID
//   - suffix: 单据号后缀（拼 SHIFTTEST- 前缀）
func seedShiftOrder(t *testing.T, db *gorm.DB, shiftID int64, suffix string) int64 {
	t.Helper()
	orderNo := fmt.Sprintf("%sO-%s", shiftTestPrefix, suffix)
	res := db.Exec(
		`INSERT INTO orders
			(order_no, community_id, user_id, status, total_amount, discount_amount, delivery_fee,
			 pay_amount, delivery_type, source, shift_id, created_at, updated_at)
		 VALUES (?, 1, 99100001, 5, 30.00, 0, 0, 30.00, 2, 2, ?, NOW(), NOW())`,
		orderNo, shiftID)
	if res.Error != nil {
		t.Fatalf("插入测试订单失败: %v", res.Error)
	}
	var id int64
	if err := db.Raw("SELECT id FROM orders WHERE order_no = ?", orderNo).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("回查测试订单失败: %v", err)
	}
	return id
}

// seedShiftPayment 直插一条支付成功（status=2）的支付单
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - orderID: 订单 ID
//   - suffix: 支付单号后缀
//   - payType: 支付方式
//   - amount: 支付金额
func seedShiftPayment(t *testing.T, db *gorm.DB, orderID int64, suffix string, payType int32, amount float64) string {
	t.Helper()
	paymentNo := fmt.Sprintf("%sP-%s", shiftTestPrefix, suffix)
	res := db.Exec(
		`INSERT INTO payments
			(payment_no, order_id, user_id, community_id, pay_type, amount, status, created_at, updated_at)
		 VALUES (?, ?, 99100001, 1, ?, ?, 2, NOW(), NOW())`,
		paymentNo, orderID, payType, amount)
	if res.Error != nil {
		t.Fatalf("插入测试支付单失败: %v", res.Error)
	}
	return paymentNo
}

// seedShiftRefund 直插一条退款单
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - orderID: 订单 ID
//   - suffix: 退款单号后缀
//   - paymentNo: 关联支付单号（空串模拟早期未回填 payment_no 的存量单）
//   - amount: 退款金额
func seedShiftRefund(t *testing.T, db *gorm.DB, orderID int64, suffix, paymentNo string, amount float64) {
	t.Helper()
	res := db.Exec(
		`INSERT INTO refunds
			(refund_no, order_id, user_id, payment_no, amount, type, status, created_at, updated_at)
		 VALUES (?, ?, 99100001, ?, ?, 1, 5, NOW(), NOW())`,
		fmt.Sprintf("%sR-%s", shiftTestPrefix, suffix), orderID, paymentNo, amount)
	if res.Error != nil {
		t.Fatalf("插入测试退款单失败: %v", res.Error)
	}
}

// TestCalcStatisticsRefund 退款统计：精确关联支付单不翻倍 + 空 payment_no 不丢单
func TestCalcStatisticsRefund(t *testing.T) {
	svc, shiftID, cleanup := newShiftStatEnv(t)
	defer cleanup()
	db := svc.db.GetDb()

	// 订单 A：现金收款 30，另有第二笔成功支付单（重复支付场景），退款 10 挂在现金单上
	orderA := seedShiftOrder(t, db, shiftID, "A")
	cashPayA := seedShiftPayment(t, db, orderA, "A-CASH", 4, 30.00)
	seedShiftPayment(t, db, orderA, "A-DUP", 1, 30.00)
	seedShiftRefund(t, db, orderA, "A", cashPayA, 10.00)

	// 订单 B：微信收款 20，退款单 payment_no 为空（早期存量数据）
	orderB := seedShiftOrder(t, db, shiftID, "B")
	seedShiftPayment(t, db, orderB, "B-WX", 1, 20.00)
	seedShiftRefund(t, db, orderB, "B", "", 20.00)

	stat, err := svc.calcStatistics(context.Background(), shiftID)
	if err != nil {
		t.Fatalf("计算班次统计失败: %v", err)
	}

	// 退款合计 = 10 + 20：若按 order_id JOIN 会把 A 的 10 翻倍成 20，
	// 若用 INNER JOIN 会漏掉 B 的空 payment_no 单（合计只剩 10）
	if stat.TotalRefund != 30.00 {
		t.Fatalf("退款合计 = %.2f, want 30.00（分组明细 %+v）", stat.TotalRefund, refundItems(stat))
	}
	got := refundItems(stat)
	if got[4] != 10.00 {
		t.Errorf("现金退款 = %.2f, want 10.00", got[4])
	}
	// B 的退款按「该订单最近一笔成功支付单」兜底归入微信(1)，不因其自身无 payment_no 而消失
	if got[1] != 20.00 {
		t.Errorf("微信退款 = %.2f, want 20.00", got[1])
	}

	// 钱箱应交 = 现金收款 - 现金退款（微信收款与微信退款都不进钱箱）
	if stat.CashTotal != 20.00 {
		t.Errorf("钱箱应交 = %.2f, want 20.00（现金收 30 - 现金退 10）", stat.CashTotal)
	}
}

// refundItems 把退款分组明细转成 pay_type -> amount 映射，便于断言
// 参数：
//   - stat: 班次统计
//
// 返回值：
//   - map[int32]float64: 各支付方式的退款金额
func refundItems(stat *order.PosShiftStatistics) map[int32]float64 {
	m := make(map[int32]float64, len(stat.Refund))
	for _, item := range stat.Refund {
		m[item.PayType] += item.Amount
	}
	return m
}
