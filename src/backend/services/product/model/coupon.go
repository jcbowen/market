package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Coupon 优惠券模型
// 对应优惠券表，支持满减券、折扣券等类型，按社区隔离
// 状态：0禁用 1启用
// 类型：1满减券 2折扣券
// 状态机：publish_status（0草稿 1已发布 2已下架）+ grant_status（0停止发放 1发放中）
// 领取规则：total_count 总发行量，per_user_limit 每人限领；仅 grant_status=1 可领取
// 有效规则（expire_type）：1=领取后 valid_days 天过期；2=指定可用范围 use_start_time/use_end_time
// start_time/end_time 始终为「领取时间窗口」
// 商品范围：coupon_product_scopes 行缺省=全场通用；指定/排除可组合
//
//nolint:lll
//go:generate go tool gormgen -struct Coupon
type Coupon struct {
	base.MysqlBaseModel
	ID              int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CommunityID     int64   `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	Name            string  `gorm:"column:name;type:varchar(100);not null;comment:优惠券名称" json:"name"`
	Type            int32   `gorm:"column:type;type:tinyint;not null;comment:1满减券 2折扣券" json:"type"`
	Threshold       float64 `gorm:"column:threshold;type:decimal(10,2);default:0.00;comment:使用门槛金额" json:"threshold"`
	DiscountValue   float64 `gorm:"column:discount_value;type:decimal(10,2);default:0.00;comment:优惠值（满减金额 / 折扣率如 0.85）" json:"discount_value"`
	TotalCount      int32   `gorm:"column:total_count;type:int;default:0;comment:发放总量" json:"total_count"`
	ReceivedCount   int32   `gorm:"column:received_count;type:int;default:0;comment:已领取量，默认0" json:"received_count"`
	UsedCount       int32   `gorm:"column:used_count;type:int;default:0;comment:已使用量，默认0" json:"used_count"`
	PerUserLimit    int32   `gorm:"column:per_user_limit;type:int;default:1;comment:每人限领，默认1" json:"per_user_limit"`
	StartTime       *string `gorm:"column:start_time;type:datetime;comment:领取窗口开始" json:"start_time"`
	EndTime         *string `gorm:"column:end_time;type:datetime;comment:领取窗口结束" json:"end_time"`
	ValidDays       int32   `gorm:"column:valid_days;type:int;default:0;comment:expire_type=1 时领取后有效天数" json:"valid_days"`
	Status          int32   `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	PublishStatus   int32   `gorm:"column:publish_status;type:tinyint;default:1;comment:发布状态 0草稿 1已发布 2已下架" json:"publish_status"`
	GrantStatus     int32   `gorm:"column:grant_status;type:tinyint;default:1;comment:发放状态 0停止发放 1发放中" json:"grant_status"`
	IsCenterVisible int32   `gorm:"column:is_center_visible;type:tinyint;default:1;comment:是否进入领券中心 0否 1是" json:"is_center_visible"`
	ExpireType      int32   `gorm:"column:expire_type;type:tinyint;default:1;comment:到期类型 1领取后N天 2指定可用范围" json:"expire_type"`
	UseStartTime    *string `gorm:"column:use_start_time;type:datetime;comment:可用范围开始（expire_type=2）" json:"use_start_time"`
	UseEndTime      *string `gorm:"column:use_end_time;type:datetime;comment:可用范围结束（expire_type=2）" json:"use_end_time"`
	RuleText        string  `gorm:"column:rule_text;type:varchar(1000);comment:规则文案" json:"rule_text"`
	CreatedAt       string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt       string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`

	// Scopes 商品范围（非持久化字段，查询时组装）
	Scopes []*CouponProductScope `gorm:"-" json:"scopes,omitempty"`
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
// 来源（source）：1用户自领 2后台手动发放；后台发放时记录经手人（operator_id/operator_name 快照）
//
//nolint:lll
//go:generate go tool gormgen -struct UserCoupon
type UserCoupon struct {
	base.MysqlBaseModel
	ID            int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	UserID        int64   `gorm:"column:user_id;type:bigint;not null;index;comment:用户 ID" json:"user_id"`
	CouponID      int64   `gorm:"column:coupon_id;type:bigint;not null;index;comment:优惠券 ID" json:"coupon_id"`
	Name          string  `gorm:"column:name;type:varchar(100);not null;comment:优惠券名称" json:"name"`
	Type          int32   `gorm:"column:type;type:tinyint;not null;comment:1满减券 2折扣券" json:"type"`
	Threshold     float64 `gorm:"column:threshold;type:decimal(10,2);default:0.00;comment:使用门槛金额" json:"threshold"`
	DiscountValue float64 `gorm:"column:discount_value;type:decimal(10,2);default:0.00;comment:优惠值（满减金额 / 折扣率如 0.85）" json:"discount_value"`
	Status        int32   `gorm:"column:status;type:tinyint;default:1;comment:1未使用 2已使用 3已过期" json:"status"`
	OrderID       int64   `gorm:"column:order_id;type:bigint;default:0;comment:关联订单 ID" json:"order_id"`
	Source        int32   `gorm:"column:source;type:tinyint;default:1;comment:来源 1用户自领 2后台发放" json:"source"`
	OperatorID    int64   `gorm:"column:operator_id;type:bigint;default:0;comment:发放经手人 ID（后台发放时）" json:"operator_id"`
	OperatorName  string  `gorm:"column:operator_name;type:varchar(100);comment:发放经手人用户名快照" json:"operator_name"`
	ExpireAt      *string `gorm:"column:expire_at;type:datetime;comment:过期时间（领取时间 + valid_days）" json:"expire_at"`
	CreatedAt     string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt     string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回用户优惠券表名
// 返回值：
//   - string: 表名
func (UserCoupon) TableName() string {
	return "user_coupons"
}

// 优惠券商品范围类型
const (
	// CouponScopeTypeAll 全场通用（等价于无范围行，显式记录用）
	CouponScopeTypeAll int32 = 1
	// CouponScopeTypeIncludeCategory 指定分类（该分类下全部商品）
	CouponScopeTypeIncludeCategory int32 = 2
	// CouponScopeTypeIncludeProduct 指定商品
	CouponScopeTypeIncludeProduct int32 = 3
	// CouponScopeTypeExcludeCategory 排除分类
	CouponScopeTypeExcludeCategory int32 = 4
	// CouponScopeTypeExcludeProduct 排除商品
	CouponScopeTypeExcludeProduct int32 = 5
)

// CouponProductScope 优惠券商品范围模型
// 记录券适用的分类/商品范围；无行=全场通用，指定与排除可组合
//
//nolint:lll
//go:generate go tool gormgen -struct CouponProductScope
type CouponProductScope struct {
	base.MysqlBaseModel
	ID         int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CouponID   int64  `gorm:"column:coupon_id;type:bigint;not null;index;comment:优惠券 ID" json:"coupon_id"`
	ScopeType  int32  `gorm:"column:scope_type;type:tinyint;not null;comment:1全场 2指定分类 3指定商品 4排除分类 5排除商品" json:"scope_type"`
	CategoryID int64  `gorm:"column:category_id;type:bigint;not null;default:0;comment:分类 ID" json:"category_id"`
	ProductID  int64  `gorm:"column:product_id;type:bigint;not null;default:0;comment:商品 ID" json:"product_id"`
	CreatedAt  string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`

	// CategoryName / ProductName 展示名（非持久化字段，查询时回填）
	CategoryName string `gorm:"-" json:"category_name,omitempty"`
	ProductName  string `gorm:"-" json:"product_name,omitempty"`
}

// TableName 返回优惠券商品范围表名
// 返回值：
//   - string: 表名
func (CouponProductScope) TableName() string {
	return "coupon_product_scopes"
}
