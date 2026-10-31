package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * 2026-10-31 提交 6e5941a 新增分支的回归测试（直连测试库，env 守卫跳过）
 *
 * 运行方式（TEST_MYSQL_PASSWORD 未设置时自动 skip）：
 *   TEST_MYSQL_PASSWORD=xxx TEST_REDIS_PASSWORD=yyy \
 *     go test ./services/order/service/ -run TestRegression -v
 *
 * 覆盖本轮修复引入的两条资金/库存口径：
 *   - P1-1 运费封顶：满减/券/手动优惠叠加后实付低于运费时，整单退金额不得超过实付
 *     （超过会被支付侧累计退款上限拒绝，退款单永久卡失败且库存/券不回补）；
 *   - P1-2 取消净额：部分退款成功后取消订单，回补数量按「明细数量 − 已退成功数量」计，
 *     已退完的行剔除、待审核退款不计入，避免跨链路重复回补。
 *
 * 数据沿用 SCHEDTEST- 订单号前缀，由调度器测试环境的 t.Cleanup 统一删除。
 */

// seedAmountOrder 直插一条可指定金额口径的测试订单
// 实付按下单侧公式写入（pay = total − 系统优惠 − 手动优惠 + 配送费），
// 用例显式给出各分量，避免依赖 seedSchedOrder 的固定 10 元口径
//
// 参数：
//   - t: 测试对象
//   - db: 测试库
//   - suffix: 订单号后缀（须保持 SCHEDTEST- 前缀，清理按前缀定位）
//   - status: 订单状态
//   - total/discount/manual/deliveryFee/payAmount: 应付/系统优惠/手动优惠/配送费/实付
//
// 返回值：
//   - int64: 订单 ID
func seedAmountOrder(t *testing.T, db *mysql.Instance, suffix string, status int32, total, discount, manual, deliveryFee, payAmount float64) int64 {
	t.Helper()
	ctx := context.Background()
	orderNo := fmt.Sprintf("%s%s-%d", schedTestOrderNoPrefix, suffix, time.Now().UnixNano())
	now := time.Now().Format("2006-01-02 15:04:05")
	if err := db.GetDb().WithContext(ctx).Exec(
		`INSERT INTO orders
			(order_no, community_id, user_id, status, total_amount, discount_amount, delivery_fee,
			 pay_amount, delivery_type, source, manual_discount, shift_id, delivery_user_id,
			 created_at, updated_at)
		 VALUES (?, 1, 99100001, ?, ?, ?, ?, ?, 2, 1, ?, 0, 0, ?, ?)`,
		orderNo, status, total, discount, deliveryFee, payAmount, manual, now, now).Error; err != nil {
		t.Fatalf("插入测试订单失败: %v", err)
	}
	var id int64
	if err := db.GetDb().WithContext(ctx).Raw(
		"SELECT id FROM orders WHERE order_no = ?", orderNo).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("回查测试订单失败: %v", err)
	}
	return id
}

// seedAmountItem 直插一条指定单价与数量的小计的商品行，返回商品行 ID
func seedAmountItem(t *testing.T, db *mysql.Instance, orderID, skuID int64, price float64, qty int32) int64 {
	t.Helper()
	ctx := context.Background()
	if err := db.GetDb().WithContext(ctx).Exec(
		`INSERT INTO order_items
			(order_id, product_id, sku_id, product_name, price, quantity, total_price, created_at)
		 VALUES (?, 1, ?, '金额口径测试商品', ?, ?, ?, NOW())`,
		orderID, skuID, price, qty, price*float64(qty)).Error; err != nil {
		t.Fatalf("插入测试商品行失败: %v", err)
	}
	var id int64
	if err := db.GetDb().WithContext(ctx).Raw(
		"SELECT id FROM order_items WHERE order_id = ? AND sku_id = ? ORDER BY id LIMIT 1",
		orderID, skuID).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("回查测试商品行失败: %v", err)
	}
	return id
}

// loadOrderForCalc 按 ID 读出订单（供汇算入参）
func loadOrderForCalc(t *testing.T, db *mysql.Instance, orderID int64) *model.Order {
	t.Helper()
	var o model.Order
	if err := db.GetDb().WithContext(context.Background()).First(&o, orderID).Error; err != nil {
		t.Fatalf("读取测试订单失败: %v", err)
	}
	return &o
}

// TestRegressionFullRefundFreightCappedByPayAmount 实付低于运费时整单退以实付封顶
// 场景：应付 100 + 运费 10、手动优惠 105 → 实付 5。
// 不封顶时运费按剩余可退运费 10 计入，退款金额算成 10 > 实付 5
func TestRegressionFullRefundFreightCappedByPayAmount(t *testing.T) {
	env := newRefundTestEnv(t)
	db := env.db.GetDb().WithContext(context.Background())

	oid := seedAmountOrder(t, env.db, "freight-cap", 5, 100, 0, 105, 10, 5)
	seedAmountItem(t, env.db, oid, 501, 100, 1)
	o := loadOrderForCalc(t, env.db, oid)

	calc, err := env.svc.calcRefundOrder(db, o, nil, 0, false, false)
	if err != nil {
		t.Fatalf("整单退汇算失败: %v", err)
	}
	if !calc.isFull {
		t.Fatal("reqItems 为空应判为整单退")
	}
	if calc.amount != 5 {
		t.Fatalf("退款金额 = %v, want 5（不得超过实付）", calc.amount)
	}
	if calc.freight != 5 {
		t.Fatalf("运费 = %v, want 5（剩余可退运费须被剩余可退总额封顶）", calc.freight)
	}
	if got := sumRefundLines(calc.lines); got != 0 {
		t.Fatalf("商品行合计 = %v, want 0（实付已全部计入运费）", got)
	}
	// 资金不变式：整单退金额不得超过实付
	if calc.amount > o.PayAmount {
		t.Fatalf("退款金额 %v 超过实付 %v", calc.amount, o.PayAmount)
	}
}

// TestRegressionFreightCapAfterPartialRefund 已退一笔运费后，剩余整单退仍按新剩余封顶
func TestRegressionFreightCapAfterPartialRefund(t *testing.T) {
	env := newRefundTestEnv(t)
	db := env.db.GetDb().WithContext(context.Background())

	oid := seedAmountOrder(t, env.db, "freight-cap-2", 7, 100, 0, 105, 10, 5)
	seedAmountItem(t, env.db, oid, 502, 100, 1)
	// 已成功退款 2 元（全部为运费）
	r := seedRefund(t, env.db, oid, 5, 2)
	if err := db.Model(&model.Refund{}).Where("id = ?", r.ID).
		Update("freight_amount", 2).Error; err != nil {
		t.Fatalf("回写已退运费失败: %v", err)
	}
	o := loadOrderForCalc(t, env.db, oid)

	calc, err := env.svc.calcRefundOrder(db, o, nil, 0, false, false)
	if err != nil {
		t.Fatalf("剩余整单退汇算失败: %v", err)
	}
	// 剩余可退 = 5 − 2 = 3；剩余可退运费 = 10 − 2 = 8 → 封顶为 3
	if calc.amount != 3 || calc.freight != 3 {
		t.Fatalf("amount/freight = %v/%v, want 3/3", calc.amount, calc.freight)
	}
	if got := sumRefundLines(calc.lines); got != 0 {
		t.Fatalf("商品行合计 = %v, want 0", got)
	}
}

// TestRegressionNotRefundedFreightKeepsGoodsAmount 实付高于运费时不触发封顶：商品行仍应拿到剩余商品金额
// 反向用例，防止封顶写成「运费优先、商品永远拿 0」
func TestRegressionNotRefundedFreightKeepsGoodsAmount(t *testing.T) {
	env := newRefundTestEnv(t)
	db := env.db.GetDb().WithContext(context.Background())

	// 应付 100 + 运费 10、无优惠 → 实付 110
	oid := seedAmountOrder(t, env.db, "freight-nocap", 5, 100, 0, 0, 10, 110)
	seedAmountItem(t, env.db, oid, 503, 100, 1)
	o := loadOrderForCalc(t, env.db, oid)

	calc, err := env.svc.calcRefundOrder(db, o, nil, 0, false, false)
	if err != nil {
		t.Fatalf("整单退汇算失败: %v", err)
	}
	if calc.freight != 10 {
		t.Fatalf("运费 = %v, want 10", calc.freight)
	}
	if got := sumRefundLines(calc.lines); got != 100 {
		t.Fatalf("商品行合计 = %v, want 100", got)
	}
	if calc.amount != 110 {
		t.Fatalf("退款金额 = %v, want 110（整单退退完实付）", calc.amount)
	}
}

// loadCancelItems 读出订单明细全量（取消回补入参）
func loadCancelItems(t *testing.T, db *mysql.Instance, orderID int64) []*model.OrderItem {
	t.Helper()
	var items []*model.OrderItem
	if err := db.GetDb().WithContext(context.Background()).Where("order_id = ?", orderID).
		Order("id").Find(&items).Error; err != nil {
		t.Fatalf("读取订单明细失败: %v", err)
	}
	return items
}

// netQtyBySku 把净额明细转成 sku_id -> 数量，便于逐行断言
func netQtyBySku(items []*model.OrderItem) map[int64]int32 {
	m := make(map[int64]int32, len(items))
	for _, it := range items {
		m[it.SkuID] = it.Quantity
	}
	return m
}

// TestRegressionNetCancelItemsAfterPartialRefund 部分退款成功后取消：回补按净额，已退完行剔除
func TestRegressionNetCancelItemsAfterPartialRefund(t *testing.T) {
	env := newRefundTestEnv(t)
	db := env.db.GetDb().WithContext(context.Background())

	// 三行：sku1 买 3 件、sku2 买 2 件、sku3 买 2 件
	oid := seedAmountOrder(t, env.db, "net-cancel", 2, 70, 0, 0, 0, 70)
	item1 := seedAmountItem(t, env.db, oid, 601, 30, 3)
	item2 := seedAmountItem(t, env.db, oid, 602, 20, 2)
	item3 := seedAmountItem(t, env.db, oid, 603, 20, 2)

	// 已退成功：sku1 退 2 件（剩 1 件）、sku3 退 2 件（退完）
	done := seedRefund(t, env.db, oid, 5, 30)
	seedRefundItem(t, env.db, done.ID, item1, 2)
	seedRefundItem(t, env.db, done.ID, item3, 2)
	// 待审核退款不应参与净额：sku2 挂 2 件在 status=1 的退款单上
	pending := seedRefund(t, env.db, oid, 1, 40)
	seedRefundItem(t, env.db, pending.ID, item2, 2)

	items := loadCancelItems(t, env.db, oid)
	if len(items) != 3 {
		t.Fatalf("订单明细应 3 行, got %d", len(items))
	}
	net, err := netCancelItems(db, oid, items)
	if err != nil {
		t.Fatalf("净额计算失败: %v", err)
	}

	got := netQtyBySku(net)
	if len(got) != 2 {
		t.Fatalf("应剩 2 行待回补（sku3 已退完剔除）, got %+v", got)
	}
	if got[601] != 1 {
		t.Fatalf("sku 601 净额 = %d, want 1（3 件已退 2 件）", got[601])
	}
	if got[602] != 2 {
		t.Fatalf("sku 602 净额 = %d, want 2（待审核退款不计入净额）", got[602])
	}
	if _, ok := got[603]; ok {
		t.Fatalf("sku 603 已退完，不应出现在回补明细: %+v", got)
	}
	// 入参明细不得被改写（浅拷贝行，取消链路后续仍按原明细展示）
	origin := netQtyBySku(loadCancelItems(t, env.db, oid))
	if origin[601] != 3 || origin[602] != 2 || origin[603] != 2 {
		t.Fatalf("原明细数量被改写: %+v", origin)
	}
}

// TestRegressionNetCancelItemsNoRefundAndFullyRefunded 无退款时全量回补；全部退完时空明细（product 侧 no-op）
func TestRegressionNetCancelItemsNoRefundAndFullyRefunded(t *testing.T) {
	env := newRefundTestEnv(t)
	db := env.db.GetDb().WithContext(context.Background())

	// 无退款订单
	plain := seedAmountOrder(t, env.db, "net-plain", 2, 50, 0, 0, 0, 50)
	seedAmountItem(t, env.db, plain, 701, 30, 3)
	seedAmountItem(t, env.db, plain, 702, 20, 1)
	net, err := netCancelItems(db, plain, loadCancelItems(t, env.db, plain))
	if err != nil {
		t.Fatalf("无退款订单净额计算失败: %v", err)
	}
	if got := netQtyBySku(net); len(got) != 2 || got[701] != 3 || got[702] != 1 {
		t.Fatalf("无退款应全量回补, got %+v", got)
	}

	// 全部退完订单：返回空切片而非 nil 长度异常，调用方据此走空明细 no-op
	full := seedAmountOrder(t, env.db, "net-full", 7, 50, 0, 0, 0, 50)
	item := seedAmountItem(t, env.db, full, 703, 50, 2)
	r := seedRefund(t, env.db, full, 5, 50)
	seedRefundItem(t, env.db, r.ID, item, 2)
	net, err = netCancelItems(db, full, loadCancelItems(t, env.db, full))
	if err != nil {
		t.Fatalf("全部退完订单净额计算失败: %v", err)
	}
	if len(net) != 0 {
		t.Fatalf("全部退完应返回空明细, got %+v", netQtyBySku(net))
	}
}
