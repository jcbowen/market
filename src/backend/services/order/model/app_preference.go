package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// AppPreference 应用偏好模型（应用页「常用应用」，社区级单行配置）
// 常用应用为社区级共享配置（同社区跨账号/跨设备一致），每社区一行，覆盖式保存
//
//nolint:lll
//go:generate go tool gormgen -struct AppPreference
type AppPreference struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	// CommunityID 社区 ID（unique 单列唯一：同社区仅一行配置）
	CommunityID int64  `gorm:"column:community_id;type:bigint;not null;unique;comment:社区 ID" json:"community_id"`
	AppKeysJSON string `gorm:"column:app_keys_json;type:varchar(500);comment:常用应用 key 列表 JSON 数组" json:"-"`
	CreatedAt   string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt   string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回应用偏好表名
// 返回值：
//   - string: 表名
func (AppPreference) TableName() string {
	return "app_preferences"
}
