package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// RefundConfig 退款配置模型
// 固定单行（id=1）存储退款相关系统配置
type RefundConfig struct {
	base.MysqlBaseModel
	ID                 int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键，固定为 1" json:"id"`
	RefundFreightPolicy string `gorm:"column:refund_freight_policy;type:varchar(20);not null;default:always;comment:运费退还策略:always始终退运费 never不退运费 user_decide用户自行选择" json:"refund_freight_policy"`
	CreatedAt          string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt          string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回退款配置表名
// 返回值：
//   - string: 表名
func (RefundConfig) TableName() string {
	return "refund_config"
}
