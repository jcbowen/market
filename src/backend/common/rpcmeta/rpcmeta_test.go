// Package rpcmeta 请求基址透传的单元测试
package rpcmeta

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

// TestWithAndFromContext 验证基址的 context 存取与空值不注入
func TestWithAndFromContext(t *testing.T) {
	if got := FromContext(context.Background()); got != "" {
		t.Fatalf("空上下文应取不到基址，got %q", got)
	}
	if got := FromContext(WithRequestBase(context.Background(), "")); got != "" {
		t.Fatalf("空基址不应注入，got %q", got)
	}
	ctx := WithRequestBase(context.Background(), "https://m.example.com")
	if got := FromContext(ctx); got != "https://m.example.com" {
		t.Fatalf("FromContext = %q, want https://m.example.com", got)
	}
}

// TestOutgoingContext 验证出站上下文构造：无基址原样透传、有基址写入 metadata 且重入幂等
func TestOutgoingContext(t *testing.T) {
	plain := context.Background()
	if got := OutgoingContext(plain); got != plain {
		t.Fatal("无基址时应原样返回 ctx")
	}

	ctx := WithRequestBase(plain, "http://192.168.1.5:8080")
	out := OutgoingContext(ctx)
	md, ok := metadata.FromOutgoingContext(out)
	if !ok {
		t.Fatal("应有出站 metadata")
	}
	if vals := md.Get(MetadataKeyRequestBase); len(vals) != 1 || vals[0] != "http://192.168.1.5:8080" {
		t.Fatalf("metadata x-request-base = %v, want [http://192.168.1.5:8080]", vals)
	}

	// 重入：Set 覆盖同键旧值，不叠加
	re := OutgoingContext(out)
	md2, _ := metadata.FromOutgoingContext(re)
	if vals := md2.Get(MetadataKeyRequestBase); len(vals) != 1 {
		t.Fatalf("重入后应仍是单值，got %v", vals)
	}
}

// TestFromIncomingContext 验证服务端读取：无 incoming metadata、空值与多值形态
func TestFromIncomingContext(t *testing.T) {
	if got := FromIncomingContext(context.Background()); got != "" {
		t.Fatalf("无 incoming metadata 应返回空串，got %q", got)
	}
	empty := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	if got := FromIncomingContext(empty); got != "" {
		t.Fatalf("incoming 无该键应返回空串，got %q", got)
	}

	in := metadata.Pairs(MetadataKeyRequestBase, "https://shop.example.com", MetadataKeyRequestBase, "  ")
	got := FromIncomingContext(metadata.NewIncomingContext(context.Background(), in))
	if got != "https://shop.example.com" {
		t.Fatalf("FromIncomingContext = %q, want 首个非空值", got)
	}
}
