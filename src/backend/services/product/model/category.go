package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Category 商品分类模型
// 对应商品分类表，支持按社区隔离、层级结构
type Category struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;index" json:"community_id"`
	ParentID    int64  `gorm:"column:parent_id;type:bigint;default:0;index" json:"parent_id"`
	Name        string `gorm:"column:name;type:varchar(50);not null" json:"name"`
	Icon        string `gorm:"column:icon;type:varchar(255)" json:"icon"`
	Sort        int32  `gorm:"column:sort;type:int;default:0" json:"sort"`
	Status      int32  `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	CreatedAt   string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回分类表名
// 返回值：
//   - string: 表名
func (Category) TableName() string {
	return "categories"
}
