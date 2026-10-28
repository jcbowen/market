package main

import (
	stdctx "context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/attachmenturl"
	"github.com/jcbowen/market/src/backend/common/proto/cart"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/cart/model"
)

/**
 * 购物车交易口径测试（P0-4 社区隔离 + P1-4 可售收口 + 规格失效收口）
 *
 * 背景：加购与购物车展示是商品档案进入交易的另一入口，口径必须与下单侧（商品服务
 * BatchGetSkus/BatchGetProducts）一致，否则出现「加得进、结算被拦」或反之的资损/体验问题。
 * 三条口径：
 *   - 加购社区必须等于 SKU 归属社区（未带社区时回落 SKU 归属）；
 *   - SKU 必须启用（status=1）：后台禁用/移除规格/商品软删连带禁用后不可加购，
 *     展示侧同口径报「规格已失效」，购物车存量行不再可结算；
 *   - 商品必须可售（status=1）：由商品服务统一判定，此处只声明 require_sellable=true。
 *
 * 覆盖（纯内存替身，不连库：命中拦截分支时不会触达 s.db）：
 *   - TestCartAddItemTradeGuards      AddItem 的越权/规格失效/商品不可售拦截与入参口径
 *   - TestCartItemInvalidReason       cartItemInvalidReason 的失效原因判定顺序
 *   - TestBuildCartItemsBatchPrefetch 展示装配整批预取（N 行只发 1 次 SKU 批量 + 每社区 1 次商品批量）
 *   - TestBuildCartItemsSnapshotFallback 批量结果缺失项回退快照并标失效（不阻断整批）
 */

// cartTestCommunityID 用例中的成交社区 ID
const cartTestCommunityID int64 = 99100098

// cartTestOtherCommunityID 用例中的他人社区 ID
const cartTestOtherCommunityID int64 = 99100097

// fakeProductClient 商品服务客户端替身：仅实现用例涉及的 GetSkuById / GetProduct /
// BatchGetSkus / BatchGetProducts，其余方法由内嵌接口提供（未赋值，被调用即 panic，
// 用于暴露意外的调用路径）
type fakeProductClient struct {
	product.ProductServiceClient
	sku       *product.SkuInfo
	skuErr    error
	detail    *product.ProductDetail
	detailErr error
	gotReqs   []*product.GetProductReq // 记录 GetProduct 入参，断言交易意图（require_sellable/community_id）

	// 批量出口替身：skuList/productList 为预置返回，gotBatch* 记录入参用于断言调用次数
	skuList          []*product.SkuInfo
	skuListErr       error
	gotBatchSkuReqs  []*product.BatchGetSkusReq
	productList      []*product.ProductInfo
	productListErr   error
	gotBatchProdReqs []*product.BatchGetProductsReq
}

// GetSkuById 返回预置的 SKU 信息与错误
//
// 参数：
//   - _ stdctx.Context: 上下文（替身不使用）
//   - in: *product.GetSkuByIdReq: 查询请求（替身不回显入参）
//   - _ ...grpc.CallOption: 调用选项（替身不使用）
//
// 返回值：
//   - *product.SkuInfo: 预置 SKU 信息
//   - error: 预置错误（模拟商品服务返回的 gRPC 错误码）
func (f *fakeProductClient) GetSkuById(_ stdctx.Context, _ *product.GetSkuByIdReq, _ ...grpc.CallOption) (*product.SkuInfo, error) {
	return f.sku, f.skuErr
}

// GetProduct 返回预置的商品详情并记录入参
//
// 参数：
//   - _ stdctx.Context: 上下文（替身不使用）
//   - in: *product.GetProductReq: 查询请求（记入 gotReqs 供断言）
//   - _ ...grpc.CallOption: 调用选项（替身不使用）
//
// 返回值：
//   - *product.ProductDetail: 预置商品详情
//   - error: 预置错误（模拟商品服务 require_sellable 拦截等业务码）
func (f *fakeProductClient) GetProduct(_ stdctx.Context, in *product.GetProductReq, _ ...grpc.CallOption) (*product.ProductDetail, error) {
	f.gotReqs = append(f.gotReqs, in)
	return f.detail, f.detailErr
}

// BatchGetSkus 返回预置的 SKU 列表并记录入参（购物车展示侧批量预取）
//
// 参数：
//   - _ stdctx.Context: 上下文（替身不使用）
//   - in: *product.BatchGetSkusReq: 批量查询请求（记入 gotBatchSkuReqs 供断言）
//   - _ ...grpc.CallOption: 调用选项（替身不使用）
//
// 返回值：
//   - *product.BatchGetSkusResp: 预置 SKU 列表
//   - error: 预置错误
func (f *fakeProductClient) BatchGetSkus(_ stdctx.Context, in *product.BatchGetSkusReq, _ ...grpc.CallOption) (*product.BatchGetSkusResp, error) {
	f.gotBatchSkuReqs = append(f.gotBatchSkuReqs, in)
	if f.skuListErr != nil {
		return nil, f.skuListErr
	}
	return &product.BatchGetSkusResp{List: f.skuList}, nil
}

// BatchGetProducts 返回预置的商品列表并记录入参（购物车展示侧批量预取）
//
// 参数：
//   - _ stdctx.Context: 上下文（替身不使用）
//   - in: *product.BatchGetProductsReq: 批量查询请求（记入 gotBatchProdReqs 供断言）
//   - _ ...grpc.CallOption: 调用选项（替身不使用）
//
// 返回值：
//   - *product.BatchGetProductsResp: 预置商品列表
//   - error: 预置错误
func (f *fakeProductClient) BatchGetProducts(_ stdctx.Context, in *product.BatchGetProductsReq, _ ...grpc.CallOption) (*product.BatchGetProductsResp, error) {
	f.gotBatchProdReqs = append(f.gotBatchProdReqs, in)
	if f.productListErr != nil {
		return nil, f.productListErr
	}
	return &product.BatchGetProductsResp{List: f.productList}, nil
}

// newCartTestServer 构造只挂替身客户端的购物车服务实例
// 参数：
//   - f: 商品服务客户端替身
//
// 返回值：
//   - *server: 被测服务（db 为空，仅用于验证拦截分支）
func newCartTestServer(f *fakeProductClient) *server {
	return &server{productClient: f}
}

// newCartTestServerWithLoader 构造带附件读取器的被测实例（展示装配用）
// attachmenturl.NewLoader(nil) 不持 DB 句柄，取配置只回缓存（恒为 nil），
// URL 拼接退化为本地默认口径，不会在单测里连库
// 参数：
//   - f: 商品服务客户端替身
//
// 返回值：
//   - *server: 被测服务
func newCartTestServerWithLoader(f *fakeProductClient) *server {
	return &server{productClient: f, attachmentLoader: attachmenturl.NewLoader(nil)}
}

// testSku 构造启用 SKU 信息
// 参数：
//   - communityID: SKU 归属社区 ID
//   - status: SKU 状态（1 启用 / 0 禁用）
//
// 返回值：
//   - *product.SkuInfo: SKU 信息
func testSku(communityID int64, status int32) *product.SkuInfo {
	return &product.SkuInfo{
		Id:          1,
		ProductId:   2,
		CommunityId: communityID,
		SpecName:    "红色 / L",
		Price:       9.90,
		Stock:       100,
		Status:      status,
	}
}

// TestCartAddItemTradeGuards AddItem 的交易入口拦截与传给商品服务的口径
func TestCartAddItemTradeGuards(t *testing.T) {
	t.Run("跨社区加购被拒", func(t *testing.T) {
		fake := &fakeProductClient{sku: testSku(cartTestOtherCommunityID, 1)}
		_, err := newCartTestServer(fake).AddItem(stdctx.Background(), &cart.AddItemReq{
			UserId: 1, CommunityId: cartTestCommunityID, SkuId: 1, Quantity: 1,
		})
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("跨社区加购应返回 PermissionDenied, got %v", err)
		}
		if len(fake.gotReqs) != 0 {
			t.Error("越权加购不应继续查询商品档案")
		}
	})

	t.Run("禁用规格加购被拒", func(t *testing.T) {
		fake := &fakeProductClient{sku: testSku(cartTestCommunityID, 0)}
		_, err := newCartTestServer(fake).AddItem(stdctx.Background(), &cart.AddItemReq{
			UserId: 1, CommunityId: cartTestCommunityID, SkuId: 1, Quantity: 1,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("禁用规格加购应返回 FailedPrecondition, got %v", err)
		}
		if got := err.Error(); got != "rpc error: code = FailedPrecondition desc = 规格已失效，暂不可购买" {
			t.Errorf("文案应提示规格已失效, got %q", got)
		}
	})

	t.Run("商品不可售按商品服务码透出", func(t *testing.T) {
		// 商品服务 require_sellable 收口的拒绝必须原样透出，
		// 包成 Internal 会让前端把「已下架」显示成系统错误
		fake := &fakeProductClient{
			sku:       testSku(cartTestCommunityID, 1),
			detailErr: status.Error(codes.FailedPrecondition, "商品「测试商品」已下架，暂不可购买"),
		}
		_, err := newCartTestServer(fake).AddItem(stdctx.Background(), &cart.AddItemReq{
			UserId: 1, CommunityId: cartTestCommunityID, SkuId: 1, Quantity: 1,
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("商品不可售应透出 FailedPrecondition, got %v", err)
		}
	})

	t.Run("加购即声明可售校验意图", func(t *testing.T) {
		fake := &fakeProductClient{
			sku:       testSku(cartTestCommunityID, 1),
			detailErr: status.Error(codes.NotFound, "商品不存在"),
		}
		// 用 NotFound 让流程停在查档之后、写库之前（db 为空，不能真正落库）
		_, err := newCartTestServer(fake).AddItem(stdctx.Background(), &cart.AddItemReq{
			UserId: 1, CommunityId: cartTestCommunityID, SkuId: 1, Quantity: 1,
		})
		if status.Code(err) != codes.NotFound {
			t.Fatalf("商品不存在应透出 NotFound, got %v", err)
		}
		if len(fake.gotReqs) != 1 {
			t.Fatalf("应查一次商品档案, got %d", len(fake.gotReqs))
		}
		if !fake.gotReqs[0].GetRequireSellable() {
			t.Error("加购必须传 require_sellable=true")
		}
		if fake.gotReqs[0].GetCommunityId() != cartTestCommunityID {
			t.Errorf("商品档案应按 SKU 归属社区查, got %d", fake.gotReqs[0].GetCommunityId())
		}
	})

	t.Run("未带社区回落 SKU 归属", func(t *testing.T) {
		fake := &fakeProductClient{
			sku:       testSku(cartTestCommunityID, 1),
			detailErr: status.Error(codes.NotFound, "商品不存在"),
		}
		_, err := newCartTestServer(fake).AddItem(stdctx.Background(), &cart.AddItemReq{
			UserId: 1, SkuId: 1, Quantity: 1,
		})
		if status.Code(err) == codes.PermissionDenied {
			t.Errorf("未带社区不应判越权, got %v", err)
		}
		if len(fake.gotReqs) != 1 || fake.gotReqs[0].GetCommunityId() != cartTestCommunityID {
			t.Errorf("应回落 SKU 归属社区查档, got %+v", fake.gotReqs)
		}
	})
}

// TestCartItemInvalidReason 失效原因判定顺序（展示口径与后端拦截同构）
// 判定入参是预取到的 SKU/商品指针，nil 即代表商品服务已取不到（批量出口把缺失项剔除）
func TestCartItemInvalidReason(t *testing.T) {
	cases := []struct {
		name        string
		sku         *product.SkuInfo
		productInfo *product.ProductInfo
		want        string
	}{
		{name: "SKU 已删除", sku: nil, productInfo: testProductInfo(1), want: "商品已失效"},
		{name: "商品已删除", sku: testSku(cartTestCommunityID, 1), productInfo: nil, want: "商品已失效"},
		{name: "规格已禁用", sku: testSku(cartTestCommunityID, 0), productInfo: testProductInfo(1), want: "规格已失效"},
		{name: "商品已下架", sku: testSku(cartTestCommunityID, 1), productInfo: testProductInfo(0), want: "商品已下架"},
		{name: "已售罄", sku: withStock(testSku(cartTestCommunityID, 1), 0), productInfo: testProductInfo(1), want: "商品已售罄"},
		{name: "有效", sku: testSku(cartTestCommunityID, 1), productInfo: testProductInfo(1), want: ""},
		{name: "商品删除优先于规格禁用", sku: testSku(cartTestCommunityID, 0), productInfo: nil, want: "商品已失效"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cartItemInvalidReason(tc.sku, tc.productInfo); got != tc.want {
				t.Errorf("失效原因应为 %q, got %q", tc.want, got)
			}
		})
	}
}

// testProductInfo 构造商品信息（批量出口返回的形态）
// 参数：
//   - status: 商品状态（0 下架/仓库中、1 上架）
//
// 返回值：
//   - *product.ProductInfo: 商品信息
func testProductInfo(status int32) *product.ProductInfo {
	return &product.ProductInfo{Id: 2, Name: "测试商品", Status: status, MinBuy: 1, SkuTotal: 3}
}

// TestBuildCartItemsBatchPrefetch 展示装配改为整批预取：N 行只发 1 次 SKU 批量 + 每社区 1 次商品批量
// 回归的是逐行 GetSkuById+GetProduct 的 N+1（每次 GetProduct 服务侧还要跑 5 条 SQL）
func TestBuildCartItemsBatchPrefetch(t *testing.T) {
	skuA := testSku(cartTestCommunityID, 1) // ID=1，商品 2
	skuB := &product.SkuInfo{Id: 5, ProductId: 6, CommunityId: cartTestCommunityID, SpecName: "500g", Price: 3.50, Stock: 20, Status: 1}
	skuOther := &product.SkuInfo{Id: 7, ProductId: 8, CommunityId: cartTestOtherCommunityID, SpecName: "1kg", Price: 12.00, Stock: 5, Status: 1}
	fake := &fakeProductClient{
		skuList: []*product.SkuInfo{skuA, skuB, skuOther},
		productList: []*product.ProductInfo{
			{Id: 2, Name: "测试商品", Status: 1, MinBuy: 1, SkuTotal: 3},
			{Id: 6, Name: "土豆", Status: 1, SkuTotal: 1},
			{Id: 8, Name: "跨社区商品", Status: 1, SkuTotal: 2},
		},
	}
	svc := newCartTestServerWithLoader(fake)

	rows := []*model.Cart{
		{SkuID: 1, ProductID: 2, Quantity: 2, Selected: true, CommunityID: cartTestCommunityID},
		{SkuID: 5, ProductID: 6, Quantity: 1, Selected: true, CommunityID: cartTestCommunityID},
		{SkuID: 7, ProductID: 8, Quantity: 1, Selected: true, CommunityID: cartTestOtherCommunityID},
	}
	items, err := svc.buildCartItems(stdctx.Background(), rows)
	if err != nil {
		t.Fatalf("装配购物车项失败: %v", err)
	}

	if len(fake.gotBatchSkuReqs) != 1 {
		t.Fatalf("SKU 应整批查一次, got %d 次", len(fake.gotBatchSkuReqs))
	}
	if got := fake.gotBatchSkuReqs[0].GetIds(); len(got) != 3 {
		t.Errorf("批量入参应含全部 3 个 SKU, got %v", got)
	}
	if !fake.gotBatchSkuReqs[0].GetTolerateMissing() {
		t.Error("展示侧必须用 tolerate_missing 逐项判定口径（单行失效不应阻断整个购物车）")
	}
	// 商品批量按规格归属社区分组：两个社区即两次调用，仍与行数无关
	if len(fake.gotBatchProdReqs) != 2 {
		t.Fatalf("商品应按社区分组各批量查一次, got %d 次", len(fake.gotBatchProdReqs))
	}
	for _, req := range fake.gotBatchProdReqs {
		if !req.GetTolerateMissing() {
			t.Error("商品批量查询应传 tolerate_missing=true")
		}
	}
	// 逐行不应再走单条 RPC
	if len(fake.gotReqs) != 0 {
		t.Errorf("装配不应再逐行调 GetProduct, got %d 次", len(fake.gotReqs))
	}

	if len(items) != 3 {
		t.Fatalf("应返回 3 个购物车项, got %d", len(items))
	}
	if items[0].Price != 9.90 || items[0].SkuTotal != 3 || items[0].MinBuy != 1 || items[0].InvalidReason != "" {
		t.Errorf("实时数据装配异常: %+v", items[0])
	}
	if items[2].ProductName != "跨社区商品" || items[2].SkuTotal != 2 {
		t.Errorf("跨社区行装配异常: %+v", items[2])
	}
}

// TestBuildCartItemsSnapshotFallback SKU/商品在批量结果中缺失时回退快照并标失效
func TestBuildCartItemsSnapshotFallback(t *testing.T) {
	fake := &fakeProductClient{
		skuList: []*product.SkuInfo{{Id: 1, ProductId: 2, CommunityId: cartTestCommunityID, Status: 1, Price: 9.90, Stock: 10}},
		// 商品 2 已被删除（批量结果不含），断言不整体报错
		productList: nil,
	}
	svc := newCartTestServerWithLoader(fake)
	items, err := svc.buildCartItems(stdctx.Background(), []*model.Cart{
		{SkuID: 1, ProductID: 2, Quantity: 1, Selected: true, CommunityID: cartTestCommunityID, ProductName: "快照商品名"},
		// SKU 9 已被删除，批量结果不含
		{SkuID: 9, ProductID: 2, Quantity: 3, Selected: true, CommunityID: cartTestCommunityID, ProductName: "快照已删商品", SpecName: "快照规格", ProductImage: "uploads/images/old.png"},
	})
	if err != nil {
		t.Fatalf("缺失项不应让整批报错: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("应返回 2 项（含失效项）, got %d", len(items))
	}
	if items[0].InvalidReason != "商品已失效" || items[0].Selected {
		t.Errorf("商品已删除应判失效且不可选: %+v", items[0])
	}
	if items[0].ProductName != "快照商品名" {
		t.Errorf("商品取不到时应回退快照名, got %q", items[0].ProductName)
	}
	if items[1].InvalidReason != "商品已失效" || items[1].Price != 0 || items[1].Stock != 0 {
		t.Errorf("SKU 已删除应判失效且价格库存归零: %+v", items[1])
	}
	if items[1].ProductName != "快照已删商品" || items[1].SpecName != "快照规格" {
		t.Errorf("SKU 已删除应回退快照展示字段: %+v", items[1])
	}
	if len(fake.gotBatchProdReqs) != 1 || len(fake.gotBatchProdReqs[0].GetIds()) != 1 {
		t.Errorf("商品批量只应查在架规格涉及的 ID, got %+v", fake.gotBatchProdReqs)
	}
}

// withStock 覆写 SKU 库存（售罄用例）
// 参数：
//   - sku: 原 SKU 信息
//   - stock: 目标库存
//
// 返回值：
//   - *product.SkuInfo: 改过库存的 SKU 信息
func withStock(sku *product.SkuInfo, stock int32) *product.SkuInfo {
	sku.Stock = stock
	return sku
}
