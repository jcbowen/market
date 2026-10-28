package client

import (
	"context"
	"time"

	"google.golang.org/grpc"

	"github.com/jcbowen/market/src/backend/common/proto/product"
)

// defaultRPCTimeout 网关→微服务调用的缺省截止时间
// 网关处理器的 ctx 来自 HTTP 请求，gin 不会给 deadline；不加兜底时，
// 下游服务卡死（连接 hang、慢查询、锁等待）会让请求长期挂起并占住连接与协程
const defaultRPCTimeout = 15 * time.Second

// longRPCTimeouts 按 gRPC 全方法名覆写超时：批量导入/附件迁移是「分钟内」的正常耗时，
// 不能用缺省值截断；其余方法不在此表内，一律走 defaultRPCTimeout
// 键取生成代码里的 FullMethodName 常量，避免手写字符串与方法改名后静默失效
var longRPCTimeouts = map[string]time.Duration{
	product.ProductService_PreviewDataImport_FullMethodName:     2 * time.Minute,
	product.ProductService_ImportDataPackage_FullMethodName:     10 * time.Minute,
	product.ProductService_ScanAttachmentMigrate_FullMethodName: 5 * time.Minute,
	product.ProductService_MigrateAttachmentItem_FullMethodName: 2 * time.Minute,
	product.ProductService_ListAttachmentMigrate_FullMethodName: 2 * time.Minute,
}

// defaultDeadlineUnaryInterceptor 为没有截止时间的调用补上缺省 deadline
// 调用方已自行设置 deadline（含更长的批量任务）时原样透传，不重复包裹；
// 只处理一元调用：事件订阅等流式调用生命周期由 WS 连接自身管理，套超时会把长连接提前截断
//
// 参数：
//   - ctx: 调用上下文
//   - method: gRPC 全方法名（如 /product.ProductService/GetProduct）
//   - req/reply: 请求与响应载体（拦截器不解析，原样交给 invoker）
//   - cc: 客户端连接
//   - invoker: 实际发起调用的函数
//   - opts: 调用选项
//
// 返回值：
//   - error: 调用错误（含 DeadlineExceeded）
//
// 使用示例：
//
//	grpc.NewClient(addr, grpc.WithUnaryInterceptor(defaultDeadlineUnaryInterceptor))
func defaultDeadlineUnaryInterceptor(
	ctx context.Context,
	method string,
	req, reply any,
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	if _, ok := ctx.Deadline(); ok {
		return invoker(ctx, method, req, reply, cc, opts...)
	}
	timeout, ok := longRPCTimeouts[method]
	if !ok {
		timeout = defaultRPCTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return invoker(callCtx, method, req, reply, cc, opts...)
}
