package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// 免配送费范围类型（与优惠券商品范围枚举保持一致，预留扩展）
const (
	// FreeShippingScopeTypeExcludeCategory 排除分类（该分类下全部商品不参与）
	FreeShippingScopeTypeExcludeCategory int32 = 4
	// FreeShippingScopeTypeExcludeProduct 排除商品
	FreeShippingScopeTypeExcludeProduct int32 = 5
)

// FreeShippingActivity 满额免配送费活动模型
// 对应免配送费活动表，按社区隔离
// 状态：0禁用 1启用
// 语义：配送订单中「参与免配送费的商品金额合计 >= threshold」则免收配送费；
// 被排除范围（free_shipping_scopes）命中的商品金额不计入门槛。
// 起送金额（社区 min_order_amount）仍按商品总额判断，与本活动门槛独立。
//
//nolint:lll
//go:generate go tool gormgen -struct FreeShippingActivity
type FreeShippingActivity struct {
	base.MysqlBaseModel
	ID          int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CommunityID int64   `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	Name        string  `gorm:"column:name;type:varchar(100);not null;comment:活动名称" json:"name"`
	Threshold   float64 `gorm:"column:threshold;type:decimal(10,2);not null;comment:免配送费门槛（参与商品额）" json:"threshold"`
	StartTime   *string `gorm:"column:start_time;type:datetime;comment:开始时间" json:"start_time"`
	EndTime     *string `gorm:"column:end_time;type:datetime;comment:结束时间" json:"end_time"`
	Status      int32   `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	CreatedAt   string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`

	// Scopes 排除范围（非持久化字段，查询时组装）
	Scopes []*FreeShippingScope `gorm:"-" json:"scopes,omitempty"`
}

// TableName 返回免配送费活动表名
// 返回值：
//   - string: 表名
func (FreeShippingActivity) TableName() string {
	return "free_shipping_activities"
}

// FreeShippingScope 免配送费排除范围模型
// 记录活动排除的分类/商品；当前仅排除语义（scope_type 4/5），保留全场/指定类型扩展
//
//nolint:lll
//go:generate go tool gormgen -struct FreeShippingScope
type FreeShippingScope struct {
	base.MysqlBaseModel
	ID         int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	ActivityID int64  `gorm:"column:activity_id;type:bigint;not null;index;comment:活动 ID" json:"activity_id"`
	ScopeType  int32  `gorm:"column:scope_type;type:tinyint;not null;comment:范围类型 4=排除分类 5=排除商品" json:"scope_type"`
	CategoryID int64  `gorm:"column:category_id;type:bigint;not null;default:0;comment:分类 ID（scope_type=4 时有效）" json:"category_id"`
	ProductID  int64  `gorm:"column:product_id;type:bigint;not null;default:0;comment:商品 ID（scope_type=5 时有效）" json:"product_id"`
	CreatedAt  string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`

	// CategoryName / ProductName 展示名（非持久化字段，查询时回填）
	CategoryName string `gorm:"-" json:"category_name,omitempty"`
	ProductName  string `gorm:"-" json:"product_name,omitempty"`
}

// TableName 返回免配送费范围表名
// 返回值：
//   - string: 表名
func (FreeShippingScope) TableName() string {
	return "free_shipping_scopes"
}
