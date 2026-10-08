package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// Banner Banner 轮播图模型
// 对应 banner 表，按社区与展示位置隔离
// 展示位置：1首页 2分类页
// 状态：0禁用 1启用
//
//nolint:lll
//go:generate go tool gormgen -struct Banner
type Banner struct {
	base.MysqlBaseModel
	ID          int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	CommunityID int64   `gorm:"column:community_id;type:bigint;not null;index" json:"community_id"`
	Title       string  `gorm:"column:title;type:varchar(100)" json:"title"`
	ImageURL    string  `gorm:"column:image_url;type:varchar(255);not null" json:"image_url"`
	LinkURL     string  `gorm:"column:link_url;type:varchar(255)" json:"link_url"`
	Position    int32   `gorm:"column:position;type:tinyint;not null;comment:1首页 2分类页" json:"position"`
	Sort        int32   `gorm:"column:sort;type:int;default:0" json:"sort"`
	Status      int32   `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	StartTime   *string `gorm:"column:start_time;type:datetime" json:"start_time"`
	EndTime     *string `gorm:"column:end_time;type:datetime" json:"end_time"`
	CreatedAt   string  `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt   string  `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回 banner 表名
// 返回值：
//   - string: 表名
func (Banner) TableName() string {
	return "banners"
}
