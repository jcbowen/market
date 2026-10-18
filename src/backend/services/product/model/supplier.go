package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Supplier 供应商模型
// 进销存基础档案：按社区隔离，供采购单/入库单引用
type Supplier struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	Name        string `gorm:"column:name;type:varchar(100);not null;comment:供应商名称" json:"name"`
	ContactName string `gorm:"column:contact_name;type:varchar(50);default:'';comment:联系人" json:"contact_name"`
	Phone       string `gorm:"column:phone;type:varchar(20);default:'';comment:联系电话" json:"phone"`
	Address     string `gorm:"column:address;type:varchar(255);default:'';comment:地址" json:"address"`
	Remark      string `gorm:"column:remark;type:varchar(255);default:'';comment:备注" json:"remark"`
	Status      int32  `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	CreatedAt   string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回供应商表名
// 返回值：
//   - string: 表名
func (Supplier) TableName() string {
	return "suppliers"
}
