package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
	"gorm.io/gorm"
)

// AttachmentConfig 附件存储配置模型（单行存储，id 固定为 1）
// 存储附件存储模式（本地/OSS）及 OSS 访问凭证，由后台「系统管理 → 附件配置」维护
// 读写均通过 product 服务，gateway 不直接访问数据库
//
//nolint:lll
//go:generate go tool gormgen -struct AttachmentConfig
type AttachmentConfig struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey;comment:主键，固定为 1" json:"id"`

	// 存储类型：local / oss（预留 cos）
	StorageType string `gorm:"column:storage_type;type:varchar(16);not null;default:'local';comment:存储类型：local / oss（预留 cos）" json:"storage_type"`

	// 本地存储目录名（对齐 jcbaseGo AttachmentStruct.LocalDir 设计）：
	// 物理落盘目录（<可执行文件同级>/<local_dir>/）与 URL 路径段（/<local_dir>/...）共用同一字段，
	// 默认 uploads；后台设置页暂不暴露（改动会导致历史 URL 前缀与宿主挂载目录失配）
	LocalDir string `gorm:"column:local_dir;type:varchar(64);not null;default:'uploads';comment:本地存储目录名，落盘目录与 URL 路径段共用，默认 uploads" json:"local_dir"`

	// 自定义访问域名，空则本地模式按请求域名动态拼接、OSS 用 bucket 域名
	VisitDomain string `gorm:"column:visit_domain;type:varchar(255);not null;default:'';comment:自定义访问域名，商品/Banner 等图片字段出库时按此域名拼接完整 URL，本地模式必填（OSS 模式图片走 oss_custom_visit_domain/bucket 域名，可空）" json:"visit_domain"`

	// 阿里云 OSS
	OssAccessKeyId       string `gorm:"column:oss_access_key_id;type:varchar(128);not null;default:'';comment:阿里云 AccessKey ID" json:"oss_access_key_id"`
	OssAccessKeySecret   string `gorm:"column:oss_access_key_secret;type:varchar(128);not null;default:'';comment:阿里云 AccessKey Secret（读取脱敏，掩码提交不覆盖）" json:"oss_access_key_secret"`
	OssEndpoint          string `gorm:"column:oss_endpoint;type:varchar(128);not null;default:'';comment:OSS Endpoint，如 oss-cn-shanghai.aliyuncs.com" json:"oss_endpoint"`
	OssBucketName        string `gorm:"column:oss_bucket_name;type:varchar(128);not null;default:'';comment:OSS Bucket 名称" json:"oss_bucket_name"`
	OssCustomVisitDomain string `gorm:"column:oss_custom_visit_domain;type:varchar(255);not null;default:'';comment:OSS 自定义访问域名（CDN），可空" json:"oss_custom_visit_domain"`

	// 图片大小上限（字节），0 表示使用默认 5MB
	MaxSize int64 `gorm:"column:max_size;type:bigint;not null;default:0;comment:图片大小上限（字节），0 表示默认 5MB" json:"max_size"`

	// updated_at 兼作版本号，服务据此检测配置变更并重建 OSS 客户端
	CreatedAt string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime;comment:更新时间，兼作配置版本号" json:"updated_at"`
}

// TableName 返回附件配置表名
//
// 返回值：
//   - string: 表名
func (AttachmentConfig) TableName() string {
	return "attachment_config"
}

// Attachment 附件记录模型
// 字段口径对齐 jcbaseGo sl_attachment 参考设计：admin_id 对应参考表 uid（上传管理员），
// mid 预留客户端用户 ID；presign 阶段 md5 置空延迟写入，confirm 成功后补写，
// 保证「md5 非空 ⇒ 对象已上传」的不变量，未完成记录天然从秒传查询中隔离
type Attachment struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`

	// 分组 ID，预留（参考口径 group_id）
	GroupID int64 `gorm:"column:group_id;type:bigint;not null;default:0;comment:分组 ID，预留" json:"group_id"`

	// 上传管理员 ID（参考口径 uid），0 表示系统上传
	AdminID int64 `gorm:"column:admin_id;type:bigint;not null;default:0;comment:上传管理员 ID（参考口径 uid），0 表示系统上传" json:"admin_id"`

	// 客户端用户 ID（参考口径 mid），预留，当前仅后台管理员上传
	Mid int64 `gorm:"column:mid;type:bigint;not null;default:0;comment:客户端用户 ID（参考口径 mid），预留，当前仅后台管理员上传" json:"mid"`

	// 文件 MIME 类型，如 image/png
	Type string `gorm:"column:type;type:varchar(64);not null;default:'';comment:文件 MIME 类型，如 image/png" json:"type"`

	// 对象相对路径：OSS 为对象 Key，本地为附件目录下相对路径
	Path string `gorm:"column:path;type:varchar(512);not null;default:'';comment:对象相对路径：OSS 为对象 Key，本地为附件目录下相对路径" json:"path"`

	// 原始文件名
	Name string `gorm:"column:name;type:varchar(255);not null;default:'';comment:原始文件名" json:"name"`

	// 文件大小（字节）
	Size int64 `gorm:"column:size;type:bigint;not null;default:0;comment:文件大小（字节）" json:"size"`

	// 文件 MD5，32 位小写十六进制；presign 阶段置空，confirm 成功后补写
	MD5 string `gorm:"column:md5;type:varchar(32);not null;default:'';index:idx_attachment_md5;comment:文件 MD5" json:"md5"`

	// 图片宽高（像素）
	Width  int `gorm:"column:width;type:int;not null;default:0;comment:图片宽度（像素）" json:"width"`
	Height int `gorm:"column:height;type:int;not null;default:0;comment:图片高度（像素）" json:"height"`

	// 是否在选择器中显示（参考口径 is_display），预留
	IsDisplay int8 `gorm:"column:is_display;type:tinyint;not null;default:0;comment:是否在附件选择器中显示：后台上传（本地上传与 OSS presign，gateway 显式传 is_display=true）写入 1，客户端用户上传不传该字段默认写入 0（不进后台选择器）。选择器列表仅返回 admin_id=当前管理员 AND is_display=1 的图片记录" json:"is_display"`

	CreatedAt string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`

	// 软删除时间，删除记录不参与秒传查询
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;type:datetime;index:idx_attachment_deleted_at;comment:软删除时间，删除记录不参与秒传查询" json:"deleted_at"`
}

// TableName 返回附件记录表名
//
// 返回值：
//   - string: 表名
func (Attachment) TableName() string {
	return "attachment"
}
