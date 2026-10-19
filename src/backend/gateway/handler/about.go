package handler

import (
	"log"
	"os"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"github.com/jcbowen/jcbaseGo/component/redis"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/response"
	"github.com/jcbowen/market/src/backend/common/version"
)

// AboutHandler 系统信息接口
// 供管理后台「关于」页面展示：项目版本号（编译时经 -ldflags 注入，
// 见 common/version）与运行时依赖 MySQL/Redis 的真实版本
type AboutHandler struct {
	cfg *config.Config

	mu  sync.Mutex
	db  *mysql.Instance
	rds *redis.Instance
}

// NewAboutHandler 创建系统信息 handler
// 使用示例：NewAboutHandler(cfg)，路由注册见 gateway/main.go registerRoutes
// 参数：
//   - cfg: 全局配置（提供 MySQL/Redis 连接参数）
//
// 返回：
//   - *AboutHandler: handler 实例
func NewAboutHandler(cfg *config.Config) *AboutHandler {
	return &AboutHandler{cfg: cfg}
}

// Get 返回系统信息
// 版本号取编译注入值（未注入时为 dev）；MySQL/Redis 版本为运行时真实查询结果，
// 查询失败对应字段返回空串（前端展示「-」），不影响接口整体成功；
// app_env 以 APP_ENV 环境变量为准（未设置视为开发环境）；
// deploy_mode 为运行时自动检测的实际部署方式（docker/binary）
// 使用示例：GET /api/v1/admin/about（登录即可访问）
// 参数：
//   - c: gin 上下文
func (h *AboutHandler) Get(c *gin.Context) {
	env := "development"
	if config.IsProduction() {
		env = "production"
	}
	response.Success(c, gin.H{
		"version":       version.AppVersion,
		"app_env":       env,
		"deploy_mode":   detectDeployMode(),
		"mysql_version": h.mysqlVersion(),
		"redis_version": h.redisVersion(),
	})
}

// detectDeployMode 检测当前实际部署运行方式
// 以 /.dockerenv 标记文件为准（Docker 官方运行时在容器根目录创建），
// 兜底解析 /proc/1/cgroup 是否包含容器运行时标识；均未命中视为二进制直接运行
// （Windows/裸机 systemd 等场景 /proc 不存在，直接归入 binary）
// 返回：
//   - string: "docker"（容器内运行）或 "binary"（二进制直接运行）
func detectDeployMode() string {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker"
	}
	if cgroup, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		for marker := range strings.SplitSeq(string(cgroup), "\n") {
			if strings.Contains(marker, "docker") || strings.Contains(marker, "containerd") {
				return "docker"
			}
		}
	}
	return "binary"
}

// mysqlVersion 查询 MySQL 服务器版本（SELECT VERSION()）
// 连接按需懒初始化并复用；初始化或查询失败返回空串
// 返回：
//   - string: MySQL 版本号（如 8.0.36），失败返回空串
func (h *AboutHandler) mysqlVersion() string {
	db, ok := h.getDb()
	if !ok {
		return ""
	}
	var ver string
	if err := db.GetDb().Raw("SELECT VERSION()").Scan(&ver).Error; err != nil {
		log.Printf("查询 MySQL 版本失败: %v", err)
		return ""
	}
	return ver
}

// redisVersion 查询 Redis 服务器版本（INFO server 段的 redis_version）
// 连接按需懒初始化并复用；初始化或查询失败返回空串
// 返回：
//   - string: Redis 版本号（如 7.2.4），失败返回空串
func (h *AboutHandler) redisVersion() string {
	rds, ok := h.getRedis()
	if !ok {
		return ""
	}
	info, err := rds.Info()
	if err != nil {
		log.Printf("查询 Redis 版本失败: %v", err)
		return ""
	}
	for line := range strings.SplitSeq(info, "\n") {
		if _, ver, ok := strings.Cut(strings.TrimSpace(line), "redis_version:"); ok {
			return ver
		}
	}
	return ""
}

// getDb 懒初始化 MySQL 连接（互斥锁防并发重复建连）
// 初始化失败不缓存成功状态，下次请求自动重试
// 返回：
//   - *mysql.Instance: MySQL 实例（未就绪时为 nil）
//   - bool: 是否可用
func (h *AboutHandler) getDb() (*mysql.Instance, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.db == nil {
		inst, err := mysql.New(h.cfg.Db)
		if err != nil {
			log.Printf("初始化 MySQL 连接失败: %v", err)
			return nil, false
		}
		h.db = inst
	}
	return h.db, true
}

// getRedis 懒初始化 Redis 连接（互斥锁防并发重复建连）
// 初始化失败不缓存成功状态，下次请求自动重试
// 返回：
//   - *redis.Instance: Redis 实例（未就绪时为 nil）
//   - bool: 是否可用
func (h *AboutHandler) getRedis() (*redis.Instance, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rds == nil {
		h.rds = redis.New(h.cfg.Redis)
	}
	return h.rds, true
}
