package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// SchedulerFailure 订单定时任务失败记录
// 用于记录自动取消/自动完成过程中处理失败的订单，便于后续重试
//
//nolint:lll
//go:generate go tool gormgen -struct SchedulerFailure
type SchedulerFailure struct {
	base.MysqlBaseModel
	ID        int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	OrderID   int64  `gorm:"column:order_id;type:bigint;not null;index;comment:订单 ID" json:"order_id"`
	TaskType  string `gorm:"column:task_type;type:varchar(20);not null;comment:任务类型 timeout_cancel/auto_complete" json:"task_type"`
	Status    int32  `gorm:"column:status;type:tinyint;default:1;comment:1待重试 2已处理 3放弃" json:"status"`
	ErrorMsg  string `gorm:"column:error_msg;type:varchar(500);comment:失败原因" json:"error_msg"`
	RetryCount int32 `gorm:"column:retry_count;type:int;default:0;comment:已重试次数，默认 0" json:"retry_count"`
	CreatedAt string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回定时任务失败记录表名
//
// 返回值：
//   - string: 表名
func (SchedulerFailure) TableName() string {
	return "order_scheduler_failures"
}
