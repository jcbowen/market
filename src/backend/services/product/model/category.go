package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Category 商品分类模型
// 对应商品分类表，支持按社区隔离、层级结构
type Category struct {
	base.MysqlBaseModel
	ID          int64  `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	ParentID    int64  `gorm:"column:parent_id;type:bigint;default:0;index;comment:父分类 ID，0 为顶级" json:"parent_id"`
	Name        string `gorm:"column:name;type:varchar(50);not null;comment:分类名称" json:"name"`
	Icon        string `gorm:"column:icon;type:varchar(255);comment:图标 URL" json:"icon"`
	Sort        int32  `gorm:"column:sort;type:int;default:0;comment:排序，默认0" json:"sort"`
	Status      int32  `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	CreatedAt   string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回分类表名
// 返回值：
//   - string: 表名
func (Category) TableName() string {
	return "categories"
}
