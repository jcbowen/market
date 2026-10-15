package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// PosHeldOrder 收银台挂单模型（G 收银台代客下单，决策点⑥）
// 未成单草稿：后端暂存（跨设备/跨收银员不丢单），不生成正式订单、不占库存；
// 结账成功或顾客放弃后由网关删除
//
//nolint:lll
//go:generate go tool gormgen -struct PosHeldOrder
type PosHeldOrder struct {
	base.MysqlBaseModel
	ID           int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CommunityID  int64   `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	OperatorID   int64   `gorm:"column:operator_id;type:bigint;default:0;comment:挂单收银员 ID" json:"operator_id"`
	OperatorName string  `gorm:"column:operator_name;type:varchar(50);comment:挂单收银员姓名快照" json:"operator_name"`
	UserID       int64   `gorm:"column:user_id;type:bigint;default:0;comment:会员 ID（0=散客）" json:"user_id"`
	UserName     string  `gorm:"column:user_name;type:varchar(50);comment:会员名称快照" json:"user_name"`
	// ItemsJSON 商品明细快照 JSON（[]PosHeldOrderItem），恢复时由 service 反序列化
	ItemsJSON   string  `gorm:"column:items_json;type:text;comment:商品明细快照 JSON" json:"-"`
	ItemCount   int32   `gorm:"column:item_count;type:int;default:0;comment:明细行数" json:"item_count"`
	TotalAmount float64 `gorm:"column:total_amount;type:decimal(10,2);default:0.00;comment:快照合计金额（结账时以服务端实时计价为准）" json:"total_amount"`
	Remark      string  `gorm:"column:remark;type:varchar(255);comment:备注" json:"remark"`
	CreatedAt   string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
}

// TableName 返回收银台挂单表名
// 返回值：
//   - string: 表名
func (PosHeldOrder) TableName() string {
	return "pos_held_orders"
}
