package service

import (
	"context"
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

	"github.com/jcbowen/market/src/backend/common/proto/common"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/internal/grpcclient"
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
 *
 * 说明：卡单退款自动重试依赖真实渠道退款失败场景，无法在本地环境构造，不在覆盖范围。
 * 测试数据统一使用 SCHEDTEST- 前缀订单号，结束即清理；
 * 与运行中 docker order-service 共用分布式锁，测试前先删除锁键并在失败时重试。
 */

// schedTestOrderNoPrefix 调度器测试订单号前缀（清理依据，勿与业务数据混淆）
const schedTestOrderNoPrefix = "SCHEDTEST-"

// stubProductClient ReleaseStock 桩：记录调用入参，不落真实库存
// 嵌入接口后仅覆写 ReleaseStock，避免实现全部方法
type stubProductClient struct {
	product.ProductServiceClient
	mu    sync.Mutex
	calls []*product.ReleaseStockReq
}

func (s *stubProductClient) ReleaseStock(ctx context.Context, in *product.ReleaseStockReq, opts ...grpc.CallOption) (*common.CommonResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, in)
	return &common.CommonResponse{}, nil
}

func (s *stubProductClient) releaseCalls() []*product.ReleaseStockReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*product.ReleaseStockReq(nil), s.calls...)
}

// newSchedulerTestEnv 构建调度器测试环境：真实 DB/Redis + 桩化的库存客户端
// 返回清理函数（恢复全局客户端、删除测试数据）
func newSchedulerTestEnv(t *testing.T) (*Scheduler, *stubProductClient, func()) {
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

	rdb := redis.NewClient(&redis.Options{
		Addr:     envOr("TEST_REDIS_ADDR", "127.0.0.1:6379"),
		Password: os.Getenv("TEST_REDIS_PASSWORD"),
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("连接测试 Redis 失败: %v", err)
	}

	stub := &stubProductClient{}
	origProduct := grpcclient.ProductClient
	origPayment := grpcclient.PaymentClient
	grpcclient.ProductClient = stub
	grpcclient.PaymentClient = nil // closePendingPayments 对 nil 客户端静默跳过

	sched := NewScheduler(dbInst, rdb, nil)
	cleanup := func() {
		grpcclient.ProductClient = origProduct
		grpcclient.PaymentClient = origPayment
		_ = rdb.Close()
		cleanupSchedTestData(t, dbInst)
	}
	return sched, stub, cleanup
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
	sched, stub, cleanup := newSchedulerTestEnv(t)
	defer cleanup()
	ctx := context.Background()
	db := sched.db

	orderID := seedSchedOrder(t, db, "cancel", 1, schedTimeAgo(2*time.Hour), schedTimeAgo(2*time.Hour))
	seedSchedItem(t, db, orderID, 44001, 1)

	runSchedOnce(t, sched.redisClient,
		[]string{"scheduler:order:timeout:cancel:lock"},
		func() { sched.cancelTimeoutOrders(ctx, 3600) },
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
	calls := stub.releaseCalls()
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
	sched, _, cleanup := newSchedulerTestEnv(t)
	defer cleanup()
	ctx := context.Background()
	db := sched.db

	orderID := seedSchedOrder(t, db, "complete", 4, schedTimeAgo(8*24*time.Hour), schedTimeAgo(8*24*time.Hour))
	seedSchedItem(t, db, orderID, 44002, 1)
	// 发货时间 8 天前（超过默认 7 天阈值），支付时间同步回填
	if err := db.GetDb().WithContext(ctx).Exec(
		"UPDATE orders SET deliver_time = ?, pay_time = ? WHERE id = ?",
		schedTimeAgo(8*24*time.Hour), schedTimeAgo(8*24*time.Hour+time.Minute), orderID).Error; err != nil {
		t.Fatalf("回填发货时间失败: %v", err)
	}

	runSchedOnce(t, sched.redisClient,
		[]string{"scheduler:order:auto:complete:lock"},
		func() { sched.autoCompleteOrders(ctx, 7) },
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
	sched, _, cleanup := newSchedulerTestEnv(t)
	defer cleanup()
	ctx := context.Background()
	db := sched.db

	// status=100（超时取消处理中）停留 11 分钟（超过 10 分钟阈值）→ 恢复为 1
	orderID := seedSchedOrder(t, db, "stuck", 100, schedTimeAgo(12*time.Minute), schedTimeAgo(11*time.Minute))
	seedSchedItem(t, db, orderID, 44003, 1)

	runSchedOnce(t, sched.redisClient, nil,
		func() { sched.recoverStuckOrders(ctx) },
		func() bool {
			st, _, _ := orderSchedStatus(t, db, orderID)
			return st == 1
		},
		"兜底恢复")

	if st, _, _ := orderSchedStatus(t, db, orderID); st != 1 {
		t.Fatalf("卡单订单应恢复为待支付(1), got %d", st)
	}
}
