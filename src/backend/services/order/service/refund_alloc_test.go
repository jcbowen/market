package service

import (
	"strings"
	"testing"

	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/services/order/model"
)

/**
 * 退款金额汇算纯计算逻辑单测（不依赖数据库）
 * 覆盖：优惠分摊口径（含收银台手动优惠）、部分数量按比例、退款行构造校验、
 *       比例收敛、尾差兜底、行合计——均为资金口径关键路径
 */

// newTestOrder 构造汇算用订单
// 参数：
//   - total/ discount/ manual: 订单总额/系统优惠/手动优惠
//
// 返回值：
//   - *model.Order
func newTestOrder(total, discount, manual float64) *model.Order {
	return &model.Order{
		ID:             1,
		TotalAmount:    total,
		DiscountAmount: discount,
		ManualDiscount: manual,
	}
}

// newTestItem 构造汇算用订单商品行
func newTestItem(id int64, name string, price float64, qty int32) *model.OrderItem {
	return &model.OrderItem{
		ID:          id,
		ProductName: name,
		Price:       price,
		Quantity:    qty,
		TotalPrice:  price * float64(qty),
	}
}

// TestItemFullRefundAmount 行全额应退 = 行小计 - 按行小计占比分摊的订单优惠
// 优惠口径 = 系统优惠 + 收银台手动优惠（POS 抹零/整单改价），合计须与实付一致
func TestItemFullRefundAmount(t *testing.T) {
	cases := []struct {
		name    string
		order   *model.Order
		item    *model.OrderItem
		want    float64
		wantErr bool // wantErr 仅用于提示该用例是边界校验
	}{
		{
			name:  "系统优惠按比例分摊",
			order: newTestOrder(100, 10, 0),
			item:  newTestItem(1, "A", 50, 1),
			want:  45, // 50 - 10*50/100
		},
		{
			name:  "手动优惠并入分摊口径（POS 抹零）",
			order: newTestOrder(100, 10, 5),
			item:  newTestItem(1, "A", 50, 1),
			want:  42.5, // 50 - 15*50/100
		},
		{
			name:  "多行各自分摊后合计等于实付",
			order: newTestOrder(100, 10, 0),
			item:  newTestItem(2, "B", 60, 1),
			want:  54.0, // 60 - 10*60/100
		},
		{
			name:  "优惠超过行小计时封顶为 0",
			order: newTestOrder(100, 200, 0),
			item:  newTestItem(1, "A", 50, 1),
			want:  0,
		},
		{
			name:  "订单总额为 0 退级为行小计",
			order: newTestOrder(0, 0, 0),
			item:  newTestItem(1, "A", 33.333, 1),
			want:  33.33,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := itemFullRefundAmount(tc.item, tc.order)
			if got != tc.want {
				t.Fatalf("itemFullRefundAmount() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestItemRefundAmount 部分数量退按行全额应退 × (qty/行数量) 比例计算
func TestItemRefundAmount(t *testing.T) {
	o := newTestOrder(100, 10, 5)
	item := newTestItem(1, "A", 10, 4) // 行小计 40，行全额应退 = 40 - 15*40/100 = 34
	// 2/4 = 17
	if got := itemRefundAmount(item, o, 2); got != 17 {
		t.Fatalf("itemRefundAmount(2/4) = %v, want 17", got)
	}
	// 1/4 = 8.5
	if got := itemRefundAmount(item, o, 1); got != 8.5 {
		t.Fatalf("itemRefundAmount(1/4) = %v, want 8.5", got)
	}
}

// TestBuildRefundLinesFull 整单退：跳过已退完的行；全部退完时报错
func TestBuildRefundLinesFull(t *testing.T) {
	o := newTestOrder(100, 10, 0)
	itemA := newTestItem(1, "A", 30, 2)
	itemB := newTestItem(2, "B", 70, 1)
	items := []*model.OrderItem{itemA, itemB}
	itemMap := map[int64]*model.OrderItem{1: itemA, 2: itemB}

	lines, isFull, err := buildRefundLines(o, items, itemMap, nil, map[int64]int32{1: 2})
	if err != nil {
		t.Fatalf("整单退（A 行已退完）不应报错: %v", err)
	}
	if !isFull {
		t.Fatal("reqItems 为空应识别为整单退")
	}
	if len(lines) != 1 || lines[0].item.ID != 2 {
		t.Fatalf("应只包含未退完的 B 行, got %+v", lines)
	}

	// 全部退完 → 业务报错
	_, _, err = buildRefundLines(o, items, itemMap, nil, map[int64]int32{1: 2, 2: 1})
	if err == nil || !strings.Contains(err.Error(), "已全部退款") {
		t.Fatalf("全部已退应报「订单商品已全部退款」，got %v", err)
	}
}

// TestBuildRefundLinesPartial 部分退：归属校验、去重、数量上限
func TestBuildRefundLinesPartial(t *testing.T) {
	o := newTestOrder(100, 10, 0)
	itemA := newTestItem(1, "A", 30, 2)
	items := []*model.OrderItem{itemA}
	itemMap := map[int64]*model.OrderItem{1: itemA}

	// 正常部分退 1 件
	lines, isFull, err := buildRefundLines(o, items, itemMap,
		[]*order.ApplyRefundItem{{OrderItemId: 1, Quantity: 1}}, nil)
	if err != nil || isFull {
		t.Fatalf("部分退 1 件应成功: isFull=%v err=%v", isFull, err)
	}
	if len(lines) != 1 || lines[0].qty != 1 {
		t.Fatalf("应构造 1 行数量 1, got %+v", lines)
	}

	// 不属于本订单的商品行
	if _, _, err = buildRefundLines(o, items, itemMap,
		[]*order.ApplyRefundItem{{OrderItemId: 99, Quantity: 1}}, nil); err == nil || !strings.Contains(err.Error(), "不属于该订单") {
		t.Fatalf("外部商品行应报「不属于该订单」，got %v", err)
	}

	// 重复提交同一行
	if _, _, err = buildRefundLines(o, items, itemMap,
		[]*order.ApplyRefundItem{{OrderItemId: 1, Quantity: 1}, {OrderItemId: 1, Quantity: 1}}, nil); err == nil || !strings.Contains(err.Error(), "重复提交") {
		t.Fatalf("重复行应报「重复提交」，got %v", err)
	}

	// 数量超剩余可退
	if _, _, err = buildRefundLines(o, items, itemMap,
		[]*order.ApplyRefundItem{{OrderItemId: 1, Quantity: 3}}, nil); err == nil || !strings.Contains(err.Error(), "可退数量不足") {
		t.Fatalf("超量应报「可退数量不足」，got %v", err)
	}

	// 已退 1 件后最多再退 1 件
	if _, _, err = buildRefundLines(o, items, itemMap,
		[]*order.ApplyRefundItem{{OrderItemId: 1, Quantity: 2}}, map[int64]int32{1: 1}); err == nil || !strings.Contains(err.Error(), "可退数量不足") {
		t.Fatalf("累计超量应报「可退数量不足」，got %v", err)
	}
}

// TestScaleLinesToTarget 部分退累计超剩余可退时按比例收敛，尾差归最后一行
func TestScaleLinesToTarget(t *testing.T) {
	lines := []*refundLine{
		{amount: 10}, {amount: 10}, {amount: 10},
	}
	scaleLinesToTarget(lines, 15)
	if got := sumRefundLines(lines); got != 15 {
		t.Fatalf("收敛后合计 = %v, want 15", got)
	}
	if lines[0].amount != 5 || lines[1].amount != 5 {
		t.Fatalf("前两行应各为 5, got %v %v", lines[0].amount, lines[1].amount)
	}
	if lines[2].amount != 5 {
		t.Fatalf("最后一行应兜底为 5, got %v", lines[2].amount)
	}

	// 目标出现非整除尾差：尾差全部由最后一行吸收
	lines2 := []*refundLine{{amount: 10}, {amount: 10}, {amount: 10}}
	scaleLinesToTarget(lines2, 15.01)
	if got := sumRefundLines(lines2); got != 15.01 {
		t.Fatalf("尾差收敛后合计 = %v, want 15.01", got)
	}
}

// TestFixLastLineTailDiff 整单退尾差：最后一行 = 目标 - 前面各行之和，负值封 0
func TestFixLastLineTailDiff(t *testing.T) {
	lines := []*refundLine{{amount: 3.33}, {amount: 3.33}, {amount: 0}}
	fixLastLineTailDiff(lines, 10)
	if lines[2].amount != 3.34 {
		t.Fatalf("尾差兜底 = %v, want 3.34", lines[2].amount)
	}

	// 目标小于前面各行之和：封 0，不允许出现负数退款行
	lines2 := []*refundLine{{amount: 5}, {amount: 5}}
	fixLastLineTailDiff(lines2, 1)
	if lines2[1].amount != 0 {
		t.Fatalf("负尾差应封 0, got %v", lines2[1].amount)
	}
}

// TestSumRefundLines 行合计四舍五入到分
func TestSumRefundLines(t *testing.T) {
	lines := []*refundLine{{amount: 0.1}, {amount: 0.2}}
	if got := sumRefundLines(lines); got != 0.3 {
		t.Fatalf("sumRefundLines = %v, want 0.3（浮点误差应被 round2 吸收）", got)
	}
}
