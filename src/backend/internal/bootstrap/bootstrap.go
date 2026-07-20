package bootstrap

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jcbowen/jcbaseGo"
	"github.com/jcbowen/jcbaseGo/component/orm/mysql"
	"github.com/jcbowen/jcbaseGo/component/redis"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/jcbowen/market/src/backend/common/config"
)

// LoadConfig 加载全局配置
// 返回：
//   - *config.Config: 全局配置指针
func LoadConfig() *config.Config {
	opt := jcbaseGo.New(jcbaseGo.Option{
		ConfigType:   jcbaseGo.ConfigTypeYAML,
		ConfigSource: getConfigPath(),
		ConfigData:   &config.Config{},
	})
	return (*opt.GetConfig()).(*config.Config)
}

// InitMySQL 根据全局配置初始化 MySQL 连接
// 参数：
//   - cfg: 全局配置
// 返回：
//   - *mysql.Instance: MySQL 实例
//   - error: 初始化失败时的错误信息
func InitMySQL(cfg *config.Config) (*mysql.Instance, error) {
	return mysql.New(cfg.Db)
}

// InitRedis 根据全局配置初始化 Redis 连接
// 参数：
//   - cfg: 全局配置
// 返回：
//   - *redis.Instance: Redis 实例
func InitRedis(cfg *config.Config) *redis.Instance {
	return redis.New(cfg.Redis)
}

// RegisterHealth 注册 gRPC 健康检查服务
// 参数：
//   - server: gRPC 服务端实例
func RegisterHealth(server *grpc.Server) {
	hs := health.NewServer()
	hs.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(server, hs)
}

// RunGRPCServer 启动 gRPC 服务并阻塞，直到收到退出信号
// 参数：
//   - server: gRPC 服务端实例
//   - port: 监听端口
// 返回：
//   - error: 启动或关闭过程中的错误信息
func RunGRPCServer(server *grpc.Server, port int) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("监听端口 %d 失败: %w", port, err)
	}

	go func() {
		log.Printf("gRPC 服务启动于 :%d", port)
		if err := server.Serve(lis); err != nil {
			log.Fatalf("gRPC 服务异常: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("正在优雅关闭 gRPC 服务...")
	server.GracefulStop()
	return nil
}

// getConfigPath 获取配置文件路径
// 优先读取 CONFIG_FILE 环境变量，未设置时使用可执行文件同级目录下的 config.yaml
//
// 返回值：
//   - string: 配置文件绝对路径
func getConfigPath() string {
	if envPath := os.Getenv("CONFIG_FILE"); envPath != "" {
		return envPath
	}
	exe, err := os.Executable()
	if err != nil {
		return "./config.yaml"
	}
	return filepath.Join(filepath.Dir(exe), "config.yaml")
}
