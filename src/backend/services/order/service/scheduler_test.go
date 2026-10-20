package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * 订单调度器集成测试（直连本地开发库，env 守卫跳过）
 *
 * 运行方式（需本地 docker 栈已启动，MySQL/Redis 端口映射到宿主机）：
 *   TEST_MYSQL_PASSWORD=xxx TEST_REDIS_PASSWORD=yyy go test ./services/order/service/ -run TestScheduler -v
 *
 * 覆盖：
 *   - cancelTimeoutOrders  超时未支付订单自动取消（状态 1→6、原因/时间落库、状态日志、库存释放幂等键）
 *   - autoCompleteOrders   发货超期自动完成（状态 4→5、完成时间落库、状态日志）
 *   - recoverStuckOrders   处理中状态（100/101）超时兜底恢复原状态
 *   - cancelOrderTx × UpdateOrderStatus 取消与支付回调竞态（资金防线：二者只能成立其一）
 *
 * 说明：卡单退款自动重试依赖真实渠道退款失败场景，无法在本地环境构造，不在覆盖范围。
 * 测试数据统一使用 SCHEDTEST- 前缀订单号，结束即清理；
 * 与运行中 docker order-service 共用分布式锁，测试前先删除锁键并在失败时重试。
 */

// schedTestOrderNoPrefix 调度器测试订单号前缀（清理依据，勿与业务数据混淆）
const schedTestOrderNoPrefix = "SCHEDTEST-"

// stubProductClient 商品服务客户端桩：记录调用入参，不落真实库存/券/销量
// 嵌入接口后仅覆写被测链路用到的方法，避免实现全部方法
type stubProductClient struct {
	product.ProductServiceClient
	mu             sync.Mutex
	releases       []*product.ReleaseStockReq
	batchReleases  []*product.BatchReleaseStockReq
	couponReleases []*product.ReleaseCouponReq
	salesAdjusts   []*product.AdjustSalesCountReq
}

func (s *stubProductClient) ReleaseStock(ctx context.Context, in *product.ReleaseStockReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases = append(s.releases, in)
	return &common.CommonResponse{}, nil
}

// BatchReleaseStock 整单批量回补（取消链路收口后的实际调用形态）
func (s *stubProductClient) BatchReleaseStock(ctx context.Context, in *product.BatchReleaseStockReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batchReleases = append(s.batchReleases, in)
	return &common.CommonResponse{}, nil
}

// BatchReleaseFlashSaleStock 秒杀库存整单回补桩：与主库存一样仅记录
func (s *stubProductClient) BatchReleaseFlashSaleStock(ctx context.Context, in *product.BatchReleaseFlashSaleStockReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	return &common.CommonResponse{}, nil
}

func (s *stubProductClient) ReleaseCoupon(ctx context.Context, in *product.ReleaseCouponReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.couponReleases = append(s.couponReleases, in)
	return &common.CommonResponse{}, nil
}

func (s *stubProductClient) AdjustSalesCount(ctx context.Context, in *product.AdjustSalesCountReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.salesAdjusts = append(s.salesAdjusts, in)
	return &common.CommonResponse{}, nil
}

// releaseCalls 返回 ReleaseStock 调用快照（含批量调用展开为逐 SKU 视图，便于按订单断言）
func (s *stubProductClient) releaseCalls() []*product.ReleaseStockReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := append([]*product.ReleaseStockReq(nil), s.releases...)
	for _, b := range s.batchReleases {
		for _, it := range b.Items {
			calls = append(calls, &product.ReleaseStockReq{
				SkuId:    it.SkuId,
				Quantity: it.Quantity,
				OrderId:  b.OrderId,
				BizNo:    b.BizNo,
			})
		}
	}
	return calls
}

// salesAdjustCalls 返回 AdjustSalesCount 调用快照（支付成功累加销量的唯一入口）
func (s *stubProductClient) salesAdjustCalls() []*product.AdjustSalesCountReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*product.AdjustSalesCountReq(nil), s.salesAdjusts...)
}

// stubPaymentClient 支付服务客户端桩：按订单号维护「渠道已收款」标记
// 竞态用例用它在回调落地前置位已收款，模拟「渠道支付成功与超时取消同时到达」的真实窗口，
// 替代原先的 nil 客户端（nil 会让 cancelOrderTx 跳过取消前反查，等于没测这道防线）
type stubPaymentClient struct {
	payment.PaymentServiceClient
	mu     sync.Mutex
	paid   map[int64]bool
	closed []int64
}

func newStubPaymentClient() *stubPaymentClient {
	return &stubPaymentClient{paid: map[int64]bool{}}
}

// markPaid 标记订单在渠道侧已收款成功
func (s *stubPaymentClient) markPaid(orderID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paid[orderID] = true
}

func (s *stubPaymentClient) GetPayment(ctx context.Context, in *payment.GetPaymentReq, opts ...grpc.CallOption) (*payment.PaymentInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.paid[in.OrderId] {
		// 与真实服务口径一致：无成功支付单返回 NotFound，isOrderPaid 据此判定未收款
		return nil, status.Error(codes.NotFound, "无支付单")
	}
	return &payment.PaymentInfo{OrderId: in.OrderId, PayType: 1, Status: 2}, nil
}

func (s *stubPaymentClient) ClosePayments(ctx context.Context, in *payment.ClosePaymentsReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = append(s.closed, in.OrderId)
	return &common.CommonResponse{}, nil
}

// schedTestEnv 调度器测试环境句柄
type schedTestEnv struct {
	sched   *Scheduler
	orders  *OrderService
	db      *mysql.Instance
	redis   *redis.Client
	product *stubProductClient
	payment *stubPaymentClient
}

// newSchedulerTestEnv 构建调度器测试环境：真实 DB/Redis + 桩化的商品/支付客户端
// 全局客户端与测试数据通过 t.Cleanup 还原，用例无需逐个 defer
//
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *schedTestEnv: 被测调度器/订单服务与桩；TEST_MYSQL_PASSWORD 未设置时 skip
func newSchedulerTestEnv(t *testing.T) *schedTestEnv {
	t.Helper()
	if os.Getenv("TEST_MYSQL_PASSWORD") == "" {
		t.Skip("TEST_MYSQL_PASSWORD 未设置，跳过调度器集成测试（需本地 docker 栈）")
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
		t.Fatalf("连接测试 MySQL 失败: %v", err)
	}
	// 测试自建表：CI 的 MySQL service 容器是空库，本地库则与之幂等兼容
	// 表结构演进统一走 AutoMigrate（与 services/order/main.go 同一模型清单）
	if err := dbInst.GetDb().AutoMigrate(
		&model.Order{}, &model.OrderItem{}, &model.OrderStatusLog{},
		&model.Refund{}, &model.RefundItem{}, &model.SchedulerFailure{},
	); err != nil {
		t.Fatalf("测试库迁移失败: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     envOr("TEST_REDIS_ADDR", "127.0.0.1:6379"),
		Password: os.Getenv("TEST_REDIS_PASSWORD"),
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("连接测试 Redis 失败: %v", err)
	}

	productStub := &stubProductClient{}
	paymentStub := newStubPaymentClient()
	origProduct := grpcclient.ProductClient
	origPayment := grpcclient.PaymentClient
	origOrder := grpcclient.OrderClient
	grpcclient.ProductClient = productStub
	grpcclient.PaymentClient = paymentStub
	// 调度器补同步走 OrderClient 回写订单状态，测试内直接指向同一 OrderService，
	// 避免依赖运行中的 order-service（否则补同步必然失败）
	ordersSvc := NewOrderService(dbInst)
	grpcclient.OrderClient = &inProcessOrderClient{svc: ordersSvc}

	sched := NewScheduler(dbInst, rdb, nil)
	t.Cleanup(func() {
		grpcclient.ProductClient = origProduct
		grpcclient.PaymentClient = origPayment
		grpcclient.OrderClient = origOrder
		_ = rdb.Close()
		cleanupSchedTestData(t, dbInst)
	})
	return &schedTestEnv{
		sched: sched, orders: ordersSvc, db: dbInst, redis: rdb,
		product: productStub, payment: paymentStub,
	}
}

// inProcessOrderClient UpdateOrderStatus 的进程内代理：其余方法未被调度链路使用，保持 nil 嵌入
// 嵌入接口后仅覆写用到的方法，误调用其他方法会 panic 直接暴露测试缺陷
type inProcessOrderClient struct {
	order.OrderServiceClient
	svc *OrderService
}

func (c *inProcessOrderClient) UpdateOrderStatus(ctx context.Context, in *order.UpdateOrderStatusReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	if err := c.svc.UpdateOrderStatus(ctx, in); err != nil {
		return nil, err
	}
	return &common.CommonResponse{}, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// cleanupSchedTestData 删除本组测试产生的订单/明细/状态日志
func cleanupSchedTestData(t *testing.T, db *mysql.Instance) {
	t.Helper()
	ctx := context.Background()
	var ids []int64
	if err := db.GetDb().WithContext(ctx).Raw(
		"SELECT id FROM orders WHERE order_no LIKE ?", schedTestOrderNoPrefix+"%").Scan(&ids).Error; err != nil {
		t.Logf("清理调度器测试数据查询失败: %v", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	_ = db.GetDb().WithContext(ctx).Exec(
		"DELETE FROM order_status_logs WHERE order_id IN ("+placeholders+")", args...).Error
	_ = db.GetDb().WithContext(ctx).Exec(
		"DELETE FROM order_items WHERE order_id IN ("+placeholders+")", args...).Error
	_ = db.GetDb().WithContext(ctx).Exec(
		"DELETE FROM orders WHERE id IN ("+placeholders+")", args...).Error
}

// seedSchedOrder 直插一条调度器测试订单（raw SQL 绕过模型时间回调，便于回填历史时间）
// 返回订单 ID
func seedSchedOrder(t *testing.T, db *mysql.Instance, suffix string, status int32, createdAt, updatedAt string) int64 {
	t.Helper()
	ctx := context.Background()
	orderNo := fmt.Sprintf("%s%s-%d", schedTestOrderNoPrefix, suffix, time.Now().UnixNano())
	res := db.GetDb().WithContext(ctx).Exec(
		`INSERT INTO orders
			(order_no, community_id, user_id, status, total_amount, discount_amount, delivery_fee,
			 pay_amount, delivery_type, source, manual_discount, shift_id, delivery_user_id,
			 created_at, updated_at)
		 VALUES (?, 1, 99100001, ?, 10.00, 0, 0, 10.00, 2, 1, 0, 0, 0, ?, ?)`,
		orderNo, status, createdAt, updatedAt)
	if res.Error != nil {
		t.Fatalf("插入测试订单失败: %v", res.Error)
	}
	var id int64
	if err := db.GetDb().WithContext(ctx).Raw(
		"SELECT id FROM orders WHERE order_no = ?", orderNo).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("回查测试订单失败: %v", err)
	}
	return id
}

// seedSchedItem 直插一条测试订单商品行
func seedSchedItem(t *testing.T, db *mysql.Instance, orderID, skuID int64, qty int32) {
	t.Helper()
	if err := db.GetDb().WithContext(context.Background()).Exec(
		`INSERT INTO order_items
			(order_id, product_id, sku_id, product_name, price, quantity, total_price, created_at)
		 VALUES (?, 1, ?, '调度器测试商品', 10.00, ?, 10.00, NOW())`,
		orderID, skuID, qty).Error; err != nil {
		t.Fatalf("插入测试订单商品行失败: %v", err)
	}
}

// orderSchedStatus 回查订单当前状态
func orderSchedStatus(t *testing.T, db *mysql.Instance, orderID int64) (int32, string, string) {
	t.Helper()
	var status int32
	var reason, cancelTime string
	if err := db.GetDb().WithContext(context.Background()).Raw(
		"SELECT status, IFNULL(cancel_reason,''), IFNULL(cancel_time,'') FROM orders WHERE id = ?",
		orderID).Row().Scan(&status, &reason, &cancelTime); err != nil {
		t.Fatalf("回查订单状态失败: %v", err)
	}
	return status, reason, cancelTime
}

// schedTimeAgo 返回 now - d 的 "2006-01-02 15:04:05" 字符串
func schedTimeAgo(d time.Duration) string {
	return time.Now().Add(-d).Format("2006-01-02 15:04:05")
}

// runSchedOnce 防抖执行：先清掉分布式锁键（与运行中 docker 调度器共享 Redis），
// 再执行 run；条件 verify 未满足时重试（最多 5 次，间隔 1.5s）
func runSchedOnce(t *testing.T, rdb *redis.Client, lockKeys []string, run func(), verify func() bool, what string) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		for _, k := range lockKeys {
			_ = rdb.Del(ctx, k).Err()
		}
		run()
		if verify() {
			return
		}
		time.Sleep(1500 * time.Millisecond)
	}
	t.Fatalf("%s：重试 5 次后条件仍未满足", what)
}

// TestSchedulerCancelTimeoutOrders 超时未支付订单自动取消
func TestSchedulerCancelTimeoutOrders(t *testing.T) {
	env := newSchedulerTestEnv(t)
	ctx := context.Background()
	db := env.db

	orderID := seedSchedOrder(t, db, "cancel", 1, schedTimeAgo(2*time.Hour), schedTimeAgo(2*time.Hour))
	seedSchedItem(t, db, orderID, 44001, 1)

	runSchedOnce(t, env.redis,
		[]string{"scheduler:order:timeout:cancel:lock"},
		func() { env.sched.cancelTimeoutOrders(ctx, 3600) },
		func() bool {
			st, _, _ := orderSchedStatus(t, db, orderID)
			return st == 6
		},
		"超时取消")

	// 订单终态与取消信息落库
	st, reason, cancelTime := orderSchedStatus(t, db, orderID)
	if st != 6 {
		t.Fatalf("订单状态 = %d, want 6", st)
	}
	if reason != "超时未支付自动取消" {
		t.Fatalf("取消原因 = %q, want 超时未支付自动取消", reason)
	}
	if cancelTime == "" {
		t.Fatal("取消时间应落库")
	}

	// 状态日志：1→6，system 操作者
	var from, to int32
	var opType, remark string
	if err := db.GetDb().WithContext(ctx).Raw(
		"SELECT from_status, to_status, operator_type, remark FROM order_status_logs WHERE order_id = ? ORDER BY id DESC LIMIT 1",
		orderID).Row().Scan(&from, &to, &opType, &remark); err != nil {
		t.Fatalf("查询状态日志失败: %v", err)
	}
	if from != 1 || to != 6 || opType != "system" || remark != "超时未支付自动取消" {
		t.Fatalf("状态日志不符: from=%d to=%d op=%s remark=%s", from, to, opType, remark)
	}

	// 库存释放：走 gRPC 客户端，幂等业务号 = cancel:{orderID}
	calls := env.product.releaseCalls()
	if len(calls) == 0 {
		t.Fatal("应调用 ReleaseStock 释放库存")
	}
	found := false
	for _, c := range calls {
		if c.GetOrderId() == orderID && c.GetSkuId() == 44001 && c.GetQuantity() == 1 && c.GetBizNo() == fmt.Sprintf("cancel:%d", orderID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("ReleaseStock 入参不符（订单 %d）: %+v", orderID, calls)
	}
}

// TestSchedulerAutoCompleteOrders 发货超期订单自动完成
func TestSchedulerAutoCompleteOrders(t *testing.T) {
	env := newSchedulerTestEnv(t)
	ctx := context.Background()
	db := env.db

	orderID := seedSchedOrder(t, db, "complete", 4, schedTimeAgo(8*24*time.Hour), schedTimeAgo(8*24*time.Hour))
	seedSchedItem(t, db, orderID, 44002, 1)
	// 发货时间 8 天前（超过默认 7 天阈值），支付时间同步回填
	if err := db.GetDb().WithContext(ctx).Exec(
		"UPDATE orders SET deliver_time = ?, pay_time = ? WHERE id = ?",
		schedTimeAgo(8*24*time.Hour), schedTimeAgo(8*24*time.Hour+time.Minute), orderID).Error; err != nil {
		t.Fatalf("回填发货时间失败: %v", err)
	}

	runSchedOnce(t, env.redis,
		[]string{"scheduler:order:auto:complete:lock"},
		func() { env.sched.autoCompleteOrders(ctx, 7) },
		func() bool {
			st, _, _ := orderSchedStatus(t, db, orderID)
			return st == 5
		},
		"自动完成")

	st, _, _ := orderSchedStatus(t, db, orderID)
	if st != 5 {
		t.Fatalf("订单状态 = %d, want 5", st)
	}
	var completeTime string
	if err := db.GetDb().WithContext(ctx).Raw(
		"SELECT IFNULL(complete_time,'') FROM orders WHERE id = ?", orderID).Row().Scan(&completeTime); err != nil {
		t.Fatalf("回查完成时间失败: %v", err)
	}
	if completeTime == "" {
		t.Fatal("完成时间应落库")
	}
	var from, to int32
	var remark string
	if err := db.GetDb().WithContext(ctx).Raw(
		"SELECT from_status, to_status, remark FROM order_status_logs WHERE order_id = ? ORDER BY id DESC LIMIT 1",
		orderID).Row().Scan(&from, &to, &remark); err != nil {
		t.Fatalf("查询状态日志失败: %v", err)
	}
	if from != 4 || to != 5 || !strings.Contains(remark, "自动完成") {
		t.Fatalf("状态日志不符: from=%d to=%d remark=%s", from, to, remark)
	}
}

// TestSchedulerRecoverStuckOrders 处理中状态超时兜底恢复
func TestSchedulerRecoverStuckOrders(t *testing.T) {
	env := newSchedulerTestEnv(t)
	ctx := context.Background()
	db := env.db

	// status=100（超时取消处理中）停留 11 分钟（超过 10 分钟阈值）→ 恢复为 1
	orderID := seedSchedOrder(t, db, "stuck", 100, schedTimeAgo(12*time.Minute), schedTimeAgo(11*time.Minute))
	seedSchedItem(t, db, orderID, 44003, 1)

	runSchedOnce(t, env.redis, nil,
		func() { env.sched.recoverStuckOrders(ctx) },
		func() bool {
			st, _, _ := orderSchedStatus(t, db, orderID)
			return st == 1
		},
		"兜底恢复")

	if st, _, _ := orderSchedStatus(t, db, orderID); st != 1 {
		t.Fatalf("卡单订单应恢复为待支付(1), got %d", st)
	}
}

// claimForCancelTest 按调度器口径抢占一条待支付测试订单（1 → 100 处理中）并返回抢占结果
//
// 参数：
//   - t: 测试对象
//   - env: 测试环境
//   - orderID: 目标订单 ID
//
// 返回值：
//   - *model.Order: 抢占到的订单（处于取消处理中状态）
func claimForCancelTest(t *testing.T, env *schedTestEnv, orderID int64) *model.Order {
	t.Helper()
	claimed := env.sched.claimOrders(context.Background(), 1, processingStatusCancel,
		"created_at < ?", schedTimeAgo(time.Hour))
	for _, o := range claimed {
		if o.ID == orderID {
			return o
		}
	}
	t.Fatalf("订单 %d 未被抢占（抢占结果 %d 条）", orderID, len(claimed))
	return nil
}

// countReleases 统计指定订单的 ReleaseStock 调用次数（回补库存次数，资损口径必须恰为 0 或 1）
func countReleases(stub *stubProductClient, orderID int64) int {
	n := 0
	for _, c := range stub.releaseCalls() {
		if c.GetOrderId() == orderID {
			n++
		}
	}
	return n
}

// payCallback 模拟支付回调二阶段：先把渠道置为已收款，再把订单更新为已支付
//
// 参数：
//   - env: 测试环境
//   - orderID: 订单 ID
//
// 返回值：
//   - error: 状态流转被拒绝时返回错误（订单已被取消即属此类）
func payCallback(env *schedTestEnv, orderID int64) error {
	env.payment.markPaid(orderID)
	return env.orders.UpdateOrderStatus(context.Background(), &order.UpdateOrderStatusReq{
		Id:           orderID,
		Status:       2,
		OperatorType: "system",
		Remark:       "支付回调成功",
	})
}

// TestSchedulerCancelVsPayCallbackRace 超时取消与支付回调竞态（三道资金防线之调度器侧）
//
// 取消与回调可能同时到达同一无支付单订单，历史上该链路把 PaymentClient 置 nil，
// 等于跳过了「取消前反查支付单」这道防线。本用例改用支付桩逐段覆盖：
//   - 回调先到（订单已在抢占态置为已支付）：调度器反查命中已收款，跳过取消且绝不回补库存
//   - 取消先到：订单进入终态 6，回调再置已支付被状态机拒绝，不出现「已取消又已支付」
//   - 二者并发对撞：任一轮终态只能是 2 或 6，且「回补库存」与「累加销量」不会同时发生
func TestSchedulerCancelVsPayCallbackRace(t *testing.T) {
	env := newSchedulerTestEnv(t)
	ctx := context.Background()
	db := env.db

	t.Run("回调先到则跳过取消", func(t *testing.T) {
		orderID := seedSchedOrder(t, db, "race-paid-first", 1, schedTimeAgo(2*time.Hour), schedTimeAgo(2*time.Hour))
		seedSchedItem(t, db, orderID, 44001, 1)
		o := claimForCancelTest(t, env, orderID)

		// 渠道已收款但回调二阶段尚未同步订单（订单仍停留抢占态）——正是取消前反查要拦的场景
		env.payment.markPaid(orderID)
		if err := env.sched.cancelOrderTx(ctx, o, 1); !errors.Is(err, errOrderAlreadyPaid) {
			t.Fatalf("已收款订单取消应返回 errOrderAlreadyPaid, got %v", err)
		}
		if st, _, _ := orderSchedStatus(t, db, orderID); st != 2 {
			t.Fatalf("已收款订单应被补同步为已支付(2), got %d", st)
		}
		if n := countReleases(env.product, orderID); n != 0 {
			t.Fatalf("已收款订单不得回补库存，ReleaseStock 调用 %d 次", n)
		}
	})

	t.Run("取消先到则回调被拒", func(t *testing.T) {
		orderID := seedSchedOrder(t, db, "race-cancel-first", 1, schedTimeAgo(2*time.Hour), schedTimeAgo(2*time.Hour))
		seedSchedItem(t, db, orderID, 44001, 1)
		o := claimForCancelTest(t, env, orderID)

		if err := env.sched.cancelOrderTx(ctx, o, 1); err != nil {
			t.Fatalf("未收款订单取消失败: %v", err)
		}
		if st, _, _ := orderSchedStatus(t, db, orderID); st != 6 {
			t.Fatalf("订单应被取消(6), got %d", st)
		}
		if err := payCallback(env, orderID); err == nil {
			t.Fatal("已取消订单的支付回调应被状态机拒绝")
		}
		if st, _, _ := orderSchedStatus(t, db, orderID); st != 6 {
			t.Fatalf("迟到回调不得改写终态，订单状态 = %d, want 6", st)
		}
		if n := countReleases(env.product, orderID); n != 1 {
			t.Fatalf("取消应恰回补一次库存，ReleaseStock 调用 %d 次", n)
		}
	})

	t.Run("并发对撞终态唯一", func(t *testing.T) {
		const rounds = 8
		paidWins, cancelledWins := 0, 0
		for i := 0; i < rounds; i++ {
			round := i + 1
			orderID := seedSchedOrder(t, db, "race-concurrent", 1, schedTimeAgo(2*time.Hour), schedTimeAgo(2*time.Hour))
			seedSchedItem(t, db, orderID, 44001, 1)
			o := claimForCancelTest(t, env, orderID)

			// 奇偶交替让出不同起跑位：奇数轮回调先抢跑，偶数轮取消先通过支付反查
			// （仅回调先抢跑会让「取消胜出」分支永远跑不到，等于只测了一半竞态）
			cancelDelay, payDelay := time.Duration(0), time.Duration(0)
			if round%2 == 0 {
				payDelay = 30 * time.Millisecond
			} else {
				cancelDelay = 30 * time.Millisecond
			}

			adjustBefore := len(env.product.salesAdjustCalls())
			releaseBefore := len(env.product.releaseCalls())
			var cancelErr, payErr error
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				time.Sleep(cancelDelay)
				cancelErr = env.sched.cancelOrderTx(ctx, o, 1)
			}()
			go func() {
				defer wg.Done()
				time.Sleep(payDelay)
				payErr = payCallback(env, orderID)
			}()
			wg.Wait()

			st, _, _ := orderSchedStatus(t, db, orderID)
			if st != 2 && st != 6 {
				t.Fatalf("第 %d 轮（订单 %d）终态只能是已支付(2)或已取消(6), got %d；cancelErr=%v payErr=%v",
					round, orderID, st, cancelErr, payErr)
			}

			released := len(env.product.releaseCalls())-releaseBefore > 0
			adjusted := len(env.product.salesAdjustCalls())-adjustBefore > 0
			// 资损核心断言：回补库存与确认收款互斥，二者同真即「钱货两失」
			if released && adjusted {
				t.Fatalf("第 %d 轮（订单 %d）同时回补库存并确认收款，存在资损", round, orderID)
			}
			switch st {
			case 6:
				cancelledWins++
				if !released {
					t.Fatalf("第 %d 轮（订单 %d）已取消但未回补库存", round, orderID)
				}
				if adjusted {
					t.Fatalf("第 %d 轮（订单 %d）已取消却累加了销量", round, orderID)
				}
			case 2:
				paidWins++
				if released {
					t.Fatalf("第 %d 轮（订单 %d）已收款却回补了库存", round, orderID)
				}
				if !adjusted {
					t.Fatalf("第 %d 轮（订单 %d）已收款但未累加销量", round, orderID)
				}
			}
			// 状态日志与终态一致：取消/收款各只留一条流转记录，不出现重复写
			var logs int64
			if err := db.GetDb().WithContext(ctx).Model(&model.OrderStatusLog{}).
				Where("order_id = ? AND to_status = ?", orderID, st).Count(&logs).Error; err != nil {
				t.Fatalf("第 %d 轮查询状态日志失败: %v", round, err)
			}
			if logs != 1 {
				t.Fatalf("第 %d 轮（订单 %d）终态 %d 的状态日志应为 1 条, got %d", round, orderID, st, logs)
			}
		}
		// 两种胜出方都需被跑到，否则说明起跑位没真正交错、用例退化成串行验证
		if paidWins == 0 || cancelledWins == 0 {
			t.Fatalf("并发轮次未覆盖两种结果：已支付 %d 轮、已取消 %d 轮", paidWins, cancelledWins)
		}
		t.Logf("并发对撞 %d 轮：回调胜出 %d 轮，取消胜出 %d 轮", rounds, paidWins, cancelledWins)
	})
}
