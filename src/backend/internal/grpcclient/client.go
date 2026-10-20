package grpcclient

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

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

// defaultCallTimeout 单次 gRPC 调用的默认超时上限
// 此前全链路无超时：下游 hang 住时，调用方（如下单事务内的库存扣减）会长时间
// 持有 DB 事务与连接池资源，单个下游故障即可拖垮整条链路，故在客户端统一封顶
const defaultCallTimeout = 10 * time.Second

// callTimeoutInterceptor 为未携带 deadline 的调用补默认超时
// 调用方已设 deadline 时保持不变（更短的先到期，由 context 语义保证）
//
// 参数：
//   - ctx: 调用上下文
//   - method: 被调用的方法全名
//   - req: 请求消息
//   - reply: 响应消息
//   - cc: 客户端连接
//   - invoker: 实际调用函数
//   - opts: 调用选项
//
// 返回值：
//   - error: 调用错误（含 DeadlineExceeded）
func callTimeoutInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultCallTimeout)
		defer cancel()
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

// dial 建立到指定 gRPC 服务的客户端连接
// keepalive 用于尽快发现对端容器已重建/半开连接，避免请求压在死连接上等待超时
//
// 参数：
//   - addr: 目标服务地址（host:port）
//   - name: 服务名，仅用于错误信息
//
// 返回值：
//   - *grpc.ClientConn: 客户端连接（由调用方进程生命周期持有，进程退出前统一 Close）
//   - error: 建连失败信息
func dial(addr, name string) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             3 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithChainUnaryInterceptor(callTimeoutInterceptor),
	)
	if err != nil {
		return nil, fmt.Errorf("连接 %s-service 失败: %w", name, err)
	}
	return conn, nil
}

// InitProductClient 初始化商品服务客户端
// 参数：
//   - addr: 商品服务 gRPC 地址
//
// 返回值：
//   - error: 初始化错误信息
func InitProductClient(addr string) error {
	conn, err := dial(addr, "product")
	if err != nil {
		return err
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
	conn, err := dial(addr, "user")
	if err != nil {
		return err
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
	conn, err := dial(addr, "order")
	if err != nil {
		return err
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
	conn, err := dial(addr, "payment")
	if err != nil {
		return err
	}
	PaymentClient = payment.NewPaymentServiceClient(conn)
	return nil
}
