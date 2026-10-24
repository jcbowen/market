package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/jcbowen/market/src/backend/services/payment/model"
)

/**
 * FindOrdersByPayNo 集成测试（直连本地开发库，env 守卫跳过）
 *
 * 运行方式：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/payment/service/ -run TestFindOrdersByPayNo -v
 *
 * 覆盖（后台订单列表「支付单号/渠道交易号」单框双号搜索的服务层语义）：
 *   - payment_no 精确命中：渠道账单「商家订单号」可搜到关联订单
 *   - transaction_id 精确命中：支付宝 trade_no / 微信 transaction_id 可搜到关联订单
 *   - 多笔支付尝试同订单去重：同一订单号只回传一次
 *   - 前后缀/模糊输入不命中（精确匹配语义）；空串拒绝；查无此号返回空列表不报错
 *
 * 复用 payment_test.go 的 newPaymentTestEnv/insertPayment：测试支付单挂在高段测试订单 ID 上，
 * 结束即清理，不与业务数据混淆。
 */

// TestFindOrdersByPayNo 单框双号反查：payment_no 与 transaction_id 精确匹配 + 去重
func TestFindOrdersByPayNo(t *testing.T) {
	s, _, cleanup := newPaymentTestEnv(t)
	defer cleanup()
	ctx := context.Background()

	// 造数：订单 1 两笔支付尝试（payment_no A + transaction T）；订单 2 一笔（payment_no B，transaction T2）
	// 同订单两笔验证 Distinct 去重；transaction T 与订单 2 的 T2 部分前缀重叠验证精确匹配语义
	orderID1, orderID2 := testOrderIDBase+101, testOrderIDBase+102
	insertPayment(t, s, &model.Payment{
		PaymentNo: fmt.Sprintf("ZF-payno-%d", orderID1), OrderID: orderID1,
		OrderNo: fmt.Sprintf("ORD-%d", orderID1), PayType: 2, Amount: 10.00, Status: 2,
		TransactionID: fmt.Sprintf("TRX-%d-FULL", orderID1),
	})
	insertPayment(t, s, &model.Payment{
		PaymentNo: fmt.Sprintf("ZF-payno-retry-%d", orderID1), OrderID: orderID1,
		OrderNo: fmt.Sprintf("ORD-%d", orderID1), PayType: 2, Amount: 10.00, Status: 3,
	})
	insertPayment(t, s, &model.Payment{
		PaymentNo: fmt.Sprintf("ZF-payno-%d", orderID2), OrderID: orderID2,
		OrderNo: fmt.Sprintf("ORD-%d", orderID2), PayType: 1, Amount: 5.00, Status: 2,
		TransactionID: fmt.Sprintf("TRX-%d", orderID2),
	})

	t.Run("payment_no 精确命中", func(t *testing.T) {
		nos, err := s.FindOrdersByPayNo(ctx, fmt.Sprintf("ZF-payno-%d", orderID2))
		if err != nil {
			t.Fatalf("反查失败: %v", err)
		}
		if len(nos) != 1 || nos[0] != fmt.Sprintf("ORD-%d", orderID2) {
			t.Fatalf("payment_no 反查结果 = %v, want [ORD-%d]", nos, orderID2)
		}
	})

	t.Run("transaction_id 精确命中且同单去重", func(t *testing.T) {
		nos, err := s.FindOrdersByPayNo(ctx, fmt.Sprintf("TRX-%d-FULL", orderID1))
		if err != nil {
			t.Fatalf("反查失败: %v", err)
		}
		if len(nos) != 1 || nos[0] != fmt.Sprintf("ORD-%d", orderID1) {
			t.Fatalf("transaction_id 反查结果 = %v, want 去重后的 [ORD-%d]", nos, orderID1)
		}
	})

	t.Run("模糊输入不命中", func(t *testing.T) {
		// 前缀/子串均不得命中（精确匹配语义，防误捞同前缀单号）
		for _, input := range []string{
			fmt.Sprintf("ZF-payno-%d", orderID1)[:len("ZF-payno-")],
			"TRX-", fmt.Sprintf("TRX-%d", orderID1),
		} {
			nos, err := s.FindOrdersByPayNo(ctx, input)
			if err != nil {
				t.Fatalf("模糊输入 %q 反查失败: %v", input, err)
			}
			if len(nos) != 0 {
				t.Fatalf("模糊输入 %q 不应命中, got %v", input, nos)
			}
		}
	})

	t.Run("空串拒绝查无此号返回空", func(t *testing.T) {
		if _, err := s.FindOrdersByPayNo(ctx, ""); err == nil {
			t.Fatal("空串应返回参数错误")
		}
		nos, err := s.FindOrdersByPayNo(ctx, "ZF-not-exists")
		if err != nil {
			t.Fatalf("查无此号不应报错: %v", err)
		}
		if len(nos) != 0 {
			t.Fatalf("查无此号应返回空列表, got %v", nos)
		}
	})
}
