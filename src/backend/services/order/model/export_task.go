package model

import (
	"github.com/jcbowen/jcbaseGo/component/orm/base"
)

// ExportTask 后台导出任务模型（下载中心）
// 订单导出改为后台异步生成：创建任务立即返回，生成完成后在下载中心取文件
// 文件生成后保留 3 天（expires_at），到期由 order 服务调度器清理（删文件 + 删记录）
//
//nolint:lll
//go:generate go tool gormgen -struct ExportTask
type ExportTask struct {
	base.MysqlBaseModel
	ID int64 `gorm:"column:id;type:bigint;primaryKey;autoIncrement;comment:主键" json:"id"`
	// TaskNo 任务号（文件命名的随机段），唯一防猜测枚举下载
	TaskNo string `gorm:"column:task_no;type:varchar(50);not null;unique;comment:任务号，UUID 生成（36 字符）" json:"task_no"`
	// ExportType 导出类型，当前仅 orders（订单导出），预留后续报表扩展
	ExportType string `gorm:"column:export_type;type:varchar(30);not null;comment:导出类型 orders" json:"export_type"`
	// Status 任务状态
	Status int32 `gorm:"column:status;type:tinyint;not null;default:1;index;comment:1待处理 2生成中 3已完成 4失败" json:"status"`
	// QueryCond 查询条件 JSON 快照（与 ListOrdersReq 过滤字段对齐），异步生成时原样还原
	QueryCond string `gorm:"column:query_cond;type:text;comment:查询条件 JSON 快照" json:"query_cond"`
	// QueryDesc 查询条件人读摘要，下载中心列表直接展示
	QueryDesc string `gorm:"column:query_desc;type:varchar(200);comment:查询条件摘要" json:"query_desc"`
	// FilePath 文件相对路径（相对 attachment 根目录，如 exports/orders-xxx.csv）；
	// 绝对路径不落库：gateway/order 服务各自以可执行文件目录定位，避免跨机路径漂移
	FilePath  string `gorm:"column:file_path;type:varchar(255);comment:文件相对路径" json:"file_path"`
	FileName  string `gorm:"column:file_name;type:varchar(255);comment:下载文件名" json:"file_name"`
	FileSize  int64  `gorm:"column:file_size;type:bigint;not null;default:0;comment:文件大小（字节）" json:"file_size"`
	RowCount  int64  `gorm:"column:row_count;type:bigint;not null;default:0;comment:导出数据行数" json:"row_count"`
	AdminID   int64  `gorm:"column:admin_id;type:bigint;not null;default:0;index;comment:创建人管理员 ID" json:"admin_id"`
	AdminName string `gorm:"column:admin_name;type:varchar(50);comment:创建人姓名快照" json:"admin_name"`
	// CommunityID 创建人社区（0=超管跨社区）；普通管理员列表按本列隔离，与订单列表口径一致
	CommunityID   int64 `gorm:"column:community_id;type:bigint;not null;default:0;index;comment:创建人社区 ID 0=超管" json:"community_id"`
	DownloadCount int32 `gorm:"column:download_count;type:int;not null;default:0;comment:累计下载次数" json:"download_count"`
	// Downloaders 下载留痕 JSON 数组 [{admin_id,admin_name,at}]；完整名单仅超管可见
	Downloaders string `gorm:"column:downloaders;type:text;comment:下载留痕 JSON" json:"downloaders"`
	ErrorMsg    string `gorm:"column:error_msg;type:varchar(500);comment:失败原因" json:"error_msg"`
	// ExpiresAt 文件过期时间（生成成功后 +3 天），调度器据此清理文件与记录；失败任务无文件也随记录一起过期
	ExpiresAt *string `gorm:"column:expires_at;type:datetime;index;comment:文件过期时间" json:"expires_at"`
	CreatedAt string  `gorm:"column:created_at;type:datetime;comment:创建时间" json:"created_at"`
	UpdatedAt string  `gorm:"column:updated_at;type:datetime;comment:更新时间" json:"updated_at"`
}

// TableName 返回导出任务表名
// 返回值：
//   - string: 表名
func (ExportTask) TableName() string {
	return "export_tasks"
}

// 导出任务状态（ExportTask.Status）
const (
	// ExportTaskStatusPending 待处理（已落库，等待生成协程拾起）
	ExportTaskStatusPending int32 = 1
	// ExportTaskStatusRunning 生成中
	ExportTaskStatusRunning int32 = 2
	// ExportTaskStatusDone 已完成（文件可下载）
	ExportTaskStatusDone int32 = 3
	// ExportTaskStatusFailed 失败（可重新发起导出）
	ExportTaskStatusFailed int32 = 4
)

// 导出类型（ExportTask.ExportType）
const (
	// ExportTypeOrders 订单导出
	ExportTypeOrders = "orders"
)

// 导出文件保留天数：生成成功后 3 天，到期调度器清理文件与记录
const ExportRetentionDays = 3

// 导出行数上限：兜底防失控导出拖垮数据库（10 万行 CSV 约 20MB，远超日常对账量）
const ExportMaxRows = 100000
