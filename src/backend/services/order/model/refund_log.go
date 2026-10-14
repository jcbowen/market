package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// RefundLog 退款审批日志模型
// 记录退款单的审核/重试操作轨迹：谁在何时做了什么动作（同意/拒绝/重试）及备注
// 动作：approve 同意 / reject 拒绝 / retry 重试
//
//nolint:lll
//go:generate go tool gormgen -struct RefundLog
type RefundLog struct {
	base.MysqlBaseModel
	ID           int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	RefundID     int64  `gorm:"column:refund_id;type:bigint;not null;index;comment:退款单 ID" json:"refund_id"`
	Action       string `gorm:"column:action;type:varchar(20);not null;comment:动作：approve/reject/retry" json:"action"`
	OperatorType string `gorm:"column:operator_type;type:varchar(20);comment:操作者类型（admin/system）" json:"operator_type"`
	OperatorID   int64  `gorm:"column:operator_id;type:bigint;default:0;comment:操作者 ID" json:"operator_id"`
	Remark       string `gorm:"column:remark;type:varchar(255);comment:备注（拒绝原因/重试结果等）" json:"remark"`
	CreatedAt    string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
}

// TableName 返回退款审批日志表名
// 返回值：
//   - string: 表名
func (RefundLog) TableName() string {
	return "refund_logs"
}
