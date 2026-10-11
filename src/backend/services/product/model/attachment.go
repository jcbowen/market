package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// AttachmentConfig 附件存储配置模型（单行存储，id 固定为 1）
// 存储附件存储模式（本地/OSS）及 OSS 访问凭证，由后台「系统管理 → 附件配置」维护
// 读写均通过 product 服务，gateway 不直接访问数据库
//
//nolint:lll
//go:generate go tool gormgen -struct AttachmentConfig
type AttachmentConfig struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey" json:"id"`

	// 存储类型：local / oss（预留 cos）
	StorageType string `gorm:"column:storage_type;type:varchar(16);not null;default:'local'" json:"storage_type"`

	// 自定义访问域名，空则本地模式按请求域名动态拼接、OSS 用 bucket 域名
	VisitDomain string `gorm:"column:visit_domain;type:varchar(255);not null;default:''" json:"visit_domain"`

	// 阿里云 OSS
	OssAccessKeyId       string `gorm:"column:oss_access_key_id;type:varchar(128);not null;default:''" json:"oss_access_key_id"`
	OssAccessKeySecret   string `gorm:"column:oss_access_key_secret;type:varchar(128);not null;default:''" json:"oss_access_key_secret"`
	OssEndpoint          string `gorm:"column:oss_endpoint;type:varchar(128);not null;default:''" json:"oss_endpoint"`
	OssBucketName        string `gorm:"column:oss_bucket_name;type:varchar(128);not null;default:''" json:"oss_bucket_name"`
	OssCustomVisitDomain string `gorm:"column:oss_custom_visit_domain;type:varchar(255);not null;default:''" json:"oss_custom_visit_domain"`

	// 图片大小上限（字节），0 表示使用默认 5MB
	MaxSize int64 `gorm:"column:max_size;type:bigint;not null;default:0" json:"max_size"`

	// updated_at 兼作版本号，服务据此检测配置变更并重建 OSS 客户端
	CreatedAt string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回附件配置表名
//
// 返回值：
//   - string: 表名
func (AttachmentConfig) TableName() string {
	return "attachment_config"
}

// Attachment 附件记录模型
// 参考 iMe mat_file 口径：presign 阶段 md5 置空延迟写入，confirm 成功后补写，
// 保证「md5 非空 ⇒ 对象已上传」的不变量，未完成记录天然从秒传查询中隔离
type Attachment struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`

	// 上传管理员 ID，0 表示系统上传
	AdminID int64 `gorm:"column:admin_id;type:bigint;not null;default:0" json:"admin_id"`

	// 文件 MIME 类型，如 image/png
	Type string `gorm:"column:type;type:varchar(64);not null;default:''" json:"type"`

	// 完整访问 URL：OSS 模式为 bucket/自定义域名 + path，local 模式为空（响应时按请求域名动态拼接）
	URL string `gorm:"column:url;type:varchar(2048);not null;default:''" json:"url"`

	// 对象相对路径：OSS 为对象 Key，本地为附件目录下相对路径
	Path string `gorm:"column:path;type:varchar(512);not null;default:''" json:"path"`

	// 原始文件名
	Name string `gorm:"column:name;type:varchar(255);not null;default:''" json:"name"`

	// 文件大小（字节）
	Size int64 `gorm:"column:size;type:bigint;not null;default:0" json:"size"`

	// 文件 MD5，32 位小写十六进制；presign 阶段置空，confirm 成功后补写
	MD5 string `gorm:"column:md5;type:varchar(32);not null;default:'';index:idx_attachment_md5" json:"md5"`

	// 图片宽高（像素）
	Width  int `gorm:"column:width;type:int;not null;default:0" json:"width"`
	Height int `gorm:"column:height;type:int;not null;default:0" json:"height"`

	CreatedAt string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回附件记录表名
//
// 返回值：
//   - string: 表名
func (Attachment) TableName() string {
	return "attachment"
}
