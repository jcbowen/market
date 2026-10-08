package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// FullReduction 满减活动模型
// 对应满减活动表，按社区隔离
// 状态：0禁用 1启用
// 当订单金额满足 threshold 时，减免 reduce_amount
//
//nolint:lll
//go:generate go tool gormgen -struct FullReduction
type FullReduction struct {
	base.MysqlBaseModel
	ID          int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	CommunityID int64   `gorm:"column:community_id;type:bigint;not null;index" json:"community_id"`
	Name        string  `gorm:"column:name;type:varchar(100);not null" json:"name"`
	Threshold    float64 `gorm:"column:threshold;type:decimal(10,2);not null" json:"threshold"`
	ReduceAmount float64 `gorm:"column:reduce_amount;type:decimal(10,2);not null" json:"reduce_amount"`
	StartTime   *string `gorm:"column:start_time;type:datetime" json:"start_time"`
	EndTime     *string `gorm:"column:end_time;type:datetime" json:"end_time"`
	Status      int32   `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	CreatedAt   string  `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt   string  `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回满减活动表名
// 返回值：
//   - string: 表名
func (FullReduction) TableName() string {
	return "full_reduction_rules"
}
