// Package rpcmeta 请求基址（scheme://host）的 gRPC metadata 透传（全系统公共能力）
//
// 背景：附件出库在「未配置显式域名」时降级为站内绝对路径 /uploads/<path>，
// H5 同域可解析，但小程序/APP 端 image 组件无法加载相对路径。为让服务层
// 拿得到当前访问域名，网关把请求基址注入请求上下文，出站拦截器统一写入
// metadata，服务端兜底拼装时读取——仅用于响应拼装，一律不入库（不改变
// 「不以首个请求域名固化配置」的原则）。
//
// 链路：gin 中间件 WithRequestBase → 出站拦截器 FromContext → metadata
// x-request-base → 服务端 FromIncomingContext → attachmenturl 兜底拼装。
package rpcmeta

import (
	"context"
	"strings"

	"google.golang.org/grpc/metadata"
)

// MetadataKeyRequestBase gRPC metadata 中请求基址的 key
const MetadataKeyRequestBase = "x-request-base"

// ctxKey context 存取私有 key 类型，避免与其它包的 string key 冲突
type ctxKey struct{}

// WithRequestBase 将请求基址写入 context（网关中间件调用）
//
// 参数:
//   - ctx: 原上下文
//   - base: 请求基址（形如 https://host，空串等价于不注入）
//
// 返回值:
//   - context.Context: 携带基址的新上下文（base 为空时原样返回）
func WithRequestBase(ctx context.Context, base string) context.Context {
	if base == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, base)
}

// FromContext 读取 context 中的请求基址（网关出站拦截器调用）
//
// 参数:
//   - ctx: 上下文
//
// 返回值:
//   - string: 请求基址，未注入时返回空串
func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	base, _ := ctx.Value(ctxKey{}).(string)
	return base
}

// OutgoingContext 从 context 取基址并构造携带出站 metadata 的新上下文
// （出站拦截器调用）。基址未注入时原样返回 ctx；已注入时与既有出站 metadata
// 合并，Set 覆盖同键旧值保证重入幂等（同一请求只会有一个基址）
//
// 参数:
//   - ctx: gRPC 调用上下文（含网关注入的基址）
//
// 返回值:
//   - context.Context: 携带 x-request-base 出站 metadata 的上下文
func OutgoingContext(ctx context.Context) context.Context {
	base := FromContext(ctx)
	if base == "" {
		return ctx
	}
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.MD{}
	} else {
		md = md.Copy()
	}
	md.Set(MetadataKeyRequestBase, base)
	return metadata.NewOutgoingContext(ctx, md)
}

// FromIncomingContext 服务端从 incoming metadata 读取请求基址
// 非 gRPC 服务端调用（单测/进程内直调）无 incoming metadata，返回空串，
// 调用方按原有站内绝对路径口径兜底
//
// 参数:
//   - ctx: 服务端方法上下文
//
// 返回值:
//   - string: 请求基址（scheme://host），未透传时返回空串
func FromIncomingContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get(MetadataKeyRequestBase)
	if len(vals) == 0 {
		return ""
	}
	// metadata 值由网关写入且只读使用；多值取首个非空
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
