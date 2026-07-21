package grpcclient

import (
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/jcbowen/market/src/backend/common/proto/order"
	"github.com/jcbowen/market/src/backend/common/proto/payment"
	"github.com/jcbowen/market/src/backend/common/proto/product"
	"github.com/jcbowen/market/src/backend/common/proto/user"
)

// ProductClient 商品服务客户端
var ProductClient product.ProductServiceClient

// UserClient 用户服务客户端
var UserClient user.UserServiceClient

// OrderClient 订单服务客户端
var OrderClient order.OrderServiceClient

// PaymentClient 支付服务客户端
var PaymentClient payment.PaymentServiceClient

// InitProductClient 初始化商品服务客户端
// 参数：
//   - addr: 商品服务 gRPC 地址
//
// 返回值：
//   - error: 初始化错误信息
func InitProductClient(addr string) error {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("连接 product-service 失败: %w", err)
	}
	ProductClient = product.NewProductServiceClient(conn)
	return nil
}

// InitUserClient 初始化用户服务客户端
// 参数：
//   - addr: 用户服务 gRPC 地址
//
// 返回值：
//   - error: 初始化错误信息
func InitUserClient(addr string) error {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("连接 user-service 失败: %w", err)
	}
	UserClient = user.NewUserServiceClient(conn)
	return nil
}

// InitOrderClient 初始化订单服务客户端
// 参数：
//   - addr: 订单服务 gRPC 地址
//
// 返回值：
//   - error: 初始化错误信息
func InitOrderClient(addr string) error {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("连接 order-service 失败: %w", err)
	}
	OrderClient = order.NewOrderServiceClient(conn)
	return nil
}

// InitPaymentClient 初始化支付服务客户端
// 参数：
//   - addr: 支付服务 gRPC 地址
//
// 返回值：
//   - error: 初始化错误信息
func InitPaymentClient(addr string) error {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("连接 payment-service 失败: %w", err)
	}
	PaymentClient = payment.NewPaymentServiceClient(conn)
	return nil
}
