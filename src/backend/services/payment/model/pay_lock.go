package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// CashCreateLockID 现金/货到付款建单串行锚点行的固定主键
// 全表只有这一行，锁它等价于锁「现金建单」这个动作
const CashCreateLockID = int64(1)

// PayLock 支付服务内部串行锚点表
// 一行只为提供一个可锁的持久对象：MySQL 事务内跨实例串行必须锁一把真实存在的行，
// GET_LOCK 这类命名锁是会话级的（连接池下事务与锁可能落在不同会话，不能用于串行化建单事务）。
// 本表不承载业务数据，行内容恒为空，禁止任何人读写其列值；
// 历史形态是借用户服务的 site_configs 固定行，跨服务写他人表使现金建单受对方改表/迁移顺序牵制，
// 故改为支付服务自有表（AutoMigrate 建表，锚点行由加锁事务内 get-or-create 兜底）
//
//nolint:lll
//go:generate go tool gormgen -struct PayLock
type PayLock struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey;comment:固定锚点 ID" json:"id"`
	// Name 无业务语义，仅让 SHOW CREATE TABLE / 手工查库时看得出该行是串行锚点而非业务数据
	Name      string `gorm:"column:name;type:varchar(32);not null;default:'';comment:锚点名称，无业务语义" json:"name"`
	CreatedAt string `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回支付串行锚点表名
//
// 返回值：
//   - string: 表名
func (PayLock) TableName() string {
	return "payment_locks"
}
