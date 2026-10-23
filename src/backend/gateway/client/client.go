package client

import (
	"fmt"
	"sync"

	"github.com/jcbowen/market/src/backend/common/config"
	"github.com/jcbowen/market/src/backend/common/proto/cart"
	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/proto/user"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ServiceClients 聚合所有 gRPC 服务客户端
type ServiceClients struct {
	UserClient    user.UserServiceClient
	ProductClient product.ProductServiceClient
	OrderClient   order.OrderServiceClient
	CartClient    cart.CartServiceClient
	PaymentClient payment.PaymentServiceClient

	conns []*grpc.ClientConn
}

var (
	instance *ServiceClients
	once     sync.Once
)

// NewServiceClients 根据全局配置初始化所有 gRPC 客户端连接
// 参数：
//   - cfg: 全局配置，包含各微服务 gRPC 地址
//
// 返回：
//   - *ServiceClients: 客户端集合
//   - error: 初始化失败时的错误信息
func NewServiceClients(cfg *config.Config) (*ServiceClients, error) {
	var err error
	once.Do(func() {
		instance, err = initClients(cfg)
	})
	if err != nil {
		return nil, err
	}
	return instance, nil
}

// GetClients 返回已初始化的客户端集合
// 返回：
//   - *ServiceClients: 客户端集合，未初始化时返回 nil
func GetClients() *ServiceClients {
	return instance
}

// initClients 建立所有 gRPC 连接并创建对应客户端
func initClients(cfg *config.Config) (*ServiceClients, error) {
	addresses := map[string]string{
		"user":    cfg.Grpc.User,
		"product": cfg.Grpc.Product,
		"order":   cfg.Grpc.Order,
		"cart":    cfg.Grpc.Cart,
		"payment": cfg.Grpc.Payment,
	}

	clients := &ServiceClients{}
	for name, addr := range addresses {
		conn, cErr := grpc.NewClient(addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
			// 本地上传模式图片经 gateway 转发，单条消息可达数 MB，需放宽客户端发送上限
			grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(10*1024*1024), grpc.MaxCallRecvMsgSize(10*1024*1024)),
		)
		if cErr != nil {
			_ = clients.Close()
			return nil, fmt.Errorf("连接 %s 服务失败 (%s): %w", name, addr, cErr)
		}
		clients.conns = append(clients.conns, conn)
		switch name {
		case "user":
			clients.UserClient = user.NewUserServiceClient(conn)
		case "product":
			clients.ProductClient = product.NewProductServiceClient(conn)
		case "order":
			clients.OrderClient = order.NewOrderServiceClient(conn)
		case "cart":
			clients.CartClient = cart.NewCartServiceClient(conn)
		case "payment":
			clients.PaymentClient = payment.NewPaymentServiceClient(conn)
		}
	}
	return clients, nil
}

// Close 关闭所有 gRPC 连接
// 返回：
//   - error: 关闭过程中的错误信息
func (s *ServiceClients) Close() error {
	var firstErr error
	for _, conn := range s.conns {
		if conn == nil {
			continue
		}
		if err := conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.conns = nil
	return firstErr
}
