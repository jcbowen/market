package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"

	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/services/product/model"
)

/**
 * 优惠券过期核销回归测试（A1）
 *
 * 盯的故障形态：expire_at 判定曾用 Go 侧字典序比较——写入是 "2006-01-02 15:04:05"，
 * 而 DSN parseTime=True 时 datetime 列读回 string 字段是 RFC3339（第 11 位 'T' 恒大于
 * 空格），`*uc.ExpireAt < now` 永远为 false，过期券照样核销成功。
 * 现口径：过期判定在库里比（条件更新带 expire_at > NOW()），与读回形态无关。
 *
 * 运行：
 *   TEST_MYSQL_PASSWORD=xxx go test ./services/product/service/ -run TestUseCouponExpiry -v
 *
 * 测试数据用 user_id=99100188 隔离，结束即清理。
 */

// couponExpiryTestUserID 过期核销用例专用用户 ID（兼作清理依据）
const couponExpiryTestUserID int64 = 99100188

// newCouponExpiryTestDB 连接测试库并迁移 user_coupons；TEST_MYSQL_PASSWORD 未设置时 skip
// 参数：
//   - t: 测试对象
//
// 返回值：
//   - *gorm.DB: 测试库句柄（用例结束自动清理）
//
// 使用示例：
//   - db := newCouponExpiryTestDB(t)
func newCouponExpiryTestDB(t *testing.T) *mysql.Instance {
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
	// Coupon 也要迁移：UseCoupon 核销会 UPDATE coupons.used_count，CI 干净库缺该表必报 1146
	if err := dbInst.GetDb().AutoMigrate(&model.UserCoupon{}, &model.Coupon{}); err != nil {
		t.Fatalf("测试库迁移失败: %v", err)
	}
	t.Cleanup(func() {
		_ = dbInst.GetDb().Where("user_id = ?", couponExpiryTestUserID).Delete(&model.UserCoupon{})
	})
	return dbInst
}

// insertUserCoupon 写入一张未使用用户券，expireAt 为 nil 表示不限期
// 过期时间按业务写入格式（"2006-01-02 15:04:05"）落库，与线上一致
func insertUserCoupon(t *testing.T, db *mysql.Instance, expireAt *string) int64 {
	t.Helper()
	now := time.Now().Format("2006-01-02 15:04:05")
	uc := &model.UserCoupon{
		UserID:    couponExpiryTestUserID,
		CouponID:  couponExpiryTestUserID,
		Name:      "COUPEXPIRY-TEST",
		Type:      1,
		Status:    1,
		ExpireAt:  expireAt,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := db.GetDb().Create(uc).Error; err != nil {
		t.Fatalf("写入测试用户券失败: %v", err)
	}
	return uc.ID
}

// TestUseCouponExpiry 过期券必须拒签，未过期与不限期券必须放行
func TestUseCouponExpiry(t *testing.T) {
	dbInst := newCouponExpiryTestDB(t)
	svc := NewMarketingService(dbInst)
	ctx := context.Background()

	// 前提取证的读回形态：parseTime=True 下 datetime 读回 string 是 RFC3339，
	// 一旦该形态变化（例如驱动或配置改动），下面的字典序回归断言就失去意义，先钉住
	past := time.Now().Add(-24 * time.Hour).Format("2006-01-02 15:04:05")
	readbackID := insertUserCoupon(t, dbInst, &past)
	var readback model.UserCoupon
	if err := dbInst.GetDb().First(&readback, readbackID).Error; err != nil {
		t.Fatalf("读回测试用户券失败: %v", err)
	}
	if readback.ExpireAt == nil || !strings.Contains(*readback.ExpireAt, "T") {
		t.Fatalf("读回的 expire_at 形态 = %v, 期望 RFC3339（含 T），前提不成立则本用例无效", readback.ExpireAt)
	}

	t.Run("已过期拒绝核销", func(t *testing.T) {
		expired := time.Now().Add(-time.Hour).Format("2006-01-02 15:04:05")
		id := insertUserCoupon(t, dbInst, &expired)
		err := svc.UseCoupon(ctx, &product.UseCouponReq{UserCouponId: id, OrderId: 99100188})
		if err == nil || !strings.Contains(err.Error(), "已过期") {
			t.Fatalf("过期券应被拒绝且文案含「已过期」，实际: %v", err)
		}
		var status int32
		if err := dbInst.GetDb().Model(&model.UserCoupon{}).Where("id = ?", id).
			Pluck("status", &status).Error; err != nil || status != 1 {
			t.Fatalf("被拒的过期券状态不应改变, status=%d err=%v", status, err)
		}
	})

	t.Run("未过期可核销", func(t *testing.T) {
		valid := time.Now().Add(24 * time.Hour).Format("2006-01-02 15:04:05")
		id := insertUserCoupon(t, dbInst, &valid)
		if err := svc.UseCoupon(ctx, &product.UseCouponReq{UserCouponId: id, OrderId: 99100188}); err != nil {
			t.Fatalf("未过期券应核销成功: %v", err)
		}
	})

	t.Run("不限期可核销", func(t *testing.T) {
		id := insertUserCoupon(t, dbInst, nil)
		if err := svc.UseCoupon(ctx, &product.UseCouponReq{UserCouponId: id, OrderId: 99100188}); err != nil {
			t.Fatalf("expire_at 为 NULL 的券应核销成功: %v", err)
		}
	})
}
