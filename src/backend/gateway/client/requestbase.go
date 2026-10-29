package client

import (
	"context"

	"google.golang.org/grpc"

	"github.com/jcbowen/market/src/backend/common/rpcmeta"
)

// requestBaseUnaryInterceptor 把网关注入请求上下文的基址写入出站 metadata
// （x-request-base），供各服务在未配置显式域名时按当前访问域名兜底拼装附件 URL；
// 基址未注入（后台任务/进程内调用）时原样透传，零开销
//
// 参数：
//   - ctx: 调用上下文（含 gin 中间件注入的请求基址）
//   - method: gRPC 全方法名
//   - req/reply: 请求与响应载体（拦截器不解析，原样交给 invoker）
//   - cc: 客户端连接
//   - invoker: 实际发起调用的函数
//   - opts: 调用选项
//
// 返回值：
//   - error: 调用错误
//
// 使用示例：
//
//	grpc.WithChainUnaryInterceptor(requestBaseUnaryInterceptor, defaultDeadlineUnaryInterceptor)
func requestBaseUnaryInterceptor(
	ctx context.Context,
	method string,
	req, reply any,
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	return invoker(rpcmeta.OutgoingContext(ctx), method, req, reply, cc, opts...)
}
