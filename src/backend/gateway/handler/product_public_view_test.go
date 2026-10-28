package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/jcbowen/market/src/backend/common/appcontext"
	"github.com/jcbowen/market/src/backend/common/errcode"
	"github.com/jcbowen/market/src/backend/common/jwt"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/proto/user"
)

/**
 * C 端商品出口社区语境与公开口径回归（P2 C2）
 *
 * 背景：C 端详情曾允许不带 community_id 请求，服务层 EnsureScope 对 scope<=0
 * 直接放行，于是匿名请求只要拿到商品 ID 就能读出任意社区的价格、库存与富文本正文；
 * 同时详情/SKU 出口按管理端口径全量出库，成本价与安全库存一并下发。
 *
 * 修法分两层：网关公开出口必须携带社区语境并把「公开」意图显式声明给服务层
 * （public_view=true），服务层按商品状态脱敏（见 services/product/service
 * 的 public_view_test.go）。本文件只锁网关侧的入参契约：
 *   - 缺省/非法 community_id → 参数错误，且不触达商品服务（旁路无从形成）；
 *   - public_view 恒为 true，include_deleted 恒为 false（C 端读不到软删除行）；
 *   - 管理端详情反向：include_deleted=true（编辑回显要能打开已删除商品）、
 *     public_view=false（成本价与安全库存是管理端进销存的必要字段）。
 *
 * 运行方式（全部离线，无需 DB/Redis）：
 *   go test ./gateway/handler/ -run 'TestPublicProduct' -v
 */

// stubPublicProductClient 商品服务桩：只覆写 C 端与管理端详情/SKU 出口，记录入参
type stubPublicProductClient struct {
	product.ProductServiceClient
	detailCalls int
	lastDetail  *product.GetProductReq
	lastListSku *product.ListSkusReq
	lastGetSku  *product.GetSkuByIdReq
	detailErr   error
}

// GetProduct 详情桩：记录入参，按预设返回或回 NotFound
func (s *stubPublicProductClient) GetProduct(ctx context.Context, in *product.GetProductReq, opts ...grpc.CallOption) (*product.ProductDetail, error) {
	s.detailCalls++
	s.lastDetail = in
	if s.detailErr != nil {
		return nil, s.detailErr
	}
	return &product.ProductDetail{Product: &product.ProductInfo{Id: in.Id, Status: 1}}, nil
}

// ListSkus 规格列表桩
func (s *stubPublicProductClient) ListSkus(ctx context.Context, in *product.ListSkusReq, opts ...grpc.CallOption) (*product.ListSkusResp, error) {
	s.lastListSku = in
	return &product.ListSkusResp{List: []*product.SkuInfo{{Id: 1, ProductId: in.ProductId}}}, nil
}

// GetSkuById 单规格桩
func (s *stubPublicProductClient) GetSkuById(ctx context.Context, in *product.GetSkuByIdReq, opts ...grpc.CallOption) (*product.SkuInfo, error) {
	s.lastGetSku = in
	return &product.SkuInfo{Id: in.Id}, nil
}

// newPublicProductRequest 构造带路由参数的公开出口请求
// 参数：
//   - path: 请求路径（query 参数用于 community_id/product_id）
//   - params: 路由参数（:id 等，gin 测试上下文不自动解析路径）
//
// 返回值：
//   - *gin.Context: 已绑定 recorder 的上下文
func newPublicProductRequest(path string, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = params
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	return c, recorder
}

// respCode 取网关响应里的业务错误码
// 参数：
//   - t: 测试对象
//   - recorder: 响应记录器
//
// 返回值：
//   - int: 业务码（0 表示成功）
func respCode(t *testing.T, recorder *httptest.ResponseRecorder) int {
	t.Helper()
	_, code := decodeRespBody(t, recorder)
	return code
}

// TestPublicProductDetailRequiresCommunityContext C 端详情：社区语境必传，公开口径恒开
func TestPublicProductDetailRequiresCommunityContext(t *testing.T) {
	stub := &stubPublicProductClient{}
	h := NewProductHandler(stub)

	t.Run("不带 community_id 直接拒绝且不得触达服务", func(t *testing.T) {
		stub.detailCalls = 0
		c, recorder := newPublicProductRequest("/api/v1/products/12", gin.Params{{Key: "id", Value: "12"}})
		h.GetProduct(c)
		if stub.detailCalls != 0 {
			t.Fatalf("缺社区语境仍调用了商品服务 %d 次（旁路重现）", stub.detailCalls)
		}
		if code := respCode(t, recorder); code != errcode.ParamError {
			t.Errorf("期望参数错误 %d，得到 %d，body=%s", errcode.ParamError, code, recorder.Body.String())
		}
	})

	for _, bad := range []string{"0", "-2", "abc", ""} {
		stub.detailCalls = 0
		c, recorder := newPublicProductRequest("/api/v1/products/12?community_id="+bad, gin.Params{{Key: "id", Value: "12"}})
		h.GetProduct(c)
		if stub.detailCalls != 0 || respCode(t, recorder) != errcode.ParamError {
			t.Errorf("非法 community_id=%q 应拒绝且不触达服务，calls=%d body=%s", bad, stub.detailCalls, recorder.Body.String())
		}
	}

	stub.detailCalls = 0
	c, recorder := newPublicProductRequest("/api/v1/products/12?community_id=3", gin.Params{{Key: "id", Value: "12"}})
	h.GetProduct(c)
	if stub.detailCalls != 1 {
		t.Fatalf("合法社区语境应调用一次商品服务，实际 %d 次", stub.detailCalls)
	}
	req := stub.lastDetail
	if req.Id != 12 || req.CommunityId != 3 {
		t.Errorf("详情入参错误：id=%d community_id=%d", req.Id, req.CommunityId)
	}
	if !req.PublicView {
		t.Error("C 端详情必须声明 public_view=true，否则服务层不脱敏，成本价与富文本正文直接下发")
	}
	if req.IncludeDeleted {
		t.Error("C 端详情不得放行软删除行（include_deleted=true 仅供管理端编辑回显）")
	}
	if req.RequireSellable {
		t.Error("浏览详情不应要求可售（下架商品要能打开页面，只按公开口径抹交易字段）")
	}
	if code := respCode(t, recorder); code != 0 {
		t.Errorf("正常详情应返回成功，得到 code=%d body=%s", code, recorder.Body.String())
	}
}

// TestPublicProductDetailNotFoundPassthrough 服务层拒绝（跨社区/已删除）时网关原样透出错误码
func TestPublicProductDetailNotFoundPassthrough(t *testing.T) {
	stub := &stubPublicProductClient{detailErr: status.Error(codes.NotFound, "商品不存在")}
	h := NewProductHandler(stub)
	c, recorder := newPublicProductRequest("/api/v1/products/99?community_id=3", gin.Params{{Key: "id", Value: "99"}})
	h.GetProduct(c)
	if recorder.Body.Len() == 0 {
		t.Fatal("服务层错误未产生任何响应")
	}
	var body map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应解析失败: %v, body=%s", err, recorder.Body.String())
	}
	if code := respCode(t, recorder); code == 0 {
		t.Errorf("跨社区/已删除商品应回错误码，body=%s", recorder.Body.String())
	}
	if body["data"] != nil {
		t.Errorf("错误响应不得携带商品数据：%v", body["data"])
	}
}

// TestPublicSkuExitsDeclarePublicView 规格出口同样声明公开口径（成本价与安全库存的泄露面）
func TestPublicSkuExitsDeclarePublicView(t *testing.T) {
	stub := &stubPublicProductClient{}
	h := NewProductHandler(stub)

	c, _ := newPublicProductRequest("/api/v1/skus?product_id=12", nil)
	h.ListSkus(c)
	if stub.lastListSku == nil {
		t.Fatal("规格列表未触达商品服务")
	}
	if !stub.lastListSku.PublicView {
		t.Error("C 端规格列表必须声明 public_view=true")
	}

	c, _ = newPublicProductRequest("/api/v1/skus/5", gin.Params{{Key: "id", Value: "5"}})
	h.GetSkuById(c)
	if stub.lastGetSku == nil {
		t.Fatal("单规格查询未触达商品服务")
	}
	if stub.lastGetSku.Id != 5 || !stub.lastGetSku.PublicView {
		t.Errorf("单规格入参错误：id=%d public_view=%v", stub.lastGetSku.Id, stub.lastGetSku.PublicView)
	}
}

// TestAdminProductDetailKeepsDeletedAndBusinessFields 管理端详情反向口径：放行软删除行、不声明公开
// 参数：
//   - t: 测试对象
func TestAdminProductDetailKeepsDeletedAndBusinessFields(t *testing.T) {
	stub := &stubPublicProductClient{}
	installAdminCtxResolver(t, &stubAdminCtxClient{info: &user.AdminContextInfo{IsSuper: true, Bound: true}})
	c, _ := newPublicProductRequest("/api/v1/admin/products/12", gin.Params{{Key: "id", Value: "12"}})
	c.Set(string(appcontext.KeyUserID), int64(7))
	c.Set(string(appcontext.KeyTokenType), jwt.TokenTypeAdmin)
	c.Set(string(appcontext.KeyRole), "super_admin")
	c.Set(string(appcontext.KeyCommunityID), int64(1))
	c.Set(string(appcontext.KeyCommunityIDs), []int64{1})

	h := &AdminHandler{productClient: stub}
	h.GetAdminProduct(c)

	if stub.detailCalls != 1 {
		t.Fatalf("管理端详情应调用一次商品服务，实际 %d 次", stub.detailCalls)
	}
	if !stub.lastDetail.IncludeDeleted {
		t.Error("管理端详情必须放行软删除行（已删除商品的编辑回显与审计依赖它）")
	}
	if stub.lastDetail.PublicView {
		t.Error("管理端详情不得声明 public_view，否则成本价与安全库存被抹除、进销存不可用")
	}
	if stub.lastDetail.CommunityId != 0 {
		t.Errorf("实时超管的数据作用域应为 0（不限制社区），得到 %d", stub.lastDetail.CommunityId)
	}
}
