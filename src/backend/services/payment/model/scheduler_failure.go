package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// SchedulerFailure 支付对账定时任务失败记录
// 记录渠道回查/挂账检测中处理失败的支付单，供下一轮优先重试；
// 口径与 order 侧 order_scheduler_failures 一致（重试上限 5 次后放弃）
//
//nolint:lll
//go:generate go tool gormgen -struct SchedulerFailure
type SchedulerFailure struct {
	base.MysqlBaseModel
	ID         int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	PaymentNo  string `gorm:"column:payment_no;type:varchar(50);not null;default:'';index;comment:支付单号" json:"payment_no"`
	OrderID    int64  `gorm:"column:order_id;type:bigint;not null;default:0;index;comment:订单 ID" json:"order_id"`
	TaskType   string `gorm:"column:task_type;type:varchar(24);not null;comment:任务类型 reconcile_pending/reconcile_stuck/channel_close" json:"task_type"`
	Status     int32  `gorm:"column:status;type:tinyint;default:1;comment:1待重试 2已处理 3放弃" json:"status"`
	ErrorMsg   string `gorm:"column:error_msg;type:varchar(500);comment:失败原因" json:"error_msg"`
	RetryCount int32  `gorm:"column:retry_count;type:int;default:0;comment:已重试次数，默认 0" json:"retry_count"`
	CreatedAt  string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt  string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回支付对账失败记录表名
//
// 返回值：
//   - string: 表名
func (SchedulerFailure) TableName() string {
	return "payment_scheduler_failures"
}

// 对账任务类型（SchedulerFailure.TaskType）
const (
	// TaskReconcilePending 待支付单的渠道回查
	TaskReconcilePending = "reconcile_pending"
	// TaskReconcileStuck 已关闭单的挂账回查
	TaskReconcileStuck = "reconcile_stuck"
	// TaskChannelClose 渠道侧交易关闭
	TaskChannelClose = "channel_close"
)

// MaxFailureRetry 失败记录最大重试次数，与 order 侧调度器保持一致
const MaxFailureRetry = 5

// 失败记录状态（SchedulerFailure.Status）
const (
	// FailureStatusPending 待重试
	FailureStatusPending = 1
	// FailureStatusDone 已处理（本轮已成功收敛，或被其他路径解决）
	FailureStatusDone = 2
	// FailureStatusAbandoned 放弃（超过重试上限或支付单已不存在）
	FailureStatusAbandoned = 3
)
