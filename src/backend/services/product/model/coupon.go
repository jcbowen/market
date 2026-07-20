package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Coupon 优惠券模型
// 对应优惠券表，支持满减券、折扣券等类型，按社区隔离
// 状态：0禁用 1启用
// 类型：1满减券 2折扣券
// 领取规则：total_count 总发行量，per_user_limit 每人限领
// 有效规则：start_time/end_time 为领取时间窗口，valid_days 为领取后有效天数
//
//nolint:lll
//go:generate go tool gormgen -struct Coupon
type Coupon struct {
	base.MysqlBaseModel
	ID            int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	CommunityID   int64   `gorm:"column:community_id;type:bigint;not null;index" json:"community_id"`
	Name          string  `gorm:"column:name;type:varchar(100);not null" json:"name"`
	Type          int32   `gorm:"column:type;type:tinyint;not null;comment:1满减券 2折扣券" json:"type"`
	Threshold     float64 `gorm:"column:threshold;type:decimal(10,2);default:0" json:"threshold"`
	DiscountValue float64 `gorm:"column:discount_value;type:decimal(10,2);default:0" json:"discount_value"`
	TotalCount    int32   `gorm:"column:total_count;type:int;default:0" json:"total_count"`
	ReceivedCount int32   `gorm:"column:received_count;type:int;default:0" json:"received_count"`
	UsedCount     int32   `gorm:"column:used_count;type:int;default:0" json:"used_count"`
	PerUserLimit  int32   `gorm:"column:per_user_limit;type:int;default:1" json:"per_user_limit"`
	StartTime     string  `gorm:"column:start_time;type:datetime" json:"start_time"`
	EndTime       string  `gorm:"column:end_time;type:datetime" json:"end_time"`
	ValidDays     int32   `gorm:"column:valid_days;type:int;default:0;comment:0表示固定 end_time" json:"valid_days"`
	Status        int32   `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	CreatedAt     string  `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt     string  `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回优惠券表名
// 返回值：
//   - string: 表名
func (Coupon) TableName() string {
	return "coupons"
}

// UserCoupon 用户优惠券模型
// 记录用户领取的优惠券及使用状态
// 状态：1未使用 2已使用 3已过期
//
//nolint:lll
//go:generate go tool gormgen -struct UserCoupon
type UserCoupon struct {
	base.MysqlBaseModel
	ID         int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	UserID     int64   `gorm:"column:user_id;type:bigint;not null;index" json:"user_id"`
	CouponID   int64   `gorm:"column:coupon_id;type:bigint;not null;index" json:"coupon_id"`
	Name       string  `gorm:"column:name;type:varchar(100);not null" json:"name"`
	Type       int32   `gorm:"column:type;type:tinyint;not null" json:"type"`
	Threshold     float64 `gorm:"column:threshold;type:decimal(10,2);default:0" json:"threshold"`
	DiscountValue float64 `gorm:"column:discount_value;type:decimal(10,2);default:0" json:"discount_value"`
	Status     int32   `gorm:"column:status;type:tinyint;default:1;comment:1未使用 2已使用 3已过期" json:"status"`
	OrderID    int64   `gorm:"column:order_id;type:bigint;default:0" json:"order_id"`
	ExpireAt   string  `gorm:"column:expire_at;type:datetime" json:"expire_at"`
	CreatedAt  string  `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt  string  `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回用户优惠券表名
// 返回值：
//   - string: 表名
func (UserCoupon) TableName() string {
	return "user_coupons"
}
