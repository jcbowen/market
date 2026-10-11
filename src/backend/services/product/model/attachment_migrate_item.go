package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// 附件迁移条目状态（本地 → OSS 迁移任务）
const (
	// MigrateStatusPending 待迁移
	MigrateStatusPending = "pending"

	// MigrateStatusMigrating 迁移中
	MigrateStatusMigrating = "migrating"

	// MigrateStatusMigrated 已迁移，待验证
	MigrateStatusMigrated = "migrated"

	// MigrateStatusVerified 验证成功，本地文件可删除
	MigrateStatusVerified = "verified"

	// MigrateStatusFailed 迁移失败，可重试
	MigrateStatusFailed = "failed"

	// MigrateStatusVerifyFailed 验证失败（对象缺失或大小不一致），可重试验证或重新迁移
	MigrateStatusVerifyFailed = "verify_failed"

	// MigrateStatusLocalDeleted 本地文件已删除（终态）
	MigrateStatusLocalDeleted = "local_deleted"

	// MigrateStatusOrphan 孤儿文件：本地存在但附件表无记录，不参与迁移
	MigrateStatusOrphan = "orphan"
)

// CanMigrateAttachmentStatus 判断迁移条目状态是否允许执行迁移
//
// 参数:
//   - status: 条目状态
//
// 返回值:
//   - bool: true 表示允许迁移
func CanMigrateAttachmentStatus(status string) bool {
	switch status {
	case MigrateStatusPending, MigrateStatusMigrating, MigrateStatusMigrated,
		MigrateStatusFailed, MigrateStatusVerifyFailed:
		return true
	default:
		return false
	}
}

// CanVerifyAttachmentStatus 判断迁移条目状态是否允许执行验证
//
// 参数:
//   - status: 条目状态
//
// 返回值:
//   - bool: true 表示允许验证
func CanVerifyAttachmentStatus(status string) bool {
	switch status {
	case MigrateStatusMigrated, MigrateStatusVerified, MigrateStatusVerifyFailed:
		return true
	default:
		return false
	}
}

// AttachmentMigrateItem 本地附件迁移 OSS 任务条目模型
// 由「附件配置 → 本地附件迁移」工具维护：扫描本地 attachment/ 目录与
// attachment 表匹配后生成清单，迁移/验证/删除本地文件均按条目逐个执行，
// 状态机见上方 MigrateStatus* 常量；孤儿文件同样入表（orphan 状态）便于前端展示
//
//nolint:lll
//go:generate go tool gormgen -struct AttachmentMigrateItem
type AttachmentMigrateItem struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`

	// 对象相对路径（与 attachment.path 同口径），唯一
	// 注意：唯一性用列级 unique tag 声明。uniqueIndex tag 会命中 gorm v1.31 的
	// TryRemoveUniqueConstraint 怪癖（列存在 UNI 索引但字段无 unique tag 时，
	// 每次 AutoMigrate 都尝试 DROP uni_ 前缀约束，MySQL 无此外键导致启动失败）
	Path string `gorm:"column:path;type:varchar(512);not null;default:'';unique" json:"path"`

	// 原始文件名
	Name string `gorm:"column:name;type:varchar(255);not null;default:''" json:"name"`

	// 文件 MIME 类型
	Type string `gorm:"column:type;type:varchar(64);not null;default:''" json:"type"`

	// 文件大小（字节）
	Size int64 `gorm:"column:size;type:bigint;not null;default:0" json:"size"`

	// 文件 MD5（孤儿文件为空）
	MD5 string `gorm:"column:md5;type:varchar(32);not null;default:''" json:"md5"`

	// 图片宽高（像素）
	Width  int `gorm:"column:width;type:int;not null;default:0" json:"width"`
	Height int `gorm:"column:height;type:int;not null;default:0" json:"height"`

	// 条目状态，取值见 MigrateStatus* 常量
	Status string `gorm:"column:status;type:varchar(24);not null;default:'pending';index:idx_migrate_status" json:"status"`

	// 最近一次迁移/验证失败原因
	ErrorMsg string `gorm:"column:error_msg;type:varchar(512);not null;default:''" json:"error_msg"`

	// 迁移完成时间（default:null 使 gorm 对零值空串省略该列，避免 datetime 严格模式报错）
	MigratedAt string `gorm:"column:migrated_at;type:datetime;default:null" json:"migrated_at"`

	// 验证通过时间
	VerifiedAt string `gorm:"column:verified_at;type:datetime;default:null" json:"verified_at"`

	// 本地文件删除时间
	LocalDeletedAt string `gorm:"column:local_deleted_at;type:datetime;default:null" json:"local_deleted_at"`

	CreatedAt string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回附件迁移条目表名
//
// 返回值:
//   - string: 表名
func (AttachmentMigrateItem) TableName() string {
	return "attachment_migrate_item"
}
