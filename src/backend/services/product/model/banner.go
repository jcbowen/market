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
	ID          int64   `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	CommunityID int64   `gorm:"column:community_id;type:bigint;not null;index;comment:社区 ID" json:"community_id"`
	Title       string  `gorm:"column:title;type:varchar(100);comment:标题" json:"title"`
	ImageURL    string  `gorm:"column:image_url;type:varchar(255);not null;comment:图片 URL" json:"image_url"`
	LinkURL     string  `gorm:"column:link_url;type:varchar(255);comment:跳转链接" json:"link_url"`
	Position    int32   `gorm:"column:position;type:tinyint;not null;comment:1首页 2分类页" json:"position"`
	Sort        int32   `gorm:"column:sort;type:int;default:0;comment:排序值" json:"sort"`
	Status      int32   `gorm:"column:status;type:tinyint;default:1;comment:0禁用 1启用" json:"status"`
	StartTime   *string `gorm:"column:start_time;type:datetime;comment:上架开始时间" json:"start_time"`
	EndTime     *string `gorm:"column:end_time;type:datetime;comment:上架结束时间" json:"end_time"`
	CreatedAt   string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回 banner 表名
// 返回值：
//   - string: 表名
func (Banner) TableName() string {
	return "banners"
}
