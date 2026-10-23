package main

import (
	stdctx "context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/proto/cart"
	"github.com/jcbowen/market/src/backend/common/proto/product"
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
 *   - TestCartAddItemTradeGuards   AddItem 的越权/规格失效/商品不可售拦截与入参口径
 *   - TestCartItemInvalidReason    invalidReasonOf 的失效原因判定顺序
 */

// cartTestCommunityID 用例中的成交社区 ID
const cartTestCommunityID int64 = 99100098

// cartTestOtherCommunityID 用例中的他人社区 ID
const cartTestOtherCommunityID int64 = 99100097

// fakeProductClient 商品服务客户端替身：仅实现用例涉及的 GetSkuById / GetProduct，
// 其余方法由内嵌接口提供（未赋值，被调用即 panic，用于暴露意外的调用路径）
type fakeProductClient struct {
	product.ProductServiceClient
	sku       *product.SkuInfo
	skuErr    error
	detail    *product.ProductDetail
	detailErr error
	gotReqs   []*product.GetProductReq // 记录 GetProduct 入参，断言交易意图（require_sellable/community_id）
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

// newCartTestServer 构造只挂替身客户端的购物车服务实例
// 参数：
//   - f: 商品服务客户端替身
//
// 返回值：
//   - *server: 被测服务（db 为空，仅用于验证拦截分支）
func newCartTestServer(f *fakeProductClient) *server {
	return &server{productClient: f}
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

// testProductDetail 构造商品详情
// 参数：
//   - status: 商品状态（0 下架/仓库中、1 上架、2 已删除）
//
// 返回值：
//   - *product.ProductDetail: 商品详情
func testProductDetail(status int32) *product.ProductDetail {
	return &product.ProductDetail{Product: &product.ProductInfo{Id: 2, Name: "测试商品", Status: status}}
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

// TestCartItemInvalidReason invalidReasonOf 的失效原因判定顺序（展示口径与后端拦截同构）
func TestCartItemInvalidReason(t *testing.T) {
	cases := []struct {
		name     string
		fake     *fakeProductClient
		want     string
		wantCode codes.Code
	}{
		{
			name: "SKU 已删除",
			fake: &fakeProductClient{skuErr: status.Error(codes.NotFound, "SKU 不存在")},
			want: "商品已失效",
		},
		{
			name:     "规格已禁用",
			fake:     &fakeProductClient{sku: testSku(cartTestCommunityID, 0), detail: testProductDetail(1)},
			want:     "规格已失效",
			wantCode: codes.OK,
		},
		{
			name:     "商品已下架",
			fake:     &fakeProductClient{sku: testSku(cartTestCommunityID, 1), detail: testProductDetail(0)},
			want:     "商品已下架",
			wantCode: codes.OK,
		},
		{
			name:     "已售罄",
			fake:     &fakeProductClient{sku: withStock(testSku(cartTestCommunityID, 1), 0), detail: testProductDetail(1)},
			want:     "商品已售罄",
			wantCode: codes.OK,
		},
		{
			name:     "有效",
			fake:     &fakeProductClient{sku: testSku(cartTestCommunityID, 1), detail: testProductDetail(1)},
			want:     "",
			wantCode: codes.OK,
		},
		{
			name:     "查档系统错误不降级为失效",
			fake:     &fakeProductClient{skuErr: status.Error(codes.Internal, "db down")},
			want:     "",
			wantCode: codes.Internal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, reason, err := newCartTestServer(tc.fake).invalidReasonOf(stdctx.Background(), 1)
			if got := status.Code(err); got != tc.wantCode {
				t.Fatalf("错误码应为 %v, got %v (%v)", tc.wantCode, got, err)
			}
			if reason != tc.want {
				t.Errorf("失效原因应为 %q, got %q", tc.want, reason)
			}
		})
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
