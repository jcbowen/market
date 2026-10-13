package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// 行政区划层级常量
const (
	RegionLevelProvince = 1 // 省级
	RegionLevelCity     = 2 // 市级
	RegionLevelDistrict = 3 // 区县级
)

// 行政区划状态常量
const (
	RegionStatusActive   = 1 // 正常
	RegionStatusDisabled = 2 // 停用（上游撤销）
)

// Region 行政区划模型
// 对应行政区划代码表（GB/T 2260 口径：省/市/区县三级），
// 数据由后台「检查更新」任务从上游数据源同步，正常情况下不做手工增删
type Region struct {
	base.MysqlBaseModel
	// 行政区划代码，省级 2 位、市级 4 位、区县级 6 位
	Code string `gorm:"column:code;type:varchar(12);primaryKey" json:"code"`
	Name string `gorm:"column:name;type:varchar(50);not null" json:"name"`
	// 父级区划代码，省级为 "0"
	ParentCode string `gorm:"column:parent_code;type:varchar(12);not null;default:'0';index" json:"parent_code"`
	Level      int32  `gorm:"column:level;type:tinyint;not null;default:1;comment:层级 1省 2市 3区县" json:"level"`
	// 状态：1 正常 2 停用（上游撤销后软标记，避免业务表已引用代码悬空）
	Status    int32  `gorm:"column:status;type:tinyint;not null;default:1;comment:状态 1正常 2停用" json:"status"`
	CreatedAt string `gorm:"column:created_at;type:datetime" json:"created_at"`
	UpdatedAt string `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回行政区划表名
// 返回：
//   - string: 表名
func (Region) TableName() string {
	return "regions"
}

// 行政区划更新任务状态常量
const (
	RegionTaskStatusPending    = 1 // 待执行
	RegionTaskStatusFetching   = 2 // 拉取中
	RegionTaskStatusDiffing    = 3 // 比对中
	RegionTaskStatusDone       = 4 // 完成（待确认覆盖）
	RegionTaskStatusFailed     = 5 // 失败
	RegionTaskStatusInProgress = "1,2,3"
)

// RegionUpdateTask 行政区划更新任务模型
// 记录一次「拉取上游区划数据 → 与库内比对」任务的执行过程与差异结果，
// 差异详情（新增/更名/停用）以 JSON 存储，管理员确认后覆盖入库
type RegionUpdateTask struct {
	base.MysqlBaseModel
	ID     int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement" json:"id"`
	Status int32 `gorm:"column:status;type:tinyint;not null;default:1;comment:1待执行 2拉取中 3比对中 4完成 5失败" json:"status"`
	// 数据源 URL
	Source       string `gorm:"column:source;type:varchar(255)" json:"source"`
	TotalRemote  int32  `gorm:"column:total_remote;type:int;default:0" json:"total_remote"`
	TotalLocal   int32  `gorm:"column:total_local;type:int;default:0" json:"total_local"`
	AddedCount   int32  `gorm:"column:added_count;type:int;default:0" json:"added_count"`
	UpdatedCount int32  `gorm:"column:updated_count;type:int;default:0" json:"updated_count"`
	RemovedCount int32  `gorm:"column:removed_count;type:int;default:0" json:"removed_count"`
	// 差异明细 JSON（[]RegionDiffItem），确认覆盖前供前端展示
	// 全量初始化时新增明细可达数百 KB，text（64KB）不够，用 mediumtext
	AddedJSON   string `gorm:"column:added_json;type:mediumtext" json:"-"`
	UpdatedJSON string `gorm:"column:updated_json;type:mediumtext" json:"-"`
	RemovedJSON string `gorm:"column:removed_json;type:mediumtext" json:"-"`
	// 失败原因
	Error   string  `gorm:"column:error;type:varchar(500)" json:"error"`
	Applied bool    `gorm:"column:applied;type:tinyint(1);default:0;comment:差异是否已覆盖入库" json:"applied"`
	// 任务结束时间（未结束时为 NULL；指针类型避免零值空串写入 datetime 报 1292）
	CreatedAt  string  `gorm:"column:created_at;type:datetime" json:"created_at"`
	FinishedAt *string `gorm:"column:finished_at;type:datetime" json:"finished_at"`
	UpdatedAt  string  `gorm:"column:updated_at;type:datetime" json:"updated_at"`
}

// TableName 返回区划更新任务表名
// 返回：
//   - string: 表名
func (RegionUpdateTask) TableName() string {
	return "region_update_tasks"
}
