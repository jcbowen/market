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

// detectDeployMode 检测当前实际部署运行方式（三态）
// 1. 非容器环境（无 /.dockerenv 且 /proc/1/cgroup 无容器运行时标识）→ binary（二进制直接运行）
// 2. 容器内且同容器可见 mysqld/redis-server 进程 → all-in-one（一体化容器，全套服务同容器）
// 3. 容器内且无上述进程 → compose（多容器编排，MySQL/Redis 在独立容器，经网络连接）
// 说明：同一 PID namespace 内进程互相可见，故「能否看到 mysqld」可区分一体/多容器；
// Windows/裸机 systemd 场景 /proc 不存在，直接归入 binary
// 返回：
//   - string: "all-in-one"（一体化容器）/ "compose"（多容器编排）/ "binary"（二进制直接运行）
func detectDeployMode() string {
	inContainer := false
	if _, err := os.Stat("/.dockerenv"); err == nil {
		inContainer = true
	} else if cgroup, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		for line := range strings.SplitSeq(string(cgroup), "\n") {
			if strings.Contains(line, "docker") || strings.Contains(line, "containerd") {
				inContainer = true
				break
			}
		}
	}
	if !inContainer {
		return "binary"
	}
	if hasDBProcessInContainer() {
		return "all-in-one"
	}
	return "compose"
}

// hasDBProcessInContainer 检测当前容器内是否运行着 MySQL/Redis 服务进程
// 遍历 /proc 数字目录读取各进程 comm（进程名，world-readable，非 root 也可读），
// 命中 mysqld 或 redis-server 即认为全套服务跑在同一容器（一体化镜像特征）
// 返回：
//   - bool: 是否存在数据库服务进程
func hasDBProcessInContainer() bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name()[0] < '0' || e.Name()[0] > '9' {
			continue
		}
		comm, err := os.ReadFile("/proc/" + e.Name() + "/comm")
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(comm))
		if name == "mysqld" || name == "redis-server" {
			return true
		}
	}
	return false
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
