package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// RefundConfig 退款配置模型
// 固定单行（id=1）存储退款相关系统配置
type RefundConfig struct {
	base.MysqlBaseModel
	ID                  int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键，固定为 1" json:"id"`
	RefundFreightPolicy string `gorm:"column:refund_freight_policy;type:varchar(20);not null;default:always;comment:运费退还策略:always始终退运费 never不退运费 user_decide用户自行选择" json:"refund_freight_policy"`
	// CompletedRefundHours 已完成单售后窗口（小时）：已完成(5)订单在完成后该时长内允许 C 端申请退款，0=不允许；
	// 仅约束 C 端自助申请，后台代客退款不受窗口限制
	CompletedRefundHours int32  `gorm:"column:completed_refund_hours;type:int;not null;default:168;comment:已完成单售后窗口(小时)，完成后该时长内C端可申请退款，0=不允许" json:"completed_refund_hours"`
	CreatedAt            string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt            string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回退款配置表名
// 返回值：
//   - string: 表名
func (RefundConfig) TableName() string {
	return "refund_config"
}
