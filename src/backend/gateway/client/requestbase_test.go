package client

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/jcbowen/market/src/backend/common/rpcmeta"
)

/**
 * 请求基址出站拦截器的离线单元测试（不发起真实网络调用）
 *
 * 覆盖：
 *   - ctx 含网关注入基址 → invoker 收到的 ctx 携带 x-request-base 出站 metadata
 *   - ctx 无基址（后台任务/进程内调用）→ 原样透传，不产生 metadata
 */

// TestRequestBaseUnaryInterceptor 验证基址注入与透传两种形态
func TestRequestBaseUnaryInterceptor(t *testing.T) {
	invoke := func(ctx context.Context) metadata.MD {
		var got metadata.MD
		err := requestBaseUnaryInterceptor(ctx, "/product.ProductService/GetProduct", nil, nil, nil,
			func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
				got, _ = metadata.FromOutgoingContext(ctx)
				return nil
			})
		if err != nil {
			t.Fatalf("拦截器不应返回错误: %v", err)
		}
		return got
	}

	// 有基址：写入出站 metadata
	withBase := rpcmeta.WithRequestBase(context.Background(), "http://192.168.1.5:8080")
	md := invoke(withBase)
	vals := md.Get(rpcmeta.MetadataKeyRequestBase)
	if len(vals) != 1 || vals[0] != "http://192.168.1.5:8080" {
		t.Fatalf("x-request-base = %v, want [http://192.168.1.5:8080]", vals)
	}

	// 无基址：原样透传，不产生 metadata
	if md := invoke(context.Background()); md != nil && len(md.Get(rpcmeta.MetadataKeyRequestBase)) != 0 {
		t.Fatalf("无基址不应写入 metadata, got %v", md.Get(rpcmeta.MetadataKeyRequestBase))
	}
}
