package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

/**
 * 「可售」判定收口测试（P1-4 + SKU 交易口径 + 社区归属）
 *
 * 背景：定时上架商品在调度器到点前 status=0（仓库中），与已下架商品同态；
 * 此前下单（BatchGetProducts 无状态过滤）与加购（GetProduct 不判状态）都不拦，
 * 未到点的商品就能成交。修法是在商品服务收口唯一口径（status=1），
 * 交易入口经 require_sellable 声明意图。
 * SKU 侧同构收口：BatchGetSkus 恒定只放启用 SKU（status=1），
 * 否则后台禁用/移除规格后，购物车存量行仍能按快照规格成交并扣库存。
 * 社区归属：BatchGetSkus/BatchGetProducts 传 community_id>0 时逐条比对归属，
 * 任一越权整体拒绝（下单透传成交社区后，跨社区商品与价格不得挂到别的社区成交）。
 *
 * 覆盖：
 *   - TestEnsureProductSellable               纯函数：上架放行，下架/删除/定时上架中拒绝
 *   - TestEnsureSkuSellable                   纯函数：启用放行，禁用（含软删/移除规格）拒绝
 *   - TestEnsureTradeAccess                   纯函数：EnsureTradeAccess 归属×可售真值表（越权优先于不可售）
 *   - TestBatchGetProductsRequireSellable     集成：require_sellable 收口 + 非收口口径不变
 *   - TestBatchGetSkusTradeGuard              集成：禁用 SKU 不可下单、跨社区明细被拒
 *   - TestBatchGetProductsCommunityScope      集成：商品侧传 community_id 的越权拒绝与放行
 *   - TestCategoryDisabledOnlyHidesCategories 集成：分类禁用仅隐藏分类项、不拦商品与交易（锁定文档口径）
 *   - TestBarcodeReleasedByProductSoftDelete  集成：商品软删连带禁用 SKU，条码即被释放可复用
 *
 * 运行方式（env 守卫跳过，与 stock_concurrency_test 同约定）：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/product/service/ -run "Sellable|TradeAccess|Barcode|Category" -v
 *   纯函数用例（TestEnsure*）不连库，任意环境都会跑。
 *
 * 测试数据使用 99100098（操作者/成交社区）与 99100097（他人社区，用于越权用例），
 * 名称/编码统一带 SELLABLETEST- 前缀，结束即清理。
 */

// sellableTestCommunityID 可售校验测试专用社区 ID（与库存测试的 99100099 错开，作为清理依据）
const sellableTestCommunityID int64 = 99100098

// sellableOtherCommunityID 跨社区用例的「他人社区」ID：作为 SKU/商品的归属方，
// 与 sellableTestCommunityID（操作者/成交社区）配对，用于验证越权拦截；同样纳入清理范围
const sellableOtherCommunityID int64 = 99100097

// sellableTestEnv 可售校验测试环境句柄
type sellableTestEnv struct {
	db      *mysql.Instance
	svc     *ProductService
	skuSvc  *SkuService
	cats    *CategoryService
	cleanup func()
}

// newSellableTestEnv 建测试库连接、迁移商品/SKU/分类/规格表，返回被测服务与清理函数
//
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *sellableTestEnv: 被测服务与清理句柄；TEST_MYSQL_PASSWORD 未设置时 skip
func newSellableTestEnv(t *testing.T) *sellableTestEnv {
	t.Helper()
	if os.Getenv("TEST_MYSQL_PASSWORD") == "" {
		t.Skip("TEST_MYSQL_PASSWORD 未设置，跳过后端集成测试")
	}
	dbInst, err := mysql.New(jcbaseGo.DbStruct{
		Host:      stockTestEnvOr("TEST_MYSQL_HOST", "127.0.0.1"),
		Port:      stockTestEnvOr("TEST_MYSQL_PORT", "3306"),
		Dbname:    stockTestEnvOr("TEST_MYSQL_DBNAME", "market"),
		Username:  stockTestEnvOr("TEST_MYSQL_USER", "root"),
		Password:  os.Getenv("TEST_MYSQL_PASSWORD"),
		Charset:   "utf8mb4",
		ParseTime: "True",
		Loc:       "Local",
	})
	if err != nil {
		t.Fatalf("连接测试 MySQL 失败: %v", err)
	}
	if err := dbInst.GetDb().AutoMigrate(
		&model.Product{}, &model.Sku{}, &model.Category{},
		&model.ProductSpec{}, &model.ProductSpecValue{},
	); err != nil {
		t.Fatalf("测试库迁移失败: %v", err)
	}
	env := &sellableTestEnv{
		db:     dbInst,
		svc:    NewProductService(dbInst),
		skuSvc: NewSkuService(dbInst),
		cats:   NewCategoryService(dbInst),
	}
	env.cleanup = func() {
		db := dbInst.GetDb()
		for _, cid := range []int64{sellableTestCommunityID, sellableOtherCommunityID} {
			_ = db.Where("community_id = ?", cid).Delete(&model.Sku{})
			_ = db.Where("community_id = ?", cid).Delete(&model.Product{})
			_ = db.Where("community_id = ?", cid).Delete(&model.Category{})
		}
	}
	return env
}

// TestEnsureProductSellable 纯函数：仅上架态可售，其余状态拒绝且文案带商品名
func TestEnsureProductSellable(t *testing.T) {
	cases := []struct {
		name    string
		status  int32
		wantErr bool
	}{
		{"上架可售", 1, false},
		{"仓库中（含定时上架未到点）不可售", 0, true},
		{"已删除不可售", 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &model.Product{Name: "SELLABLETEST-单元", Status: tc.status}
			err := EnsureProductSellable(p)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("status=%d 应拒绝", tc.status)
				}
				var be *bizerr.Error
				if !errors.As(err, &be) || be.Code() != codes.FailedPrecondition {
					t.Fatalf("应返回 FailedPrecondition 业务错误, got %v", err)
				}
				if !strings.Contains(err.Error(), p.Name) {
					t.Errorf("错误文案应带商品名便于前端展示, got %q", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("status=%d 应放行, got %v", tc.status, err)
			}
		})
	}
}

// TestBatchGetProductsRequireSellable 集成：下单取档案的可售收口
// 前置：同社区下四个商品——上架 / 定时上架中(0+未来 on_sale_at) / 仓库中(0) / 已删除(2)
// 断言：
//   - 全上架：require_sellable=true 正常返回
//   - 混入定时上架中/仓库中/已删除：true 即整体报错（FailedPrecondition，带商品名），零部分结果
//   - require_sellable=false：原行为不变，全部返回（管理端/展示口径不受影响）
func TestBatchGetProductsRequireSellable(t *testing.T) {
	env := newSellableTestEnv(t)
	defer env.cleanup()
	ctx := context.Background()

	future := time.Now().Add(2 * time.Hour).Format("2006-01-02 15:04:05")
	seed := func(name string, status int32, onSaleAt *string) int64 {
		t.Helper()
		now := time.Now().Format("2006-01-02 15:04:05")
		p := &model.Product{
			CommunityID: sellableTestCommunityID,
			CategoryID:  1,
			Name:        name,
			Images:      "[]",
			Status:      status,
			OnSaleAt:    onSaleAt,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := env.db.GetDb().Create(p).Error; err != nil {
			t.Fatalf("创建测试商品失败: %v", err)
		}
		// Status 列带 default:1 标签：Create 传零值(0)会被 GORM 跳列、落库默认 1，
		// 与生产链路同一坑；这里显式回写期望状态（0=仓库中/定时上架中）
		if status == 0 {
			if err := env.db.GetDb().Model(p).Update("status", 0).Error; err != nil {
				t.Fatalf("回写测试商品状态失败: %v", err)
			}
		}
		return p.ID
	}
	onSale := seed("SELLABLETEST-上架", 1, nil)
	scheduled := seed("SELLABLETEST-定时上架中", 0, &future)
	offSale := seed("SELLABLETEST-仓库中", 0, nil)
	deleted := seed("SELLABLETEST-已删除", 2, nil)

	// 全上架：true 放行
	got, err := env.svc.BatchGetProducts(ctx, []int64{onSale}, 0, true)
	if err != nil {
		t.Fatalf("全上架商品应放行, got %v", err)
	}
	if len(got) != 1 || got[0].ID != onSale {
		t.Errorf("返回结果异常: %+v", got)
	}

	// 任一不可售：true 即整体报错，且文案指明是哪个商品
	for _, bad := range []struct {
		name string
		id   int64
	}{
		{"定时上架中", scheduled},
		{"仓库中", offSale},
		{"已删除", deleted},
	} {
		_, err := env.svc.BatchGetProducts(ctx, []int64{onSale, bad.id}, 0, true)
		if err == nil {
			t.Fatalf("混入%s商品应整体报错", bad.name)
		}
		var be *bizerr.Error
		if !errors.As(err, &be) || be.Code() != codes.FailedPrecondition {
			t.Errorf("%s：应返回 FailedPrecondition, got %v", bad.name, err)
		}
	}

	// false：原行为不变，不过滤状态（管理端/展示口径）
	all, err := env.svc.BatchGetProducts(ctx, []int64{onSale, scheduled, offSale, deleted}, 0, false)
	if err != nil {
		t.Fatalf("require_sellable=false 不应报错, got %v", err)
	}
	if len(all) != 4 {
		t.Errorf("require_sellable=false 应返回全部 4 条, got %d", len(all))
	}

	// 缺 ID 仍按不存在报错（不受可售标志影响）
	if _, err := env.svc.BatchGetProducts(ctx, []int64{onSale, 999999999}, 0, true); err == nil {
		t.Error("缺失 ID 应报不存在")
	}
}

// seedSellableProduct 直插一条商品档案（绕过表单校验，只验交易口径），返回商品 ID
// status 列带 default:1 标签：Create 传零值(0) 会被 GORM 跳列、落库默认 1，
// 与生产链路同一坑，故非上架态都要显式回写
//
// 参数：
//   - t: 测试对象（落库失败直接终止，用例不覆盖写库错误分支）
//   - env: 测试环境
//   - communityID: 商品归属社区 ID
//   - name: 商品名（统一 SELLABLETEST- 前缀，便于人工排查与错误文案断言）
//   - status: 商品状态（0 仓库中/定时上架中、1 上架、2 已删除）
//   - categoryID: 挂载分类 ID（0 表示不挂分类）
//
// 返回值：
//   - int64: 落库后的商品 ID
//
// 使用示例：
//
//	pID := seedSellableProduct(t, env, sellableTestCommunityID, "SELLABLETEST-上架", 1, 0)
func seedSellableProduct(t *testing.T, env *sellableTestEnv, communityID int64, name string, status int32, categoryID int64) int64 {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	p := &model.Product{
		CommunityID: communityID,
		CategoryID:  categoryID,
		Name:        name,
		Images:      "[]",
		Status:      status,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := env.db.GetDb().Create(p).Error; err != nil {
		t.Fatalf("创建测试商品失败: %v", err)
	}
	if status != 1 {
		if err := env.db.GetDb().Model(p).Update("status", status).Error; err != nil {
			t.Fatalf("回写测试商品状态失败: %v", err)
		}
	}
	return p.ID
}

// seedSellableSku 直插一条 SKU，返回 SKU ID
// sku_code 在同社区内唯一（idx_sku_code_community 复合唯一索引），用例需给出互不相同的编码；
// status 同样是 default:1，禁用态（0）要显式回写
//
// 参数：
//   - t: 测试对象
//   - env: 测试环境
//   - productID: 所属商品 ID
//   - communityID: SKU 归属社区 ID（与商品不一致即模拟归属漂移/跨社区取用）
//   - skuCode: SKU 编码（同社区内唯一）
//   - specName: 规格展示名（错误文案断言用，可为空以触发回落）
//   - barcode: 条形码（空串表示未设置）
//   - status: SKU 状态（1 启用 / 0 禁用）
//
// 返回值：
//   - int64: 落库后的 SKU ID
//
// 使用示例：
//
//	skuID := seedSellableSku(t, env, pID, sellableTestCommunityID, "SELLABLETEST-SKU-OK", "红色 / L", "", 1)
func seedSellableSku(t *testing.T, env *sellableTestEnv, productID, communityID int64, skuCode, specName, barcode string, status int32) int64 {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	sku := &model.Sku{
		ProductID:   productID,
		CommunityID: communityID,
		SkuCode:     skuCode,
		SpecName:    specName,
		SpecJSON:    "{}",
		Price:       9.90,
		Barcode:     barcode,
		Stock:       100,
		Status:      status,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := env.db.GetDb().Create(sku).Error; err != nil {
		t.Fatalf("创建测试 SKU 失败: %v", err)
	}
	if status != 1 {
		if err := env.db.GetDb().Model(sku).Update("status", 0).Error; err != nil {
			t.Fatalf("回写测试 SKU 状态失败: %v", err)
		}
	}
	return sku.ID
}

// assertBizCode 断言错误码：want 为 codes.OK 时要求 err 为 nil，否则要求同码的 bizerr
// 商品/购物车服务统一用 bizerr 承载业务码（server.go 经 bizerr.ToStatus 映射为 gRPC code），
// 用例按码断言而非按文案断言，避免文案微调即误报
//
// 参数：
//   - t: 测试对象
//   - name: 用例名（失败时打印，便于定位是哪条断言）
//   - err: 被测错误
//   - want: 期望码（codes.OK=期望无错误；期望无错误时若拿到 gRPC status 错误也算失败）
func assertBizCode(t *testing.T, name string, err error, want codes.Code) {
	t.Helper()
	if want == codes.OK {
		if err != nil {
			t.Fatalf("%s：应放行, got %v", name, err)
		}
		return
	}
	if err == nil {
		t.Fatalf("%s：应返回 %s 错误", name, want)
	}
	var be *bizerr.Error
	if !errors.As(err, &be) || be.Code() != want {
		t.Fatalf("%s：应返回 %s 业务错误, got %v", name, want, err)
	}
}

// TestEnsureSkuSellable 纯函数：仅启用态（status=1）可售，禁用/移除规格一律拒绝
// 禁用态来源有三种（后台禁用、商品编辑移除规格、商品软删连带禁用），落库都是 status=0，
// 因此判定只看 status：三种来源必须同态拒绝，否则购物车存量行仍能按快照规格成交并扣库存
func TestEnsureSkuSellable(t *testing.T) {
	if err := EnsureSkuSellable(&model.Sku{ID: 1, SpecName: "红色 / L", Status: SkuStatusEnabled}); err != nil {
		t.Fatalf("启用规格应放行, got %v", err)
	}
	cases := []struct {
		name     string
		sku      *model.Sku
		wantText string
	}{
		{"带规格名", &model.Sku{ID: 1, SpecName: "红色 / L", Status: 0}, "红色 / L"},
		{"无规格名回落 SKU 编码", &model.Sku{ID: 2, SkuCode: "P1-ABCD1234", Status: 0}, "P1-ABCD1234"},
		{"编码与规格名皆空回落 SKU ID", &model.Sku{ID: 3, Status: 0}, "SKU 3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := EnsureSkuSellable(tc.sku)
			assertBizCode(t, tc.name, err, codes.FailedPrecondition)
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("错误文案应带可识别的规格标识, got %q, want 含 %q", err.Error(), tc.wantText)
			}
		})
	}
}

// TestEnsureTradeAccess 纯函数：EnsureScope 判定与 require_sellable 收口的统一真值表
// 归属判定永远执行（require_sellable=false 也拦越权），可售判定只在 true 时执行，
// 同一行内归属先于可售——否则跨社区取用会把他人社区的商品/规格名泄露到错误文案里
func TestEnsureTradeAccess(t *testing.T) {
	onSale := &model.Product{ID: 1, CommunityID: sellableTestCommunityID, Name: "SELLABLETEST-上架", Status: 1}
	offSale := &model.Product{ID: 2, CommunityID: sellableTestCommunityID, Name: "SELLABLETEST-仓库中", Status: 0}
	foreign := &model.Product{ID: 3, CommunityID: sellableOtherCommunityID, Name: "SELLABLETEST-他人商品", Status: 1}
	shared := &model.Product{ID: 4, CommunityID: 0, Name: "SELLABLETEST-全平台", Status: 1}
	skuOK := &model.Sku{ID: 11, CommunityID: sellableTestCommunityID, SpecName: "红色 / L", Status: SkuStatusEnabled}
	skuOff := &model.Sku{ID: 12, CommunityID: sellableTestCommunityID, SpecName: "蓝色 / XL", Status: 0}
	skuForeign := &model.Sku{ID: 13, CommunityID: sellableOtherCommunityID, SpecName: "绿色 / M", Status: SkuStatusEnabled}
	skuForeignOff := &model.Sku{ID: 14, CommunityID: sellableOtherCommunityID, SpecName: "黑色 / S", Status: 0}

	cases := []struct {
		name            string
		scope           int64
		p               *model.Product
		sku             *model.Sku
		requireSellable bool
		want            codes.Code
	}{
		{"归属一致+上架+要求可售：放行", sellableTestCommunityID, onSale, skuOK, true, codes.OK},
		{"归属一致+仓库中+要求可售：不可售", sellableTestCommunityID, offSale, skuOK, true, codes.FailedPrecondition},
		{"归属一致+仓库中+不要求可售：放行（管理端/展示口径）", sellableTestCommunityID, offSale, nil, false, codes.OK},
		{"归属一致+禁用规格+要求可售：规格已失效", sellableTestCommunityID, nil, skuOff, true, codes.FailedPrecondition},
		{"商品归属他人：越权", sellableTestCommunityID, foreign, skuOK, true, codes.PermissionDenied},
		{"SKU 归属他人：越权", sellableTestCommunityID, onSale, skuForeign, true, codes.PermissionDenied},
		{"越权优先于不可售（同一行既禁用又归属他人）", sellableTestCommunityID, nil, skuForeignOff, true, codes.PermissionDenied},
		{"不要求可售也拦越权", sellableTestCommunityID, foreign, nil, false, codes.PermissionDenied},
		{"全平台共享数据（归属 0）对社区管理员拒绝", sellableTestCommunityID, shared, nil, false, codes.PermissionDenied},
		{"scope=0：跳过归属校验（他人 SKU 也放行）", 0, nil, skuForeign, true, codes.OK},
		{"scope=0：仍拦不可售（内部调用不放松可售口径）", 0, nil, skuOff, true, codes.FailedPrecondition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := EnsureTradeAccess(tc.scope, tc.p, tc.sku, tc.requireSellable)
			assertBizCode(t, tc.name, err, tc.want)
			if tc.want == codes.PermissionDenied && strings.Contains(err.Error(), "失效") {
				t.Errorf("越权不应泄露可售状态文案, got %q", err.Error())
			}
		})
	}
}

// TestBatchGetSkusTradeGuard 集成：下单批量取 SKU 的三条收口（BatchGetSkus 恒定生效，无开关）
// 前置：同一商品下三条 SKU——启用 / 禁用（等价后台禁用、移除规格、商品软删）/ 归属他人社区
// 断言：
//   - 启用且归属一致：放行
//   - 混入禁用 SKU：整体拒绝（FailedPrecondition，文案带规格名），购物车存量行不得再成交
//   - 混入他人社区 SKU 且传成交社区：整体拒绝（PermissionDenied）
//   - community_id=0：只跳过归属校验，可售校验不放松
//   - 缺失 ID：NotFound（订单明细不允许部分命中）
func TestBatchGetSkusTradeGuard(t *testing.T) {
	env := newSellableTestEnv(t)
	defer env.cleanup()
	ctx := context.Background()

	pID := seedSellableProduct(t, env, sellableTestCommunityID, "SELLABLETEST-下单商品", 1, 0)
	skuOK := seedSellableSku(t, env, pID, sellableTestCommunityID, "SELLABLETEST-SKU-OK", "红色 / L", "", 1)
	skuOff := seedSellableSku(t, env, pID, sellableTestCommunityID, "SELLABLETEST-SKU-OFF", "蓝色 / XL", "", 0)
	skuForeign := seedSellableSku(t, env, pID, sellableOtherCommunityID, "SELLABLETEST-SKU-OTHER", "绿色 / M", "", 1)

	got, err := env.skuSvc.BatchGetSkus(ctx, []int64{skuOK}, sellableTestCommunityID)
	assertBizCode(t, "启用且归属一致", err, codes.OK)
	if len(got) != 1 || got[0].ID != skuOK {
		t.Errorf("返回结果异常: %+v", got)
	}

	_, err = env.skuSvc.BatchGetSkus(ctx, []int64{skuOK, skuOff}, sellableTestCommunityID)
	assertBizCode(t, "混入禁用 SKU", err, codes.FailedPrecondition)
	if !strings.Contains(err.Error(), "蓝色 / XL") {
		t.Errorf("文案应指明失效规格便于前端展示, got %q", err.Error())
	}

	_, err = env.skuSvc.BatchGetSkus(ctx, []int64{skuOK, skuForeign}, sellableTestCommunityID)
	assertBizCode(t, "混入他人社区 SKU", err, codes.PermissionDenied)

	// 不传社区（管理端/内部调用）：归属不校验，禁用仍拦
	if _, err := env.skuSvc.BatchGetSkus(ctx, []int64{skuForeign}, 0); err != nil {
		t.Errorf("community_id=0 应跳过归属校验, got %v", err)
	}
	if _, err := env.skuSvc.BatchGetSkus(ctx, []int64{skuOff}, 0); err == nil {
		t.Error("community_id=0 不应放松可售校验")
	}

	_, err = env.skuSvc.BatchGetSkus(ctx, []int64{skuOK, 999999999}, sellableTestCommunityID)
	assertBizCode(t, "缺失 SKU", err, codes.NotFound)

	// 列表回填口径：FindSkusByIDs 容忍缺失与禁用，不参与交易收口（交易必须走 BatchGetSkus）
	backfill, err := env.skuSvc.FindSkusByIDs(ctx, []int64{skuOK, skuOff, 999999999})
	if err != nil {
		t.Fatalf("FindSkusByIDs 不应报错, got %v", err)
	}
	if len(backfill) != 2 {
		t.Errorf("FindSkusByIDs 应返回命中的 2 条, got %d", len(backfill))
	}
}

// TestBatchGetProductsCommunityScope 集成：商品侧批量取档案的社区归属收口
// 与 TestBatchGetSkusTradeGuard 成对：下单链路两个入口必须传同一个 community_id
// 断言：归属一致放行；归属不一致一律 PermissionDenied（与 require_sellable 无关，且不泄露不可售文案）；
// scope=0 放行；不可售只在要求可售时报 FailedPrecondition
func TestBatchGetProductsCommunityScope(t *testing.T) {
	env := newSellableTestEnv(t)
	defer env.cleanup()
	ctx := context.Background()

	onSale := seedSellableProduct(t, env, sellableTestCommunityID, "SELLABLETEST-在售", 1, 0)
	offSale := seedSellableProduct(t, env, sellableTestCommunityID, "SELLABLETEST-仓库中", 0, 0)
	foreign := seedSellableProduct(t, env, sellableOtherCommunityID, "SELLABLETEST-他人社区", 1, 0)

	got, err := env.svc.BatchGetProducts(ctx, []int64{onSale}, sellableTestCommunityID, true)
	assertBizCode(t, "归属一致", err, codes.OK)
	if len(got) != 1 {
		t.Errorf("返回结果异常: %+v", got)
	}

	// 跨社区取用：即使不要求可售（纯展示口径）也必须拦
	for _, requireSellable := range []bool{true, false} {
		_, err := env.svc.BatchGetProducts(ctx, []int64{onSale, foreign}, sellableTestCommunityID, requireSellable)
		assertBizCode(t, fmt.Sprintf("混入他人社区商品(require_sellable=%v)", requireSellable), err, codes.PermissionDenied)
	}

	// 同一行内归属先于可售：他人社区的仓库中商品报越权，不泄露「已下架」文案
	// （多行请求按主键顺序逐条判定，先命中哪条取决于数据，跨行的先后不在收口承诺内）
	foreignOff := seedSellableProduct(t, env, sellableOtherCommunityID, "SELLABLETEST-他人仓库中", 0, 0)
	_, err = env.svc.BatchGetProducts(ctx, []int64{foreignOff}, sellableTestCommunityID, true)
	assertBizCode(t, "越权优先于不可售", err, codes.PermissionDenied)

	// scope=0：超管/管理端跨社区读取仍放行
	all, err := env.svc.BatchGetProducts(ctx, []int64{onSale, foreign}, 0, false)
	assertBizCode(t, "scope=0 跨社区读", err, codes.OK)
	if len(all) != 2 {
		t.Errorf("scope=0 应返回 2 条, got %d", len(all))
	}

	_, err = env.svc.BatchGetProducts(ctx, []int64{onSale, offSale}, sellableTestCommunityID, true)
	assertBizCode(t, "混入仓库中商品", err, codes.FailedPrecondition)
}

// TestCategoryDisabledOnlyHidesCategories 集成：分类禁用只隐藏分类项，不隐藏商品、不拦交易
// 需求出处（取证结论：文档只要求隐藏分类，故本用例锁定现状而非改交易逻辑）：
//   - docs/需求文档/06-接口设计/03-后台管理接口.md:41「管理端固定含禁用分类，禁用行灰显可再启用；
//     C 端公开接口仅启用分类」——隐藏对象是分类列表本身；
//   - docs/需求文档/09-gRPC接口设计/03-product.proto.md:166 include_disabled 同口径；
//   - 无任何条款要求禁用分类后其下商品下架或不可下单。
//
// 断言：禁用后 C 端分类列表（include_disabled=false）不含该分类、管理端列表（true）仍含；
// 商品列表按分类筛选仍命中；下单取档案（require_sellable=true）仍放行
func TestCategoryDisabledOnlyHidesCategories(t *testing.T) {
	env := newSellableTestEnv(t)
	defer env.cleanup()
	ctx := context.Background()

	now := time.Now().Format("2006-01-02 15:04:05")
	cate := &model.Category{
		CommunityID: sellableTestCommunityID,
		Name:        "SELLABLETEST-分类",
		Status:      1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := env.db.GetDb().Create(cate).Error; err != nil {
		t.Fatalf("创建测试分类失败: %v", err)
	}
	pID := seedSellableProduct(t, env, sellableTestCommunityID, "SELLABLETEST-分类下商品", 1, cate.ID)

	// status 列 default:1，禁用需显式回写（与 UpdateCategory 全量赋值的最终态一致）
	if err := env.db.GetDb().Model(cate).Update("status", 0).Error; err != nil {
		t.Fatalf("禁用测试分类失败: %v", err)
	}

	enabled, err := env.cats.ListCategories(ctx, sellableTestCommunityID, false)
	assertBizCode(t, "C 端启用分类列表", err, codes.OK)
	if containsCategory(enabled, cate.ID) {
		t.Error("禁用分类不应出现在 C 端分类列表")
	}
	full, err := env.cats.ListCategories(ctx, sellableTestCommunityID, true)
	assertBizCode(t, "管理端全量分类列表", err, codes.OK)
	if !containsCategory(full, cate.ID) {
		t.Error("管理端列表应含禁用分类（灰显可再启用）")
	}

	list, total, err := env.svc.ListProducts(ctx, &product.ListProductsReq{
		CommunityId: sellableTestCommunityID,
		CategoryId:  cate.ID,
	})
	assertBizCode(t, "禁用分类下的商品列表", err, codes.OK)
	if total != 1 || len(list) != 1 || list[0].ID != pID {
		t.Errorf("禁用分类不应隐藏其下商品, got total=%d list=%+v", total, list)
	}

	if _, err := env.svc.BatchGetProducts(ctx, []int64{pID}, sellableTestCommunityID, true); err != nil {
		t.Errorf("禁用分类不应拦交易取档, got %v", err)
	}
}

// containsCategory 判断分类列表是否含指定 ID
//
// 参数：
//   - list: 分类列表
//   - id: 目标分类 ID
//
// 返回值：
//   - bool: 命中返回 true
func containsCategory(list []*model.Category, id int64) bool {
	for _, c := range list {
		if c.ID == id {
			return true
		}
	}
	return false
}

// TestBarcodeReleasedByProductSoftDelete 集成：商品软删连带禁用 SKU，条码随即释放可复用
// 条码占用与库级函数索引（main.go ensureSkuBarcodeUniqueIndex）同以「SKU status=1 且商品未删除」为存活口径：
//   - 禁用 SKU 不清空 barcode 字段，只靠 status 让出条码，历史行仍可追溯原条码；
//   - DeleteProduct 必须同事务把其启用 SKU 一并置 0，否则该条码永久占用、其他商品无法保存；
//   - 应用层 ensureSkuBarcodeAvailable 与函数索引必须同构，否则前端能保存、落库撞 1062。
func TestBarcodeReleasedByProductSoftDelete(t *testing.T) {
	env := newSellableTestEnv(t)
	defer env.cleanup()
	ctx := context.Background()
	barcode := "6901234567890"

	oldPID := seedSellableProduct(t, env, sellableTestCommunityID, "SELLABLETEST-占码商品", 1, 0)
	oldSku := seedSellableSku(t, env, oldPID, sellableTestCommunityID, "SELLABLETEST-SKU-BAR", "默认", barcode, 1)
	newPID := seedSellableProduct(t, env, sellableTestCommunityID, "SELLABLETEST-复用商品", 1, 0)

	// 占用中：复用即报错，文案带占用商品名
	err := ensureSkuBarcodeAvailable(env.db.GetDb(), sellableTestCommunityID, newPID, barcode)
	assertBizCode(t, "条码被在售商品占用", err, codes.InvalidArgument)
	if !strings.Contains(err.Error(), "SELLABLETEST-占码商品") {
		t.Errorf("文案应指明占用商品, got %q", err.Error())
	}

	if err := env.svc.DeleteProduct(ctx, oldPID, sellableTestCommunityID); err != nil {
		t.Fatalf("软删商品失败: %v", err)
	}
	sku, err := env.skuSvc.GetSkuByID(ctx, oldSku)
	assertBizCode(t, "读取被连带禁用的 SKU", err, codes.OK)
	if sku.Status != 0 {
		t.Errorf("商品软删应连带禁用 SKU, got status=%d", sku.Status)
	}
	if sku.Barcode != barcode {
		t.Errorf("禁用不应清空条码字段（历史可追溯）, got %q", sku.Barcode)
	}

	// 释放后可复用：应用层放行，与函数索引口径一致
	assertBizCode(t, "软删后复用条码", ensureSkuBarcodeAvailable(env.db.GetDb(), sellableTestCommunityID, newPID, barcode), codes.OK)

	// 扫码口径同步：已删除商品不再命中，否则会扫出不可售商品
	if _, _, err := env.svc.GetByBarcode(ctx, sellableTestCommunityID, barcode); err == nil {
		t.Error("已删除商品的条码不应再被扫中")
	}

	// 下单取档同步：连带禁用后 SKU 侧也会以「规格已失效」拒绝
	_, err = env.skuSvc.BatchGetSkus(ctx, []int64{oldSku}, sellableTestCommunityID)
	assertBizCode(t, "软删商品的 SKU 不可下单", err, codes.FailedPrecondition)
}

// int32Ptr 取 int32 地址，供 proto3 optional 字段显式传值（含零值）
// 参数：
//   - v: 目标值
//
// 返回值：
//   - *int32: 指向 v 副本的指针
func int32Ptr(v int32) *int32 {
	return &v
}

// TestSkuStatusAndBarcodeGuard 集成：独立 SKU 接口可显式禁用，且条码占用不再漏检
// 两条防线各对应一个历史缺陷：
//   - UpdateSkuReq.status 原为裸 int32，「未传」与「传 0」同为 0，服务端判 !=0 后
//     永远无法把 SKU 置为禁用；改 optional 后 nil 才是不改，显式 0 生效；
//   - CreateSku/UpdateSku 原先不做条码占用校验，落库只撞库级函数索引回一个裸 1062；
//     现与商品保存链路共用 ensureSkuBarcodeAvailable，报错为 InvalidArgument 且带占用商品名。
func TestSkuStatusAndBarcodeGuard(t *testing.T) {
	env := newSellableTestEnv(t)
	defer env.cleanup()
	ctx := context.Background()
	occupied := "6901234567891"

	pidA := seedSellableProduct(t, env, sellableTestCommunityID, "SELLABLETEST-占码A", 1, 0)
	skuA := seedSellableSku(t, env, pidA, sellableTestCommunityID, "SELLABLETEST-GUARD-A", "默认", occupied, 1)
	pidB := seedSellableProduct(t, env, sellableTestCommunityID, "SELLABLETEST-待赋码B", 1, 0)
	skuB := seedSellableSku(t, env, pidB, sellableTestCommunityID, "SELLABLETEST-GUARD-B", "默认", "", 1)

	// 未传 status：状态保持启用（optional 的 nil 语义）
	if _, err := env.skuSvc.UpdateSku(ctx, &product.UpdateSkuReq{
		Id: skuA, CommunityId: sellableTestCommunityID, SpecName: "改名不影响状态",
	}); err != nil {
		t.Fatalf("更新 SKU 失败: %v", err)
	}
	sku, err := env.skuSvc.GetSkuByID(ctx, skuA)
	assertBizCode(t, "读取未改状态的 SKU", err, codes.OK)
	if sku.Status != 1 || sku.SpecName != "改名不影响状态" {
		t.Fatalf("未传 status 应保持启用, got status=%d spec_name=%q", sku.Status, sku.SpecName)
	}

	// 赋被占用的条码：应用层拦下，不再是裸 1062
	_, err = env.skuSvc.UpdateSku(ctx, &product.UpdateSkuReq{
		Id: skuB, CommunityId: sellableTestCommunityID, Barcode: occupied,
	})
	assertBizCode(t, "赋码撞在售 SKU", err, codes.InvalidArgument)
	if !strings.Contains(err.Error(), "SELLABLETEST-占码A") {
		t.Errorf("文案应指明占用商品, got %q", err.Error())
	}

	// 显式禁用：optional 传 0 生效（历史缺陷点）
	if _, err := env.skuSvc.UpdateSku(ctx, &product.UpdateSkuReq{
		Id: skuA, CommunityId: sellableTestCommunityID, Status: int32Ptr(0),
	}); err != nil {
		t.Fatalf("显式禁用 SKU 失败: %v", err)
	}
	sku, err = env.skuSvc.GetSkuByID(ctx, skuA)
	assertBizCode(t, "读取被禁用的 SKU", err, codes.OK)
	if sku.Status != 0 {
		t.Fatalf("显式传 status=0 应禁用, got %d", sku.Status)
	}

	// 禁用即让出条码：同一码可赋给 B（与函数索引「status=1 才算占用」同构）
	if _, err := env.skuSvc.UpdateSku(ctx, &product.UpdateSkuReq{
		Id: skuB, CommunityId: sellableTestCommunityID, Barcode: occupied,
	}); err != nil {
		t.Fatalf("复用已释放条码失败: %v", err)
	}

	// CreateSku 同样收口：占用中报错，非法字符报格式错误
	_, err = env.skuSvc.CreateSku(ctx, &product.CreateSkuReq{
		ProductId: pidB, CommunityId: sellableTestCommunityID, SkuCode: "SELLABLETEST-GUARD-C",
		SpecName: "默认", Barcode: occupied,
	})
	assertBizCode(t, "创建时撞占用条码", err, codes.InvalidArgument)

	if _, err := env.skuSvc.CreateSku(ctx, &product.CreateSkuReq{
		ProductId: pidB, CommunityId: sellableTestCommunityID, SkuCode: "SELLABLETEST-GUARD-D",
		SpecName: "默认", Barcode: "abc123",
	}); err == nil {
		t.Error("非数字条码应报错")
	}
}
