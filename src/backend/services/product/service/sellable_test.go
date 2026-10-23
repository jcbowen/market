package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"google.golang.org/grpc/codes"

	"github.com/jcbowen/market/src/backend/common/bizerr"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

/**
 * 「可售」判定收口测试（P1-4）
 *
 * 背景：定时上架商品在调度器到点前 status=0（仓库中），与已下架商品同态；
 * 此前下单（BatchGetProducts 无状态过滤）与加购（GetProduct 不判状态）都不拦，
 * 未到点的商品就能成交。修法是在商品服务收口唯一口径（status=1），
 * 交易入口经 require_sellable 声明意图。
 *
 * 覆盖：
 *   - TestEnsureProductSellable          纯函数：上架放行，下架/删除/定时上架中拒绝
 *   - TestBatchGetProductsRequireSellable 集成：require_sellable=true 时任一不可售即整体报错；
 *                                          false 时保持原行为（不过滤状态）
 *
 * 运行方式（env 守卫跳过，与 stock_concurrency_test 同约定）：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/product/service/ -run "Sellable" -v
 *
 * 测试数据使用 99100098 社区 ID 与 SELLABLETEST- 名称前缀，结束即清理。
 */

// sellableTestCommunityID 可售校验测试专用社区 ID（与库存测试的 99100099 错开，作为清理依据）
const sellableTestCommunityID int64 = 99100098

// sellableTestEnv 可售校验测试环境句柄
type sellableTestEnv struct {
	db      *mysql.Instance
	svc     *ProductService
	cleanup func()
}

// newSellableTestEnv 建测试库连接、迁移商品表，返回被测服务与清理函数
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
	if err := dbInst.GetDb().AutoMigrate(&model.Product{}); err != nil {
		t.Fatalf("测试库迁移失败: %v", err)
	}
	env := &sellableTestEnv{
		db:  dbInst,
		svc: NewProductService(dbInst),
	}
	env.cleanup = func() {
		_ = dbInst.GetDb().Where("community_id = ?", sellableTestCommunityID).Delete(&model.Product{})
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
