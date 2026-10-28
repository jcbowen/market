package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// PosShift 收银台班次模型（交班体系）
// 收银前必须开工（无进行中班次不能收银，网关下单前校验）；
// 交班时生成统计快照（收款/退款按支付方式分组）并置为已结束。
// 状态：1进行中 2已结束
//
//nolint:lll
//go:generate go tool gormgen -struct PosShift
type PosShift struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	// CommunityID/OperatorID/ActiveFlag 三者组成复合唯一索引 uq_shift_active：
	// 「同社区同操作员至多一条进行中班次」由库层保证，双击开工与 POS 弱网重发不再产生双开班次
	CommunityID  int64  `gorm:"column:community_id;type:bigint;not null;index;uniqueIndex:uq_shift_active,priority:1;comment:社区 ID" json:"community_id"`
	OperatorID   int64  `gorm:"column:operator_id;type:bigint;not null;index;uniqueIndex:uq_shift_active,priority:2;comment:操作员（收银员/管理员）ID" json:"operator_id"`
	OperatorName string `gorm:"column:operator_name;type:varchar(50);comment:操作员姓名快照" json:"operator_name"`
	StartTime    string `gorm:"column:start_time;type:datetime;not null;comment:开工时间" json:"start_time"`
	// EndTime 交班时间；进行中班次为 NULL
	EndTime *string `gorm:"column:end_time;type:datetime;comment:交班时间（进行中为 NULL）" json:"end_time"`
	// Status 1进行中 2已结束
	Status int32 `gorm:"column:status;type:tinyint;default:1;comment:1进行中 2已结束" json:"status"`
	// ActiveFlag 活跃标记：进行中=1、已结束=NULL。
	// MySQL 唯一索引允许多条 NULL，故历史班次（已交班）不受 uq_shift_active 约束，
	// 只有进行中班次互相排斥——这正是「业务校验 + 库层兜底」两全的形态
	ActiveFlag *int8  `gorm:"column:active_flag;type:tinyint;uniqueIndex:uq_shift_active,priority:3;comment:活跃标记：进行中=1 已结束=NULL（供复合唯一索引互斥进行中班次）" json:"-"`
	Note       string `gorm:"column:note;type:varchar(255);comment:班次备注（开工/交班说明）" json:"note"`
	// StatisticsJSON 交班统计快照 JSON（PosShiftStatistics 结构）；进行中为空串（实时计算）
	StatisticsJSON string `gorm:"column:statistics_json;type:text;comment:交班统计快照 JSON" json:"-"`
	CreatedAt      string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt      string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回收银班次表名
// 返回值：
//   - string: 表名
func (PosShift) TableName() string {
	return "pos_shifts"
}
