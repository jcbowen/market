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
//
// 完整地址的前提是 local 模式 visit_domain 恒有值：gateway 在站点设置保存时与运行期
// 惰性补写（站点域名 > 当前访问域名，单向只补空）；仅在该补写尚未完成的短暂窗口内，
// 降级拼站内相对路径 /uploads/<path> 并记一次告警日志。
package attachmenturl

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/attachment"
	"gorm.io/gorm"
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

// LocalDirName 取规范化的本地存储目录名（空值回默认）
//
// 参数:
//   - dir: 配置的目录名（可为空串）
//
// 返回值:
//   - string: 目录名（不含首尾斜杠，形如 uploads）
func LocalDirName(dir string) string {
	if dir == "" {
		return DefaultLocalDir
	}
	return strings.Trim(dir, "/")
}

// configRow attachment_config 单行的轻量读取模型（各服务共享同一 market 库）
type configRow struct {
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
	if err := db.WithContext(ctx).First(&row, 1).Error; err != nil {
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

// NewConverter 按配置构造转换器
// 配置为 nil，或 local 模式未配访问域名（gateway 补写尚未完成的异常窗口）时返回 nil，
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
	base := &jcbaseGo.AttachmentStruct{
		StorageType: cfg.StorageType,
		// LocalDir 设计对齐 jcbaseGo：物理落盘目录名与 URL 路径段共用同一字段
		LocalDir:         LocalDirName(cfg.LocalDir),
		LocalVisitDomain: ensureTrailingSlash(cfg.VisitDomain),
	}
	switch cfg.StorageType {
	case "oss":
		domain := cfg.OssCustomVisitDomain
		if domain == "" {
			domain = fmt.Sprintf("https://%s.%s/", cfg.OssBucketName, cfg.OssEndpoint)
		}
		base.VisitDomain = ensureTrailingSlash(domain)
	default:
		if cfg.VisitDomain == "" {
			return nil
		}
	}
	return &Converter{conv: &attachment.Attachment{BaseConfig: base}}
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
	rel, passthrough := RelPath(src)
	if passthrough {
		return rel
	}
	conv := NewConverter(l.Get(ctx))
	if conv == nil {
		return FallbackURL(src)
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

// SourcePath 纯函数版入库归一化（配置由调用方提供，可离线测试）
//
// 参数:
//   - cfg: 附件配置（可为 nil，此时仅剥离本站路径，OSS 候选域名不可用）
//   - src: 完整 URL、站内路径或已是相对路径
//
// 返回值:
//   - string: 入库相对路径或原样外链 URL
func SourcePath(cfg *Config, src string) string {
	if src == "" {
		return ""
	}
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		// 本站附件：path 以 /uploads/ 开头直接提取相对路径（query/锚点丢弃），与域名无关，
		// 任何历史按请求域名拼出的存量 URL 都能归一
		if u, err := url.Parse(src); err == nil {
			if rel, ok := strings.CutPrefix(u.Path, "/uploads/"); ok {
				return rel
			}
		}
		// OSS/CDN：域名命中附件配置候选时剥离为对象 Key，未命中的外链原样保存
		conv := NewConverter(cfg)
		if conv != nil {
			base := conv.conv.BaseConfig
			for _, domain := range []string{base.VisitDomain, base.LocalVisitDomain} {
				if domain == "" || domain == "/" || !strings.HasPrefix(src, domain) {
					continue
				}
				return strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(src, domain), "/"), "uploads/")
			}
		}
		return src
	}
	// 纯路径形态：剥离开头的 / 与 uploads/ 目录前缀
	if _, rest, ok := strings.Cut(strings.Trim(src, "/"), "uploads/"); ok {
		return rest
	}
	return src
}

// RelPath 将任意形态的附件地址归一化为拼接前置形态
//
// 参数:
//   - src: 相对路径、/uploads/... 站内路径、uploads/... 前缀路径或完整 URL
//
// 返回值:
//   - rel: 归一化结果：外链与未知 / 开头绝对路径原样透传，
//     /uploads/ 与 uploads/ 前缀剥离为相对路径，其余视为已是相对路径
//   - passthrough: true 表示 rel 无需拼接、应原样返回
func RelPath(src string) (rel string, passthrough bool) {
	if src == "" || IsExternalMediaRef(src) {
		return src, true
	}
	// / 开头：/uploads/ 剥离为相对路径参与拼接（含 /uploads/seed 演示素材），
	// 其余未知绝对路径不强行拼接，原样透传
	if strings.HasPrefix(src, "/") {
		if rest, ok := strings.CutPrefix(src, "/uploads/"); ok {
			return rest, false
		}
		return src, true
	}
	// 存量 uploads/ 目录前缀形态归一为相对路径
	return strings.TrimPrefix(src, "uploads/"), false
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

// fallbackWarnOnce 保证站内路径降级告警每个进程只记一次，避免列表页逐行刷日志
var fallbackWarnOnce sync.Once

// FallbackURL 附件访问域名不可用时的兜底拼装（配置缺失/补写未完成的异常窗口）
// 返回站内绝对路径 /uploads/<path>：网关 /uploads 静态路由承接，且不随浏览器当前页面
// 路径解析（SPA 子路径如 /b/ 下不会错拼成 /b/images/...）；首次触发记告警日志
//
// 参数:
//   - src: 相对路径或存量完整 URL
//
// 返回值:
//   - string: 站内绝对路径；外链或未知绝对路径原样返回
func FallbackURL(src string) string {
	rel, passthrough := RelPath(src)
	if passthrough {
		return rel
	}
	fallbackWarnOnce.Do(func() {
		log.Printf("[WARN] 附件访问域名未就绪，图片地址降级为站内相对路径（网关会自动补写 visit_domain）: %s", rel)
	})
	return "/uploads/" + rel
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
