package service

import (
	"testing"

	"github.com/jcbowen/market/src/backend/common/proto/product"
)

/**
 * C 端公开详情脱敏口径测试（P1 越权与经营数据泄露）
 *
 * 背景：详情/规格出口此前按管理端口径全量出库，成本价与安全库存随 C 端
 * 公开详情一并返回；下架与软删除商品也照常带出价格与富文本正文，
 * 与「列表默认不露出、详情需社区语境」的可见性口径自相矛盾。
 * 修法是把脱敏策略收在归属层（本文件两个函数），出口只声明 public_view 意图：
 *   - cost_price/safety_stock 一律抹除：任何公开出口都不需要经营数据；
 *   - 价格/库存/正文按商品状态抹除：只有 status=1（上架）才允许展示交易信息；
 *   - sku_code 保留：收银台规格名用 spec_name || sku_code 兜底，抹掉会显示空白。
 */

// newPublicViewDetail 构造一份字段全为哨兵值的详情，便于逐项断言抹除口径
// 参数：
//   - status: 商品状态（1=上架，0=仓库中/下架，2=软删除）
//
// 返回值：
//   - *product.ProductDetail: 主商品 + 两个 SKU 的详情，字段值为可辨识的哨兵
func newPublicViewDetail(status int32) *product.ProductDetail {
	sentinelSku := func(id int64) *product.SkuInfo {
		return &product.SkuInfo{
			Id:            id,
			SkuCode:       "CODE-1",
			Barcode:       "1234567890",
			Price:         9900,
			OriginalPrice: 12800,
			CostPrice:     4200,
			Stock:         35,
			SafetyStock:   8,
		}
	}
	return &product.ProductDetail{
		Product: &product.ProductInfo{
			Id:            12,
			Status:        status,
			Price:         9900,
			MinPrice:      9900,
			MaxPrice:      15800,
			OriginalPrice: 12800,
			Stock:         35,
			SalesCount:    66,
			Content:       "<p>富文本正文</p>",
			Name:          "有机白菜",
			CoverImage:    "/uploads/cover.png",
			VirtualSales:  10,
			ShowSales:     true,
		},
		Skus: []*product.SkuInfo{sentinelSku(1), sentinelSku(2)},
	}
}

// assertBusinessFieldsCleared 断言经营字段（成本价/安全库存）在任意状态下都已抹除
// 参数：
//   - t: 测试上下文
//   - detail: 脱敏后的详情
func assertBusinessFieldsCleared(t *testing.T, detail *product.ProductDetail) {
	t.Helper()
	for _, sku := range detail.Skus {
		if sku.CostPrice != 0 {
			t.Errorf("SKU %d 成本价未抹除：%v", sku.Id, sku.CostPrice)
		}
		if sku.SafetyStock != 0 {
			t.Errorf("SKU %d 安全库存未抹除：%d", sku.Id, sku.SafetyStock)
		}
		if sku.SkuCode == "" {
			t.Errorf("SKU %d 编码被误抹除（收银台规格名兜底依赖它）", sku.Id)
		}
		if sku.Barcode == "" {
			t.Errorf("SKU %d 条码被误抹除（扫码加购依赖它）", sku.Id)
		}
	}
}

// TestRedactPublicProductDetailOnSale 上架商品：保留交易信息，只抹经营字段
func TestRedactPublicProductDetailOnSale(t *testing.T) {
	detail := newPublicViewDetail(ProductStatusOnSale)
	RedactPublicProductDetail(detail)

	assertBusinessFieldsCleared(t, detail)

	p := detail.Product
	if p.Price != 9900 || p.MinPrice != 9900 || p.MaxPrice != 15800 {
		t.Errorf("上架商品价格被误抹除：%+v", p)
	}
	if p.OriginalPrice != 12800 {
		t.Errorf("上架商品划线价被误抹除：%v", p.OriginalPrice)
	}
	if p.Stock != 35 {
		t.Errorf("上架商品库存被误抹除：%d", p.Stock)
	}
	if p.Content == "" {
		t.Error("上架商品富文本正文被误抹除（C 端详情页正文缺失）")
	}
	if p.Name != "有机白菜" || p.CoverImage == "" || p.SalesCount != 66 {
		t.Errorf("上架商品展示字段被误抹除：%+v", p)
	}
}

// TestRedactPublicProductDetailHideTrade 非上架（下架/软删除/定时上架）：价格、库存、正文全抹
func TestRedactPublicProductDetailHideTrade(t *testing.T) {
	for _, status := range []int32{0, 2, 3} {
		detail := newPublicViewDetail(status)
		RedactPublicProductDetail(detail)

		assertBusinessFieldsCleared(t, detail)

		p := detail.Product
		if p.Price != 0 || p.MinPrice != 0 || p.MaxPrice != 0 || p.OriginalPrice != 0 {
			t.Errorf("status=%d 价格未抹除：price=%v min=%v max=%v original=%v",
				status, p.Price, p.MinPrice, p.MaxPrice, p.OriginalPrice)
		}
		if p.Stock != 0 {
			t.Errorf("status=%d 库存未抹除：%d", status, p.Stock)
		}
		if p.Content != "" {
			t.Errorf("status=%d 富文本正文未抹除：%q", status, p.Content)
		}
		for _, sku := range detail.Skus {
			if sku.Price != 0 || sku.OriginalPrice != 0 || sku.Stock != 0 {
				t.Errorf("status=%d SKU %d 价格/库存未抹除：price=%v original=%v stock=%d",
					status, sku.Id, sku.Price, sku.OriginalPrice, sku.Stock)
			}
		}
		if p.Name == "" || p.CoverImage == "" {
			t.Errorf("status=%d 展示字段被过度抹除，公开页无法说明商品身份：%+v", status, p)
		}
	}
}

// TestRedactPublicProductDetailNilSafe 空入参不得 panic（出口拿到 nil 详情时仍可安全调用）
func TestRedactPublicProductDetailNilSafe(t *testing.T) {
	RedactPublicProductDetail(nil)
	RedactPublicProductDetail(&product.ProductDetail{})
	RedactPublicSku(nil, true)
}

// TestRedactPublicSkuStandalone SKU 单点脱敏：hideTrade=false 时只抹经营字段
func TestRedactPublicSkuStandalone(t *testing.T) {
	sku := &product.SkuInfo{SkuCode: "C1", Price: 9900, OriginalPrice: 12800, CostPrice: 4200, Stock: 35, SafetyStock: 8}
	RedactPublicSku(sku, false)
	if sku.Price != 9900 || sku.OriginalPrice != 12800 || sku.Stock != 35 {
		t.Errorf("hideTrade=false 误抹交易字段：%+v", sku)
	}
	if sku.CostPrice != 0 || sku.SafetyStock != 0 {
		t.Errorf("hideTrade=false 未抹经营字段：cost=%v safety=%d", sku.CostPrice, sku.SafetyStock)
	}

	sku = &product.SkuInfo{SkuCode: "C1", Price: 9900, OriginalPrice: 12800, CostPrice: 4200, Stock: 35, SafetyStock: 8}
	RedactPublicSku(sku, true)
	if sku.Price != 0 || sku.OriginalPrice != 0 || sku.Stock != 0 {
		t.Errorf("hideTrade=true 未抹交易字段：%+v", sku)
	}
	if sku.SkuCode != "C1" {
		t.Errorf("hideTrade=true 误抹 SKU 编码：%q", sku.SkuCode)
	}
}
