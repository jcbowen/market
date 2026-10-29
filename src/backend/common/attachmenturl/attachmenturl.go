// Package attachmenturl 附件访问地址统一拼装（全系统公共能力）
//
// 口径总纲：数据库一律存附件相对路径（remote_path 形态，如 images/2026/10/11/xxx.jpg，
// 不含 uploads 目录前缀），出库统一拼为「配置的附件域名 + 附件路径」的完整地址：
//   - local 模式：<visit_domain>/uploads/<path>（与 gateway /uploads 静态路由一致）
//   - oss 模式：<自定义 CDN 域名或 bucket 域名>/<path>
//
// 所有服务共享同一 market 库，本包提供：
//   - Loader：attachment_config 单行的进程内缓存读取（默认 60s TTL，失败降级旧缓存）
//   - Converter：按配置构造的 jcbaseGo 转换器薄封装
//   - RelPath / FallbackURL / SourcePath：纯函数形态的归一化与兜底，无 DB 依赖
//     （SourcePath 委托 jcbaseGo attachment.ToSource，剥离口径以上游实现为准）
//
// 完整地址的前提是 local 模式 visit_domain 恒有值：该值只来自显式配置（后台手填、
// 站点设置 base_url / SITE_BASE_URL 联动，见 gateway site.go）；从未显式配置时
// visit_domain 保持为空，出库兜底优先按网关透传的当前访问域名（gRPC metadata
// x-request-base，见 common/rpcmeta）拼完整地址——小程序/APP 端 image 组件无法解析
// 站内相对路径，必须给完整 URL；基址未透传（后台任务/直调/单测）降级拼站内相对路径
// /uploads/<path>，由浏览器按当前访问域名解析。兜底仅发生在响应层，一律不入库，
// 不以「首个请求的域名」固化配置（健康检查/本机调试的首访不会污染 attachment_config）。
// 降级期间按节流周期记提示日志，提醒管理员到后台配置正式域名（分享/导出等脱离
// 浏览器上下文的场景需要）。
package attachmenturl

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/attachment"
	"gorm.io/gorm"

	"github.com/jcbowen/market/src/backend/common/rpcmeta"
)

// DefaultCacheTTL 配置缓存默认有效期
// 附件配置只有单行，却会经 URL 拼装被列表逐行触发；保存配置方负责主动 Invalidate，
// 其余实例靠 TTL 收敛（多实例部署下秒级滞后可接受）
const DefaultCacheTTL = 60 * time.Second

// DefaultLocalDir 本地存储目录默认值（与 jcbaseGo AttachmentStruct.LocalDir 的 default 一致）
// 物理落盘目录名 = URL 路径段 = 本字段，三者由 local_dir 配置统一驱动
const DefaultLocalDir = "uploads"

// Config 附件配置的公共最小集（与 attachment_config 表字段对齐）
// 不依赖任何服务 model，避免公共包反向依赖服务层
type Config struct {
	StorageType          string // local / oss
	LocalDir             string // 本地存储目录名（落盘目录与 URL 路径段共用，空值回 DefaultLocalDir）
	VisitDomain          string // local 模式附件访问域名（含协议，如 https://m.example.com）
	OssCustomVisitDomain string // OSS 自定义访问域名（CDN），空回落 bucket 域名
	OssBucketName        string
	OssEndpoint          string
}

// LocalDirName 取规范化的本地存储目录名（剥离首尾斜杠后为空回默认）
//
// 参数:
//   - dir: 配置的目录名（可为空串或纯斜杠）
//
// 返回值:
//   - string: 目录名（不含首尾斜杠且恒非空，形如 uploads）
func LocalDirName(dir string) string {
	if trimmed := strings.Trim(dir, "/"); trimmed != "" {
		return trimmed
	}
	return DefaultLocalDir
}

// DirOf 取配置的规范化本地目录名（cfg 为 nil 回默认 uploads）
//
// 参数:
//   - cfg: 附件配置（可为 nil）
//
// 返回值:
//   - string: 目录名（不含首尾斜杠，形如 uploads）
func DirOf(cfg *Config) string {
	if cfg == nil {
		return DefaultLocalDir
	}
	return LocalDirName(cfg.LocalDir)
}

// dirCandidates 目录前缀剥离候选序列：配置目录优先、默认 uploads 兜底。
// local_dir 变更后存量 URL 仍是旧前缀（或默认前缀），两种形态都必须能归一，
// 否则归一化失败会把「目录名/路径」整段当相对路径入库，拼 URL 时出现双目录前缀
//
// 参数:
//   - dir: 配置的本地目录名
//
// 返回值:
//   - []string: 去重后的候选目录名列表
func dirCandidates(dir string) []string {
	dir = LocalDirName(dir)
	if dir == DefaultLocalDir {
		return []string{DefaultLocalDir}
	}
	return []string{dir, DefaultLocalDir}
}

// configRow attachment_config 单行的轻量读取模型（各服务共享同一 market 库）
// ID 必须显式声明主键：GORM 对无主键模型会把第一个字段推断为主键，
// First(&row, 1) 会被错误编译成 WHERE storage_type = 1，
// 而 storage_type 取值恒为 'local'/'oss'，该比较永不相等，
// 导致所有服务读配置失败、URL 全部降级为站内相对路径（cart 出库相对路径事故根因）
type configRow struct {
	ID                   uint   `gorm:"column:id"`
	StorageType          string `gorm:"column:storage_type"`
	LocalDir             string `gorm:"column:local_dir"`
	VisitDomain          string `gorm:"column:visit_domain"`
	OssCustomVisitDomain string `gorm:"column:oss_custom_visit_domain"`
	OssBucketName        string `gorm:"column:oss_bucket_name"`
	OssEndpoint          string `gorm:"column:oss_endpoint"`
}

// TableName 固定单行配置表名（主键 id=1）
func (configRow) TableName() string { return "attachment_config" }

// LoadConfig 直读附件配置（无缓存），读不到/读失败返回错误
// 显式 WHERE id = 1 而非 First(&row, 1)：不依赖 GORM 主键推断，防止模型字段调整后查询条件漂移
//
// 参数:
//   - ctx: 上下文
//   - db: gorm 句柄（本服务自己的 DB 连接）
//
// 返回值:
//   - *Config: 附件配置
//   - error: 查询失败或行不存在时返回错误
func LoadConfig(ctx context.Context, db *gorm.DB) (*Config, error) {
	var row configRow
	if err := db.WithContext(ctx).Where("id = ?", 1).Take(&row).Error; err != nil {
		return nil, fmt.Errorf("读取附件配置失败: %w", err)
	}
	return &Config{
		StorageType:          row.StorageType,
		LocalDir:             row.LocalDir,
		VisitDomain:          row.VisitDomain,
		OssCustomVisitDomain: row.OssCustomVisitDomain,
		OssBucketName:        row.OssBucketName,
		OssEndpoint:          row.OssEndpoint,
	}, nil
}

// Loader 带进程内缓存的附件配置读取器
// 命中期内不查库；查库失败时降级返回旧缓存，避免 DB 抖动阻断图片 URL 拼装链路
type Loader struct {
	db  *gorm.DB
	ttl time.Duration

	mu       sync.RWMutex
	cached   *Config
	cachedAt time.Time
}

// NewLoader 创建缓存读取器
//
// 参数:
//   - db: gorm 句柄
//   - ttl: 缓存有效期，缺省 DefaultCacheTTL
//
// 返回值:
//   - *Loader: 配置读取器
func NewLoader(db *gorm.DB, ttl ...time.Duration) *Loader {
	t := DefaultCacheTTL
	if len(ttl) > 0 && ttl[0] > 0 {
		t = ttl[0]
	}
	return &Loader{db: db, ttl: t}
}

// Get 取附件配置（缓存优先，过期回源；回源失败降级旧缓存，均不可用时返回 nil）
//
// 参数:
//   - ctx: 上下文
//
// 返回值:
//   - *Config: 附件配置；配置行不存在或 DB 不可用时返回 nil，调用方按兜底口径处理
func (l *Loader) Get(ctx context.Context) *Config {
	l.mu.RLock()
	cached, fresh := l.cached, l.cached != nil && time.Since(l.cachedAt) < l.ttl
	l.mu.RUnlock()
	if fresh {
		return cached
	}
	// 无 DB 句柄（测试构造/异常初始化）时只回缓存，避免空指针
	if l.db == nil {
		return cached
	}

	cfg, err := LoadConfig(ctx, l.db)
	if err != nil {
		log.Printf("读取附件配置失败（使用旧缓存兜底）: %v", err)
		return cached
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.cached = cfg
	l.cachedAt = time.Now()
	return l.cached
}

// Invalidate 立即失效缓存（配置保存方调用，让本实例下一次 Get 直读库）
func (l *Loader) Invalidate() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cachedAt = time.Time{}
}

// Converter 按附件配置构造的 URL 转换器薄封装
type Converter struct {
	conv *attachment.Attachment
}

// buildAttachment 按配置构造 jcbaseGo 附件转换器底层结构（出库拼接与入库归一共用同一份映射）
// cfg 为 nil 时按默认本地存储（StorageType 空回 local、目录回 uploads、无域名）构造，
// 保证 ToSource 等纯剥离口径始终可用
//
// 参数:
//   - cfg: 附件配置（可为 nil）
//
// 返回值:
//   - *attachment.Attachment: jcbaseGo 附件组件实例（恒非 nil）
func buildAttachment(cfg *Config) *attachment.Attachment {
	base := &jcbaseGo.AttachmentStruct{
		// LocalDir 设计对齐 jcbaseGo：物理落盘目录名与 URL 路径段共用同一字段
		LocalDir: DefaultLocalDir,
	}
	if cfg != nil {
		base.StorageType = cfg.StorageType
		base.LocalDir = LocalDirName(cfg.LocalDir)
		switch cfg.StorageType {
		case "oss":
			domain := cfg.OssCustomVisitDomain
			if domain == "" {
				domain = fmt.Sprintf("https://%s.%s/", cfg.OssBucketName, cfg.OssEndpoint)
			}
			base.VisitDomain = ensureTrailingSlash(domain)
		default:
			base.LocalVisitDomain = ensureTrailingSlash(cfg.VisitDomain)
		}
	}
	return &attachment.Attachment{BaseConfig: base}
}

// NewConverter 按配置构造转换器
// 配置为 nil，或 local 模式未配访问域名（未做显式配置的常态）时返回 nil，
// 调用方按 FallbackURL 兜底
//
// 参数:
//   - cfg: 附件配置
//
// 返回值:
//   - *Converter: 转换器；不可用时返回 nil
func NewConverter(cfg *Config) *Converter {
	if cfg == nil {
		return nil
	}
	if cfg.StorageType != "oss" && cfg.VisitDomain == "" {
		return nil
	}
	return &Converter{conv: buildAttachment(cfg)}
}

// ToMedia 将入库相对路径拼接为完整访问 URL
//
// 参数:
//   - rel: 相对路径（不含 uploads 目录前缀）
//
// 返回值:
//   - string: 完整访问 URL
func (c *Converter) ToMedia(rel string) string {
	return c.conv.ToMedia(rel)
}

// MediaURL 一步到位：归一化 + 按当前配置拼完整地址
// 外链与未知 / 开头绝对路径原样返回（信任输入形态；存量快照请用 RestoreURL）
//
// 参数:
//   - ctx: 上下文
//   - src: 相对路径或存量完整 URL
//
// 返回值:
//   - string: 完整访问 URL
func (l *Loader) MediaURL(ctx context.Context, src string) string {
	cfg := l.Get(ctx)
	dir := DirOf(cfg)
	rel, passthrough := RelPathIn(dir, src)
	if passthrough {
		return rel
	}
	conv := NewConverter(cfg)
	if conv == nil {
		return FallbackURLInCtx(ctx, dir, src)
	}
	return conv.ToMedia(rel)
}

// RestoreURL 出库还原口径：先 SourcePath 剥离为相对路径再重新拼接
// 专供「快照型」字段（签收凭证、商品图快照等历史存过完整 URL 的列）：
// 存量任意本站域名的 /uploads/ URL 与命中当前配置候选的 OSS URL 都会归一重拼，
// 域名/OSS 变更后旧数据自动跟随新配置；真正的外部链接原样保留
//
// 参数:
//   - ctx: 上下文
//   - src: 相对路径、任意本站完整 URL 或外链
//
// 返回值:
//   - string: 完整访问 URL
func (l *Loader) RestoreURL(ctx context.Context, src string) string {
	return l.MediaURL(ctx, l.SourcePath(ctx, src))
}

// SourcePath 将附件地址归一化为入库相对路径（remote_path 形态，不含 uploads 前缀）
// 规则：
//   - 本站附件 URL（path 以 /uploads/ 开头，与域名无关）→ 剥离为相对路径
//   - OSS/CDN URL（域名命中附件配置候选）→ 剥离为对象 Key
//   - 其他外链 URL（如 placehold.co 占位图）→ 原样保存，出库时同样原样返回
//   - 纯路径（/uploads/... 或 uploads/... 前缀）→ 剥离为相对路径
//
// 参数:
//   - ctx: 上下文
//   - src: 完整 URL、站内路径或已是相对路径
//
// 返回值:
//   - string: 入库相对路径或原样外链 URL
func (l *Loader) SourcePath(ctx context.Context, src string) string {
	return SourcePath(l.Get(ctx), src)
}

// SourcePath 将附件地址归一化为入库相对路径（remote_path 形态，不含 uploads 前缀）
// 实现委托 jcbaseGo attachment.ToSource（上游口径，见其 README）：
//   - 命中本地/远程附件域名：优先从类型目录（images/ 等）截取，本地域名下未命中类型目录时剥离 LocalDir 前缀
//   - 未命中域名的 http(s) URL：local 存储模式按 /<local_dir>/ 路径前缀剥离（覆盖历史按任意请求域名拼出的存量 URL）；
//     其余视为外链原样返回（含查询参数不破坏）
//   - 纯路径形态（/uploads/... 或 uploads/...）：local 模式剥离 LocalDir 前缀，其余仅去掉开头的 /
//
// 参数:
//   - cfg: 附件配置（可为 nil，按默认本地存储、目录 uploads 归一）
//   - src: 完整 URL、站内路径或已是相对路径
//
// 返回值:
//   - string: 入库相对路径或原样外链 URL
func SourcePath(cfg *Config, src string) string {
	return buildAttachment(cfg).ToSource(src)
}

// RelPath 将任意形态的附件地址归一化为拼接前置形态（默认目录 uploads）
// 需跟随自定义 local_dir 时请用 RelPathIn
//
// 参数:
//   - src: 相对路径、/uploads/... 站内路径、uploads/... 前缀路径或完整 URL
//
// 返回值:
//   - rel: 归一化结果：外链与未知 / 开头绝对路径原样透传，
//     /uploads/ 与 uploads/ 前缀剥离为相对路径，其余视为已是相对路径
//   - passthrough: true 表示 rel 无需拼接、应原样返回
func RelPath(src string) (rel string, passthrough bool) {
	return RelPathIn(DefaultLocalDir, src)
}

// RelPathIn RelPath 的目录参数版：按配置目录（与默认 uploads 兜底）剥离前缀
// local_dir 改为非 uploads 时，落盘与 URL 路径段同步变化，归一化必须吃同一目录名；
// 剥离候选恒含默认 uploads，旧前缀存量 URL 仍可归一
//
// 参数:
//   - dir: 配置的本地存储目录名（空值回默认 uploads）
//   - src: 相对路径、/<dir>/... 站内路径、<dir>/... 前缀路径或完整 URL
//
// 返回值:
//   - rel: 归一化结果：外链与未知 / 开头绝对路径原样透传，
//     目录前缀剥离为相对路径，其余视为已是相对路径
//   - passthrough: true 表示 rel 无需拼接、应原样返回
func RelPathIn(dir, src string) (rel string, passthrough bool) {
	if src == "" || IsExternalMediaRef(src) {
		return src, true
	}
	dirs := dirCandidates(dir)
	// / 开头：/<目录>/ 剥离为相对路径参与拼接（含 /<目录>/seed 演示素材），
	// 其余未知绝对路径不强行拼接，原样透传
	if strings.HasPrefix(src, "/") {
		for _, d := range dirs {
			if rest, ok := strings.CutPrefix(src, "/"+d+"/"); ok {
				return rest, false
			}
		}
		return src, true
	}
	// 存量目录前缀形态归一为相对路径
	for _, d := range dirs {
		if rest, ok := strings.CutPrefix(src, d+"/"); ok {
			return rest, false
		}
	}
	return src, false
}

// IsExternalMediaRef 判断是否为外部完整 URL（http/https/协议相对）
//
// 参数:
//   - src: 附件地址
//
// 返回值:
//   - bool: 外部 URL 返回 true
func IsExternalMediaRef(src string) bool {
	return strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") ||
		strings.HasPrefix(src, "//")
}

// fallbackReminderInterval 附件访问域名未配置的提示日志节流间隔
// 降级是合法运行态（零配置开箱即用）而非异常，按周期低噪提醒即可，避免列表页逐行刷日志
const fallbackReminderInterval = 10 * time.Minute

// fallbackLastRemind 提示日志的节流时间戳（进程内共享，写入点仅 remindFallback 一处）
var fallbackLastRemind time.Time

var fallbackRemindMu sync.Mutex

// remindFallback 节流记录「未配置附件访问域名」的提示日志
// 临界区只圈时间戳判定与推进，log 本身也在锁内：输出顺序与节流判定一致，
// 且临界区极短，列表页并发触发的争用可忽略
func remindFallback() {
	fallbackRemindMu.Lock()
	defer fallbackRemindMu.Unlock()
	if !fallbackLastRemind.IsZero() && time.Since(fallbackLastRemind) < fallbackReminderInterval {
		return
	}
	fallbackLastRemind = time.Now()
	log.Printf("[提示] 附件访问域名未配置，图片地址暂按当前访问域名返回（各端均可正常显示）；" +
		"微信分享/导出等脱离浏览器上下文的场景需要正式域名，请到后台附件配置或站点设置填写")
}

// FallbackURL 附件访问域名不可用时的兜底拼装（未做显式配置的常态运行形态）
// 默认目录 uploads 版；自定义 local_dir 请用 FallbackURLIn
// 返回站内绝对路径 /<dir>/<path>：网关对应静态路由承接，且不随浏览器当前页面
// 路径解析（SPA 子路径如 /b/ 下不会错拼成 /b/images/...），按节流周期记提示日志
//
// 参数:
//   - src: 相对路径或存量完整 URL
//
// 返回值:
//   - string: 站内绝对路径；外链或未知绝对路径原样返回
func FallbackURL(src string) string {
	return FallbackURLIn(DefaultLocalDir, src)
}

// FallbackURLIn FallbackURL 的目录参数版：兜底路径段与配置目录一致
// 无 gRPC 上下文形态（纯函数/单测），带请求上下文请用 FallbackURLInCtx
//
// 参数:
//   - dir: 配置的本地存储目录名（空值回默认 uploads）
//   - src: 相对路径或存量完整 URL
//
// 返回值:
//   - string: 站内绝对路径；外链或未知绝对路径原样返回
func FallbackURLIn(dir, src string) string {
	return FallbackURLInCtx(nil, dir, src)
}

// FallbackURLInCtx FallbackURLIn 的上下文版：附件访问域名未配置时的出库兜底
// 优先按网关透传的请求基址（gRPC metadata x-request-base）拼完整 URL——
// 小程序/APP 端 image 组件无法解析站内相对路径，必须给完整地址；
// 基址未透传（后台任务/进程内直调/单测）回落站内绝对路径 /<dir>/<path>，
// 由浏览器按当前访问域名解析。仅响应层拼装，一律不入库
//
// 参数:
//   - ctx: 上下文（gRPC 服务端方法上下文可读出请求基址，可为 nil）
//   - dir: 配置的本地存储目录名（空值回默认 uploads）
//   - src: 相对路径或存量完整 URL
//
// 返回值:
//   - string: 完整访问 URL（有请求基址）或站内绝对路径；外链原样返回
func FallbackURLInCtx(ctx context.Context, dir, src string) string {
	rel, passthrough := RelPathIn(dir, src)
	if passthrough {
		return rel
	}
	remindFallback()
	segment := "/" + LocalDirName(dir) + "/" + rel
	if base := rpcmeta.FromIncomingContext(ctx); base != "" {
		return strings.TrimSuffix(base, "/") + segment
	}
	return segment
}

// ensureTrailingSlash 确保域名以 / 结尾
//
// 参数:
//   - domain: 域名字符串
//
// 返回值:
//   - string: 以 / 结尾的域名（空串原样返回）
func ensureTrailingSlash(domain string) string {
	if domain == "" {
		return ""
	}
	return strings.TrimSuffix(domain, "/") + "/"
}
